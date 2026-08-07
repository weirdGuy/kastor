package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/schema"
)

// Check kinds this provider contributes to a kastor doctor report.
const (
	checkCredential     = "credential"
	checkToolPermission = "tool_permission"
)

var _ provider.Checker = (*Provider)(nil)

// vaultCredential is the provider's own reduction of a vault credential to
// the fields readiness depends on. Nothing here is the credential's value:
// kastor reads enough to verify that a reference resolves and no more
// (SPEC.md §3.5).
type vaultCredential struct {
	ID           string
	DisplayName  string // nullable on the platform; empty when absent
	Archived     bool
	MCPServerURL string
	AuthType     string     // mcp_oauth | static_bearer | environment_variable
	ExpiresAt    *time.Time // mcp_oauth only; nil when the grant does not expire
	HasRefresh   bool
}

// errCredentialNotFound distinguishes "the vault answered, and there is no
// such credential" from "the vault did not answer". The first is a finding;
// the second is the absence of one.
var errCredentialNotFound = errors.New("credential not found")

// Check implements provider.Checker: it verifies the connection:// credentials
// this agent's MCP servers reference against the target's vault, and checks
// that the deployed agent is actually permitted to call the tools it declares.
//
// Every outcome is a Check, never an error, so "could not verify" (an
// unreachable vault) stays distinguishable from "credential missing". An error
// return here means the desired config could not be read at all.
func (p *Provider) Check(ctx context.Context, desired *provider.Resource, remote provider.Object) ([]provider.Check, error) {
	if desired == nil {
		return nil, fmt.Errorf("claude: desired resource is nil")
	}
	servers, err := specMCPServers(desired.Addr, desired.Config["mcp_servers"])
	if err != nil {
		return nil, err
	}

	checks := p.credentialChecks(ctx, servers)
	permissions, err := p.permissionChecks(desired, remote)
	if err != nil {
		return nil, err
	}
	return append(checks, permissions...), nil
}

// credentialChecks verifies each connection:// ref against the vault, in
// server-name order so a report is stable run to run.
func (p *Provider) credentialChecks(ctx context.Context, servers map[string]specMCPServer) []provider.Check {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var checks []provider.Check
	for _, name := range names {
		server := servers[name]
		if server.authRef == "" {
			// SPEC.md §3.6: a server with no auth block is unauthenticated
			// everywhere, which is the normal case for a public server. That
			// is a declaration, not a fault — report it and move on.
			checks = append(checks, provider.Check{
				Kind:    checkCredential,
				Status:  provider.StatusOK,
				Subject: name,
				Summary: "connection is unauthenticated by declaration",
				Detail:  fmt.Sprintf("mcp_server.%s declares no auth for this target, so the platform dials %s with no credential", name, server.url),
			})
			continue
		}
		scheme, value, err := schema.ParseCredentialRef(server.authRef)
		if err != nil || scheme != schema.SchemeConnection {
			// env:// cannot bind on this target and validate rejects it, so
			// reaching here means the module skipped the pipeline.
			continue
		}
		checks = append(checks, p.credentialCheck(ctx, name, server, value))
	}
	return checks
}

// credentialCheck verifies one credential id. The distinction this function
// exists to preserve: a vault that does not answer yields StatusUnknown, and
// only a vault that answers "no such credential" yields StatusFailed.
func (p *Provider) credentialCheck(ctx context.Context, server string, spec specMCPServer, credentialID string) provider.Check {
	base := provider.Check{Kind: checkCredential, Subject: credentialID}

	if p.vaultID == "" {
		return withStatus(base, provider.StatusUnknown,
			"could not verify the credential: no vault to look it up in",
			fmt.Sprintf("mcp_server.%s references %s, but the target declares no vault_id", server, spec.authRef))
	}

	credential, err := p.credential(ctx, credentialID)
	switch {
	case errors.Is(err, errCredentialNotFound):
		return withStatus(base, provider.StatusFailed,
			"credential does not exist in the vault",
			fmt.Sprintf("mcp_server.%s references %s, and vault %s holds no credential with that id", server, spec.authRef, p.vaultID))
	case err != nil:
		// Deliberately not StatusFailed: the vault did not answer, so
		// whether the credential exists is simply not known.
		return withStatus(base, provider.StatusUnknown,
			"could not verify the credential against the vault",
			fmt.Sprintf("mcp_server.%s references %s; reading vault %s failed: %v", server, spec.authRef, p.vaultID, err))
	}

	base.SubjectName = credential.DisplayName

	switch {
	case credential.Archived:
		return withStatus(base, provider.StatusFailed,
			"credential is archived",
			fmt.Sprintf("mcp_server.%s references %s, which has been archived on the platform and can no longer authenticate", server, spec.authRef))

	case spec.url != "" && credential.MCPServerURL != "" && !sameEndpoint(credential.MCPServerURL, spec.url):
		return withStatus(base, provider.StatusFailed,
			"credential authenticates a different server",
			fmt.Sprintf("mcp_server.%s declares url %q, but the credential authenticates %q; the agent would present a credential the server will reject",
				server, spec.url, credential.MCPServerURL))

	case credential.expired():
		return withStatus(base, provider.StatusFailed,
			"connection is not authenticated: the OAuth grant has expired",
			fmt.Sprintf("mcp_server.%s references %s, whose grant expired at %s and carries no refresh token; re-authorize the connection on the platform",
				server, spec.authRef, credential.ExpiresAt.UTC().Format(time.RFC3339)))
	}

	return withStatus(base, provider.StatusOK,
		"credential resolves and authenticates this server",
		fmt.Sprintf("mcp_server.%s → %s", server, credential.MCPServerURL))
}

// expired reports whether an OAuth grant has lapsed with no way back. A
// credential that can refresh itself is not a readiness problem: the platform
// renews it at dial time.
func (c *vaultCredential) expired() bool {
	if c.ExpiresAt == nil || c.HasRefresh {
		return false
	}
	return c.ExpiresAt.Before(time.Now())
}

// permissionChecks compares the tools the spec grants against the tools the
// deployed agent may actually call. This is the KAS-57 failure as a check
// rather than as an outage: an agent whose permissions deny everything matches
// its spec exactly and cannot serve a request.
func (p *Provider) permissionChecks(desired *provider.Resource, remote provider.Object) ([]provider.Check, error) {
	spec, _, err := normalizeDesired(desired)
	if err != nil {
		return nil, err
	}
	wanted, err := declaredToolPolicies(spec["tools"])
	if err != nil {
		return nil, err
	}
	actual, err := declaredToolPolicies(remote["tools"])
	if err != nil {
		return nil, fmt.Errorf("%s: reading remote tool permissions: %w", desired.Addr, err)
	}

	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	var checks []provider.Check
	for _, name := range names {
		want := wanted[name]
		base := provider.Check{Kind: checkToolPermission, Subject: name}

		got, present := actual[name]
		switch {
		case !present:
			checks = append(checks, withStatus(base, provider.StatusFailed,
				"the deployed agent is not granted this tool",
				fmt.Sprintf("%s declares it, but the remote agent's toolsets do not list it; run kastor plan", toolAddr(name, want))))
		case !got.enabled:
			checks = append(checks, withStatus(base, provider.StatusFailed,
				"the deployed agent is not granted this tool",
				fmt.Sprintf("the remote agent has it disabled, so a call to it is refused; %s grants it", toolAddr(name, want))))
		case got.policy != want.policy:
			checks = append(checks, withStatus(base, provider.StatusFailed,
				fmt.Sprintf("tool permission is %q, expected %q", policyName(got.policy), policyName(want.policy)),
				permissionDetail(name, want, got)))
		default:
			checks = append(checks, withStatus(base, provider.StatusOK,
				fmt.Sprintf("tool is granted with permission %q", policyName(got.policy)), ""))
		}
	}
	return checks, nil
}

// permissionDetail explains a policy mismatch in terms of what it costs.
func permissionDetail(name string, want, got toolPolicy) string {
	if want.policy == alwaysAllowPolicy && got.policy == alwaysAskPolicy {
		return fmt.Sprintf("the remote agent stops for a human before calling it; the spec grants it unsupervised, "+
			"so a session with no human attached cannot use %s", name)
	}
	return fmt.Sprintf("the remote agent's permission for %s was changed outside kastor; run kastor plan", name)
}

// toolAddr renders a tool's kastor address, qualified by its MCP server when
// it has one — the platform's tool name is the server's, not kastor's.
func toolAddr(name string, p toolPolicy) string {
	if p.server != "" {
		return fmt.Sprintf("mcp_server.%s tool %q", p.server, name)
	}
	return "tool." + name
}

func policyName(policy string) string {
	if policy == "" {
		return "unset"
	}
	return policy
}

// toolPolicy is one tool's permission state, on either side of the comparison.
type toolPolicy struct {
	enabled bool
	policy  string // always_allow | always_ask; "" when the platform left it unset
	server  string // MCP server name; empty for the builtin agent toolset
}

// declaredToolPolicies flattens an agent's toolsets into tool name → policy.
// It reads both the normalized spec and a raw API response, which carry the
// same shape here.
//
// TODO(KAS-62): the spec side is always always_allow until requires_approval
// reaches the language; once it does, this comparison starts reporting a tool
// that should be gated and is not, with no change to its shape.
func declaredToolPolicies(raw any) (map[string]toolPolicy, error) {
	policies := map[string]toolPolicy{}
	if raw == nil {
		return policies, nil
	}
	toolsets, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("tools must be an array, got %T", raw)
	}
	for i, value := range toolsets {
		toolset, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tools[%d] must be an object, got %T", i, value)
		}
		server, _ := toolset["mcp_server_name"].(string)
		configs, ok := toolset["configs"].([]any)
		if !ok {
			// A remote toolset kastor does not author may legitimately carry
			// no configs; it grants none of the module's tools either way.
			continue
		}
		for j, rawConfig := range configs {
			config, ok := rawConfig.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tools[%d].configs[%d] must be an object, got %T", i, j, rawConfig)
			}
			name, ok := config["name"].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("tools[%d].configs[%d].name must be a non-empty string", i, j)
			}
			enabled, _ := config["enabled"].(bool)
			policy := ""
			if p, ok := config["permission_policy"].(map[string]any); ok {
				policy, _ = p["type"].(string)
			}
			policies[name] = toolPolicy{enabled: enabled, policy: policy, server: server}
		}
	}
	return policies, nil
}

// credential fetches one credential from the target's vault, using the
// injected lookup when a test supplied one.
func (p *Provider) credential(ctx context.Context, credentialID string) (*vaultCredential, error) {
	if p.fetchCredential != nil {
		return p.fetchCredential(ctx, p.vaultID, credentialID)
	}
	raw, err := doRequest(ctx, p, "read credential", credentialID, p.readPacer, func() (*anthropic.BetaManagedAgentsCredential, error) {
		return p.client.Beta.Vaults.Credentials.Get(ctx, credentialID, anthropic.BetaVaultCredentialGetParams{VaultID: p.vaultID})
	})
	if errors.Is(err, ErrNotFound) {
		return nil, errCredentialNotFound
	}
	if err != nil {
		return nil, err
	}
	return decodeCredential(raw)
}

// decodeCredential reduces an API credential to the readiness fields. It goes
// through RawJSON for the same reason Read does: archived_at and expires_at
// are nullable on the wire but non-pointer time.Time on the struct, so the
// struct alone cannot distinguish "not archived" from "archived at the zero
// time".
func decodeCredential(raw *anthropic.BetaManagedAgentsCredential) (*vaultCredential, error) {
	if raw == nil {
		return nil, fmt.Errorf("claude: vault API returned a nil credential")
	}
	var wire struct {
		ID          string  `json:"id"`
		DisplayName *string `json:"display_name"`
		ArchivedAt  *string `json:"archived_at"`
		Auth        struct {
			Type         string  `json:"type"`
			MCPServerURL string  `json:"mcp_server_url"`
			ExpiresAt    *string `json:"expires_at"`
			Refresh      *struct {
				Type string `json:"type"`
			} `json:"refresh"`
		} `json:"auth"`
	}
	body := raw.RawJSON()
	if body == "" {
		return nil, fmt.Errorf("claude: vault API returned a credential without response JSON")
	}
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		return nil, fmt.Errorf("claude: decode vault credential: %w", err)
	}

	credential := &vaultCredential{
		ID:           wire.ID,
		Archived:     wire.ArchivedAt != nil,
		MCPServerURL: wire.Auth.MCPServerURL,
		AuthType:     wire.Auth.Type,
		HasRefresh:   wire.Auth.Refresh != nil,
	}
	if wire.DisplayName != nil {
		credential.DisplayName = *wire.DisplayName
	}
	if wire.Auth.ExpiresAt != nil {
		expires, err := time.Parse(time.RFC3339, *wire.Auth.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("claude: credential %s has an unparseable expires_at %q: %w", wire.ID, *wire.Auth.ExpiresAt, err)
		}
		credential.ExpiresAt = &expires
	}
	return credential, nil
}

// sameEndpoint compares two MCP server URLs the way a dial would: a trailing
// slash is not a different server, and neither is a difference in case in the
// scheme or host. Anything beyond that is left alone — a differing path is a
// differing server.
func sameEndpoint(a, b string) bool {
	normalize := func(u string) string {
		u = strings.TrimSuffix(u, "/")
		scheme, rest, found := strings.Cut(u, "://")
		if !found {
			return u
		}
		host, path, hasPath := strings.Cut(rest, "/")
		out := strings.ToLower(scheme) + "://" + strings.ToLower(host)
		if hasPath {
			out += "/" + path
		}
		return out
	}
	return normalize(a) == normalize(b)
}

func withStatus(base provider.Check, status provider.Status, summary, detail string) provider.Check {
	base.Status = status
	base.Summary = summary
	base.Detail = detail
	return base
}
