package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/parser"
	"github.com/weirdGuy/agentform/internal/schema"
)

func TestParsePromptFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    *schema.Prompt
		wantErr string // substring the error must contain; empty means no error
	}{
		{
			name: "full prompt with requires, repeated and spaced variables",
			file: "valid_full.prompt",
			want: &schema.Prompt{
				Name:     "weather_system",
				Requires: []string{"location", "date"},
				Body: "You are a weather assistant. The user is asking about {{location}} on {{ date }}.\n" +
					"Answer concisely. Location again: {{location}}.\n",
				Vars: []string{"location", "date"},
			},
		},
		{
			name: "prompt without requires or variables",
			file: "valid_no_vars.prompt",
			want: &schema.Prompt{
				Name: "greeting",
				Body: "Say hello politely.\n",
			},
		},
		{
			name: "malformed brace sequences are literal text, not variables",
			file: "valid_literal_braces.prompt",
			want: &schema.Prompt{
				Name:     "json_example",
				Requires: []string{"payload"},
				Body: "Return JSON shaped like {\"ok\": true}. Sequences such as {{ not-a-var }},\n" +
					"{{1bad}}, and a lone {{ are literal text, not variables.\n" +
					"Data: {{payload}}\n",
				Vars: []string{"payload"},
			},
		},
		{
			name: "requires omitted is inferred from body variables",
			file: "valid_inferred_requires.prompt",
			want: &schema.Prompt{
				Name: "forecast_summary",
				Body: "Summarize the forecast for {{location}} on {{date}}.\n",
				Vars: []string{"location", "date"},
			},
		},
		{
			name:    "explicit empty requires rejects body variables",
			file:    "invalid_empty_requires.prompt",
			wantErr: `prompt.weather_system: variable "location" used in body but not declared in requires`,
		},
		{
			name:    "empty body is rejected",
			file:    "invalid_empty_body.prompt",
			wantErr: `prompt.empty: prompt body is empty`,
		},
		{
			name:    "file without frontmatter is rejected",
			file:    "invalid_no_frontmatter.prompt",
			wantErr: `invalid_no_frontmatter.prompt: prompt file must begin with a "---" frontmatter delimiter`,
		},
		{
			name:    "frontmatter without closing delimiter is rejected",
			file:    "invalid_unclosed_frontmatter.prompt",
			wantErr: `invalid_unclosed_frontmatter.prompt: unterminated frontmatter: closing "---" not found`,
		},
		{
			name:    "frontmatter missing required name attribute",
			file:    "invalid_missing_name.prompt",
			wantErr: `The argument "name" is required`,
		},
		{
			name:    "unknown frontmatter attributes are rejected",
			file:    "invalid_unknown_attr.prompt",
			wantErr: "Unsupported argument",
		},
		{
			name:    "requires must be a list of strings",
			file:    "invalid_requires_type.prompt",
			wantErr: "list of string required, but have string",
		},
		{
			name:    "body variable missing from requires is rejected",
			file:    "invalid_undeclared_var.prompt",
			wantErr: `prompt.weather_system: variable "date" used in body but not declared in requires`,
		},
		{
			name:    "declared variable never used in body is rejected",
			file:    "invalid_unused_require.prompt",
			wantErr: `prompt.weather_system: required variable "date" is never used in the body`,
		},
		{
			name:    "duplicate entries in requires are rejected",
			file:    "invalid_dup_require.prompt",
			wantErr: `prompt.weather_system: variable "location" declared more than once in requires`,
		},
		{
			name:    "HCL syntax errors in frontmatter surface as diagnostics",
			file:    "invalid_frontmatter_syntax.prompt",
			wantErr: "Invalid expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.ParsePromptFile(filepath.Join("testdata", tt.file))

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
				t.Errorf("ParsePromptFile(%s) mismatch (-want +got):\n%s", tt.file, diff)
			}
		})
	}
}

// Frontmatter is decoded with a line offset so HCL diagnostics point at the
// real position in the .prompt file, not at line 1 of the extracted region.
func TestParsePrompt_DiagnosticLineOffset(t *testing.T) {
	src := []byte("---\nname = \"x\"\nbogus_attr = true\n---\nbody\n")
	_, err := parser.ParsePrompt("offset.prompt", src)
	if err == nil {
		t.Fatal("expected error for unknown attribute, got nil")
	}
	if !strings.Contains(err.Error(), "offset.prompt:3") {
		t.Fatalf("error %q does not point at offset.prompt:3", err)
	}
}

func TestParsePromptFile_MissingFile(t *testing.T) {
	_, err := parser.ParsePromptFile(filepath.Join("testdata", "does_not_exist.prompt"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
