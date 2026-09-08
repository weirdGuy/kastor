package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkastordev/kastor/internal/module"
	"github.com/getkastordev/kastor/internal/schema"
)

func TestLegacyClaudeAgentsProviderRegistered(t *testing.T) {
	const env = "KASTOR_TEST_CLAUDE_REGISTRY_KEY"
	t.Setenv(env, "test-key")
	factory, exists := providerFactories["claude_agents"]
	if !exists {
		t.Fatal("providerFactories does not contain claude_agents")
	}
	got, err := factory(&schema.Target{
		Name:   "claude_agents",
		Type:   "platform",
		Config: map[string]any{"api_key_env": env},
	})
	if err != nil || got == nil {
		t.Fatalf("claude_agents factory = %v, %v; want provider, nil", got, err)
	}
}

func TestBuiltinMemoryProviderRegistered(t *testing.T) {
	mod, err := module.Load(filepath.Join("..", "..", "examples", "weather"))
	if err != nil {
		t.Fatalf("module.Load: %v", err)
	}
	var target *schema.Target
	for _, candidate := range mod.Targets {
		if candidate.Name == "memory" {
			target = candidate
			break
		}
	}
	if target == nil {
		t.Fatal("weather example does not declare target.memory")
	}
	if target.Plugin != "" {
		t.Fatalf("target.memory plugin = %q, want built-in target with no selector", target.Plugin)
	}
	got, closeProvider, err := providerFor(context.Background(), mod, target)
	if err != nil || got == nil {
		t.Fatalf("providerFor(target.memory) = %v, %v; want provider, nil", got, err)
	}
	if closeProvider != nil {
		t.Fatal("built-in memory provider unexpectedly owns an external process")
	}
}

func TestExplicitPlatformTargetUsesProtocolClient(t *testing.T) {
	client := newFakePluginClient(claudePluginSource, "platform")
	useFakePlugins(t, client)
	mod, err := module.Load(filepath.Join("testdata", "external_anthropic"))
	if err != nil {
		t.Fatal(err)
	}
	target := mod.Targets[0]
	got, closeProvider, err := providerFor(context.Background(), mod, target)
	if err != nil || got == nil || closeProvider == nil {
		t.Fatalf("providerFor(explicit target) = %T, close=%v, err=%v", got, closeProvider != nil, err)
	}
	if err := closeProvider(); err != nil {
		t.Fatal(err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", client.closeCalls)
	}
}

func TestExplicitPlatformPlanUsesProtocolClient(t *testing.T) {
	client := newFakePluginClient(claudePluginSource, "platform")
	useFakePlugins(t, client)
	dir := copyModule(t, filepath.Join("testdata", "external_anthropic"))
	out, err := runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ agent.probe (not in state)") {
		t.Fatalf("plan did not include external resource:\n%s", out)
	}
	if client.validateCalls != 1 || client.diffCalls != 1 || client.closeCalls != 2 {
		t.Fatalf("protocol calls: validate=%d diff=%d close=%d; want 1, 1, 2",
			client.validateCalls, client.diffCalls, client.closeCalls)
	}
}
