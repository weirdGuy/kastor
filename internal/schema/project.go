// Package schema defines the typed configuration structs that parsed ADL
// files decode into.
package schema

// ProjectFile is the decoded form of a project file (adl.hcl / .adl).
// Block order follows source order so downstream output stays deterministic.
type ProjectFile struct {
	Models  []*Model
	Targets []*Target
}

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
	Name   string // block label
	Type   string // "codegen" or "platform"
	Output string // codegen only: output directory for generated code
	Auth   *Auth  // platform only
}

// Addr returns the block address used in references and diagnostics.
func (t *Target) Addr() string { return "target." + t.Name }

// Auth configures credentials for a platform target.
type Auth struct {
	APIKeyEnv string // environment variable holding the API key
}
