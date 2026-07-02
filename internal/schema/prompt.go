package schema

// Prompt is a pure template decoded from a .prompt file (SPEC.md §3.4).
// It carries no model and no IO contract — agents own those.
type Prompt struct {
	Name     string   // frontmatter "name", e.g. "weather_system" in prompt.weather_system
	Requires []string // frontmatter "requires": variables the template needs, in declaration order
	Body     string   // raw template body after the closing frontmatter delimiter
	Vars     []string // {{var}} references extracted from Body, in first-occurrence order
}

// Addr returns the block address used in references and diagnostics.
func (p *Prompt) Addr() string { return "prompt." + p.Name }
