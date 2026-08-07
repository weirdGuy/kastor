// Package claude maps Kastor resources onto Claude Managed Agents.
package claude

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/weirdGuy/kastor/internal/provider"
)

const (
	defaultAPIKeyEnv = "ANTHROPIC_API_KEY"
	maxAttempts      = 3
)

var (
	// ErrAuthentication classifies rejected Claude API credentials.
	ErrAuthentication = errors.New("claude authentication failure")
	// ErrNotFound classifies a missing remote agent outside Read and Delete,
	// where the provider contract handles absence as data or idempotent success.
	ErrNotFound = errors.New("claude agent not found")
	// ErrConflict classifies optimistic-concurrency failures.
	ErrConflict = errors.New("claude agent update conflict")
	// ErrTransient classifies exhausted retries for rate limits and server errors.
	ErrTransient = errors.New("claude transient failure")
)

// Provider implements provider.Provider for Claude Managed Agents.
type Provider struct {
	client       anthropic.Client
	authEnv      string
	vaultID      string // target.vault_id; read by Check only (SPEC.md §5.3)
	sleep        func(context.Context, time.Duration) error
	createPacer  *requestPacer
	readPacer    *requestPacer
	retryBackoff func(error, int) time.Duration
	// fetchCredential is the vault lookup, injectable so kastor doctor is
	// testable without a network. Nil means use the SDK.
	fetchCredential func(ctx context.Context, vaultID, credentialID string) (*vaultCredential, error)
}

var _ provider.Provider = (*Provider)(nil)

// New returns a Claude Managed Agents provider. Options are primarily useful
// for tests that provide a fake base URL. Factory is the production entrypoint.
func New(opts ...option.RequestOption) *Provider {
	clientOpts := []option.RequestOption{option.WithoutEnvironmentDefaults()}
	clientOpts = append(clientOpts, opts...)
	// The SDK retries conflicts by default. Keep this last so the provider's
	// narrower retry taxonomy cannot be overridden by a caller option.
	clientOpts = append(clientOpts, option.WithMaxRetries(0))
	return &Provider{
		client:       anthropic.NewClient(clientOpts...),
		authEnv:      defaultAPIKeyEnv,
		sleep:        sleepContext,
		createPacer:  newRequestPacer(300),
		readPacer:    newRequestPacer(600),
		retryBackoff: transientBackoff,
	}
}

// Read implements provider.Provider.
func (p *Provider) Read(ctx context.Context, id string) (provider.Object, bool, error) {
	agent, err := doRequest(ctx, p, "read", id, p.readPacer, func() (*anthropic.BetaManagedAgentsAgent, error) {
		return p.client.Beta.Agents.Get(ctx, id, anthropic.BetaAgentGetParams{})
	})
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	remote, err := normalizeAPIResponse(agent)
	if err != nil {
		return nil, false, fmt.Errorf("read Claude agent %q: %w", id, err)
	}
	archived, err := normalizedAPIArchived(remote)
	if err != nil {
		return nil, false, fmt.Errorf("read Claude agent %q: %w", id, err)
	}
	if archived {
		return nil, false, nil
	}
	return remote, true, nil
}

// Create implements provider.Provider.
func (p *Provider) Create(ctx context.Context, desired *provider.Resource) (string, error) {
	params, err := normalizeCreateParams(desired)
	if err != nil {
		return "", err
	}
	agent, err := doRequest(ctx, p, "create", desired.Addr, p.createPacer, func() (*anthropic.BetaManagedAgentsAgent, error) {
		return p.client.Beta.Agents.New(ctx, params)
	})
	if err != nil {
		return "", err
	}
	remote, err := normalizeAPIResponse(agent)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", desired.Addr, err)
	}
	id, err := normalizedAPIID(remote)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", desired.Addr, err)
	}
	return id, nil
}

// Update implements provider.Provider.
func (p *Provider) Update(ctx context.Context, id string, desired *provider.Resource) error {
	if desired == nil {
		return fmt.Errorf("claude: desired resource is nil")
	}
	remote, found, err := p.Read(ctx, id)
	if err != nil {
		return fmt.Errorf("prepare update for %s: %w", desired.Addr, err)
	}
	if !found {
		return &lifecycleError{
			kind: ErrNotFound,
			msg:  fmt.Sprintf("prepare update for %s: Claude agent %q is missing or archived; re-run kastor plan", desired.Addr, id),
		}
	}
	version, err := normalizedAPIVersion(remote)
	if err != nil {
		return fmt.Errorf("prepare update for %s: %w", desired.Addr, err)
	}
	params, err := normalizeUpdateParams(desired, version)
	if err != nil {
		return err
	}
	_, err = doRequest(ctx, p, "update", id, nil, func() (*anthropic.BetaManagedAgentsAgent, error) {
		return p.client.Beta.Agents.Update(ctx, id, params)
	})
	return err
}

// Delete implements provider.Provider by archiving the agent. Archive is
// irreversible, so the pre-read is also the idempotence guard: a missing or
// already-archived agent succeeds without another archive request.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, found, err := p.Read(ctx, id)
	if err != nil {
		return fmt.Errorf("prepare archive of Claude agent %q: %w", id, err)
	}
	if !found {
		return nil
	}
	_, err = doRequest(ctx, p, "archive", id, nil, func() (*anthropic.BetaManagedAgentsAgent, error) {
		return p.client.Beta.Agents.Archive(ctx, id, anthropic.BetaAgentArchiveParams{})
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Diff implements provider.Provider. Both inputs are normalized into the
// Managed Agents comparison shape before the pure structural comparison.
func (*Provider) Diff(desired *provider.Resource, remote provider.Object) ([]provider.AttrDiff, error) {
	spec, echo, err := normalizeForDiff(desired, remote)
	if err != nil {
		return nil, err
	}
	return diffObjects(spec, echo), nil
}

type lifecycleError struct {
	kind error
	msg  string
	err  error
}

func (e *lifecycleError) Error() string {
	if e.err == nil {
		return e.msg
	}
	return e.msg + ": " + e.err.Error()
}

func (e *lifecycleError) Unwrap() error { return e.err }

func (e *lifecycleError) Is(target error) bool { return target == e.kind }

func doRequest[T any](
	ctx context.Context,
	p *Provider,
	operation string,
	id string,
	pacer *requestPacer,
	call func() (T, error),
) (T, error) {
	var zero T
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if pacer != nil {
			if err := pacer.Wait(ctx, p.sleep); err != nil {
				return zero, fmt.Errorf("Claude Managed Agents %s %q: wait for rate limit: %w", operation, id, err)
			}
		}
		result, err := call()
		if err == nil {
			return result, nil
		}
		kind, status := classifyError(err)
		if kind != ErrTransient {
			return zero, p.operationError(operation, id, kind, status, attempt, err)
		}
		if attempt == maxAttempts {
			return zero, p.operationError(operation, id, kind, status, attempt, err)
		}
		if err := p.sleep(ctx, p.retryBackoff(err, attempt)); err != nil {
			return zero, fmt.Errorf("Claude Managed Agents %s %q: retry backoff: %w", operation, id, err)
		}
	}
	panic("unreachable")
}

func classifyError(err error) (kind error, status int) {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return nil, 0
	}
	status = apiErr.StatusCode
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrAuthentication, status
	case status == http.StatusNotFound:
		return ErrNotFound, status
	case status == http.StatusConflict:
		return ErrConflict, status
	case status == http.StatusTooManyRequests || status >= http.StatusInternalServerError:
		return ErrTransient, status
	default:
		return nil, status
	}
}

func (p *Provider) operationError(operation, id string, kind error, status, attempts int, err error) error {
	prefix := fmt.Sprintf("Claude Managed Agents %s %q", operation, id)
	switch kind {
	case ErrAuthentication:
		return &lifecycleError{
			kind: kind,
			msg:  fmt.Sprintf("%s: authentication failed (HTTP %d); check %s", prefix, status, p.authEnv),
			err:  err,
		}
	case ErrNotFound:
		return &lifecycleError{
			kind: kind,
			msg:  fmt.Sprintf("%s: agent not found (HTTP %d)", prefix, status),
			err:  err,
		}
	case ErrConflict:
		return &lifecycleError{
			kind: kind,
			msg:  fmt.Sprintf("%s: concurrent out-of-band edit detected (HTTP %d); re-run kastor plan", prefix, status),
			err:  err,
		}
	case ErrTransient:
		return &lifecycleError{
			kind: kind,
			msg:  fmt.Sprintf("%s: transient failure (HTTP %d) after %d attempts", prefix, status, attempts),
			err:  err,
		}
	default:
		if status != 0 {
			return fmt.Errorf("%s: API request failed (HTTP %d): %w", prefix, status, err)
		}
		return fmt.Errorf("%s: request failed: %w", prefix, err)
	}
}

type requestPacer struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	now      func() time.Time
}

func newRequestPacer(requestsPerMinute int) *requestPacer {
	return &requestPacer{
		interval: time.Minute / time.Duration(requestsPerMinute),
		now:      time.Now,
	}
}

func (p *requestPacer) Wait(ctx context.Context, sleep func(context.Context, time.Duration) error) error {
	p.mu.Lock()
	now := p.now()
	delay := time.Duration(0)
	if now.Before(p.next) {
		delay = p.next.Sub(now)
		p.next = p.next.Add(p.interval)
	} else {
		p.next = now.Add(p.interval)
	}
	p.mu.Unlock()
	if delay == 0 {
		return nil
	}
	return sleep(ctx, delay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func transientBackoff(err error, attempt int) time.Duration {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr.Response != nil {
		if value := apiErr.Response.Header.Get("Retry-After-Ms"); value != "" {
			if milliseconds, parseErr := strconv.ParseFloat(value, 64); parseErr == nil {
				return max(0, time.Duration(milliseconds*float64(time.Millisecond)))
			}
		}
		if value := apiErr.Response.Header.Get("Retry-After"); value != "" {
			if seconds, parseErr := strconv.ParseFloat(value, 64); parseErr == nil {
				return max(0, time.Duration(seconds*float64(time.Second)))
			}
			if deadline, parseErr := http.ParseTime(value); parseErr == nil {
				return max(0, time.Until(deadline))
			}
		}
	}
	return 250 * time.Millisecond * time.Duration(1<<(attempt-1))
}
