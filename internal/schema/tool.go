package schema

// Tool is an interface + implementation source decoded from a .tool file
// (SPEC.md §3.3).
type Tool struct {
	Name        string       // block label, e.g. "web_search" in tool.web_search
	Description string       // optional; surfaced to platforms as the function description
	Params      []*ToolParam // declaration order
	Returns     *ToolReturns
	Source      *ToolSource
}

// Addr returns the block address used in references and diagnostics.
func (t *Tool) Addr() string { return "tool." + t.Name }

// ToolParam is one input parameter of a tool.
type ToolParam struct {
	Name        string // block label
	Type        string // "string", "number", or "bool"
	Description string // optional
	Default     any    // typed to match Type; nil when the param has no default
}

// ToolReturns describes a tool's return value.
type ToolReturns struct {
	Type string // "string", "number", or "bool"
}

// ToolSource is the single implementation source of a tool.
type ToolSource struct {
	Kind string // mcp | http | builtin | runtime | script
	URI  string // required for mcp/http/script, empty for builtin/runtime
}
