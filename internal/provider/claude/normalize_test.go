package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/kastor/internal/provider"
)

const fullMCPURL = "https://api.githubcopilot.com/mcp/"

func TestNormalizeGoldenResponses(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		spec     string
		response string
		mcp      bool
	}{
		{
			name:     "every managed field",
			addr:     "agent.weather",
			spec:     "full_spec.json",
			response: "full_api_response.json",
			mcp:      true,
		},
		{
			name:     "server defaults and empty arrays",
			addr:     "agent.minimal",
			spec:     "minimal_spec.json",
			response: "minimal_api_response.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mcp {
				setFullMCPEnv(t)
			}
			desired := &provider.Resource{Addr: tt.addr, Config: loadObject(t, tt.spec)}
			spec, rules, err := normalizeSpec(desired)
			if err != nil {
				t.Fatalf("normalizeSpec: %v", err)
			}
			echo, err := normalizeAPIEcho(loadObject(t, tt.response), rules)
			if err != nil {
				t.Fatalf("normalizeAPIEcho: %v", err)
			}
			if diff := cmp.Diff(spec, echo); diff != "" {
				t.Errorf("normalized spec != normalized API echo (-spec +echo):\n%s", diff)
			}
		})
	}
}

func TestNormalizeModelStringAndDefaults(t *testing.T) {
	got, err := normalizeEchoModel("claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("normalizeEchoModel: %v", err)
	}
	want := map[string]any{"id": "claude-sonnet-4-5", "speed": "standard"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("model normalization mismatch (-want +got):\n%s", diff)
	}
}

func TestNormalizeSpecModelSupportsOnlySDKFields(t *testing.T) {
	cfg := loadObject(t, "minimal_spec.json")
	cfg["model"].(map[string]any)["params"] = map[string]any{"speed": "fast"}
	spec, _, err := normalizeSpec(&provider.Resource{Addr: "agent.minimal", Config: cfg})
	if err != nil {
		t.Fatalf("normalizeSpec: %v", err)
	}
	want := map[string]any{"id": "claude-sonnet-4-5", "speed": "fast"}
	if diff := cmp.Diff(want, spec["model"]); diff != "" {
		t.Errorf("model mismatch (-want +got):\n%s", diff)
	}
}

func TestMappingValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(provider.Object)
		wantErr []string
	}{
		{
			name: "non-anthropic model",
			mutate: func(cfg provider.Object) {
				cfg["model"].(map[string]any)["provider"] = "openai"
			},
			wantErr: []string{"agent.weather", "model.provider", "openai", "anthropic"},
		},
		{
			name: "unsupported model effort",
			mutate: func(cfg provider.Object) {
				cfg["model"].(map[string]any)["params"] = map[string]any{
					"effort": map[string]any{"type": "high"},
				}
			},
			wantErr: []string{"agent.weather", "model.params.effort", "unsupported", "speed"},
		},
		{
			name:    "http tool",
			mutate:  replaceToolsWithKind("weather_http", "http"),
			wantErr: []string{"tool.weather_http", `source kind "http"`, "client-executed", "not a runtime", "MCP-server wrapper"},
		},
		{
			name:    "script tool",
			mutate:  replaceToolsWithKind("deploy", "script"),
			wantErr: []string{"tool.deploy", `source kind "script"`, "client-executed", "not a runtime", "MCP-server wrapper"},
		},
		{
			name:    "runtime tool",
			mutate:  replaceToolsWithKind("lookup", "runtime"),
			wantErr: []string{"tool.lookup", `source kind "runtime"`, "client-executed", "not a runtime", "MCP-server wrapper"},
		},
		{
			name: "malformed MCP identity",
			mutate: func(cfg provider.Object) {
				cfg["tools"] = []any{map[string]any{
					"name":   "issues",
					"source": map[string]any{"kind": "mcp", "uri": "https://mcp.example.com"},
				}}
			},
			wantErr: []string{"tool.issues", "https://mcp.example.com", "mcp://<server>/<tool>"},
		},
		{
			name: "unknown builtin",
			mutate: func(cfg provider.Object) {
				cfg["tools"] = []any{map[string]any{
					"name":   "database_admin",
					"source": map[string]any{"kind": "builtin"},
				}}
			},
			wantErr: []string{"tool.database_admin", "builtin tool", agentToolsetType},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadObject(t, "full_spec.json")
			tt.mutate(cfg)
			_, _, err := normalizeSpec(&provider.Resource{Addr: "agent.weather", Config: cfg})
			if err == nil {
				t.Fatal("normalizeSpec succeeded, want validation error")
			}
			for _, text := range tt.wantErr {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("error %q does not contain %q", err, text)
				}
			}
		})
	}
}

func TestDiffRequiresMCPServerURLAtPlanTime(t *testing.T) {
	t.Setenv("KASTOR_MCP_GITHUB_URL", "")
	_, err := New().Diff(
		&provider.Resource{Addr: "agent.weather", Config: loadObject(t, "full_spec.json")},
		loadObject(t, "full_api_response.json"),
	)
	if err == nil {
		t.Fatal("Diff succeeded without an MCP endpoint URL")
	}
	for _, text := range []string{"tool.github_get_issue", `MCP server "github"`, "KASTOR_MCP_GITHUB_URL"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
}

func TestMCPServerURLEnvNameMatchesEveConvention(t *testing.T) {
	tests := map[string]string{
		"github":               "KASTOR_MCP_GITHUB_URL",
		"github-enterprise.v2": "KASTOR_MCP_GITHUB_ENTERPRISE_V2_URL",
		"server 42":            "KASTOR_MCP_SERVER_42_URL",
	}
	for server, want := range tests {
		if got := mcpServerURLEnvName(server); got != want {
			t.Errorf("mcpServerURLEnvName(%q) = %q, want %q", server, got, want)
		}
	}
}

func TestToolsetPermissionDefaults(t *testing.T) {
	agentRequest := newToolset(agentToolsetType, "").object
	mcpRequest := newToolset(mcpToolsetType, "github").object

	agentDefault := agentRequest["default_config"].(map[string]any)
	if got := agentDefault["permission_policy"]; !cmp.Equal(got, map[string]any{"type": "always_allow"}) {
		t.Errorf("agent request permission policy = %#v, want always_allow", got)
	}
	mcpDefault := mcpRequest["default_config"].(map[string]any)
	if _, exists := mcpDefault["permission_policy"]; exists {
		t.Errorf("MCP request unexpectedly authors permission_policy: %#v", mcpDefault)
	}

	normalized := normalizeSpecToolsetDefaults([]any{agentRequest, mcpRequest})
	normalizedMCPDefault := normalized[1].(map[string]any)["default_config"].(map[string]any)
	if got := normalizedMCPDefault["permission_policy"]; !cmp.Equal(got, map[string]any{"type": "always_ask"}) {
		t.Errorf("normalized MCP permission policy = %#v, want always_ask", got)
	}
}

func TestLifecycleOperationsAreExplicitStubs(t *testing.T) {
	ctx := context.Background()
	p := New()
	resource := &provider.Resource{Addr: "agent.a", Config: provider.Object{}}

	_, _, readErr := p.Read(ctx, "agent_1")
	_, createErr := p.Create(ctx, resource)
	updateErr := p.Update(ctx, "agent_1", resource)
	deleteErr := p.Delete(ctx, "agent_1")
	for name, err := range map[string]error{
		"Read": readErr, "Create": createErr, "Update": updateErr, "Delete": deleteErr,
	} {
		if !errors.Is(err, ErrLifecycleNotImplemented) {
			t.Errorf("%s error = %v, want ErrLifecycleNotImplemented", name, err)
		}
	}
}

func replaceToolsWithKind(name, kind string) func(provider.Object) {
	return func(cfg provider.Object) {
		cfg["tools"] = []any{map[string]any{
			"name":   name,
			"source": map[string]any{"kind": kind},
		}}
	}
}

func setFullMCPEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KASTOR_MCP_GITHUB_URL", fullMCPURL)
}

func fullResource(t *testing.T) *provider.Resource {
	t.Helper()
	setFullMCPEnv(t)
	return &provider.Resource{Addr: "agent.weather", Config: loadObject(t, "full_spec.json")}
}

func loadObject(t *testing.T, name string) provider.Object {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var object provider.Object
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return object
}
