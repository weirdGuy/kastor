package parser

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"github.com/weirdGuy/agentform/internal/schema"
)

type toolFileHCL struct {
	Tools []toolHCL `hcl:"tool,block"`
}

// toolHCL decodes returns and source as slices so cardinality violations get
// address-prefixed errors instead of generic gohcl diagnostics.
type toolHCL struct {
	Label       string           `hcl:"name,label"`
	Description *string          `hcl:"description"`
	Params      []toolParamHCL   `hcl:"param,block"`
	Returns     []toolReturnsHCL `hcl:"returns,block"`
	Sources     []toolSourceHCL  `hcl:"source,block"`
}

// toolParamHCL keeps type as a raw expression (it is a bare keyword, not a
// string value) and default as a cty.Value so an absent attribute (NilVal)
// stays distinguishable from an explicit null.
type toolParamHCL struct {
	Label       string         `hcl:"name,label"`
	Type        hcl.Expression `hcl:"type"`
	Description *string        `hcl:"description"`
	Default     cty.Value      `hcl:"default,optional"`
}

type toolReturnsHCL struct {
	Type hcl.Expression `hcl:"type"`
}

type toolSourceHCL struct {
	Kind string  `hcl:"kind"`
	URI  *string `hcl:"uri"`
}

// paramTypes maps the closed type enum to the cty type a default must have.
var paramTypes = map[string]cty.Type{
	"string": cty.String,
	"number": cty.Number,
	"bool":   cty.Bool,
}

// ParseToolFile reads and decodes a tool file (.tool).
func ParseToolFile(path string) ([]*schema.Tool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading tool file: %w", err)
	}
	return ParseTools(path, src)
}

// ParseTools decodes tool file source. filename is used in diagnostics.
func ParseTools(filename string, src []byte) ([]*schema.Tool, error) {
	file, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, diags
	}

	var raw toolFileHCL
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, diags
	}

	var tools []*schema.Tool
	seen := map[string]bool{}
	for _, t := range raw.Tools {
		tool, err := decodeTool(t)
		if err != nil {
			return nil, err
		}
		if seen[tool.Name] {
			return nil, fmt.Errorf("%s: declared more than once", tool.Addr())
		}
		seen[tool.Name] = true
		tools = append(tools, tool)
	}
	return tools, nil
}

func decodeTool(t toolHCL) (*schema.Tool, error) {
	tool := &schema.Tool{Name: t.Label}
	if t.Description != nil {
		tool.Description = *t.Description
	}

	seenParams := map[string]bool{}
	for _, p := range t.Params {
		param, err := decodeToolParam(tool.Addr(), p)
		if err != nil {
			return nil, err
		}
		if seenParams[param.Name] {
			return nil, fmt.Errorf("%s: param %q declared more than once", tool.Addr(), param.Name)
		}
		seenParams[param.Name] = true
		tool.Params = append(tool.Params, param)
	}

	if len(t.Returns) != 1 {
		return nil, fmt.Errorf("%s: exactly one \"returns\" block is required, found %d", tool.Addr(), len(t.Returns))
	}
	retType, err := typeKeyword(fmt.Sprintf("%s: returns", tool.Addr()), t.Returns[0].Type)
	if err != nil {
		return nil, err
	}
	tool.Returns = &schema.ToolReturns{Type: retType}

	if len(t.Sources) != 1 {
		return nil, fmt.Errorf("%s: exactly one \"source\" block is required, found %d", tool.Addr(), len(t.Sources))
	}
	source, err := decodeToolSource(tool.Addr(), t.Sources[0])
	if err != nil {
		return nil, err
	}
	tool.Source = source

	return tool, nil
}

func decodeToolParam(toolAddr string, p toolParamHCL) (*schema.ToolParam, error) {
	addr := fmt.Sprintf("%s: param %q", toolAddr, p.Label)

	param := &schema.ToolParam{Name: p.Label}
	if p.Description != nil {
		param.Description = *p.Description
	}

	typ, err := typeKeyword(addr, p.Type)
	if err != nil {
		return nil, err
	}
	param.Type = typ

	if p.Default != cty.NilVal {
		if p.Default.IsNull() {
			return nil, fmt.Errorf("%s: default cannot be null; omit the attribute instead", addr)
		}
		if want := paramTypes[typ]; p.Default.Type() != want {
			return nil, fmt.Errorf("%s: default must be %s, got %s", addr, typ, p.Default.Type().FriendlyName())
		}
		goVal, err := ctyToGo(p.Default)
		if err != nil {
			return nil, fmt.Errorf("%s: default: %w", addr, err)
		}
		param.Default = goVal
	}

	return param, nil
}

func decodeToolSource(toolAddr string, s toolSourceHCL) (*schema.ToolSource, error) {
	source := &schema.ToolSource{Kind: s.Kind}
	if s.URI != nil {
		source.URI = *s.URI
	}

	switch source.Kind {
	case "mcp", "http", "script":
		if s.URI == nil {
			return nil, fmt.Errorf("%s: source kind %q requires \"uri\"", toolAddr, source.Kind)
		}
	case "builtin", "runtime":
		if s.URI != nil {
			return nil, fmt.Errorf("%s: source kind %q does not allow \"uri\"", toolAddr, source.Kind)
		}
	default:
		return nil, fmt.Errorf("%s: invalid source kind %q (expected \"mcp\", \"http\", \"builtin\", \"runtime\", or \"script\")", toolAddr, source.Kind)
	}
	return source, nil
}

// typeKeyword resolves a type attribute written as a bare keyword
// (type = string) against the closed v0 type enum.
func typeKeyword(addr string, expr hcl.Expression) (string, error) {
	kw := hcl.ExprAsKeyword(expr)
	if kw == "" {
		return "", fmt.Errorf("%s: type must be a bare keyword (string, number, or bool)", addr)
	}
	if _, ok := paramTypes[kw]; !ok {
		return "", fmt.Errorf("%s: invalid type %q (expected string, number, or bool)", addr, kw)
	}
	return kw, nil
}
