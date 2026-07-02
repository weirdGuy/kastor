package parser_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/parser"
	"github.com/weirdGuy/agentform/internal/schema"
)

func TestParseToolFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    []*schema.Tool
		wantErr string // substring the error must contain; empty means no error
	}{
		{
			name: "full tool with typed params, defaults, returns and mcp source",
			file: "valid_full.tool",
			want: []*schema.Tool{
				{
					Name:        "web_search",
					Description: "Search the web",
					Params: []*schema.ToolParam{
						{Name: "query", Type: "string", Description: "The query to search for"},
						{Name: "max_results", Type: "number", Default: int64(10)},
						{Name: "include_images", Type: "bool", Default: false},
					},
					Returns: &schema.ToolReturns{Type: "string"},
					Source:  &schema.ToolSource{Kind: "mcp", URI: "mcp://search-server/web_search"},
				},
			},
		},
		{
			name: "multiple tools per file; runtime and builtin sources take no uri, script does",
			file: "valid_multi.tool",
			want: []*schema.Tool{
				{
					Name:    "scratchpad",
					Params:  []*schema.ToolParam{{Name: "note", Type: "string"}},
					Returns: &schema.ToolReturns{Type: "string"},
					Source:  &schema.ToolSource{Kind: "runtime"},
				},
				{
					Name:    "platform_search",
					Returns: &schema.ToolReturns{Type: "string"},
					Source:  &schema.ToolSource{Kind: "builtin"},
				},
				{
					Name:    "summarize",
					Params:  []*schema.ToolParam{{Name: "text", Type: "string"}},
					Returns: &schema.ToolReturns{Type: "string"},
					Source:  &schema.ToolSource{Kind: "script", URI: "./scripts/summarize.py"},
				},
			},
		},
		{
			name:    "duplicate tool names are rejected",
			file:    "invalid_dup_tool.tool",
			wantErr: `tool.web_search: declared more than once`,
		},
		{
			name:    "duplicate param names are rejected",
			file:    "invalid_dup_param.tool",
			wantErr: `tool.web_search: param "query" declared more than once`,
		},
		{
			name:    "param type outside the enum is rejected",
			file:    "invalid_param_type.tool",
			wantErr: `tool.web_search: param "query": invalid type "integer" (expected string, number, or bool)`,
		},
		{
			name:    "quoted param type is rejected, types are bare keywords",
			file:    "invalid_param_type_quoted.tool",
			wantErr: `tool.web_search: param "query": type must be a bare keyword (string, number, or bool)`,
		},
		{
			name:    "default value must match the declared param type",
			file:    "invalid_default_type.tool",
			wantErr: `tool.web_search: param "max_results": default must be number, got string`,
		},
		{
			name:    "null default is rejected",
			file:    "invalid_default_null.tool",
			wantErr: `tool.web_search: param "query": default cannot be null; omit the attribute instead`,
		},
		{
			name:    "missing source block is rejected",
			file:    "invalid_no_source.tool",
			wantErr: `tool.web_search: exactly one "source" block is required, found 0`,
		},
		{
			name:    "two source blocks are rejected",
			file:    "invalid_two_sources.tool",
			wantErr: `tool.web_search: exactly one "source" block is required, found 2`,
		},
		{
			name:    "source kind outside the enum is rejected",
			file:    "invalid_source_kind.tool",
			wantErr: `tool.web_search: invalid source kind "graphql" (expected "mcp", "http", "builtin", "runtime", or "script")`,
		},
		{
			name:    "mcp source requires a uri",
			file:    "invalid_mcp_no_uri.tool",
			wantErr: `tool.web_search: source kind "mcp" requires "uri"`,
		},
		{
			name:    "builtin source rejects a uri",
			file:    "invalid_builtin_uri.tool",
			wantErr: `tool.web_search: source kind "builtin" does not allow "uri"`,
		},
		{
			name:    "missing returns block is rejected",
			file:    "invalid_no_returns.tool",
			wantErr: `tool.web_search: exactly one "returns" block is required, found 0`,
		},
		{
			name:    "two returns blocks are rejected",
			file:    "invalid_two_returns.tool",
			wantErr: `tool.web_search: exactly one "returns" block is required, found 2`,
		},
		{
			name:    "returns type outside the enum is rejected",
			file:    "invalid_returns_type.tool",
			wantErr: `tool.web_search: returns: invalid type "json" (expected string, number, or bool)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.ParseToolFile(filepath.Join("testdata", tt.file))

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
				t.Errorf("ParseToolFile(%s) mismatch (-want +got):\n%s", tt.file, diff)
			}
		})
	}
}

func TestParseToolFile_MissingFile(t *testing.T) {
	_, err := parser.ParseToolFile(filepath.Join("testdata", "does_not_exist.tool"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
