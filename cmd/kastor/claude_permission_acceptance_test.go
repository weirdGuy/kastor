package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/weirdGuy/kastor/internal/provider"
)

// KAS-57 acceptance helpers. Declaring a tool in the spec is the grant, so the
// checks here are about what the deployed agent may do, not only about what
// kastor sent: a create that records the right attribute but leaves the agent
// unable to call its tools is the exact failure this ticket closes.
const (
	acceptanceAllowPolicy = "always_allow"
	// acceptanceGatedPolicy is the out-of-band change the drift test makes.
	// It is not "always_deny": managed-agents-2026-04-01 rejects that value
	// (400, `"always_deny" is not a valid value`), so always_ask is the only
	// policy the API accepts that is not the grant. Do not "fix" this to deny.
	acceptanceGatedPolicy = "always_ask"
	// acceptanceMCPToolEnv overrides the MCP tool the acceptance module
	// declares, for servers that expose something other than "echo".
	acceptanceMCPToolEnv     = "KASTOR_MCP_ACCEPTANCE_TOOL"
	acceptanceDefaultMCPTool = "echo"
	// acceptanceSessionTimeout bounds the live tool-call turn. The session is a
	// container start plus one model turn, so this is generous on purpose.
	acceptanceSessionTimeout = 5 * time.Minute
)

// acceptanceMCPTool is the tool name the acceptance agent declares on its MCP
// server. Only the name reaches Managed Agents — the tool block's params are
// not sent, because the server owns the schema — so pointing the module at
// whatever tool the configured server actually exposes is enough to exercise
// the permission end to end.
func acceptanceMCPTool() string {
	if name := strings.TrimSpace(os.Getenv(acceptanceMCPToolEnv)); name != "" {
		return name
	}
	return acceptanceDefaultMCPTool
}

// retargetAcceptanceMCPTool rewrites the copied module's MCP URI so the agent
// declares a tool the configured server serves. A tool the server does not
// expose is never offered to the model, and a turn that never calls the tool
// cannot demonstrate anything about its permission.
func retargetAcceptanceMCPTool(t *testing.T, dir string) {
	t.Helper()
	name := acceptanceMCPTool()
	if name == acceptanceDefaultMCPTool {
		return
	}
	path := filepath.Join(dir, "acceptance.tool")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance tool: %v", err)
	}
	const old = "mcp://kastor-acceptance/" + acceptanceDefaultMCPTool
	if !strings.Contains(string(data), old) {
		t.Fatalf("acceptance tool does not declare %s", old)
	}
	updated := strings.Replace(string(data), old, "mcp://kastor-acceptance/"+name, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("retarget acceptance tool: %v", err)
	}
	t.Logf("acceptance MCP tool retargeted to %q via %s", name, acceptanceMCPToolEnv)
}

// retargetAcceptanceMCPServer rewrites the copied module's mcp_server url to
// the server the run was given. Since KAS-63 the address is spec, not
// environment (SPEC.md §3.6), so the harness supplies it by editing the copy
// rather than by exporting a variable the provider reads at plan time.
func retargetAcceptanceMCPServer(t *testing.T, dir string) {
	t.Helper()
	url := strings.TrimSpace(os.Getenv(claudeAcceptanceMCPEnv))
	if url == "" {
		t.Fatalf("%s is unset", claudeAcceptanceMCPEnv)
	}
	path := filepath.Join(dir, "kastor.hcl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance project file: %v", err)
	}
	const placeholder = `"https://mcp.invalid/acceptance"`
	if !strings.Contains(string(data), placeholder) {
		t.Fatalf("acceptance project file does not contain the url placeholder %s", placeholder)
	}
	updated := strings.Replace(string(data), placeholder, strconv.Quote(url), 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("retarget acceptance MCP server: %v", err)
	}
	t.Logf("acceptance MCP server url set to %q via %s", url, claudeAcceptanceMCPEnv)
}

// assertAcceptanceToolGrants reads the live agent and checks that every tool
// in the closure carries the grant. Reading the remote (rather than the
// request kastor built) is the point: it proves the platform stored the
// permission instead of applying its own restrictive default.
func assertAcceptanceToolGrants(t *testing.T, p provider.Provider, id, want string) {
	t.Helper()
	remote, found, err := p.Read(context.Background(), id)
	if err != nil {
		t.Fatalf("read Claude agent %s: %v", id, err)
	}
	if !found {
		t.Fatalf("Claude agent %s was not found", id)
	}

	tools, ok := remote["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("remote agent declares no tools: %#v", remote["tools"])
	}
	seen := 0
	for i, rawToolset := range tools {
		toolset := rawToolset.(map[string]any)
		configs, ok := toolset["configs"].([]any)
		if !ok {
			t.Fatalf("remote tools[%d].configs = %#v, want an array", i, toolset["configs"])
		}
		for j, rawConfig := range configs {
			config := rawConfig.(map[string]any)
			seen++
			policy, ok := config["permission_policy"].(map[string]any)
			if !ok {
				t.Errorf("remote tools[%d].configs[%d] (%v) has no permission_policy: %#v",
					i, j, config["name"], config)
				continue
			}
			if got := policy["type"]; got != want {
				t.Errorf("remote tools[%d].configs[%d] (%v) permission_policy.type = %v, want %q",
					i, j, config["name"], got, want)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("remote agent declares toolsets but no tool configs: %#v", tools)
	}
	t.Logf("remote agent %s: %d declared tools carry permission_policy %q", id, seen, want)
}

// assertAcceptanceToolPolicy checks one named tool's live permission, for the
// half-gated state the drift step creates: the tools kastor did not touch must
// keep their grant.
func assertAcceptanceToolPolicy(t *testing.T, p provider.Provider, id, tool, want string) {
	t.Helper()
	remote, found, err := p.Read(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("read Claude agent %s: %v (found=%v)", id, err, found)
	}
	for _, rawToolset := range remote["tools"].([]any) {
		for _, rawConfig := range rawToolset.(map[string]any)["configs"].([]any) {
			config := rawConfig.(map[string]any)
			if fmt.Sprintf("%v", config["name"]) != tool {
				continue
			}
			policy, _ := config["permission_policy"].(map[string]any)
			if policy == nil || policy["type"] != want {
				t.Fatalf("remote tool %q permission_policy = %#v, want %q", tool, config["permission_policy"], want)
			}
			return
		}
	}
	t.Fatalf("remote agent %s declares no tool named %q", id, tool)
}

// gateAcceptanceToolOutOfBand takes the grant away from one MCP tool the way
// a console edit would: a full-replacement update sent outside kastor, leaving
// state untouched. It returns the tool it gated.
func gateAcceptanceToolOutOfBand(t *testing.T, p provider.Provider, id string) string {
	t.Helper()
	remote, found, err := p.Read(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("read Claude agent %s: %v (found=%v)", id, err, found)
	}

	request := provider.Object{}
	for key, value := range remote {
		switch key {
		case "id", "type", "created_at", "updated_at", "archived_at":
			// Response envelope; not an updatable field.
		default:
			request[key] = value
		}
	}

	gated := ""
	for _, rawToolset := range request["tools"].([]any) {
		toolset := rawToolset.(map[string]any)
		if toolset["type"] != "mcp_toolset" {
			continue
		}
		for _, rawConfig := range toolset["configs"].([]any) {
			config := rawConfig.(map[string]any)
			config["permission_policy"] = map[string]any{"type": acceptanceGatedPolicy}
			gated = fmt.Sprintf("%v", config["name"])
			break
		}
		break
	}
	if gated == "" {
		t.Fatal("acceptance agent declares no MCP tool to gate")
	}

	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode out-of-band update: %v", err)
	}
	var params anthropic.BetaAgentUpdateParams
	param.SetJSON(body, &params)
	client := acceptanceClient(t)
	if _, err := client.Beta.Agents.Update(context.Background(), id, params); err != nil {
		t.Fatalf("gate %q out of band: %v", gated, err)
	}
	t.Logf("out-of-band edit: MCP tool %q set to %s on %s", gated, acceptanceGatedPolicy, id)
	return gated
}

// assertMCPToolIsCallable runs the deployed agent for one turn and checks that
// its MCP tool actually executes. This is the acceptance criterion no CRUD
// assertion can stand in for: with the permission unset the platform evaluates
// the call as a denial, the plan is still clean, and the agent is useless.
func assertMCPToolIsCallable(t *testing.T, agentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), acceptanceSessionTimeout)
	defer cancel()
	client := acceptanceClient(t)

	stamp := time.Now().UTC().Format("20060102T150405")
	environment, err := client.Beta.Environments.New(ctx, anthropic.BetaEnvironmentNewParams{
		Name: "kastor-kas-57-" + stamp,
		Config: anthropic.BetaEnvironmentNewParamsConfigUnion{
			OfCloud: &anthropic.BetaCloudConfigParams{
				Networking: anthropic.BetaCloudConfigParamsNetworkingUnion{
					OfUnrestricted: &anthropic.BetaUnrestrictedNetworkParam{},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create acceptance environment: %v", err)
	}

	session, err := client.Beta.Sessions.New(ctx, anthropic.BetaSessionNewParams{
		Agent:         anthropic.BetaSessionNewParamsAgentUnion{OfString: anthropic.String(agentID)},
		EnvironmentID: environment.ID,
		Title:         anthropic.String("KAS-57 tool permission acceptance " + stamp),
	})
	if err != nil {
		t.Fatalf("create acceptance session: %v", err)
	}
	t.Logf("session trace: https://platform.claude.com/workspaces/default/sessions/%s", session.ID)

	// Sessions and environments are reversible, unlike the agent archive the
	// lifecycle test accepts, so this run leaves nothing behind.
	t.Cleanup(func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), time.Minute)
		defer cancelCleanup()
		if _, err := client.Beta.Sessions.Delete(cleanup, session.ID, anthropic.BetaSessionDeleteParams{}); err != nil {
			t.Errorf("delete acceptance session %s: %v", session.ID, err)
			return
		}
		if _, err := client.Beta.Environments.Delete(cleanup, environment.ID, anthropic.BetaEnvironmentDeleteParams{}); err != nil {
			t.Errorf("delete acceptance environment %s: %v", environment.ID, err)
		}
	})

	// Stream first: the stream only carries events emitted after it opens.
	stream := client.Beta.Sessions.Events.StreamEvents(ctx, session.ID, anthropic.BetaSessionEventStreamParams{})
	defer stream.Close()

	tool := acceptanceMCPTool()
	prompt := fmt.Sprintf(
		"Call your %q MCP tool exactly once, with any reasonable input for it "+
			"(use \"kastor-kas-57\" wherever a free-text value is needed). "+
			"Then reply with what it returned. Do not ask for confirmation.", tool)
	if _, err := client.Beta.Sessions.Events.Send(ctx, session.ID, anthropic.BetaSessionEventSendParams{
		Events: []anthropic.BetaManagedAgentsEventParamsUnion{{
			OfUserMessage: &anthropic.BetaManagedAgentsUserMessageEventParams{
				Type: anthropic.BetaManagedAgentsUserMessageEventParamsTypeUserMessage,
				Content: []anthropic.BetaManagedAgentsUserMessageEventParamsContentUnion{{
					OfText: &anthropic.BetaManagedAgentsTextBlockParam{
						Type: anthropic.BetaManagedAgentsTextBlockTypeText,
						Text: prompt,
					},
				}},
			},
		}},
	}); err != nil {
		t.Fatalf("send acceptance user message: %v", err)
	}

	var (
		permission string
		called     bool
		toolErr    bool
		blocked    bool
		reply      string
	)
	for stream.Next() {
		switch event := stream.Current().AsAny().(type) {
		case anthropic.BetaManagedAgentsAgentMCPToolUseEvent:
			called = true
			permission = string(event.EvaluatedPermission)
			t.Logf("agent.mcp_tool_use %s/%s evaluated_permission=%q", event.MCPServerName, event.Name, permission)
		case anthropic.BetaManagedAgentsAgentMessageEvent:
			for _, block := range event.Content {
				reply += block.Text
			}
		case anthropic.BetaManagedAgentsAgentMCPToolResultEvent:
			toolErr = event.IsError
		case anthropic.BetaManagedAgentsSessionErrorEvent:
			t.Errorf("session error: %s", event.Error.Message)
		case anthropic.BetaManagedAgentsSessionStatusIdleEvent:
			// requires_action means the platform is waiting on a confirmation
			// or a tool result — for an MCP tool that is the ask/deny gate this
			// ticket exists to remove, so it ends the turn as a failure rather
			// than something to answer.
			if event.StopReason.Type == "requires_action" {
				blocked = true
			}
			goto done
		case anthropic.BetaManagedAgentsSessionStatusTerminatedEvent:
			goto done
		}
	}
done:
	if err := stream.Err(); err != nil {
		t.Fatalf("stream acceptance session events: %v", err)
	}

	switch {
	case !called:
		// Most often the configured server does not serve a tool by this name,
		// so the platform never offers it to the model — a setup problem, not
		// a permission one. The agent's own reply usually says which.
		t.Errorf("agent never invoked MCP tool %q, so the turn cannot demonstrate the grant.\n"+
			"Check that the server behind %s serves that tool, or name the one it does serve in %s.\n"+
			"agent reply: %s", tool, claudeAcceptanceMCPEnv, acceptanceMCPToolEnv, reply)
	case permission != "allow":
		t.Errorf("MCP tool evaluated_permission = %q, want \"allow\" — the deployed agent cannot call a tool it declares", permission)
	case blocked:
		t.Errorf("session went idle awaiting action after the MCP tool call; the tool is gated, not granted")
	case toolErr:
		// Not a permission failure: the tool ran and the server answered with
		// an error. Report it so a broken acceptance MCP server is not read as
		// a kastor regression.
		t.Errorf("MCP tool returned is_error; check the server behind %s", claudeAcceptanceMCPEnv)
	default:
		t.Logf("MCP tool executed with evaluated_permission=allow and no console edit")
	}
}

func acceptanceClient(t *testing.T) anthropic.Client {
	t.Helper()
	return anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
}
