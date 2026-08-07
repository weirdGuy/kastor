package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/weirdGuy/kastor/internal/provider"
)

const (
	testVaultID   = "vlt_011CZaBcDeFgHiJkLmNoPqRs"
	testCredID    = "cred_011CZkZDLs7fYzm1hXNPeRjv"
	testServerURL = "https://api.githubcopilot.com/mcp/"
)

// checkerProvider returns a Provider whose vault lookup is the given function,
// so readiness is exercised with no network.
func checkerProvider(vaultID string, fetch func(ctx context.Context, vaultID, credentialID string) (*vaultCredential, error)) *Provider {
	p := New()
	p.vaultID = vaultID
	p.fetchCredential = fetch
	return p
}

func vaultHolding(c *vaultCredential) func(context.Context, string, string) (*vaultCredential, error) {
	return func(context.Context, string, string) (*vaultCredential, error) { return c, nil }
}

// rawCredential builds an SDK credential from wire JSON, so decoding is
// exercised through the same path a real response takes.
func rawCredential(t *testing.T, body string) *anthropic.BetaManagedAgentsCredential {
	t.Helper()
	var credential anthropic.BetaManagedAgentsCredential
	if err := credential.UnmarshalJSON([]byte(body)); err != nil {
		t.Fatalf("unmarshal credential fixture: %v", err)
	}
	return &credential
}

// findCheck returns the first check of the given kind.
func findCheck(t *testing.T, checks []provider.Check, kind string) provider.Check {
	t.Helper()
	for _, c := range checks {
		if c.Kind == kind {
			return c
		}
	}
	t.Fatalf("no %s check in %+v", kind, checks)
	return provider.Check{}
}

func TestCheckCredentialVerdicts(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	tests := []struct {
		name       string
		fetch      func(context.Context, string, string) (*vaultCredential, error)
		vaultID    string
		wantStatus provider.Status
		wantName   string
		wantText   []string
		denyText   []string
	}{
		{
			name:       "resolves and matches the declared url",
			fetch:      vaultHolding(&vaultCredential{ID: testCredID, DisplayName: "GitHub Prod", MCPServerURL: testServerURL}),
			vaultID:    testVaultID,
			wantStatus: provider.StatusOK,
			wantName:   "GitHub Prod",
		},
		{
			// The vault answered. That makes absence a verdict, not a gap.
			name:       "absent from the vault is a failure",
			fetch:      func(context.Context, string, string) (*vaultCredential, error) { return nil, errCredentialNotFound },
			vaultID:    testVaultID,
			wantStatus: provider.StatusFailed,
			wantText:   []string{"does not exist in the vault", testCredID},
			denyText:   []string{"could not verify"},
		},
		{
			// The vault did not answer, so whether the credential exists is
			// simply not known — the distinction this feature turns on.
			name: "an unreachable vault is unknown, never missing",
			fetch: func(context.Context, string, string) (*vaultCredential, error) {
				return nil, errors.New("dial tcp 1.2.3.4:443: connect: connection refused")
			},
			vaultID:    testVaultID,
			wantStatus: provider.StatusUnknown,
			wantText:   []string{"could not verify"},
			denyText:   []string{"does not exist", "missing"},
		},
		{
			name:       "archived credential",
			fetch:      vaultHolding(&vaultCredential{ID: testCredID, DisplayName: "Old", Archived: true, MCPServerURL: testServerURL}),
			vaultID:    testVaultID,
			wantStatus: provider.StatusFailed,
			wantName:   "Old",
			wantText:   []string{"archived"},
		},
		{
			name:       "credential authenticates a different server",
			fetch:      vaultHolding(&vaultCredential{ID: testCredID, MCPServerURL: "https://mcp.elsewhere.com"}),
			vaultID:    testVaultID,
			wantStatus: provider.StatusFailed,
			wantText:   []string{"different server", "https://mcp.elsewhere.com", testServerURL},
		},
		{
			name: "an expired OAuth grant with no refresh is an unauthenticated connection",
			fetch: vaultHolding(&vaultCredential{
				ID: testCredID, MCPServerURL: testServerURL, AuthType: "mcp_oauth", ExpiresAt: &expired,
			}),
			vaultID:    testVaultID,
			wantStatus: provider.StatusFailed,
			wantText:   []string{"not authenticated", "expired"},
		},
		{
			// The platform renews a refreshable grant at dial time, so a past
			// expiry is not a readiness problem.
			name: "an expired grant that can refresh is fine",
			fetch: vaultHolding(&vaultCredential{
				ID: testCredID, MCPServerURL: testServerURL, AuthType: "mcp_oauth", ExpiresAt: &expired, HasRefresh: true,
			}),
			vaultID:    testVaultID,
			wantStatus: provider.StatusOK,
		},
		{
			name: "an unexpired grant is fine",
			fetch: vaultHolding(&vaultCredential{
				ID: testCredID, MCPServerURL: testServerURL, AuthType: "mcp_oauth", ExpiresAt: &future,
			}),
			vaultID:    testVaultID,
			wantStatus: provider.StatusOK,
		},
		{
			// No vault means the lookup cannot be attempted. That is unknown:
			// claiming the credential is missing would be inventing a verdict.
			name:       "no vault_id yields unknown, not failed",
			fetch:      vaultHolding(&vaultCredential{ID: testCredID, MCPServerURL: testServerURL}),
			vaultID:    "",
			wantStatus: provider.StatusUnknown,
			wantText:   []string{"no vault", "vault_id"},
			denyText:   []string{"does not exist"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := checkerProvider(tt.vaultID, tt.fetch)
			checks, err := p.Check(context.Background(), fullResource(t), loadObject(t, "full_api_response.json"))
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			got := findCheck(t, checks, checkCredential)

			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q (summary: %s)", got.Status, tt.wantStatus, got.Summary)
			}
			if got.Subject != testCredID {
				t.Errorf("subject = %q, want the credential id %q", got.Subject, testCredID)
			}
			if got.SubjectName != tt.wantName {
				t.Errorf("subject name = %q, want %q", got.SubjectName, tt.wantName)
			}
			text := strings.ToLower(got.Summary + " " + got.Detail)
			for _, want := range tt.wantText {
				if !strings.Contains(text, strings.ToLower(want)) {
					t.Errorf("check text %q does not contain %q", text, want)
				}
			}
			for _, deny := range tt.denyText {
				if strings.Contains(text, strings.ToLower(deny)) {
					t.Errorf("check text %q must not contain %q", text, deny)
				}
			}
		})
	}
}

// A server with no auth block is unauthenticated by declaration (SPEC.md
// §3.6), which is the normal case for a public server and not a fault.
func TestCheckUnauthenticatedServerIsNotAFailure(t *testing.T) {
	cfg := loadObject(t, "full_spec.json")
	delete(cfg["mcp_servers"].([]any)[0].(map[string]any), "auth_ref")

	p := checkerProvider(testVaultID, func(context.Context, string, string) (*vaultCredential, error) {
		t.Error("the vault was consulted for a server that references no credential")
		return nil, errCredentialNotFound
	})
	checks, err := p.Check(context.Background(),
		&provider.Resource{Addr: "agent.weather", Config: cfg},
		loadObject(t, "full_api_response.json"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := findCheck(t, checks, checkCredential)
	if got.Status != provider.StatusOK {
		t.Errorf("status = %q, want ok — declaring no auth is legal", got.Status)
	}
	if !strings.Contains(got.Summary, "unauthenticated by declaration") {
		t.Errorf("summary = %q, want it to say the connection is unauthenticated by declaration", got.Summary)
	}
}

func TestCheckToolPermissions(t *testing.T) {
	remoteWith := func(mutate func(configs []any)) provider.Object {
		remote := loadObject(t, "full_api_response.json")
		for _, raw := range remote["tools"].([]any) {
			mutate(raw.(map[string]any)["configs"].([]any))
		}
		return remote
	}

	t.Run("every declared tool granted", func(t *testing.T) {
		p := checkerProvider(testVaultID, vaultHolding(&vaultCredential{ID: testCredID, MCPServerURL: testServerURL}))
		checks, err := p.Check(context.Background(), fullResource(t), loadObject(t, "full_api_response.json"))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		seen := 0
		for _, c := range checks {
			if c.Kind != checkToolPermission {
				continue
			}
			seen++
			if c.Status != provider.StatusOK {
				t.Errorf("tool %q status = %q (%s), want ok", c.Subject, c.Status, c.Summary)
			}
		}
		if seen == 0 {
			t.Fatal("no tool permission checks were produced")
		}
	})

	// The KAS-57 outage as a check: the agent matches its spec and cannot
	// call the tool it declares.
	t.Run("a gated tool the spec grants unsupervised", func(t *testing.T) {
		remote := remoteWith(func(configs []any) {
			for _, raw := range configs {
				raw.(map[string]any)["permission_policy"] = map[string]any{"type": alwaysAskPolicy}
			}
		})
		p := checkerProvider(testVaultID, vaultHolding(&vaultCredential{ID: testCredID, MCPServerURL: testServerURL}))
		checks, err := p.Check(context.Background(), fullResource(t), remote)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		got := findCheck(t, checks, checkToolPermission)
		if got.Status != provider.StatusFailed {
			t.Errorf("status = %q, want failed", got.Status)
		}
		for _, want := range []string{alwaysAskPolicy, alwaysAllowPolicy} {
			if !strings.Contains(got.Summary, want) {
				t.Errorf("summary %q does not name %q", got.Summary, want)
			}
		}
		if !strings.Contains(got.Detail, "no human attached") {
			t.Errorf("detail = %q, want it to say what the gate costs", got.Detail)
		}
	})

	t.Run("a disabled tool", func(t *testing.T) {
		remote := remoteWith(func(configs []any) {
			for _, raw := range configs {
				raw.(map[string]any)["enabled"] = false
			}
		})
		p := checkerProvider(testVaultID, vaultHolding(&vaultCredential{ID: testCredID, MCPServerURL: testServerURL}))
		checks, err := p.Check(context.Background(), fullResource(t), remote)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		got := findCheck(t, checks, checkToolPermission)
		if got.Status != provider.StatusFailed {
			t.Errorf("status = %q, want failed", got.Status)
		}
		if !strings.Contains(got.Summary, "not granted") {
			t.Errorf("summary = %q, want it to say the tool is not granted", got.Summary)
		}
	})

	t.Run("a tool the remote does not list at all", func(t *testing.T) {
		remote := loadObject(t, "full_api_response.json")
		remote["tools"] = []any{}
		p := checkerProvider(testVaultID, vaultHolding(&vaultCredential{ID: testCredID, MCPServerURL: testServerURL}))
		checks, err := p.Check(context.Background(), fullResource(t), remote)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		failures := 0
		for _, c := range checks {
			if c.Kind == checkToolPermission && c.Status == provider.StatusFailed {
				failures++
			}
		}
		if failures == 0 {
			t.Fatalf("a remote granting no tools produced no failures: %+v", checks)
		}
	})
}

// Endpoints differing only in a trailing slash or in scheme/host case are the
// same server; a differing path is not.
func TestSameEndpoint(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"https://mcp.example.com", "https://mcp.example.com/", true},
		{"https://MCP.Example.com/mcp", "https://mcp.example.com/mcp", true},
		{"HTTPS://mcp.example.com", "https://mcp.example.com", true},
		{"https://mcp.example.com/a", "https://mcp.example.com/b", false},
		{"https://mcp.example.com", "https://other.example.com", false},
		{"https://mcp.example.com/mcp", "https://mcp.example.com", false},
	}
	for _, tt := range tests {
		if got := sameEndpoint(tt.a, tt.b); got != tt.want {
			t.Errorf("sameEndpoint(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// archived_at and expires_at are nullable on the wire but non-pointer
// time.Time on the SDK struct, so decoding goes through the raw JSON — the
// same reason Read does.
func TestDecodeCredentialDistinguishesNullFromZeroTime(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantArchived bool
		wantName     string
		wantExpires  bool
	}{
		{
			name:         "null archived_at and display_name",
			body:         `{"id":"cred_1","archived_at":null,"display_name":null,"auth":{"type":"static_bearer","mcp_server_url":"https://a"}}`,
			wantArchived: false,
			wantName:     "",
		},
		{
			name:         "archived",
			body:         `{"id":"cred_1","archived_at":"2026-01-02T03:04:05Z","display_name":"Old","auth":{"type":"static_bearer","mcp_server_url":"https://a"}}`,
			wantArchived: true,
			wantName:     "Old",
		},
		{
			name:        "oauth with an expiry",
			body:        `{"id":"cred_1","archived_at":null,"auth":{"type":"mcp_oauth","mcp_server_url":"https://a","expires_at":"2026-01-02T03:04:05Z"}}`,
			wantExpires: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeCredential(rawCredential(t, tt.body))
			if err != nil {
				t.Fatalf("decodeCredential: %v", err)
			}
			if got.Archived != tt.wantArchived {
				t.Errorf("Archived = %v, want %v", got.Archived, tt.wantArchived)
			}
			if got.DisplayName != tt.wantName {
				t.Errorf("DisplayName = %q, want %q", got.DisplayName, tt.wantName)
			}
			if (got.ExpiresAt != nil) != tt.wantExpires {
				t.Errorf("ExpiresAt = %v, want present=%v", got.ExpiresAt, tt.wantExpires)
			}
		})
	}
}
