package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/weirdGuy/kastor/internal/module"
	"github.com/weirdGuy/kastor/internal/schema"
)

// Official source addresses are the stable identities stored in
// required_plugins and, later, the lock file. The short names remain only as
// v0.2 compatibility aliases while modules migrate to explicit selectors.
const (
	langgraphPluginSource = "github.com/getkastordev/kastor-langgraph"
	evePluginSource       = "github.com/getkastordev/kastor-eve"
	claudePluginSource    = "github.com/getkastordev/kastor-anthropic"
	memoryPluginSource    = "github.com/getkastordev/kastor-memory"
)

// targetPluginSource resolves a target instance to an implementation
// identity. Explicit targets always resolve through required_plugins. An empty
// selector is the v0.2 compatibility path, where the target label was also the
// implementation name.
func targetPluginSource(mod *module.Module, tgt *schema.Target) (string, error) {
	if tgt.Plugin == "" {
		return tgt.Name, nil
	}
	requirement, ok := mod.PluginRequirement(tgt.Plugin)
	if !ok {
		return "", fmt.Errorf("%s: plugin %q is not declared in kastor.required_plugins", tgt.Addr(), tgt.Plugin)
	}
	return requirement.Source, nil
}

type targetCapabilities struct {
	stdio                 bool
	envCredentials        bool
	connectionCredentials bool
	requiresVault         bool
	stringConfigKeys      map[string]bool
}

var builtinCapabilities = map[string]targetCapabilities{
	"langgraph": {
		stdio:          true,
		envCredentials: true,
	},
	langgraphPluginSource: {
		stdio:          true,
		envCredentials: true,
	},
	"eve": {
		envCredentials: true,
	},
	evePluginSource: {
		envCredentials: true,
	},
	"claude_agents": {
		connectionCredentials: true,
		requiresVault:         true,
		stringConfigKeys:      map[string]bool{"api_key_env": true, "vault_id": true},
	},
	claudePluginSource: {
		connectionCredentials: true,
		requiresVault:         true,
		stringConfigKeys:      map[string]bool{"api_key_env": true, "vault_id": true},
	},
	"memory": {
		envCredentials: true,
	},
	memoryPluginSource: {
		envCredentials: true,
	},
}

// validateTargetPlugins is the temporary in-process implementation adapter.
// Core parsing and module resolution know only opaque config and plugin
// identities; target-specific transport and credential rules live here until
// the protocol-v1 capability RPC replaces this table (KAS-77).
func validateTargetPlugins(mod *module.Module) error {
	referenced := referencedMCPServers(mod)
	var errs []error
	for _, tgt := range mod.Targets {
		source, err := targetPluginSource(mod, tgt)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		caps, known := builtinCapabilities[source]
		if !known {
			continue
		}
		configKeys := make([]string, 0, len(tgt.Config))
		for key := range tgt.Config {
			configKeys = append(configKeys, key)
		}
		sort.Strings(configKeys)
		for _, key := range configKeys {
			if !caps.stringConfigKeys[key] {
				errs = append(errs, fmt.Errorf("%s: plugin %q does not support config attribute %q", tgt.Addr(), source, key))
				continue
			}
			if _, ok := tgt.Config[key].(string); !ok {
				errs = append(errs, fmt.Errorf("%s: plugin %q config attribute %q must be a string", tgt.Addr(), source, key))
			}
		}
		for _, server := range mod.MCPServers {
			if !referenced[server.Name] {
				continue
			}
			if server.Transport == "stdio" && !caps.stdio {
				errs = append(errs, fmt.Errorf("%s: transport \"stdio\" cannot be bound on %s; plugin %q does not advertise local-process support",
					server.Addr(), tgt.Addr(), source))
			}
			auth, ok := server.AuthFor(tgt.Addr())
			if !ok {
				continue
			}
			scheme, _, err := schema.ParseCredentialRef(auth.Ref)
			if err != nil {
				continue
			}
			switch scheme {
			case schema.SchemeEnv:
				if !caps.envCredentials {
					errs = append(errs, fmt.Errorf("%s: auth ref %q cannot be bound on %s; plugin %q does not support env:// credentials",
						server.Addr(), auth.Ref, tgt.Addr(), source))
				}
			case schema.SchemeConnection:
				if !caps.connectionCredentials {
					errs = append(errs, fmt.Errorf("%s: auth ref %q cannot be bound on %s; plugin %q does not support connection:// credentials",
						server.Addr(), auth.Ref, tgt.Addr(), source))
					continue
				}
				if caps.requiresVault {
					vault, ok := tgt.ConfigString("vault_id")
					if !ok || vault == "" {
						errs = append(errs, fmt.Errorf("%s: auth ref %q needs a vault to resolve against; %s plugin config must declare \"vault_id\"",
							server.Addr(), auth.Ref, tgt.Addr()))
					}
				}
			}
		}
	}
	return errors.Join(errs...)
}

func referencedMCPServers(mod *module.Module) map[string]bool {
	referenced := map[string]bool{}
	for _, tool := range mod.Tools {
		if tool.Source == nil || tool.Source.Kind != "mcp" {
			continue
		}
		server, _, err := schema.ParseMCPURI(tool.Source.URI)
		if err == nil {
			referenced[server] = true
		}
	}
	return referenced
}
