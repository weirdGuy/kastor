package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/getkastordev/kastor/internal/provider"
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

func TestNormalizeStateConfigMatchesGoldenEcho(t *testing.T) {
	desired := fullResource(t)
	stateConfig, err := New().NormalizeStateConfig(desired)
	if err != nil {
		t.Fatalf("NormalizeStateConfig: %v", err)
	}
	_, rules, err := normalizeSpec(desired)
	if err != nil {
		t.Fatalf("normalizeSpec: %v", err)
	}
	echo, err := normalizeAPIEcho(loadObject(t, "full_api_response.json"), rules)
	if err != nil {
		t.Fatalf("normalizeAPIEcho: %v", err)
	}
	if diff := cmp.Diff(echo, stateConfig); diff != "" {
		t.Errorf("normalized state != API echo (-echo +state):\n%s", diff)
	}

	diffs, err := New().Diff(&provider.Resource{Addr: desired.Addr, Config: stateConfig}, loadObject(t, "full_api_response.json"))
	if err != nil {
		t.Fatalf("Diff normalized state: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("normalized state has drift against its API echo: %#v", diffs)
	}
}

// TestGoldenEchoToolConfigsCarryPermissions pins the response shape the rest
// of the suite is written against: the API returns a permission on every tool
// config, so a fixture that omits one would hide exactly the attribute KAS-57
// is about.
func TestGoldenEchoToolConfigsCarryPermissions(t *testing.T) {
	echo := loadObject(t, "full_api_response.json")
	for i, rawToolset := range echo["tools"].([]any) {
		toolset := rawToolset.(map[string]any)
		for j, rawConfig := range toolset["configs"].([]any) {
			config := rawConfig.(map[string]any)
			if len(config) != 3 || config["name"] == nil || config["enabled"] == nil {
				t.Errorf("tools[%d].configs[%d] = %#v, want name, enabled and permission_policy", i, j, config)
			}
			policy, ok := config["permission_policy"].(map[string]any)
			if !ok || policy["type"] == "" {
				t.Errorf("tools[%d].configs[%d].permission_policy = %#v, want a typed policy", i, j, config["permission_policy"])
			}
		}
	}
}

// A server's address is spec (SPEC.md §3.6), so editing the mcp_server block's
// url is an ordinary visible diff — the property the environment-derived URL
// could not offer, since it compared one operator's shell against another's.
// The last-applied config recorded in state is unaffected by that edit: it is
// the resolved config from the apply that wrote it.
func TestMCPServerURLChangeIsAVisibleDiff(t *testing.T) {
	desired := fullResource(t)
	stateConfig, err := New().NormalizeStateConfig(desired)
	if err != nil {
		t.Fatalf("NormalizeStateConfig: %v", err)
	}

	diffs, err := New().Diff(
		&provider.Resource{Addr: desired.Addr, Config: stateConfig},
		loadObject(t, "full_api_response.json"),
	)
	if err != nil {
		t.Fatalf("Diff normalized state: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("last-applied state does not round-trip: %#v", diffs)
	}

	edited := loadObject(t, "full_spec.json")
	edited["mcp_servers"].([]any)[0].(map[string]any)["url"] = "https://changed.example.com/mcp"
	diffs, err = New().Diff(&provider.Resource{Addr: desired.Addr, Config: edited}, loadObject(t, "full_api_response.json"))
	if err != nil {
		t.Fatalf("Diff edited desired: %v", err)
	}
	if diff := cmp.Diff([]string{"mcp_servers[0]"}, paths(diffs)); diff != "" {
		t.Errorf("edited MCP URL change paths (-want +got):\n%s", diff)
	}
}

// The credential ref reaches the provider in the closure and must not reach
// the platform: kastor sends the server's name and URL only (SPEC.md §3.6).
func TestMCPConnectionOmitsCredentialRef(t *testing.T) {
	request, err := normalizeAPIRequest(fullResource(t))
	if err != nil {
		t.Fatalf("normalizeAPIRequest: %v", err)
	}
	servers, ok := request["mcp_servers"].([]any)
	if !ok || len(servers) == 0 {
		t.Fatalf("request declares no mcp_servers: %#v", request["mcp_servers"])
	}
	for i, raw := range servers {
		server := raw.(map[string]any)
		if got := len(server); got != 3 {
			t.Errorf("mcp_servers[%d] = %#v, want exactly type, name and url", i, server)
		}
		for _, key := range []string{"auth_ref", "auth", "transport"} {
			if _, exists := server[key]; exists {
				t.Errorf("mcp_servers[%d] leaks %q to the platform: %#v", i, key, server)
			}
		}
	}
}

// TestNormalizeRequestAuthorsEveryDeclaredToolPolicy pins both halves of the
// grant: each declared tool is enabled, and requires_approval selects ask
// without changing which tools the agent may call.
func TestNormalizeRequestAuthorsEveryDeclaredToolPolicy(t *testing.T) {
	request, err := normalizeAPIRequest(fullResource(t))
	if err != nil {
		t.Fatalf("normalizeAPIRequest: %v", err)
	}

	wantPolicies := map[string]string{
		"read":      alwaysAllowPolicy,
		"write":     alwaysAskPolicy,
		"get_issue": alwaysAskPolicy,
	}
	granted := map[string]any{}
	for i, rawToolset := range request["tools"].([]any) {
		toolset := rawToolset.(map[string]any)
		for j, rawConfig := range toolset["configs"].([]any) {
			config := rawConfig.(map[string]any)
			want := map[string]any{"type": wantPolicies[config["name"].(string)]}
			if diff := cmp.Diff(want, config["permission_policy"]); diff != "" {
				t.Errorf("request.tools[%d].configs[%d] permission (-want +got):\n%s", i, j, diff)
			}
			granted[config["name"].(string)] = config["permission_policy"]
		}
		// The MCP toolset default governs the tools the agent does not
		// declare, so the request must still leave it to the platform.
		if toolset["type"] != mcpToolsetType {
			continue
		}
		defaultConfig := toolset["default_config"].(map[string]any)
		if _, exists := defaultConfig["permission_policy"]; exists {
			t.Errorf("request.tools[%d].default_config authors permission_policy: %#v", i, defaultConfig)
		}
	}

	for _, name := range []string{"read", "write", "get_issue"} {
		if granted[name] == nil {
			t.Errorf("request does not grant declared tool %q: granted = %v", name, granted)
		}
	}
}

// TestUpdateParamsAuthorsEveryDeclaredToolPolicy covers the reconcile half:
// console edits are replaced by the exact allow/ask split in the spec.
func TestUpdateParamsAuthorsEveryDeclaredToolPolicy(t *testing.T) {
	params, err := normalizeUpdateParams(fullResource(t), 7)
	if err != nil {
		t.Fatalf("normalizeUpdateParams: %v", err)
	}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal update params: %v", err)
	}
	var request provider.Object
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode update params: %v", err)
	}

	wantPolicies := map[string]string{
		"read":      alwaysAllowPolicy,
		"write":     alwaysAskPolicy,
		"get_issue": alwaysAskPolicy,
	}
	for i, rawToolset := range request["tools"].([]any) {
		toolset := rawToolset.(map[string]any)
		for j, rawConfig := range toolset["configs"].([]any) {
			config := rawConfig.(map[string]any)
			want := map[string]any{"type": wantPolicies[config["name"].(string)]}
			if diff := cmp.Diff(want, config["permission_policy"]); diff != "" {
				t.Errorf("update.tools[%d].configs[%d] permission (-want +got):\n%s", i, j, diff)
			}
		}
	}
}

// TestDiffReportsConsoleSideApprovalChangeAsDrift is KAS-66's negative case:
// removing a required gate outside kastor is drift, not a silent no-op.
func TestDiffReportsConsoleSideApprovalChangeAsDrift(t *testing.T) {
	desired := fullResource(t)
	stateConfig, err := New().NormalizeStateConfig(desired)
	if err != nil {
		t.Fatalf("NormalizeStateConfig: %v", err)
	}

	remote := loadObject(t, "full_api_response.json")
	mcpToolset := remote["tools"].([]any)[1].(map[string]any)
	denied := mcpToolset["configs"].([]any)[0].(map[string]any)
	denied["permission_policy"] = map[string]any{"type": alwaysAllowPolicy}

	for _, tt := range []struct {
		name   string
		config provider.Object
	}{
		{name: "against the spec", config: desired.Config},
		{name: "against last-applied state", config: stateConfig},
	} {
		t.Run(tt.name, func(t *testing.T) {
			diffs, err := New().Diff(&provider.Resource{Addr: desired.Addr, Config: tt.config}, remote)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if diff := cmp.Diff([]string{"tools[1]"}, paths(diffs)); diff != "" {
				t.Fatalf("drift paths (-want +got):\n%s", diff)
			}

			old := diffs[0].Old.(map[string]any)["configs"].([]any)[0].(map[string]any)
			if got := old["permission_policy"]; !cmp.Equal(got, map[string]any{"type": alwaysAllowPolicy}) {
				t.Errorf("drift does not report the removed remote gate: %#v", got)
			}
			want := diffs[0].New.(map[string]any)["configs"].([]any)[0].(map[string]any)
			if got := want["permission_policy"]; !cmp.Equal(got, map[string]any{"type": alwaysAskPolicy}) {
				t.Errorf("drift does not name the approval gate kastor will restore: %#v", got)
			}
		})
	}
}

// TestDiffAcceptsPreKAS57StateConfigs keeps the upgrade path quiet: a config
// written before kastor authored permissions must still normalize, so plan
// reports the remote denial rather than failing to read its own state.
func TestDiffAcceptsPreKAS57StateConfigs(t *testing.T) {
	desired := fullResource(t)
	legacy, err := New().NormalizeStateConfig(desired)
	if err != nil {
		t.Fatalf("NormalizeStateConfig: %v", err)
	}
	for _, rawToolset := range legacy["tools"].([]any) {
		toolset := rawToolset.(map[string]any)
		for _, rawConfig := range toolset["configs"].([]any) {
			delete(rawConfig.(map[string]any), "permission_policy")
		}
	}

	remote := loadObject(t, "full_api_response.json")
	for _, rawToolset := range remote["tools"].([]any) {
		for _, rawConfig := range rawToolset.(map[string]any)["configs"].([]any) {
			rawConfig.(map[string]any)["permission_policy"] = map[string]any{"type": alwaysAllowPolicy}
		}
	}
	diffs, err := New().Diff(
		&provider.Resource{Addr: desired.Addr, Config: legacy},
		remote,
	)
	if err != nil {
		t.Fatalf("Diff pre-KAS-57 state config: %v", err)
	}
	if len(diffs) != 0 {
		t.Errorf("pre-KAS-57 state config drifts against a granted remote: %#v", diffs)
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

func TestNormalizeSpecModelSupportsOnlyDocumentedFields(t *testing.T) {
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

// TestDiffCreatePathValidatesTheSpec is the KAS-55 contract: a nil remote is
// how the engine asks "could this be created?", so every mapping error must
// surface there instead of waiting for apply.
func TestDiffCreatePathValidatesTheSpec(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(provider.Object)
		wantErr []string
	}{
		{
			name: "non-anthropic model provider",
			mutate: func(cfg provider.Object) {
				cfg["model"].(map[string]any)["provider"] = "openai"
			},
			wantErr: []string{"agent.weather", "model.provider", "openai", "anthropic"},
		},
		{
			name: "unsupported model param",
			mutate: func(cfg provider.Object) {
				cfg["model"].(map[string]any)["params"] = map[string]any{"temperature": 0.2}
			},
			wantErr: []string{"agent.weather", "model.params.temperature", "unsupported", "speed"},
		},
		{
			name:    "http tool source",
			mutate:  replaceToolsWithKind("weather_http", "http"),
			wantErr: []string{"tool.weather_http", `source kind "http"`, "MCP-server wrapper"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadObject(t, "full_spec.json")
			tt.mutate(cfg)

			_, err := New().Diff(&provider.Resource{Addr: "agent.weather", Config: cfg}, nil)
			if err == nil {
				t.Fatal("Diff against an absent remote succeeded, want validation error")
			}
			for _, text := range tt.wantErr {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("error %q does not contain %q", err, text)
				}
			}
		})
	}
}

func TestDiffCreatePathReturnsTheAttributesCreateWillSet(t *testing.T) {
	diffs, err := New().Diff(fullResource(t), nil)
	if err != nil {
		t.Fatalf("Diff against an absent remote: %v", err)
	}

	paths := map[string]bool{}
	for _, d := range diffs {
		if d.Old != nil {
			t.Errorf("%s: Old = %v, want nil — nothing exists remotely yet", d.Path, d.Old)
		}
		if d.New == nil {
			t.Errorf("%s: unset attribute reported as an addition", d.Path)
		}
		paths[d.Path] = true
	}
	for _, want := range []string{"name", "model", "system", "description", "tools", "mcp_servers"} {
		if !paths[want] {
			t.Errorf("create diffs do not set %s: %+v", want, diffs)
		}
	}
	// The ownership marker is stamped by Create, not user configuration.
	if paths["metadata."+managedMarkerKey] {
		t.Errorf("create diffs expose the %s marker: %+v", managedMarkerKey, diffs)
	}
}

// The connection's address is spec, not environment (SPEC.md §3.6): a closure
// whose MCP tool names a server the closure does not declare is a provider
// error, so plan fails rather than apply sending a connection with no URL.
func TestDiffRejectsUndeclaredMCPServer(t *testing.T) {
	cfg := loadObject(t, "full_spec.json")
	delete(cfg, "mcp_servers")

	_, err := New().Diff(
		&provider.Resource{Addr: "agent.weather", Config: cfg},
		loadObject(t, "full_api_response.json"),
	)
	if err == nil {
		t.Fatal("Diff succeeded with no mcp_server declared for the tool")
	}
	for _, text := range []string{"tool.github_get_issue", `MCP server "github"`, "mcp_server block"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
		}
	}
}

// A stdio server has no URL for the platform to dial, and §3.6's transport
// matrix makes that an error rather than a connection that fails at run time.
func TestDiffRejectsStdioMCPServer(t *testing.T) {
	cfg := loadObject(t, "full_spec.json")
	cfg["mcp_servers"] = []any{map[string]any{
		"name":      "github",
		"transport": "stdio",
		"command":   "uvx",
	}}

	_, err := New().Diff(
		&provider.Resource{Addr: "agent.weather", Config: cfg},
		loadObject(t, "full_api_response.json"),
	)
	if err == nil {
		t.Fatal("Diff succeeded with a stdio MCP server")
	}
	for _, text := range []string{"mcp_server.github", `"stdio"`, "dials a URL"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not contain %q", err, text)
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

func replaceToolsWithKind(name, kind string) func(provider.Object) {
	return func(cfg provider.Object) {
		cfg["tools"] = []any{map[string]any{
			"name":   name,
			"source": map[string]any{"kind": kind},
		}}
	}
}

func fullResource(t *testing.T) *provider.Resource {
	t.Helper()
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
