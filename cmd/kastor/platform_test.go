package main

import (
	"testing"

	"github.com/weirdGuy/kastor/internal/schema"
)

func TestClaudeAgentsProviderRegistered(t *testing.T) {
	const env = "KASTOR_TEST_CLAUDE_REGISTRY_KEY"
	t.Setenv(env, "test-key")
	factory, exists := providerFactories["claude_agents"]
	if !exists {
		t.Fatal("providerFactories does not contain claude_agents")
	}
	got, err := factory(&schema.Target{
		Name: "claude_agents",
		Type: "platform",
		Auth: &schema.Auth{APIKeyEnv: env},
	})
	if err != nil || got == nil {
		t.Fatalf("claude_agents factory = %v, %v; want provider, nil", got, err)
	}
}
