package claude

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/schema"
)

// Factory resolves the target's API-key environment variable and constructs
// the registered Claude provider. A target without an auth block uses the
// documented ambient credential variable.
func Factory(tgt *schema.Target) (provider.Provider, error) {
	if tgt == nil {
		return nil, fmt.Errorf("claude: target is nil")
	}
	env := defaultAPIKeyEnv
	if tgt.Auth != nil {
		env = tgt.Auth.APIKeyEnv
		if env == "" {
			return nil, fmt.Errorf("auth.api_key_env is empty; set it to %s", defaultAPIKeyEnv)
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
	p.vaultID = tgt.VaultID
	return p, nil
}
