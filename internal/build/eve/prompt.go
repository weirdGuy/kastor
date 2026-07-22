package eve

import (
	"fmt"
	"strings"

	"github.com/weirdGuy/kastor/internal/schema"
)

// genInstructions emits an agent directory's instructions.md — eve's
// always-on system prompt. The prompt body is preserved verbatim (no
// generated banner: instructions.md is injected into every model call, so
// provenance lives in README.md and agent.ts instead). The agent's typed IO
// contract has no eve equivalent, so it degrades to convention: generated
// Inputs/Outputs sections the model is instructed to honor.
func genInstructions(a *schema.Agent, prompt *schema.Prompt) []byte {
	var b strings.Builder
	switch {
	case prompt != nil:
		b.WriteString(strings.TrimRight(prompt.Body, "\n"))
		b.WriteString("\n")
	case a.Description != "":
		b.WriteString(a.Description + "\n")
	default:
		b.WriteString("Kastor agent " + a.Addr() + ".\n")
	}

	if len(a.Inputs) > 0 {
		b.WriteString("\n## Inputs\n\n")
		if prompt != nil {
			b.WriteString("Inputs arrive in the user message; `{{name}}` placeholders above refer to them.\n\n")
		} else {
			b.WriteString("Inputs arrive in the user message.\n\n")
		}
		for _, in := range a.Inputs {
			b.WriteString(inputLine(in) + "\n")
		}
	}

	if len(a.Outputs) > 0 {
		b.WriteString("\n## Outputs\n\nReply with a single JSON object holding exactly these fields:\n\n")
		for _, out := range a.Outputs {
			fmt.Fprintf(&b, "- `%s` (%s)", out.Name, out.Type)
			if out.Description != "" {
				b.WriteString(" — " + out.Description)
			}
			b.WriteString("\n")
		}
	}
	return []byte(b.String())
}

// inputLine renders one bullet of the Inputs section.
func inputLine(in *schema.AgentInput) string {
	mode := "required"
	if in.Optional || in.Default != nil || in.DefaultRef != "" {
		mode = "optional"
	}
	line := fmt.Sprintf("- `%s` (%s, %s)", in.Name, in.Type, mode)
	if in.Default != nil {
		// The literal was validated against the input's type at parse time;
		// its plain rendering is unambiguous in prose.
		line += fmt.Sprintf(" — defaults to %v", in.Default)
		if in.Description != "" {
			line = fmt.Sprintf("- `%s` (%s, %s) — %s (defaults to %v)", in.Name, in.Type, mode, in.Description, in.Default)
		}
		return line
	}
	doc := inputRefDoc(in)
	if doc != "" {
		line += " — " + doc
	}
	return line
}

// inputRefDoc renders an input's description, folding in the cross-agent
// default note: v0 wires no data flow (SPEC.md §4), but on eve the
// referenced agent is present as a subagent, so the value can be produced by
// delegating to it.
func inputRefDoc(in *schema.AgentInput) string {
	if in.DefaultRef == "" {
		return in.Description
	}
	owner := refOwner(in.DefaultRef)
	note := fmt.Sprintf("declared default is %s: delegate to the `%s` subagent for it, or take the value from the message", in.DefaultRef, owner)
	if in.Description != "" {
		return in.Description + " (" + note + ")"
	}
	return strings.ToUpper(note[:1]) + note[1:]
}

// refOwner extracts the owning agent name from an
// "agent.<name>.output.<name>" reference.
func refOwner(ref string) string {
	rest := strings.TrimPrefix(ref, "agent.")
	if owner, _, ok := strings.Cut(rest, ".output."); ok {
		return owner
	}
	return rest
}

// genSkill emits skills/<name>.md for a prompt no agent uses as its system
// prompt: the body verbatim, loadable on demand. eve advertises the first
// line as the skill's description.
func genSkill(p *schema.Prompt) []byte {
	return []byte(strings.TrimRight(p.Body, "\n") + "\n")
}
