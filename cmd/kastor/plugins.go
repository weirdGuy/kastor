package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/weirdGuy/kastor/internal/module"
	pluginruntime "github.com/weirdGuy/kastor/internal/plugin"
	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

// Official source addresses are the stable identities stored in
// required_plugins and, later, the lock file. The short names remain only as
// v0.2 compatibility aliases while modules migrate to explicit selectors.
const (
	langgraphPluginSource = "github.com/getkastordev/kastor-langgraph"
	evePluginSource       = "github.com/getkastordev/kastor-eve"
	claudePluginSource    = "github.com/getkastordev/kastor-anthropic"
)

// openPlugin is a narrow test seam around executable discovery and startup.
// Production always uses pluginruntime.Open.
var openPlugin = pluginruntime.Open

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
	"eve": {
		envCredentials: true,
	},
	"claude_agents": {
		connectionCredentials: true,
		requiresVault:         true,
		stringConfigKeys:      map[string]bool{"api_key_env": true, "vault_id": true},
	},
	"memory": {
		envCredentials: true,
	},
}

// validateTargetPlugins delegates explicit selectors to the executable that
// owns them. The static table is retained only for v0.2 targets with no
// plugin selector, so old modules keep working during the migration window.
func validateTargetPlugins(ctx context.Context, warnings io.Writer, mod *module.Module) error {
	referenced := referencedMCPServers(mod)
	var errs []error
	clients := map[string]pluginruntime.Client{}

	for _, tgt := range mod.Targets {
		if tgt.Plugin != "" {
			client := clients[tgt.Plugin]
			if client == nil {
				var err error
				client, err = openTargetPlugin(ctx, mod, tgt)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				clients[tgt.Plugin] = client
			} else if err := requireTargetKind(tgt, client.Metadata()); err != nil {
				errs = append(errs, err)
				continue
			}
			response, err := client.Validate(ctx, &protocol.ValidateRequest{
				Module: pluginruntime.ModuleIR(mod, nil),
				Target: pluginruntime.TargetIR(tgt),
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: plugin validation failed: %w", tgt.Addr(), err))
				continue
			}
			if response == nil {
				errs = append(errs, fmt.Errorf("%s: plugin validation returned no response", tgt.Addr()))
				continue
			}
			for _, diagnostic := range response.Diagnostics {
				if diagnostic.Severity == protocol.SeverityWarning {
					fmt.Fprintf(warnings, "Warning: %s\n", diagnosticError(diagnostic))
					continue
				}
				errs = append(errs, diagnosticError(diagnostic))
			}
			continue
		}

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
	localNames := make([]string, 0, len(clients))
	for localName := range clients {
		localNames = append(localNames, localName)
	}
	sort.Strings(localNames)
	for _, localName := range localNames {
		client := clients[localName]
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("plugin.%s: close: %w", localName, err))
		}
	}
	return errors.Join(errs...)
}

func openTargetPlugin(ctx context.Context, mod *module.Module, tgt *schema.Target) (pluginruntime.Client, error) {
	requirement, ok := mod.PluginRequirement(tgt.Plugin)
	if !ok {
		return nil, fmt.Errorf("%s: plugin %q is not declared in kastor.required_plugins", tgt.Addr(), tgt.Plugin)
	}
	client, err := openPlugin(ctx, tgt.Plugin, requirement)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tgt.Addr(), err)
	}
	if err := requireTargetKind(tgt, client.Metadata()); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func requireTargetKind(tgt *schema.Target, metadata protocol.Metadata) error {
	kind := protocol.Kind(tgt.Type)
	if metadata.Supports(kind) {
		return nil
	}
	return fmt.Errorf("%s: plugin %q does not advertise %s support", tgt.Addr(), metadata.Source, kind)
}

func diagnosticError(diagnostic protocol.Diagnostic) error {
	message := diagnostic.Summary
	if diagnostic.Addr != "" {
		message = diagnostic.Addr + ": " + message
	}
	if diagnostic.Detail != "" {
		message += " (" + diagnostic.Detail + ")"
	}
	return errors.New(message)
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
