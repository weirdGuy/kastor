package schema

import "fmt"

// ValidatePromptVars enforces the ownership rule from SPEC.md §3.2: every
// variable an agent's system prompt requires must be satisfiable from the
// agent's inputs or outputs. A variable is satisfied by name; prompt
// variables are untyped in v0, so there is no type check, and optional
// inputs satisfy a variable like any other.
//
// The prompt's effective variable set is Vars (the {{var}} references in the
// body): when frontmatter declares requires, the parser has already enforced
// that it matches the body exactly, and when it is omitted the set is
// inferred from the body.
//
// All unsatisfied variables are reported, one error each, in body order.
// Errors name the agent, the prompt, and the variable; the caller adds file
// context.
func ValidatePromptVars(a *Agent, p *Prompt) []error {
	satisfied := make(map[string]bool, len(a.Inputs)+len(a.Outputs))
	for _, in := range a.Inputs {
		satisfied[in.Name] = true
	}
	for _, out := range a.Outputs {
		satisfied[out.Name] = true
	}

	var errs []error
	for _, v := range p.Vars {
		if !satisfied[v] {
			errs = append(errs, fmt.Errorf("%s: system_prompt %s: variable %q is not an input or output of the agent", a.Addr(), p.Addr(), v))
		}
	}
	return errs
}
