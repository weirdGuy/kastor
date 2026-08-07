package langgraph

import (
	"strings"
	"testing"

	"github.com/weirdGuy/kastor/internal/schema"
)

// TestMCPAuthEnvRejectsPlatformScheme covers the codegen half of §3.6's
// scheme matrix at the unit level rather than through a fixture: `kastor
// validate` rejects connection:// on every non-platform target, so a module
// carrying this pair never loads and the generator's own guard is
// unreachable from a testdata directory. It is kept as a backstop — the
// generator refusing to emit something it cannot mean — and tested here.
func TestMCPAuthEnvRejectsPlatformScheme(t *testing.T) {
	server := &schema.MCPServer{
		Name:      "hubspot",
		Transport: "http",
		URL:       "https://mcp.hubspot.com",
		Auth:      []*schema.MCPAuth{{Ref: "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"}},
	}
	bound := map[string]bool{"hubspot": true}

	_, err := mcpAuthEnv([]*schema.MCPServer{server}, bound, "target.langgraph")
	if err == nil {
		t.Fatal("mcpAuthEnv accepted a connection:// ref on a codegen target")
	}
	for _, want := range []string{"mcp_server.hubspot", "connection://cred_011CZkZDLs7fYzm1hXNPeRjv", "target.langgraph", "env://<NAME>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q\nwant substring %q", err, want)
		}
	}
}

// TestMCPAuthEnvSkipsUnboundServer pins the one asymmetry between the config
// file and the auth map: every declared server gets connection config, but a
// server no tool binds gets no auth wiring. `validate` deliberately ignores
// an unreferenced server's bindings (it is bound nowhere), so erroring on one
// here would fail a build for a module that validates clean.
func TestMCPAuthEnvSkipsUnboundServer(t *testing.T) {
	server := &schema.MCPServer{
		Name:      "hubspot",
		Transport: "http",
		URL:       "https://mcp.hubspot.com",
		Auth:      []*schema.MCPAuth{{Ref: "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"}},
	}

	env, err := mcpAuthEnv([]*schema.MCPServer{server}, map[string]bool{}, "target.langgraph")
	if err != nil {
		t.Fatalf("mcpAuthEnv on an unreferenced server: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("unreferenced server contributed auth wiring: %v", env)
	}
}

// TestMCPAuthEnvSelectsPerTargetBinding pins that a server authenticating
// differently per target (the §3.6 multi-target shape) contributes only the
// binding for the target being generated.
func TestMCPAuthEnvSelectsPerTargetBinding(t *testing.T) {
	server := &schema.MCPServer{
		Name:      "hubspot",
		Transport: "http",
		URL:       "https://mcp.hubspot.com",
		Auth: []*schema.MCPAuth{
			{Ref: "connection://cred_011CZkZDLs7fYzm1hXNPeRjv", Targets: []string{"target.claude_agents"}},
			{Ref: "env://HUBSPOT_TOKEN", Targets: []string{"target.langgraph"}},
		},
	}
	bound := map[string]bool{"hubspot": true}

	env, err := mcpAuthEnv([]*schema.MCPServer{server}, bound, "target.langgraph")
	if err != nil {
		t.Fatalf("mcpAuthEnv: %v", err)
	}
	if got := env["hubspot"]; got != "HUBSPOT_TOKEN" {
		t.Errorf("auth env for hubspot = %q, want %q", got, "HUBSPOT_TOKEN")
	}
}

// TestGenMCPServersEscapes pins that the emitted config is JSON, not string
// concatenation that happens to look like it: a url or command carrying a
// quote must not be able to break out of its literal.
func TestGenMCPServersEscapes(t *testing.T) {
	server := &schema.MCPServer{
		Name:      `odd"name`,
		Transport: "stdio",
		Command:   `say "hi"`,
		Args:      []string{`a"b`},
	}

	data, err := genMCPServers([]*schema.MCPServer{server})
	if err != nil {
		t.Fatalf("genMCPServers: %v", err)
	}
	for _, want := range []string{`"odd\"name"`, `"say \"hi\""`, `["a\"b"]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("generated config missing escaped %s:\n%s", want, data)
		}
	}
}
