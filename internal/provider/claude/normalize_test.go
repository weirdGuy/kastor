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

func TestNormalizeGoldenResponses(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		spec     string
		response string
	}{
		{
			name:     "every managed field",
			addr:     "agent.weather",
			spec:     "full_spec.json",
			response: "full_api_response.json",
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
	got, err := normalizeEchoModel("claude-sonnet-4-5", map[string]bool{"id": true, "speed": true})
	if err != nil {
		t.Fatalf("normalizeEchoModel: %v", err)
	}
	want := map[string]any{"id": "claude-sonnet-4-5", "speed": "standard"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("model normalization mismatch (-want +got):\n%s", diff)
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
