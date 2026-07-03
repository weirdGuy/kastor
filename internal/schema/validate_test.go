package schema_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/schema"
)

func TestValidatePromptVars(t *testing.T) {
	agent := &schema.Agent{
		Name:         "weather",
		Model:        "model.fast",
		SystemPrompt: "prompt.weather_system",
		Inputs: []*schema.AgentInput{
			{Name: "location", Type: "string"},
			{Name: "date", Type: "string", Optional: true},
		},
		Outputs: []*schema.AgentOutput{
			{Name: "report", Type: "string"},
		},
	}

	tests := []struct {
		name     string
		vars     []string // prompt body variables, in body order
		wantErrs []string // exact error messages, in order; empty = valid
	}{
		{
			name: "no variables is trivially satisfied",
			vars: nil,
		},
		{
			name: "variables satisfied by inputs",
			vars: []string{"location", "date"},
		},
		{
			name: "optional input satisfies a variable",
			vars: []string{"date"},
		},
		{
			name: "output satisfies a variable",
			vars: []string{"report"},
		},
		{
			name: "unsatisfied variable names agent, prompt, and variable",
			vars: []string{"location", "city"},
			wantErrs: []string{
				`agent.weather: system_prompt prompt.weather_system: variable "city" is not an input or output of the agent`,
			},
		},
		{
			name: "all unsatisfied variables are reported in body order",
			vars: []string{"city", "location", "mood"},
			wantErrs: []string{
				`agent.weather: system_prompt prompt.weather_system: variable "city" is not an input or output of the agent`,
				`agent.weather: system_prompt prompt.weather_system: variable "mood" is not an input or output of the agent`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt := &schema.Prompt{Name: "weather_system", Vars: tc.vars}
			var got []string
			for _, err := range schema.ValidatePromptVars(agent, prompt) {
				got = append(got, err.Error())
			}
			if diff := cmp.Diff(tc.wantErrs, got); diff != "" {
				t.Errorf("ValidatePromptVars errors (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidatePromptVarsEmptyContract(t *testing.T) {
	agent := &schema.Agent{Name: "bare", Model: "model.fast", SystemPrompt: "prompt.bare"}
	prompt := &schema.Prompt{Name: "bare", Vars: []string{"anything"}}

	errs := schema.ValidatePromptVars(agent, prompt)
	if len(errs) != 1 {
		t.Fatalf("ValidatePromptVars returned %d errors, want 1: %v", len(errs), errs)
	}
	want := `agent.bare: system_prompt prompt.bare: variable "anything" is not an input or output of the agent`
	if errs[0].Error() != want {
		t.Errorf("error = %q, want %q", errs[0], want)
	}
}
