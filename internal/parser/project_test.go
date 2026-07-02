package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/parser"
	"github.com/weirdGuy/agentform/internal/schema"
)

func TestParseProjectFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    *schema.ProjectFile
		wantErr string // substring the error must contain; empty means no error
	}{
		{
			name: "full project file with params, codegen and platform targets",
			file: "valid_full.hcl",
			want: &schema.ProjectFile{
				Models: []*schema.Model{
					{
						Name:     "fast",
						Provider: "openai",
						ID:       "gpt-4o-mini",
						Params: map[string]any{
							"temperature": 0.2,
							"max_tokens":  int64(4096),
						},
					},
					{
						Name:     "smart",
						Provider: "anthropic",
						ID:       "claude-sonnet-5",
					},
				},
				Targets: []*schema.Target{
					{
						Name:   "langgraph",
						Type:   "codegen",
						Output: "./gen/langgraph",
					},
					{
						Name: "openai_assistants",
						Type: "platform",
						Auth: &schema.Auth{APIKeyEnv: "OPENAI_API_KEY"},
					},
				},
			},
		},
		{
			name: "minimal project file, model without params or targets",
			file: "valid_minimal.hcl",
			want: &schema.ProjectFile{
				Models: []*schema.Model{
					{
						Name:     "local",
						Provider: "ollama",
						ID:       "llama3.1",
					},
				},
			},
		},
		{
			name:    "unclosed block is a syntax error",
			file:    "invalid_syntax.hcl",
			wantErr: "Unclosed configuration block",
		},
		{
			name:    "model missing required provider attribute",
			file:    "invalid_missing_provider.hcl",
			wantErr: `The argument "provider" is required`,
		},
		{
			name:    "model missing required id attribute",
			file:    "invalid_missing_id.hcl",
			wantErr: `The argument "id" is required`,
		},
		{
			name:    "model params must live in the params block, not the model body",
			file:    "invalid_unknown_attr.hcl",
			wantErr: "Unsupported argument",
		},
		{
			name:    "target type must be codegen or platform",
			file:    "invalid_target_type.hcl",
			wantErr: `target.deploy: invalid type "docker"`,
		},
		{
			name:    "duplicate model names are rejected",
			file:    "invalid_dup_model.hcl",
			wantErr: `model.fast: declared more than once`,
		},
		{
			name:    "codegen target requires an output path",
			file:    "invalid_codegen_no_output.hcl",
			wantErr: `target.langgraph: codegen target requires "output"`,
		},
		{
			name:    "codegen target rejects auth block",
			file:    "invalid_codegen_auth.hcl",
			wantErr: `target.langgraph: codegen target does not allow "auth"`,
		},
		{
			name:    "platform target rejects output attribute",
			file:    "invalid_platform_output.hcl",
			wantErr: `target.openai_assistants: platform target does not allow "output"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.ParseProjectFile(filepath.Join("testdata", tt.file))

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
				t.Errorf("ParseProjectFile(%s) mismatch (-want +got):\n%s", tt.file, diff)
			}
		})
	}
}

func TestParseProjectFile_MissingFile(t *testing.T) {
	_, err := parser.ParseProjectFile(filepath.Join("testdata", "does_not_exist.hcl"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
