// Package plugin adapts Kastor's internal compiler model to the public
// protocol-v1 IR and discovers executable target plugins.
package plugin

import (
	"github.com/weirdGuy/kastor/internal/graph"
	"github.com/weirdGuy/kastor/internal/module"
	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

// ModuleIR makes an ownership-separated protocol value. No internal core
// struct crosses the executable boundary.
func ModuleIR(mod *module.Module, dependencyGraph *graph.Graph) *protocol.Module {
	ir := &protocol.Module{Dependencies: map[string][]string{}}
	for _, model := range mod.Models {
		ir.Models = append(ir.Models, modelIR(model))
	}
	for _, prompt := range mod.Prompts {
		ir.Prompts = append(ir.Prompts, promptIR(prompt))
	}
	for _, tool := range mod.Tools {
		ir.Tools = append(ir.Tools, toolIR(tool))
	}
	for _, agent := range mod.Agents {
		ir.Agents = append(ir.Agents, agentIR(agent))
	}
	for _, target := range mod.Targets {
		ir.Targets = append(ir.Targets, TargetIR(target))
	}
	for _, server := range mod.MCPServers {
		ir.MCPServers = append(ir.MCPServers, mcpServerIR(server))
	}
	if dependencyGraph != nil {
		ir.TopologicalOrder = append([]string(nil), dependencyGraph.Order()...)
		for _, addr := range ir.TopologicalOrder {
			ir.Dependencies[addr] = append([]string(nil), dependencyGraph.Dependencies(addr)...)
		}
	}
	return ir
}

func TargetIR(target *schema.Target) *protocol.Target {
	if target == nil {
		return nil
	}
	return &protocol.Target{
		Name:   target.Name,
		Type:   target.Type,
		Plugin: target.Plugin,
		Output: target.Output,
		Config: cloneMap(target.Config),
	}
}

func modelIR(model *schema.Model) *protocol.Model {
	return &protocol.Model{Name: model.Name, Provider: model.Provider, ID: model.ID, Params: cloneMap(model.Params)}
}

func promptIR(prompt *schema.Prompt) *protocol.Prompt {
	return &protocol.Prompt{
		Name:     prompt.Name,
		Requires: append([]string(nil), prompt.Requires...),
		Body:     prompt.Body,
		Vars:     append([]string(nil), prompt.Vars...),
	}
}

func toolIR(tool *schema.Tool) *protocol.Tool {
	result := &protocol.Tool{Name: tool.Name, Description: tool.Description}
	for _, param := range tool.Params {
		result.Params = append(result.Params, &protocol.ToolParam{
			Name: param.Name, Type: param.Type, Description: param.Description, Default: param.Default,
		})
	}
	if tool.Returns != nil {
		result.Returns = &protocol.ToolReturns{Type: tool.Returns.Type}
	}
	if tool.Source != nil {
		result.Source = &protocol.ToolSource{Kind: tool.Source.Kind, URI: tool.Source.URI}
	}
	return result
}

func agentIR(agent *schema.Agent) *protocol.Agent {
	result := &protocol.Agent{
		Name:             agent.Name,
		Description:      agent.Description,
		Model:            agent.Model,
		SystemPrompt:     agent.SystemPrompt,
		Tools:            append([]string(nil), agent.Tools...),
		RequiresApproval: append([]string(nil), agent.RequiresApproval...),
		DependsOn:        append([]string(nil), agent.DependsOn...),
	}
	for _, input := range agent.Inputs {
		result.Inputs = append(result.Inputs, &protocol.AgentInput{
			Name: input.Name, Type: input.Type, Description: input.Description,
			Optional: input.Optional, Default: input.Default, DefaultRef: input.DefaultRef,
		})
	}
	for _, output := range agent.Outputs {
		result.Outputs = append(result.Outputs, &protocol.AgentOutput{
			Name: output.Name, Type: output.Type, Description: output.Description,
		})
	}
	return result
}

func mcpServerIR(server *schema.MCPServer) *protocol.MCPServer {
	result := &protocol.MCPServer{
		Name: server.Name, Transport: server.Transport, URL: server.URL,
		Command: server.Command, Args: append([]string(nil), server.Args...),
	}
	for _, auth := range server.Auth {
		result.Auth = append(result.Auth, &protocol.MCPAuth{
			Ref: auth.Ref, Targets: append([]string(nil), auth.Targets...),
		})
	}
	return result
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
