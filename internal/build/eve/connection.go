package eve

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/getkastordev/kastor/internal/schema"
)

// mcpServer collects the tools one agent binds on one MCP server: eve
// connects per server (connections/<server>.ts), with the spec's pinned tool
// names as the connection's allow-list.
type mcpServer struct {
	Name             string
	Tools            []*schema.Tool    // kastor tool blocks bound to this server
	Allow            []string          // server-side tool names, sorted unique
	RequiresApproval []string          // gated server-side tool names, sorted unique
	Decl             *schema.MCPServer // the mcp_server block this binds (SPEC.md §3.6)
	AuthEnv          string            // env var holding the bearer token; "" unauthenticated
}

// parseMCPURI splits mcp://<server>/<tool> into its two parts.
func parseMCPURI(t *schema.Tool) (server, tool string, err error) {
	fail := func() (string, string, error) {
		return "", "", fmt.Errorf("%s: source uri %q: expected mcp://<server>/<tool>", t.Addr(), t.Source.URI)
	}
	u, parseErr := url.Parse(t.Source.URI)
	if parseErr != nil || u.Scheme != "mcp" || u.Host == "" {
		return fail()
	}
	tool = strings.TrimPrefix(u.Path, "/")
	if tool == "" || strings.Contains(tool, "/") {
		return fail()
	}
	return u.Host, tool, nil
}

// groupMCPServers buckets an agent's mcp-kind tools by server, in sorted
// server order with sorted unique allow-lists, resolving each against its
// declared mcp_server block and this target's auth binding, and marking the
// tools this agent gates behind approval.
func groupMCPServers(tools []*schema.Tool, agent *schema.Agent, decls map[string]*schema.MCPServer, targetAddr string) ([]*mcpServer, error) {
	byName := map[string]*mcpServer{}
	for _, t := range tools {
		server, name, err := parseMCPURI(t)
		if err != nil {
			return nil, err
		}
		s := byName[server]
		if s == nil {
			decl, ok := decls[server]
			if !ok {
				return nil, fmt.Errorf("%s: source uri %q names undeclared MCP server %q; declare mcp_server %q in the project file",
					t.Addr(), t.Source.URI, server, server)
			}
			s = &mcpServer{Name: server, Decl: decl}
			if s.AuthEnv, err = connectionAuthEnv(decl, targetAddr); err != nil {
				return nil, err
			}
			if err := checkConnectionTransport(decl); err != nil {
				return nil, err
			}
			byName[server] = s
		}
		s.Tools = append(s.Tools, t)
		s.Allow = append(s.Allow, name)
		if agent.ApprovalRequired(t.Addr()) {
			s.RequiresApproval = append(s.RequiresApproval, name)
		}
	}

	servers := make([]*mcpServer, 0, len(byName))
	for _, name := range sortedKeys(byName) {
		s := byName[name]
		sort.Slice(s.Tools, func(i, j int) bool { return s.Tools[i].Name < s.Tools[j].Name })
		sort.Strings(s.Allow)
		s.Allow = dedupe(s.Allow)
		sort.Strings(s.RequiresApproval)
		s.RequiresApproval = dedupe(s.RequiresApproval)
		servers = append(servers, s)
	}
	return servers, nil
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// checkConnectionTransport rejects the transports an eve connection cannot
// serve (SPEC.md §3.6): the generated connection is an HTTP client, so it
// cannot spawn a local process. `kastor validate` reports this first — this
// is the generator refusing to emit something it cannot mean.
func checkConnectionTransport(decl *schema.MCPServer) error {
	if decl.Transport != "http" {
		return fmt.Errorf("%s: transport %q is not supported by the eve target; the generated connection is an HTTP client — put an HTTP bridge in front of the server and declare its url", decl.Addr(), decl.Transport)
	}
	if decl.URL == "" {
		return fmt.Errorf("%s: declares no url", decl.Addr())
	}
	return nil
}

// connectionAuthEnv resolves the server's auth binding on this target to the
// environment variable holding its bearer token (SPEC.md §3.6). env:// is the
// only scheme this target supports: connection:// names a credential a
// platform holds, and a generated project is not on a platform.
func connectionAuthEnv(decl *schema.MCPServer, targetAddr string) (string, error) {
	auth, ok := decl.AuthFor(targetAddr)
	if !ok {
		return "", nil
	}
	scheme, value, err := schema.ParseCredentialRef(auth.Ref)
	if err != nil {
		return "", fmt.Errorf("%s: %w", decl.Addr(), err)
	}
	if scheme != schema.SchemeEnv {
		return "", fmt.Errorf("%s: auth ref %q cannot be bound on %s; a generated project holds no platform connections — use env://<NAME>",
			decl.Addr(), auth.Ref, targetAddr)
	}
	return value, nil
}

// genConnection emits connections/<server>.ts. The server's address is its
// mcp_server block's url (SPEC.md §3.6) and is emitted literally: a target's
// generated config no longer depends on the operator's shell. The pinned tool
// names become the connection's allow-list, so the agent discovers exactly
// the tools the spec bound and nothing else the server happens to expose.
//
// The credential is referenced, never held: an env:// ref names a variable
// this file reads in the user's own process. That read stays in the headers
// callback — first server contact — never at module top level, because eve
// build evaluates connection modules and a build must succeed without
// deployment credentials (CI has none).
func genConnection(s *mcpServer) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "// MCP server %q — bound by: %s.\n//\n// Generated by kastor build. Do not edit.\n//\n", s.Name, boundBy(s))
	fmt.Fprintf(&b, "// The endpoint comes from %s in the module's project file.\n", s.Decl.Addr())
	if s.AuthEnv != "" {
		fmt.Fprintf(&b, "// Its credential is referenced, never held: set %s in the environment\n// this agent runs in (auth ref env://%s).\n", s.AuthEnv, s.AuthEnv)
	} else {
		b.WriteString("// It declares no auth, so the connection is unauthenticated.\n")
	}
	b.WriteString("\nimport { defineMcpClientConnection } from \"eve/connections\";\n")
	if len(s.RequiresApproval) > 0 {
		values := make([]string, len(s.RequiresApproval))
		for i, name := range s.RequiresApproval {
			values[i] = tsString(name)
		}
		fmt.Fprintf(&b, "\nconst requiresApproval = new Set([%s]);\n", strings.Join(values, ", "))
	}
	b.WriteString("\nexport default defineMcpClientConnection({\n")
	fmt.Fprintf(&b, "  url: %s,\n", tsString(s.Decl.URL))
	fmt.Fprintf(&b, "  description: %s,\n", tsString(connectionDescription(s)))
	allow := make([]string, len(s.Allow))
	for i, name := range s.Allow {
		allow[i] = tsString(name)
	}
	fmt.Fprintf(&b, "  tools: { allow: [%s] },\n", strings.Join(allow, ", "))
	if len(s.RequiresApproval) > 0 {
		b.WriteString("  approval: ({ toolName }) => requiresApproval.has(toolName),\n")
	}
	if s.AuthEnv != "" {
		b.WriteString("  // Read at first server contact, not at build: eve build evaluates this\n")
		b.WriteString("  // module, so the credential check cannot live at the top level.\n")
		b.WriteString("  headers: () => {\n")
		fmt.Fprintf(&b, "    const token = process.env.%s;\n", s.AuthEnv)
		b.WriteString("    if (!token) {\n")
		fmt.Fprintf(&b, "      throw new Error(\n        %s,\n      );\n",
			tsString(fmt.Sprintf("kastor: MCP server %q needs %s set to its credential", s.Name, s.AuthEnv)))
		b.WriteString("    }\n")
		b.WriteString("    return { Authorization: `Bearer ${token}` };\n")
		b.WriteString("  },\n")
	}
	b.WriteString("});\n")
	return []byte(b.String())
}

// connectionDescription is the connection's routing description: the bound
// tool's description for a single-tool server, a per-tool summary otherwise.
func connectionDescription(s *mcpServer) string {
	if len(s.Tools) == 1 {
		return toolSummary(s.Tools[0])
	}
	parts := make([]string, 0, len(s.Tools))
	for _, t := range s.Tools {
		_, name, _ := parseMCPURI(t)
		parts = append(parts, name+": "+toolSummary(t))
	}
	return strings.Join(parts, " · ")
}

// boundBy lists the kastor block addresses behind a connection, for the
// header comment.
func boundBy(s *mcpServer) string {
	addrs := make([]string, 0, len(s.Tools))
	for _, t := range s.Tools {
		addrs = append(addrs, t.Addr())
	}
	return strings.Join(addrs, ", ")
}
