package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/kastor/internal/parser"
	"github.com/weirdGuy/kastor/internal/schema"
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
			name: "mcp_server blocks, both transports and per-target auth",
			file: "valid_mcp_servers.hcl",
			want: &schema.ProjectFile{
				Targets: []*schema.Target{
					{
						Name:   "langgraph",
						Type:   "codegen",
						Output: "./gen/langgraph",
					},
					{
						Name:    "claude_agents",
						Type:    "platform",
						VaultID: "vlt_011CZaBcDeFgHiJkLmNoPqRs",
					},
				},
				MCPServers: []*schema.MCPServer{
					{
						Name:      "hubspot",
						Transport: "http",
						URL:       "https://mcp.hubspot.com",
						Auth: []*schema.MCPAuth{
							{Ref: "connection://cred_011CZkZDLs7fYzm1hXNPeRjv", Targets: []string{"target.claude_agents"}},
							{Ref: "env://HUBSPOT_TOKEN", Targets: []string{"target.langgraph"}},
						},
					},
					{
						Name:      "fetch",
						Transport: "stdio",
						Command:   "uvx",
						Args:      []string{"mcp-server-fetch"},
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
		{
			name:    "stdio transport rejects url",
			file:    "invalid_mcp_stdio_url.hcl",
			wantErr: `mcp_server.fetch: transport "stdio" does not allow "url"`,
		},
		{
			name:    "http transport requires url",
			file:    "invalid_mcp_http_no_url.hcl",
			wantErr: `mcp_server.hubspot: transport "http" requires "url"`,
		},
		{
			name:    "transport is a closed enum",
			file:    "invalid_mcp_transport.hcl",
			wantErr: `mcp_server.hubspot: invalid transport "grpc"`,
		},
		{
			name:    "stdio transport rejects auth",
			file:    "invalid_mcp_stdio_auth.hcl",
			wantErr: `mcp_server.fetch: transport "stdio" does not allow "auth"`,
		},
		{
			name:    "credential scheme set is closed",
			file:    "invalid_mcp_scheme.hcl",
			wantErr: `unknown scheme "vault"; known schemes are connection://… or env://…`,
		},
		{
			name:    "a server has at most one default auth binding",
			file:    "invalid_mcp_two_defaults.hcl",
			wantErr: `mcp_server.hubspot: more than one auth block without "targets"`,
		},
		{
			name:    "two auth blocks cannot name the same target",
			file:    "invalid_mcp_dup_target.hcl",
			wantErr: `mcp_server.hubspot: two auth blocks name target.langgraph`,
		},
		{
			name:    "duplicate server names are rejected",
			file:    "invalid_mcp_dup_server.hcl",
			wantErr: `mcp_server.hubspot: declared more than once`,
		},
		{
			name:    "vault_id is meaningless off the Claude target",
			file:    "invalid_vault_id_wrong_target.hcl",
			wantErr: `target.memory: "vault_id" is only valid on target "claude_agents"`,
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
