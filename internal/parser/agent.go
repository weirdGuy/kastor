package parser

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/weirdGuy/agentform/internal/schema"
)

type agentFileHCL struct {
	Agents []agentHCL `hcl:"agent,block"`
}

// agentHCL keeps reference-valued attributes as raw expressions: references
// like model.fast are scope traversals, not string values, and must not be
// evaluated at parse time.
type agentHCL struct {
	Label        string           `hcl:"name,label"`
	Description  *string          `hcl:"description"`
	Model        hcl.Expression   `hcl:"model"`
	SystemPrompt hcl.Expression   `hcl:"system_prompt"`
	Tools        hcl.Expression   `hcl:"tools,optional"`
	Inputs       []agentInputHCL  `hcl:"input,block"`
	Outputs      []agentOutputHCL `hcl:"output,block"`
	DependsOn    hcl.Expression   `hcl:"depends_on,optional"`
}

// agentInputHCL keeps default as a raw expression because it may be either a
// literal or an agent output reference.
type agentInputHCL struct {
	Label       string         `hcl:"name,label"`
	Type        hcl.Expression `hcl:"type"`
	Description *string        `hcl:"description"`
	Optional    *bool          `hcl:"optional"`
	Default     hcl.Expression `hcl:"default,optional"`
}

type agentOutputHCL struct {
	Label       string         `hcl:"name,label"`
	Type        hcl.Expression `hcl:"type"`
	Description *string        `hcl:"description"`
}

// ParseAgentFile reads and decodes an agent file (.agent).
func ParseAgentFile(path string) ([]*schema.Agent, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading agent file: %w", err)
	}
	return ParseAgents(path, src)
}

// ParseAgents decodes agent file source. filename is used in diagnostics.
func ParseAgents(filename string, src []byte) ([]*schema.Agent, error) {
	file, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, diags
	}

	var raw agentFileHCL
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, diags
	}

	var agents []*schema.Agent
	seen := map[string]bool{}
	for _, a := range raw.Agents {
		agent, err := decodeAgent(a)
		if err != nil {
			return nil, err
		}
		if seen[agent.Name] {
			return nil, fmt.Errorf("%s: declared more than once", agent.Addr())
		}
		seen[agent.Name] = true
		agents = append(agents, agent)
	}
	return agents, nil
}

func decodeAgent(a agentHCL) (*schema.Agent, error) {
	agent := &schema.Agent{Name: a.Label}
	if a.Description != nil {
		agent.Description = *a.Description
	}

	model, err := requiredRef(agent.Addr(), "model", "model", a.Model)
	if err != nil {
		return nil, err
	}
	agent.Model = model

	systemPrompt, err := requiredRef(agent.Addr(), "system_prompt", "prompt", a.SystemPrompt)
	if err != nil {
		return nil, err
	}
	agent.SystemPrompt = systemPrompt

	tools, err := refList(agent.Addr(), "tools", "tool", a.Tools)
	if err != nil {
		return nil, err
	}
	agent.Tools = tools

	dependsOn, err := refList(agent.Addr(), "depends_on", "agent", a.DependsOn)
	if err != nil {
		return nil, err
	}
	agent.DependsOn = dependsOn

	seenIO := map[string]bool{}
	for _, in := range a.Inputs {
		input, err := decodeAgentInput(agent.Addr(), in)
		if err != nil {
			return nil, err
		}
		if seenIO[input.Name] {
			return nil, fmt.Errorf("%s: input %q declared more than once", agent.Addr(), input.Name)
		}
		seenIO[input.Name] = true
		agent.Inputs = append(agent.Inputs, input)
	}

	seenOutputs := map[string]bool{}
	for _, out := range a.Outputs {
		output, err := decodeAgentOutput(agent.Addr(), out)
		if err != nil {
			return nil, err
		}
		if seenOutputs[output.Name] {
			return nil, fmt.Errorf("%s: output %q declared more than once", agent.Addr(), output.Name)
		}
		if seenIO[output.Name] {
			return nil, fmt.Errorf("%s: %q declared as both input and output", agent.Addr(), output.Name)
		}
		seenOutputs[output.Name] = true
		agent.Outputs = append(agent.Outputs, output)
	}

	return agent, nil
}

func decodeAgentInput(agentAddr string, in agentInputHCL) (*schema.AgentInput, error) {
	addr := fmt.Sprintf("%s: input %q", agentAddr, in.Label)

	input := &schema.AgentInput{Name: in.Label}
	if in.Description != nil {
		input.Description = *in.Description
	}
	if in.Optional != nil {
		input.Optional = *in.Optional
	}

	typ, err := typeKeyword(addr, in.Type)
	if err != nil {
		return nil, err
	}
	input.Type = typ

	if !absentExpr(in.Default) {
		// Literal first: bare null/true/false parse as single-step traversals
		// too, so evaluation is the reliable literal test.
		if val, diags := in.Default.Value(nil); !diags.HasErrors() {
			if val.IsNull() {
				return nil, fmt.Errorf("%s: default cannot be null; omit the attribute instead", addr)
			}
			if want := paramTypes[typ]; val.Type() != want {
				return nil, fmt.Errorf("%s: default must be %s, got %s", addr, typ, val.Type().FriendlyName())
			}
			goVal, err := ctyToGo(val)
			if err != nil {
				return nil, fmt.Errorf("%s: default: %w", addr, err)
			}
			input.Default = goVal
			return input, nil
		}

		parts, ok := traversalParts(in.Default)
		if !ok {
			return nil, fmt.Errorf("%s: default must be a literal or a reference like agent.<name>.output.<name>", addr)
		}
		if len(parts) != 4 || parts[0] != "agent" || parts[2] != "output" {
			return nil, fmt.Errorf("%s: default must be a literal or a reference like agent.<name>.output.<name>, got %q", addr, strings.Join(parts, "."))
		}
		input.DefaultRef = strings.Join(parts, ".")
	}

	return input, nil
}

func decodeAgentOutput(agentAddr string, out agentOutputHCL) (*schema.AgentOutput, error) {
	addr := fmt.Sprintf("%s: output %q", agentAddr, out.Label)

	output := &schema.AgentOutput{Name: out.Label}
	if out.Description != nil {
		output.Description = *out.Description
	}

	typ, err := typeKeyword(addr, out.Type)
	if err != nil {
		return nil, err
	}
	output.Type = typ

	return output, nil
}

// requiredRef decodes an attribute that must be present and hold a reference
// of the form <root>.<name>.
func requiredRef(addr, attr, root string, expr hcl.Expression) (string, error) {
	if absentExpr(expr) {
		return "", fmt.Errorf("%s: missing required attribute %q", addr, attr)
	}
	return simpleRef(addr, attr, root, expr)
}

// simpleRef decodes an expression that must be a reference of the form
// <root>.<name> and returns it as an unresolved address string.
func simpleRef(addr, attr, root string, expr hcl.Expression) (string, error) {
	parts, ok := traversalParts(expr)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a reference like %s.<name>", addr, attr, root)
	}
	if len(parts) != 2 || parts[0] != root {
		return "", fmt.Errorf("%s: %s must be a reference like %s.<name>, got %q", addr, attr, root, strings.Join(parts, "."))
	}
	return parts[0] + "." + parts[1], nil
}

// refList decodes an optional list attribute whose elements must all be
// references of the form <root>.<name>. A nil expression (attribute absent)
// yields a nil slice.
func refList(addr, attr, root string, expr hcl.Expression) ([]string, error) {
	if absentExpr(expr) {
		return nil, nil
	}

	elems, diags := hcl.ExprList(expr)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s must be a list of %s references", addr, attr, root)
	}

	var refs []string
	seen := map[string]bool{}
	for _, elem := range elems {
		ref, err := simpleRef(addr, attr+" element", root, elem)
		if err != nil {
			return nil, err
		}
		if seen[ref] {
			return nil, fmt.Errorf("%s: %s: %q listed more than once", addr, attr, ref)
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs, nil
}

// absentExpr reports whether an expression attribute was omitted in source.
// gohcl fills omitted expression fields with a synthetic null literal whose
// range is zero-length, which distinguishes it from an explicit null.
func absentExpr(expr hcl.Expression) bool {
	if expr == nil {
		return true
	}
	if !expr.Range().Empty() {
		return false
	}
	val, diags := expr.Value(nil)
	return !diags.HasErrors() && val.IsNull()
}

// traversalParts flattens a reference expression like agent.forecast.output.summary
// into its dotted parts. It reports false for anything that is not plain
// attribute access (literals, strings, index steps).
func traversalParts(expr hcl.Expression) ([]string, bool) {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		return nil, false
	}

	parts := make([]string, 0, len(trav))
	for _, step := range trav {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			parts = append(parts, s.Name)
		case hcl.TraverseAttr:
			parts = append(parts, s.Name)
		default:
			return nil, false
		}
	}
	return parts, true
}
