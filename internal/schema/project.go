// Package schema defines the typed configuration structs that parsed Kastor
// files decode into.
package schema

// ProjectFile is the decoded form of a project file (kastor.hcl / .kastor).
// Block order follows source order so downstream output stays deterministic.
type ProjectFile struct {
	Plugins    []*PluginRequirement
	Models     []*Model
	Targets    []*Target
	MCPServers []*MCPServer
}

// PluginRequirement pins the external implementation selected by one or more
// targets. Name is the module-local identifier used by target.plugin; Source
// and Version are installation coordinates, not Go package names.
type PluginRequirement struct {
	Name    string
	Source  string
	Version string
}

// Addr returns the module-local address used in diagnostics.
func (p *PluginRequirement) Addr() string { return "plugin." + p.Name }

// Model is a vendor-neutral model definition (SPEC.md §3.1). Agents
// reference it by address (model.<Name>), never by raw provider strings.
type Model struct {
	Name     string // block label, e.g. "fast" in model.fast
	Provider string // openai | anthropic | google | ollama | ...
	ID       string // provider's model identifier, the "id" attribute
	Params   map[string]any
}

// Addr returns the block address used in references and diagnostics.
func (m *Model) Addr() string { return "model." + m.Name }

// Target is a build or deployment destination (SPEC.md §3.5).
type Target struct {
	Name   string         // block label: module-local instance identity
	Type   string         // "codegen" or "platform"
	Plugin string         // local name from kastor.required_plugins
	Output string         // codegen only: output directory for generated code
	Config map[string]any // opaque plugin-owned configuration
}

// Addr returns the block address used in references and diagnostics.
func (t *Target) Addr() string { return "target." + t.Name }

// ConfigString returns one string-valued plugin configuration entry. Keeping
// this small typed accessor beside the opaque map lets adapters validate their
// own fields without pushing provider vocabulary back into the core schema.
func (t *Target) ConfigString(name string) (string, bool) {
	if t == nil || t.Config == nil {
		return "", false
	}
	value, ok := t.Config[name].(string)
	return value, ok
}
