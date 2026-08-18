package main

import (
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
				`mcp_server.hubspot: auth ref "connection://cred_011CZkZDLs7fYzm1hXNPeRjv" needs a vault to resolve against; target.prod plugin config must declare "vault_id"`,
				`mcp_server.airtable: auth ref "env://AIRTABLE_TOKEN" cannot be bound on target.prod; plugin "github.com/getkastordev/kastor-anthropic" does not support env:// credentials`,
			},
		},
		{
			dir: "stdio_on_platform",
			wantErrs: []string{
				`mcp_server.fetch: transport "stdio" cannot be bound on target.prod; plugin "github.com/getkastordev/kastor-anthropic" does not advertise local-process support`,
			},
		},
		{
			dir: "stdio_on_eve",
			wantErrs: []string{
				`mcp_server.fetch: transport "stdio" cannot be bound on target.typescript; plugin "github.com/getkastordev/kastor-eve" does not advertise local-process support`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			root := filepath.Join("..", "..", "internal", "module", "testdata", tc.dir)
			mod, err := module.Load(root)
			if err != nil {
				t.Fatalf("module.Load: %v", err)
			}
			err = validateTargetPlugins(mod)
			if err == nil {
				t.Fatalf("validateTargetPlugins: expected %q, got nil", tc.wantErrs)
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("validateTargetPlugins error = %q\nwant substring %q", err, want)
				}
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
