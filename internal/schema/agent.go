package schema

// Agent is a declarative agent definition decoded from a .agent file
// (SPEC.md §3.2). All references are captured as unresolved block addresses
// (e.g. "model.fast"); resolution against the module is a later pass.
type Agent struct {
	Name         string // block label, e.g. "weather" in agent.weather
	Description  string // optional
	Model        string // required reference, "model.<name>"
	SystemPrompt string // optional reference, "prompt.<name>"; empty when absent
	Tools        []string
	Inputs       []*AgentInput
	Outputs      []*AgentOutput
	DependsOn    []string
}

// Addr returns the block address used in references and diagnostics.
func (a *Agent) Addr() string { return "agent." + a.Name }

// AgentInput is one declared input of an agent's IO contract.
type AgentInput struct {
	Name        string
	Type        string // "string", "number", or "bool"
	Description string // optional
	Optional    bool
	Default     any    // literal default; nil when absent or when DefaultRef is set
	DefaultRef  string // unresolved "agent.<name>.output.<name>" reference; empty otherwise
}

// AgentOutput is one declared output of an agent's IO contract.
type AgentOutput struct {
	Name        string
	Type        string // "string", "number", or "bool"
	Description string // optional
}
