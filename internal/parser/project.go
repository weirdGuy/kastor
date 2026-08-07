// Package parser decodes Kastor source files into the typed structs in
// internal/schema using hashicorp/hcl/v2.
package parser

import (
	"fmt"
	"math/big"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"github.com/weirdGuy/kastor/internal/schema"
)

// projectFileHCL mirrors the raw HCL layout of a project file. It exists only
// as a decode target; callers get the cleaned-up schema.ProjectFile.
type projectFileHCL struct {
	Models     []modelHCL     `hcl:"model,block"`
	Targets    []targetHCL    `hcl:"target,block"`
	MCPServers []mcpServerHCL `hcl:"mcp_server,block"`
}

type modelHCL struct {
	Label    string     `hcl:"name,label"`
	Provider string     `hcl:"provider"`
	ID       string     `hcl:"id"`
	Params   *paramsHCL `hcl:"params,block"`
}

// paramsHCL captures the params block as an open body: provider parameters
// are arbitrary key/value pairs, not a fixed schema.
type paramsHCL struct {
	Body hcl.Body `hcl:",remain"`
}

type targetHCL struct {
	Label   string   `hcl:"name,label"`
	Type    string   `hcl:"type"`
	Output  *string  `hcl:"output"`
	VaultID *string  `hcl:"vault_id"`
	Auth    *authHCL `hcl:"auth,block"`
}

type authHCL struct {
	APIKeyEnv string `hcl:"api_key_env"`
}

// mcpServerHCL mirrors an mcp_server block (SPEC.md §3.6). Every attribute is
// optional at decode time so the per-transport rules can be enforced with
// address-prefixed errors rather than generic gohcl diagnostics.
type mcpServerHCL struct {
	Label     string       `hcl:"name,label"`
	Transport *string      `hcl:"transport"`
	URL       *string      `hcl:"url"`
	Command   *string      `hcl:"command"`
	Args      *[]string    `hcl:"args"`
	Auth      []mcpAuthHCL `hcl:"auth,block"`
}

// mcpAuthHCL keeps targets as a raw expression: target.<name> entries are
// scope traversals, not string values (SPEC.md §3.6, mirroring §3.3).
type mcpAuthHCL struct {
	Ref     string         `hcl:"ref"`
	Targets hcl.Expression `hcl:"targets,optional"`
}

// ParseProjectFile reads and decodes a project file (kastor.hcl / .kastor).
func ParseProjectFile(path string) (*schema.ProjectFile, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}
	return ParseProject(path, src)
}

// ParseProject decodes project file source. filename is used in diagnostics.
func ParseProject(filename string, src []byte) (*schema.ProjectFile, error) {
	file, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, diags
	}

	var raw projectFileHCL
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, diags
	}

	project := &schema.ProjectFile{}

	seenModels := map[string]bool{}
	for _, m := range raw.Models {
		model := &schema.Model{
			Name:     m.Label,
			Provider: m.Provider,
			ID:       m.ID,
		}
		if seenModels[model.Name] {
			return nil, fmt.Errorf("%s: declared more than once", model.Addr())
		}
		seenModels[model.Name] = true

		if m.Params != nil {
			params, err := decodeParams(model.Addr(), m.Params.Body)
			if err != nil {
				return nil, err
			}
			model.Params = params
		}
		project.Models = append(project.Models, model)
	}

	seenTargets := map[string]bool{}
	for _, t := range raw.Targets {
		target := &schema.Target{
			Name: t.Label,
			Type: t.Type,
		}
		if seenTargets[target.Name] {
			return nil, fmt.Errorf("%s: declared more than once", target.Addr())
		}
		seenTargets[target.Name] = true

		if t.Output != nil {
			target.Output = *t.Output
		}
		if t.Auth != nil {
			target.Auth = &schema.Auth{APIKeyEnv: t.Auth.APIKeyEnv}
		}
		if t.VaultID != nil {
			target.VaultID = *t.VaultID
		}

		switch target.Type {
		case "codegen":
			if target.Output == "" {
				return nil, fmt.Errorf("%s: codegen target requires \"output\"", target.Addr())
			}
			if target.Auth != nil {
				return nil, fmt.Errorf("%s: codegen target does not allow \"auth\"", target.Addr())
			}
		case "platform":
			// auth is optional; credentials may come from the environment
			if t.Output != nil {
				return nil, fmt.Errorf("%s: platform target does not allow \"output\"", target.Addr())
			}
		default:
			return nil, fmt.Errorf("%s: invalid type %q (expected \"codegen\" or \"platform\")", target.Addr(), target.Type)
		}

		// SPEC.md §3.5: vault_id names the Anthropic vault backing this
		// target's connection:// refs, so it is meaningless anywhere else —
		// and meaningless fields are errors, not ignored.
		if t.VaultID != nil {
			if target.Name != claudeTargetName {
				return nil, fmt.Errorf("%s: \"vault_id\" is only valid on target %q, which selects the Claude Managed Agents provider", target.Addr(), claudeTargetName)
			}
			if target.VaultID == "" {
				return nil, fmt.Errorf("%s: \"vault_id\" cannot be empty; omit the attribute instead", target.Addr())
			}
		}
		project.Targets = append(project.Targets, target)
	}

	seenServers := map[string]bool{}
	for _, s := range raw.MCPServers {
		server, err := decodeMCPServer(s)
		if err != nil {
			return nil, err
		}
		if seenServers[server.Name] {
			return nil, fmt.Errorf("%s: declared more than once", server.Addr())
		}
		seenServers[server.Name] = true
		project.MCPServers = append(project.MCPServers, server)
	}

	return project, nil
}

// claudeTargetName is the target label that selects the Claude Managed Agents
// provider (SPEC.md §3.5: a platform target's label selects its provider).
const claudeTargetName = "claude_agents"

// decodeMCPServer decodes one mcp_server block and enforces the per-transport
// field rules of SPEC.md §3.6. Fields meaningless for a transport are errors,
// not ignored.
func decodeMCPServer(s mcpServerHCL) (*schema.MCPServer, error) {
	server := &schema.MCPServer{Name: s.Label, Transport: "http"}
	if s.Transport != nil {
		server.Transport = *s.Transport
	}
	addr := server.Addr()

	switch server.Transport {
	case "http":
		if s.URL == nil || *s.URL == "" {
			return nil, fmt.Errorf("%s: transport %q requires \"url\"", addr, server.Transport)
		}
		if s.Command != nil {
			return nil, fmt.Errorf("%s: transport %q does not allow \"command\"", addr, server.Transport)
		}
		if s.Args != nil {
			return nil, fmt.Errorf("%s: transport %q does not allow \"args\"", addr, server.Transport)
		}
		server.URL = *s.URL
	case "stdio":
		if s.Command == nil || *s.Command == "" {
			return nil, fmt.Errorf("%s: transport %q requires \"command\"", addr, server.Transport)
		}
		if s.URL != nil {
			return nil, fmt.Errorf("%s: transport %q does not allow \"url\"", addr, server.Transport)
		}
		// A spawned local process inherits the environment that spawned it,
		// so it has nowhere to put a credential reference.
		if len(s.Auth) > 0 {
			return nil, fmt.Errorf("%s: transport %q does not allow \"auth\"; a spawned local process inherits the environment that spawned it", addr, server.Transport)
		}
		server.Command = *s.Command
		if s.Args != nil {
			server.Args = *s.Args
		}
	default:
		return nil, fmt.Errorf("%s: invalid transport %q (expected \"http\" or \"stdio\")", addr, server.Transport)
	}

	defaults := 0
	boundTargets := map[string]bool{}
	for i, a := range s.Auth {
		authAddr := fmt.Sprintf("%s: auth block %d", addr, i+1)
		if _, _, err := schema.ParseCredentialRef(a.Ref); err != nil {
			return nil, fmt.Errorf("%s: %w", authAddr, err)
		}
		targets, err := refList(authAddr, "targets", "target", a.Targets)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			defaults++
			if defaults > 1 {
				return nil, fmt.Errorf("%s: more than one auth block without \"targets\"; at most one is the server's default binding", addr)
			}
		}
		for _, t := range targets {
			if boundTargets[t] {
				return nil, fmt.Errorf("%s: two auth blocks name %s; a target has exactly one binding", addr, t)
			}
			boundTargets[t] = true
		}
		server.Auth = append(server.Auth, &schema.MCPAuth{Ref: a.Ref, Targets: targets})
	}

	return server, nil
}

// decodeParams converts a params block into plain Go values.
func decodeParams(addr string, body hcl.Body) (map[string]any, error) {
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return nil, diags
	}

	params := make(map[string]any, len(attrs))
	for name, attr := range attrs {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return nil, diags
		}
		goVal, err := ctyToGo(val)
		if err != nil {
			return nil, fmt.Errorf("%s: param %q: %w", addr, name, err)
		}
		params[name] = goVal
	}
	return params, nil
}

// ctyToGo converts an HCL value to a plain Go value. Whole numbers become
// int64 so codegen emits 4096, not 4096.0.
func ctyToGo(v cty.Value) (any, error) {
	if v.IsNull() {
		return nil, nil
	}

	t := v.Type()
	switch {
	case t == cty.String:
		return v.AsString(), nil
	case t == cty.Bool:
		return v.True(), nil
	case t == cty.Number:
		bf := v.AsBigFloat()
		if i, acc := bf.Int64(); acc == big.Exact {
			return i, nil
		}
		f, _ := bf.Float64()
		return f, nil
	case t.IsTupleType() || t.IsListType() || t.IsSetType():
		var out []any
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			goVal, err := ctyToGo(ev)
			if err != nil {
				return nil, err
			}
			out = append(out, goVal)
		}
		return out, nil
	case t.IsObjectType() || t.IsMapType():
		out := make(map[string]any)
		for it := v.ElementIterator(); it.Next(); {
			kv, ev := it.Element()
			goVal, err := ctyToGo(ev)
			if err != nil {
				return nil, err
			}
			out[kv.AsString()] = goVal
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value type %s", t.FriendlyName())
}
