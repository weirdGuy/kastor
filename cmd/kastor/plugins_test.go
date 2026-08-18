package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weirdGuy/kastor/internal/module"
)

func TestExplicitPluginCapabilityValidation(t *testing.T) {
	tests := []struct {
		dir      string
		wantErrs []string
	}{
		{
			dir: "bad_credential_targets",
			wantErrs: []string{
				`target.prod: vault_id is required for connection credentials (connection://cred_011CZkZDLs7fYzm1hXNPeRjv)`,
				`mcp_server.airtable: unsupported credential scheme (env://)`,
			},
		},
		{
			dir: "stdio_on_platform",
			wantErrs: []string{
				`mcp_server.fetch: stdio transport is not supported (Claude Managed Agents requires an HTTP endpoint)`,
			},
		},
		{
			dir: "stdio_on_eve",
			wantErrs: []string{
				`mcp_server.fetch: stdio transport is not supported (Eve connections require an HTTP endpoint)`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			anthropic := newFakePluginClient(claudePluginSource, "platform")
			eve := newFakePluginClient(evePluginSource, "codegen")
			useFakePlugins(t, anthropic, eve)
			root := filepath.Join("..", "..", "internal", "module", "testdata", tc.dir)
			mod, err := module.Load(root)
			if err != nil {
				t.Fatalf("module.Load: %v", err)
			}
			err = validateTargetPlugins(context.Background(), io.Discard, mod)
			if err == nil {
				t.Fatalf("validateTargetPlugins: expected %q, got nil", tc.wantErrs)
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("validateTargetPlugins error = %q\nwant substring %q", err, want)
				}
			}
			if anthropic.validateCalls+eve.validateCalls != 1 {
				t.Errorf("external validate calls = %d, want 1", anthropic.validateCalls+eve.validateCalls)
			}
		})
	}
}

func TestTargetPluginSourceUsesRequirementNotTargetLabel(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "module", "testdata", "stdio_on_eve")
	mod, err := module.Load(root)
	if err != nil {
		t.Fatalf("module.Load: %v", err)
	}
	got, err := targetPluginSource(mod, mod.Targets[0])
	if err != nil {
		t.Fatalf("targetPluginSource: %v", err)
	}
	if got != evePluginSource {
		t.Errorf("targetPluginSource = %q, want %q", got, evePluginSource)
	}
}

func TestLegacyTargetReportsMigrationWarning(t *testing.T) {
	mod, err := module.Load(filepath.Join("testdata", "build", "single"))
	if err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	if err := validateTargetPlugins(context.Background(), &warnings, mod); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"target.langgraph", "kastor.required_plugins", "set plugin"} {
		if !strings.Contains(warnings.String(), want) {
			t.Errorf("warning missing %q:\n%s", want, warnings.String())
		}
	}
}
