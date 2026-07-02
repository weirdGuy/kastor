// Package parser decodes ADL source files into the typed structs in
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

	"github.com/weirdGuy/agentform/internal/schema"
)

// projectFileHCL mirrors the raw HCL layout of a project file. It exists only
// as a decode target; callers get the cleaned-up schema.ProjectFile.
type projectFileHCL struct {
	Models  []modelHCL  `hcl:"model,block"`
	Targets []targetHCL `hcl:"target,block"`
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
	Label  string   `hcl:"name,label"`
	Type   string   `hcl:"type"`
	Output *string  `hcl:"output"`
	Auth   *authHCL `hcl:"auth,block"`
}

type authHCL struct {
	APIKeyEnv string `hcl:"api_key_env"`
}

// ParseProjectFile reads and decodes a project file (adl.hcl / .adl).
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
		project.Targets = append(project.Targets, target)
	}

	return project, nil
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
