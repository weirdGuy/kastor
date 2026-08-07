package provider

import (
	"encoding/json"
	"fmt"

	"github.com/weirdGuy/kastor/internal/module"
	"github.com/weirdGuy/kastor/internal/schema"
)

// DesiredConfig renders one agent's closure — the agent block plus the
// model, prompt, tools, and MCP servers it references — into the neutral
// configuration providers consume. Optional fields are omitted when empty.
// The result is normalized through a JSON round-trip so every number is a
// float64 and equality never depends on Go-side types.
//
// tgt selects the per-target auth binding on each MCP server (SPEC.md §3.6);
// nothing else in the closure varies by target in v0.
//
// An input default that references another agent's output is deliberately
// not part of the config: references are ordering-only in v0 (SPEC.md §4),
// so changing one must not read as a remote update.
func DesiredConfig(mod *module.Module, a *schema.Agent, tgt *schema.Target) (Object, error) {
	cfg := map[string]any{}
	if a.Description != "" {
		cfg["description"] = a.Description
	}

	mdl, err := lookupBlock[*schema.Model](mod, a, a.Model)
	if err != nil {
		return nil, err
	}
	modelCfg := map[string]any{"provider": mdl.Provider, "id": mdl.ID}
	if len(mdl.Params) > 0 {
		modelCfg["params"] = mdl.Params
	}
	cfg["model"] = modelCfg

	if a.SystemPrompt != "" {
		prompt, err := lookupBlock[*schema.Prompt](mod, a, a.SystemPrompt)
		if err != nil {
			return nil, err
		}
		cfg["instructions"] = prompt.Body
	}

	var tools []any
	var servers []any
	seenServers := map[string]bool{}
	for _, ref := range a.Tools {
		tool, err := lookupBlock[*schema.Tool](mod, a, ref)
		if err != nil {
			return nil, err
		}
		tools = append(tools, toolConfig(tool))

		// An MCP tool drags its server's connection config into the closure:
		// on the platform path apply *is* the deployment, so the server's
		// address has nowhere to come from but the spec (SPEC.md §3.6).
		server, err := mcpServerFor(mod, a, tool)
		if err != nil {
			return nil, err
		}
		if server == nil || seenServers[server.Name] {
			continue
		}
		seenServers[server.Name] = true
		servers = append(servers, mcpServerConfig(server, tgt))
	}
	if len(tools) > 0 {
		cfg["tools"] = tools
	}
	if len(servers) > 0 {
		cfg["mcp_servers"] = servers
	}

	var inputs []any
	for _, in := range a.Inputs {
		ic := map[string]any{"name": in.Name, "type": in.Type}
		if in.Description != "" {
			ic["description"] = in.Description
		}
		if in.Optional {
			ic["optional"] = true
		}
		if in.Default != nil {
			ic["default"] = in.Default
		}
		inputs = append(inputs, ic)
	}
	if len(inputs) > 0 {
		cfg["inputs"] = inputs
	}

	var outputs []any
	for _, out := range a.Outputs {
		oc := map[string]any{"name": out.Name, "type": out.Type}
		if out.Description != "" {
			oc["description"] = out.Description
		}
		outputs = append(outputs, oc)
	}
	if len(outputs) > 0 {
		cfg["outputs"] = outputs
	}

	return normalize(cfg)
}

// toolConfig renders one tool's interface + source.
func toolConfig(t *schema.Tool) map[string]any {
	tc := map[string]any{"name": t.Name}
	if t.Description != "" {
		tc["description"] = t.Description
	}
	var params []any
	for _, p := range t.Params {
		pc := map[string]any{"name": p.Name, "type": p.Type}
		if p.Description != "" {
			pc["description"] = p.Description
		}
		if p.Default != nil {
			pc["default"] = p.Default
		}
		params = append(params, pc)
	}
	if len(params) > 0 {
		tc["params"] = params
	}
	tc["returns"] = map[string]any{"type": t.Returns.Type}
	src := map[string]any{"kind": t.Source.Kind}
	if t.Source.URI != "" {
		src["uri"] = t.Source.URI
	}
	tc["source"] = src
	return tc
}

// mcpServerFor resolves the mcp_server block a tool's source binds to, or nil
// when the tool is not an MCP tool. Both failure modes here mean the module
// skipped the validate pipeline, which resolves these references.
func mcpServerFor(mod *module.Module, a *schema.Agent, t *schema.Tool) (*schema.MCPServer, error) {
	if t.Source == nil || t.Source.Kind != "mcp" {
		return nil, nil
	}
	name, _, err := schema.ParseMCPURI(t.Source.URI)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", a.Addr(), t.Addr(), err)
	}
	sym, ok := mod.Lookup("mcp_server." + name)
	if !ok {
		return nil, fmt.Errorf("%s: %s: unknown reference mcp_server.%s", a.Addr(), t.Addr(), name)
	}
	server, ok := sym.Block.(*schema.MCPServer)
	if !ok {
		return nil, fmt.Errorf("%s: %s: mcp_server.%s is not an mcp_server block", a.Addr(), t.Addr(), name)
	}
	return server, nil
}

// mcpServerConfig renders one MCP server for the given target. The auth ref is
// recorded verbatim and the credential's *value* never appears — that is what
// keeps the state file free of secrets (SPEC.md §5.1).
func mcpServerConfig(s *schema.MCPServer, tgt *schema.Target) map[string]any {
	sc := map[string]any{"name": s.Name, "transport": s.Transport}
	if s.URL != "" {
		sc["url"] = s.URL
	}
	if s.Command != "" {
		sc["command"] = s.Command
	}
	if len(s.Args) > 0 {
		args := make([]any, len(s.Args))
		for i, a := range s.Args {
			args[i] = a
		}
		sc["args"] = args
	}
	if tgt != nil {
		if auth, ok := s.AuthFor(tgt.Addr()); ok {
			sc["auth_ref"] = auth.Ref
		}
	}
	return sc
}

// MarshalConfig serializes a config canonically: encoding/json sorts map
// keys, so equal configs always produce identical bytes. This is the form
// stored in the state file and compared for staleness.
func MarshalConfig(o Object) (json.RawMessage, error) {
	data, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	return data, nil
}

// normalize round-trips a value tree through JSON so it lands exactly on
// the encoding/json value model (all numbers float64).
func normalize(cfg map[string]any) (Object, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	var out Object
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("normalizing config: %w", err)
	}
	return out, nil
}

// lookupBlock resolves a reference from an agent against the module and
// asserts its block type. Failures here mean the module was not run through
// the validate pipeline first — that is a bug in the caller, but it still
// surfaces as a diagnostic with the block address rather than a panic.
func lookupBlock[T any](mod *module.Module, a *schema.Agent, ref string) (T, error) {
	var zero T
	sym, ok := mod.Lookup(ref)
	if !ok {
		return zero, fmt.Errorf("%s: unknown reference %s", a.Addr(), ref)
	}
	block, ok := sym.Block.(T)
	if !ok {
		return zero, fmt.Errorf("%s: reference %s is not the expected block kind", a.Addr(), ref)
	}
	return block, nil
}
