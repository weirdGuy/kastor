package main

import (
	"context"
	"fmt"
	"sort"
	"testing"

	corebuild "github.com/weirdGuy/kastor/internal/build"
	"github.com/weirdGuy/kastor/internal/build/eve"
	"github.com/weirdGuy/kastor/internal/build/langgraph"
	"github.com/weirdGuy/kastor/internal/module"
	pluginruntime "github.com/weirdGuy/kastor/internal/plugin"
	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

type fakePluginClient struct {
	metadata      protocol.Metadata
	validateCalls int
	generateCalls int
	diffCalls     int
	closeCalls    int
}

func newFakePluginClient(source string, kind protocol.Kind) *fakePluginClient {
	return &fakePluginClient{metadata: protocol.Metadata{
		Protocol: protocol.Version,
		Source:   source,
		Version:  "0.1.0",
		Kinds:    []protocol.Kind{kind},
	}}
}

func useFakePlugins(t *testing.T, clients ...*fakePluginClient) {
	t.Helper()
	bySource := map[string]*fakePluginClient{}
	for _, client := range clients {
		bySource[client.metadata.Source] = client
	}
	previous := openPlugin
	openPlugin = func(_ context.Context, localName string, requirement *schema.PluginRequirement) (pluginruntime.Client, error) {
		client := bySource[requirement.Source]
		if client == nil {
			return nil, fmt.Errorf("plugin.%s: no test plugin for %q", localName, requirement.Source)
		}
		return client, nil
	}
	t.Cleanup(func() { openPlugin = previous })
}

func useFakeCodegenPlugins(t *testing.T) (*fakePluginClient, *fakePluginClient) {
	t.Helper()
	langgraphClient := newFakePluginClient(langgraphPluginSource, protocol.KindCodegen)
	eveClient := newFakePluginClient(evePluginSource, protocol.KindCodegen)
	useFakePlugins(t, langgraphClient, eveClient)
	return langgraphClient, eveClient
}

func (c *fakePluginClient) Metadata() protocol.Metadata { return c.metadata }

func (c *fakePluginClient) Validate(_ context.Context, request *protocol.ValidateRequest) (*protocol.ValidateResponse, error) {
	c.validateCalls++
	return validateLikeOfficialPlugin(c.metadata.Source, request), nil
}

func (c *fakePluginClient) Generate(_ context.Context, request *protocol.GenerateRequest) (*protocol.GenerateResponse, error) {
	c.generateCalls++
	mod, target := moduleFromProtocol(request.Module, request.Target)
	var generator corebuild.Generator
	switch c.metadata.Source {
	case langgraphPluginSource:
		generator = langgraph.Generator{}
	case evePluginSource:
		generator = eve.Generator{}
	default:
		return nil, fmt.Errorf("no test generator for %q", c.metadata.Source)
	}
	files, err := generator.Generate(&corebuild.Job{Module: mod, Target: target})
	if err != nil {
		return nil, err
	}
	result := make([]protocol.File, len(files))
	for i, file := range files {
		result[i] = protocol.File{Path: file.Path, Data: file.Data, Preserve: file.Preserve}
	}
	return &protocol.GenerateResponse{Files: result}, nil
}

func (c *fakePluginClient) Read(context.Context, *protocol.ReadRequest) (*protocol.ReadResponse, error) {
	return &protocol.ReadResponse{}, nil
}

func (c *fakePluginClient) Create(context.Context, *protocol.CreateRequest) (*protocol.CreateResponse, error) {
	return &protocol.CreateResponse{ID: "fake-id"}, nil
}

func (c *fakePluginClient) Update(context.Context, *protocol.UpdateRequest) error { return nil }
func (c *fakePluginClient) Delete(context.Context, *protocol.DeleteRequest) error { return nil }

func (c *fakePluginClient) Diff(context.Context, *protocol.DiffRequest) (*protocol.DiffResponse, error) {
	c.diffCalls++
	return &protocol.DiffResponse{}, nil
}

func (c *fakePluginClient) Check(context.Context, *protocol.CheckRequest) (*protocol.CheckResponse, error) {
	return &protocol.CheckResponse{}, nil
}

func (c *fakePluginClient) Close() error {
	c.closeCalls++
	return nil
}

func validateLikeOfficialPlugin(source string, request *protocol.ValidateRequest) *protocol.ValidateResponse {
	if request == nil || request.Module == nil || request.Target == nil {
		return &protocol.ValidateResponse{Diagnostics: []protocol.Diagnostic{externalDiagnostic("", "incomplete validation request", "")}}
	}
	target := request.Target
	referenced := map[string]bool{}
	for _, tool := range request.Module.Tools {
		if tool.Source == nil || tool.Source.Kind != "mcp" {
			continue
		}
		server, _, err := protocol.ParseMCPURI(tool.Source.URI)
		if err == nil {
			referenced[server] = true
		}
	}

	var diagnostics []protocol.Diagnostic
	keys := make([]string, 0, len(target.Config))
	for key := range target.Config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if source != claudePluginSource || (key != "api_key_env" && key != "vault_id") {
			diagnostics = append(diagnostics, externalDiagnostic(target.Addr(), "unsupported config attribute", key))
			continue
		}
		if _, ok := target.Config[key].(string); !ok {
			diagnostics = append(diagnostics, externalDiagnostic(target.Addr(), "config attribute must be a string", key))
		}
	}

	for _, server := range request.Module.MCPServers {
		if !referenced[server.Name] {
			continue
		}
		if server.Transport == "stdio" && source != langgraphPluginSource {
			detail := "Eve connections require an HTTP endpoint"
			if source == claudePluginSource {
				detail = "Claude Managed Agents requires an HTTP endpoint"
			}
			diagnostics = append(diagnostics, externalDiagnostic(server.Addr(), "stdio transport is not supported", detail))
		}
		auth, ok := server.AuthFor(target.Addr())
		if !ok {
			continue
		}
		scheme, _, err := protocol.ParseCredentialRef(auth.Ref)
		if err != nil {
			continue
		}
		wantScheme := protocol.SchemeEnv
		if source == claudePluginSource {
			wantScheme = protocol.SchemeConnection
		}
		if scheme != wantScheme {
			diagnostics = append(diagnostics, externalDiagnostic(server.Addr(), "unsupported credential scheme", scheme+"://"))
			continue
		}
		if source == claudePluginSource && scheme == protocol.SchemeConnection {
			vault, ok := target.ConfigString("vault_id")
			if !ok || vault == "" {
				diagnostics = append(diagnostics, externalDiagnostic(target.Addr(), "vault_id is required for connection credentials", auth.Ref))
			}
		}
	}
	return &protocol.ValidateResponse{Diagnostics: diagnostics}
}

func externalDiagnostic(addr, summary, detail string) protocol.Diagnostic {
	return protocol.Diagnostic{Severity: protocol.SeverityError, Addr: addr, Summary: summary, Detail: detail}
}

func moduleFromProtocol(input *protocol.Module, selected *protocol.Target) (*module.Module, *schema.Target) {
	mod := &module.Module{}
	for _, model := range input.Models {
		mod.Models = append(mod.Models, &schema.Model{Name: model.Name, Provider: model.Provider, ID: model.ID, Params: model.Params})
	}
	for _, prompt := range input.Prompts {
		mod.Prompts = append(mod.Prompts, &schema.Prompt{Name: prompt.Name, Requires: prompt.Requires, Body: prompt.Body, Vars: prompt.Vars})
	}
	for _, tool := range input.Tools {
		converted := &schema.Tool{Name: tool.Name, Description: tool.Description}
		for _, param := range tool.Params {
			converted.Params = append(converted.Params, &schema.ToolParam{Name: param.Name, Type: param.Type, Description: param.Description, Default: param.Default})
		}
		if tool.Returns != nil {
			converted.Returns = &schema.ToolReturns{Type: tool.Returns.Type}
		}
		if tool.Source != nil {
			converted.Source = &schema.ToolSource{Kind: tool.Source.Kind, URI: tool.Source.URI}
		}
		mod.Tools = append(mod.Tools, converted)
	}
	for _, agent := range input.Agents {
		converted := &schema.Agent{
			Name: agent.Name, Description: agent.Description, Model: agent.Model,
			SystemPrompt: agent.SystemPrompt, Tools: agent.Tools,
			RequiresApproval: agent.RequiresApproval, DependsOn: agent.DependsOn,
		}
		for _, item := range agent.Inputs {
			converted.Inputs = append(converted.Inputs, &schema.AgentInput{
				Name: item.Name, Type: item.Type, Description: item.Description,
				Optional: item.Optional, Default: item.Default, DefaultRef: item.DefaultRef,
			})
		}
		for _, item := range agent.Outputs {
			converted.Outputs = append(converted.Outputs, &schema.AgentOutput{Name: item.Name, Type: item.Type, Description: item.Description})
		}
		mod.Agents = append(mod.Agents, converted)
	}
	for _, server := range input.MCPServers {
		converted := &schema.MCPServer{
			Name: server.Name, Transport: server.Transport, URL: server.URL,
			Command: server.Command, Args: server.Args,
		}
		for _, auth := range server.Auth {
			converted.Auth = append(converted.Auth, &schema.MCPAuth{Ref: auth.Ref, Targets: auth.Targets})
		}
		mod.MCPServers = append(mod.MCPServers, converted)
	}
	for _, target := range input.Targets {
		mod.Targets = append(mod.Targets, &schema.Target{
			Name: target.Name, Type: target.Type, Plugin: target.Plugin,
			Output: target.Output, Config: target.Config,
		})
	}
	return mod, &schema.Target{
		Name: selected.Name, Type: selected.Type, Plugin: selected.Plugin,
		Output: selected.Output, Config: selected.Config,
	}
}
