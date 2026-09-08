package eve

import (
	"strings"
	"testing"

	"github.com/getkastordev/kastor/internal/schema"
)

// TestConnectionAuthEnvRejectsPlatformScheme is the eve half of §3.6's scheme
// matrix. Like the langgraph case it is a unit test rather than a fixture:
// `kastor validate` rejects connection:// on every non-platform target, so a
// module carrying this pair never loads and the generator's guard is
// unreachable from a testdata directory.
func TestConnectionAuthEnvRejectsPlatformScheme(t *testing.T) {
	decl := &schema.MCPServer{
		Name:      "hubspot",
		Transport: "http",
		URL:       "https://mcp.hubspot.com",
		Auth:      []*schema.MCPAuth{{Ref: "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"}},
	}

	_, err := connectionAuthEnv(decl, "target.eve")
	if err == nil {
		t.Fatal("connectionAuthEnv accepted a connection:// ref on a codegen target")
	}
	for _, want := range []string{"mcp_server.hubspot", "connection://cred_011CZkZDLs7fYzm1hXNPeRjv", "target.eve", "env://<NAME>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q\nwant substring %q", err, want)
		}
	}
}

// TestConnectionAuthEnvUnauthenticated pins the normal case for a public
// server: no auth block means no binding and no headers callback to generate.
func TestConnectionAuthEnvUnauthenticated(t *testing.T) {
	decl := &schema.MCPServer{Name: "public", Transport: "http", URL: "https://mcp.example.com"}

	env, err := connectionAuthEnv(decl, "target.eve")
	if err != nil {
		t.Fatalf("connectionAuthEnv: %v", err)
	}
	if env != "" {
		t.Errorf("unauthenticated server resolved to variable %q", env)
	}

	generated := string(genConnection(&mcpServer{Name: "public", Decl: decl}))
	if strings.Contains(generated, "headers") {
		t.Errorf("unauthenticated connection emitted a headers callback:\n%s", generated)
	}
	if !strings.Contains(generated, `url: "https://mcp.example.com"`) {
		t.Errorf("connection does not dial the declared url:\n%s", generated)
	}
}

// TestGenConnectionHoldsNoCredential pins that an authenticated connection
// carries the variable's *name* and reads it at call time — the generated
// file never holds a token, and the read stays inside the callback so `eve
// build` (which evaluates this module) succeeds without deployment secrets.
func TestGenConnectionHoldsNoCredential(t *testing.T) {
	decl := &schema.MCPServer{
		Name:      "search-server",
		Transport: "http",
		URL:       "https://mcp.tavily.com/mcp",
		Auth:      []*schema.MCPAuth{{Ref: "env://TAVILY_API_KEY"}},
	}
	generated := string(genConnection(&mcpServer{Name: "search-server", Decl: decl, AuthEnv: "TAVILY_API_KEY"}))

	for _, want := range []string{
		`url: "https://mcp.tavily.com/mcp"`,
		"headers: () => {",
		"const token = process.env.TAVILY_API_KEY;",
		"Bearer ${token}",
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated connection missing %q:\n%s", want, generated)
		}
	}
	// The credential read must not escape the callback into module scope:
	// eve build evaluates connection modules and CI has no credentials.
	head, _, _ := strings.Cut(generated, "headers: () => {")
	if strings.Contains(head, "process.env") {
		t.Errorf("credential is read at module top level:\n%s", generated)
	}
}
