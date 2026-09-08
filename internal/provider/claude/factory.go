package claude

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/getkastordev/kastor/internal/provider"
	"github.com/getkastordev/kastor/internal/schema"
)

// Factory validates the Claude plugin's opaque target config, resolves its
// API-key environment variable, and constructs the provider. A target without
// api_key_env uses the documented ambient credential variable.
func Factory(tgt *schema.Target) (provider.Provider, error) {
	if tgt == nil {
		return nil, fmt.Errorf("claude: target is nil")
	}
	for key := range tgt.Config {
		if key != "api_key_env" && key != "vault_id" {
			return nil, fmt.Errorf("unsupported config attribute %q", key)
		}
	}
	env := defaultAPIKeyEnv
	if configured, exists := tgt.Config["api_key_env"]; exists {
		var ok bool
		env, ok = configured.(string)
		if !ok {
			return nil, fmt.Errorf("config.api_key_env must be a string")
		}
		if env == "" {
			return nil, fmt.Errorf("config.api_key_env is empty; set it to %s", defaultAPIKeyEnv)
		}
	}
	key, exists := os.LookupEnv(env)
	if !exists || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("Claude Managed Agents API key is missing; set %s", env)
	}
	p := New(option.WithAPIKey(key))
	p.authEnv = env
	// The vault backs kastor doctor's credential verification and nothing
	// else: plan and apply never contact it (SPEC.md §3.5, §5.3).
	if configured, exists := tgt.Config["vault_id"]; exists {
		var ok bool
		p.vaultID, ok = configured.(string)
		if !ok {
			return nil, fmt.Errorf("config.vault_id must be a string")
		}
	}
	return p, nil
}
