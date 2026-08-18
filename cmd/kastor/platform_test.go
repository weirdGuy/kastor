package main

import (
	"path/filepath"
	"testing"

	"github.com/weirdGuy/kastor/internal/module"
	"github.com/weirdGuy/kastor/internal/schema"
)

func TestClaudeAgentsProviderRegistered(t *testing.T) {
	const env = "KASTOR_TEST_CLAUDE_REGISTRY_KEY"
	t.Setenv(env, "test-key")
	factory, exists := providerFactories[claudePluginSource]
	if !exists {
		t.Fatalf("providerFactories does not contain %s", claudePluginSource)
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

func TestExplicitMemoryPluginRegistered(t *testing.T) {
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
	got, err := providerFor(mod, target)
	if err != nil || got == nil {
		t.Fatalf("providerFor(target.memory) = %v, %v; want provider, nil", got, err)
	}
}
