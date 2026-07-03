package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/parser"
	"github.com/weirdGuy/agentform/internal/schema"
)

func TestParseAgentFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    []*schema.Agent
		wantErr string // substring the error must contain; empty means no error
	}{
		{
			name: "full agent with refs, tools, typed inputs and outputs, depends_on",
			file: "valid_full.agent",
			want: []*schema.Agent{
				{
					Name:         "weather",
					Description:  "Answers weather questions for a location and date",
					Model:        "model.fast",
					SystemPrompt: "prompt.weather_system",
					Tools:        []string{"tool.web_search", "tool.geocode"},
					Inputs: []*schema.AgentInput{
						{Name: "location", Type: "string", Description: "The location to get the weather for"},
						{Name: "date", Type: "string", Optional: true},
						{Name: "max_hops", Type: "number", Default: int64(3)},
						{Name: "forecast_context", Type: "string", DefaultRef: "agent.forecast.output.summary"},
					},
					Outputs: []*schema.AgentOutput{
						{Name: "weather", Type: "string", Description: "The weather report"},
					},
					DependsOn: []string{"agent.geocoder"},
				},
			},
		},
		{
			name: "minimal agent needs only model and system_prompt",
			file: "valid_minimal.agent",
			want: []*schema.Agent{
				{
					Name:         "echo",
					Model:        "model.fast",
					SystemPrompt: "prompt.echo_system",
				},
			},
		},
		{
			name: "multiple agents per file; output refs captured unresolved",
			file: "valid_multi.agent",
			want: []*schema.Agent{
				{
					Name:         "geocoder",
					Model:        "model.fast",
					SystemPrompt: "prompt.geocoder_system",
					Inputs:       []*schema.AgentInput{{Name: "place", Type: "string"}},
					Outputs:      []*schema.AgentOutput{{Name: "coordinates", Type: "string"}},
				},
				{
					Name:         "forecast",
					Model:        "model.smart",
					SystemPrompt: "prompt.forecast_system",
					Inputs: []*schema.AgentInput{
						{Name: "coordinates", Type: "string", DefaultRef: "agent.geocoder.output.coordinates"},
					},
					Outputs: []*schema.AgentOutput{{Name: "summary", Type: "string"}},
				},
			},
		},
		{
			name:    "duplicate agent names are rejected",
			file:    "invalid_dup_agent.agent",
			wantErr: `agent.weather: declared more than once`,
		},
		{
			name:    "missing model is rejected",
			file:    "invalid_missing_model.agent",
			wantErr: `agent.weather: missing required attribute "model"`,
		},
		{
			name:    "model referencing a non-model address is rejected",
			file:    "invalid_model_ref_kind.agent",
			wantErr: `agent.weather: model must be a reference like model.<name>, got "prompt.weather_system"`,
		},
		{
			name:    "quoted string is not a model reference",
			file:    "invalid_model_string.agent",
			wantErr: `agent.weather: model must be a reference like model.<name>`,
		},
		{
			name:    "system_prompt referencing a non-prompt address is rejected",
			file:    "invalid_prompt_ref_kind.agent",
			wantErr: `agent.weather: system_prompt must be a reference like prompt.<name>, got "tool.web_search"`,
		},
		{
			name:    "tools must be a list",
			file:    "invalid_tools_not_list.agent",
			wantErr: `agent.weather: tools must be a list of tool references`,
		},
		{
			name:    "tools element referencing a non-tool address is rejected",
			file:    "invalid_tool_ref_kind.agent",
			wantErr: `agent.weather: tools element must be a reference like tool.<name>, got "model.fast"`,
		},
		{
			name:    "duplicate tool references are rejected",
			file:    "invalid_dup_tool_ref.agent",
			wantErr: `agent.weather: tools: "tool.web_search" listed more than once`,
		},
		{
			name:    "duplicate input names are rejected",
			file:    "invalid_dup_input.agent",
			wantErr: `agent.weather: input "location" declared more than once`,
		},
		{
			name:    "duplicate output names are rejected",
			file:    "invalid_dup_output.agent",
			wantErr: `agent.weather: output "weather" declared more than once`,
		},
		{
			name:    "input and output sharing a name are rejected",
			file:    "invalid_io_collision.agent",
			wantErr: `agent.weather: "weather" declared as both input and output`,
		},
		{
			name:    "input type outside the enum is rejected",
			file:    "invalid_input_type.agent",
			wantErr: `agent.weather: input "location": invalid type "integer" (expected string, number, or bool)`,
		},
		{
			name:    "quoted output type is rejected, types are bare keywords",
			file:    "invalid_output_type_quoted.agent",
			wantErr: `agent.weather: output "weather": type must be a bare keyword (string, number, or bool)`,
		},
		{
			name:    "literal default must match the declared input type",
			file:    "invalid_default_type.agent",
			wantErr: `agent.weather: input "location": default must be string, got number`,
		},
		{
			name:    "null default is rejected",
			file:    "invalid_default_null.agent",
			wantErr: `agent.weather: input "location": default cannot be null; omit the attribute instead`,
		},
		{
			name:    "default reference must be an agent output address",
			file:    "invalid_default_ref_shape.agent",
			wantErr: `agent.weather: input "forecast_context": default must be a literal or a reference like agent.<name>.output.<name>, got "agent.forecast.summary"`,
		},
		{
			name:    "default reference to a non-agent address is rejected",
			file:    "invalid_default_ref_kind.agent",
			wantErr: `agent.weather: input "forecast_context": default must be a literal or a reference like agent.<name>.output.<name>, got "tool.web_search"`,
		},
		{
			name:    "depends_on element referencing a non-agent address is rejected",
			file:    "invalid_depends_on_kind.agent",
			wantErr: `agent.weather: depends_on element must be a reference like agent.<name>, got "tool.web_search"`,
		},
		{
			name:    "unknown attributes are rejected",
			file:    "invalid_unknown_attr.agent",
			wantErr: `Unsupported argument`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.ParseAgentFile(filepath.Join("testdata", tt.file))

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseAgentFile(%s) mismatch (-want +got):\n%s", tt.file, diff)
			}
		})
	}
}

func TestParseAgentFile_MissingFile(t *testing.T) {
	_, err := parser.ParseAgentFile(filepath.Join("testdata", "does_not_exist.agent"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
