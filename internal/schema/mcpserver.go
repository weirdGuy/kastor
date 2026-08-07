package schema

import (
	"fmt"
	"strings"
)

// MCPServer is an MCP server the module's tools bind to (SPEC.md §3.6).
// Declaring the server is what makes mcp://<server>/<tool> resolvable.
type MCPServer struct {
	Name      string     // block label, e.g. "hubspot" in mcp_server.hubspot
	Transport string     // "http" or "stdio"; defaulted to "http" at parse time
	URL       string     // http only
	Command   string     // stdio only
	Args      []string   // stdio only; nil when absent
	Auth      []*MCPAuth // declaration order; empty means unauthenticated everywhere
}

// Addr returns the block address used in references and diagnostics.
func (s *MCPServer) Addr() string { return "mcp_server." + s.Name }

// MCPAuth is one auth binding on an MCP server: where a credential lives,
// and which targets it applies to. Targets empty means the default binding.
type MCPAuth struct {
	Ref     string   // credential reference URI, e.g. "connection://cred_011CZ…"
	Targets []string // "target.<name>" references; empty is the default binding
}

// Credential reference schemes (SPEC.md §3.6). The set is closed in v0;
// unknown schemes are compile errors so new resolvers stay additive.
const (
	SchemeEnv        = "env"
	SchemeConnection = "connection"
)

// credentialSchemes lists the known schemes in the order diagnostics list them.
var credentialSchemes = []string{SchemeConnection, SchemeEnv}

// ParseCredentialRef splits an auth.ref into its scheme and value —
// "env://AIRTABLE_TOKEN" into ("env", "AIRTABLE_TOKEN"), and
// "connection://cred_011CZ…" into ("connection", "cred_011CZ…").
func ParseCredentialRef(ref string) (scheme, value string, err error) {
	scheme, value, found := strings.Cut(ref, "://")
	if !found || scheme == "" {
		return "", "", fmt.Errorf("credential ref %q is not a URI; expected %s", ref, credentialSchemeList())
	}
	if value == "" {
		return "", "", fmt.Errorf("credential ref %q names no credential; expected %s", ref, credentialSchemeList())
	}
	if scheme != SchemeEnv && scheme != SchemeConnection {
		return "", "", fmt.Errorf("credential ref %q uses unknown scheme %q; known schemes are %s", ref, scheme, credentialSchemeList())
	}
	return scheme, value, nil
}

func credentialSchemeList() string {
	quoted := make([]string, len(credentialSchemes))
	for i, s := range credentialSchemes {
		quoted[i] = s + "://…"
	}
	return strings.Join(quoted, " or ")
}

// AuthFor returns the auth binding that applies to targetAddr ("target.<name>"),
// following the same selection rule a tool's source blocks use (SPEC.md §3.3):
// the binding whose Targets names the target, otherwise the default binding.
// Reports false when the server is unauthenticated on that target.
func (s *MCPServer) AuthFor(targetAddr string) (*MCPAuth, bool) {
	var fallback *MCPAuth
	for _, auth := range s.Auth {
		if len(auth.Targets) == 0 {
			fallback = auth
			continue
		}
		for _, t := range auth.Targets {
			if t == targetAddr {
				return auth, true
			}
		}
	}
	if fallback != nil {
		return fallback, true
	}
	return nil, false
}

// ParseMCPURI splits an mcp:// tool source uri into the server name and the
// server's own name for the tool. The server segment is what §3.6 requires to
// match a declared mcp_server block.
func ParseMCPURI(uri string) (server, tool string, err error) {
	const prefix = "mcp://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", fmt.Errorf("MCP source uri is %q, expected mcp://<server>/<tool>", uri)
	}
	server, tool, found := strings.Cut(strings.TrimPrefix(uri, prefix), "/")
	if !found || server == "" || tool == "" || strings.Contains(tool, "/") {
		return "", "", fmt.Errorf("MCP source uri is %q, expected mcp://<server>/<tool>", uri)
	}
	return server, tool, nil
}
