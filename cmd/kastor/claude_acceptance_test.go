package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/schema"
	"github.com/weirdGuy/kastor/internal/state"
)

const (
	claudeAcceptanceTarget    = "claude_agents"
	claudeAcceptanceAddr      = "agent.kas_38_acceptance"
	claudeAcceptanceMarkerKey = "kastor_acceptance_test"
	claudeAcceptanceMCPEnv    = "KASTOR_MCP_KASTOR_ACCEPTANCE_URL"
)

// TestClaudeManagedAgentsAcceptance exercises KAS-22's CLI apply/destroy
// lifecycle against the registered Claude provider. Archive is irreversible,
// so every run creates one permanently retained agent. The metadata assertions
// make those test agents identifiable in the Anthropic organization. The MCP
// URL supplied through claudeAcceptanceMCPEnv must expose an "echo" tool.
func TestClaudeManagedAgentsAcceptance(t *testing.T) {
	requireClaudeAcceptance(t)

	marker := "KAS-38:" + time.Now().UTC().Format("20060102T150405.000000000Z")
	realFactory := providerFactories[claudeAcceptanceTarget]
	if realFactory == nil {
		t.Fatalf("providerFactories does not contain %s", claudeAcceptanceTarget)
	}
	providerFactories[claudeAcceptanceTarget] = func(target *schema.Target) (provider.Provider, error) {
		realProvider, err := realFactory(target)
		if err != nil {
			return nil, err
		}
		return &acceptanceMetadataProvider{Provider: realProvider, marker: marker}, nil
	}
	t.Cleanup(func() { providerFactories[claudeAcceptanceTarget] = realFactory })

	dir := copyModule(t, "testdata/claude_acceptance")
	retargetAcceptanceMCPTool(t, dir)
	retargetAcceptanceMCPServer(t, dir)
	destroyed := false
	t.Cleanup(func() {
		if destroyed {
			return
		}
		out, err := runCLI(t, "destroy", "--target", claudeAcceptanceTarget, dir)
		if err != nil {
			t.Errorf("cleanup destroy: %v\n%s", err, out)
		}
	})

	// 1. Apply creates the agent through the real provider and records its ID.
	out := runAcceptanceCLI(t, "apply", dir)
	assertOutputContains(t, out,
		"+ "+claudeAcceptanceAddr+" (not in state)",
		"Applied target.claude_agents: 1 created, 0 updated, 0 deleted.",
	)

	st := loadAcceptanceState(t, dir)
	resource := st.Target(claudeAcceptanceTarget).Resources[claudeAcceptanceAddr]
	if resource == nil || resource.ID == "" {
		t.Fatalf("state does not track a remote ID for %s", claudeAcceptanceAddr)
	}

	realProvider, err := realFactory(&schema.Target{
		Name: claudeAcceptanceTarget,
		Type: "platform",
		Auth: &schema.Auth{APIKeyEnv: "ANTHROPIC_API_KEY"},
	})
	if err != nil {
		t.Fatalf("construct real Claude provider: %v", err)
	}
	assertAcceptanceMetadata(t, realProvider, resource.ID, marker)

	// 1b. Create states the tool permission (KAS-57): every tool in the agent
	// closure is granted on the remote object, without a console edit.
	acceptancePolicies := map[string]string{
		"read":              acceptanceGatedPolicy,
		acceptanceMCPTool(): acceptanceAllowPolicy,
	}
	assertAcceptanceToolPolicies(t, realProvider, resource.ID, acceptancePolicies)

	// 1c. The grant is only worth anything if the agent can use it, so run one
	// live turn and watch the platform evaluate the MCP call.
	assertMCPToolIsCallable(t, resource.ID)
	assertBuiltinToolPrompts(t, resource.ID, "read")

	// 1d. doctor against the live agent (KAS-63). The acceptance module's
	// server declares no auth, so what this covers is the other half: the
	// remote object resolves and the deployed agent is permitted to call
	// every tool it declares — the KAS-57 outage as a readiness check rather
	// than as a silent failure at the first tool call.
	out = runAcceptanceCLI(t, "doctor", dir)
	assertOutputContains(t, out,
		claudeAcceptanceAddr,
		"remote object exists",
		`tool is granted with permission "always_allow"`,
		`tool is granted with permission "always_ask"`,
		"is ready",
	)
	if strings.Contains(out, "could not be verified") {
		t.Errorf("doctor could not complete a check against the live platform:\n%s", out)
	}

	// 2. A plan immediately after creation is clean.
	out = runAcceptanceCLI(t, "plan", dir)
	assertOutputContains(t, out,
		"No changes for target.claude_agents: remote matches the spec (1 resource).",
	)

	// 3. Re-applying a clean plan makes no remote change.
	out = runAcceptanceCLI(t, "apply", dir)
	assertOutputContains(t, out,
		"No changes for target.claude_agents: remote matches the spec (1 resource).",
	)
	if strings.Contains(out, "Applied target.claude_agents:") {
		t.Fatalf("clean re-apply reported a remote mutation:\n%s", out)
	}

	// 4. Change one field, apply the update, and verify convergence.
	replaceAcceptanceDescription(t, dir,
		"KAS-38 Claude Managed Agents acceptance test",
		"KAS-38 Claude Managed Agents acceptance test updated",
	)
	out = runAcceptanceCLI(t, "apply", dir)
	assertOutputContains(t, out,
		"~ "+claudeAcceptanceAddr,
		"description:",
		"Applied target.claude_agents: 0 created, 1 updated, 0 deleted.",
	)
	out = runAcceptanceCLI(t, "plan", dir)
	assertOutputContains(t, out,
		"No changes for target.claude_agents: remote matches the spec (1 resource).",
	)

	// 5. Update the live object without changing Kastor state, then confirm
	// that plan reports both the drift warning and the converging update.
	st = loadAcceptanceState(t, dir)
	resource = st.Target(claudeAcceptanceTarget).Resources[claudeAcceptanceAddr]
	drifted := decodeAcceptanceConfig(t, resource.Config)
	drifted["description"] = "KAS-38 out-of-band acceptance drift"
	if err := realProvider.Update(context.Background(), resource.ID, &provider.Resource{
		Addr:   claudeAcceptanceAddr,
		Config: drifted,
	}); err != nil {
		t.Fatalf("simulate out-of-band drift: %v", err)
	}
	out = runAcceptanceCLI(t, "plan", dir)
	assertOutputContains(t, out,
		"~ "+claudeAcceptanceAddr,
		"Warning: "+claudeAcceptanceAddr+": remote object changed outside kastor",
		"changed attributes: description",
		"Plan for target.claude_agents: 0 to create, 1 to update, 0 to delete, 0 unchanged.",
	)

	// 5b. KAS-57's negative case: a tool whose grant is taken away outside
	// kastor is drift, and apply reconciles the attribute back to the spec.
	gated := gateAcceptanceToolOutOfBand(t, realProvider, resource.ID)
	assertAcceptanceToolPolicy(t, realProvider, resource.ID, gated, acceptanceGatedPolicy)

	out = runAcceptanceCLI(t, "plan", dir)
	assertOutputContains(t, out,
		"~ "+claudeAcceptanceAddr,
		"Warning: "+claudeAcceptanceAddr+": remote object changed outside kastor",
		// tools is a replace-whole array, so the diff names the changed
		// toolset rather than the leaf attribute inside it.
		"tools[",
	)

	out = runAcceptanceCLI(t, "apply", dir)
	assertOutputContains(t, out,
		"Applied target.claude_agents: 0 created, 1 updated, 0 deleted.",
	)
	assertAcceptanceToolPolicies(t, realProvider, resource.ID, acceptancePolicies)
	out = runAcceptanceCLI(t, "plan", dir)
	assertOutputContains(t, out,
		"No changes for target.claude_agents: remote matches the spec (1 resource).",
	)

	// 6. Destroy archives the live agent through KAS-22's existing CLI path.
	out = runAcceptanceCLI(t, "destroy", dir)
	assertOutputContains(t, out,
		"- "+claudeAcceptanceAddr,
		"Destroyed target.claude_agents: 1 deleted.",
	)
	destroyed = true

	// 7. State is empty after destroy, so the next plan proposes recreation.
	out = runAcceptanceCLI(t, "plan", dir)
	assertOutputContains(t, out,
		"+ "+claudeAcceptanceAddr+" (not in state)",
		"Plan for target.claude_agents: 1 to create, 0 to update, 0 to delete, 0 unchanged.",
	)
}

type acceptanceMetadataProvider struct {
	provider.Provider
	marker string
}

func (p *acceptanceMetadataProvider) Create(ctx context.Context, desired *provider.Resource) (string, error) {
	return p.Provider.Create(ctx, p.withMetadata(desired))
}

func (p *acceptanceMetadataProvider) Update(ctx context.Context, id string, desired *provider.Resource) error {
	return p.Provider.Update(ctx, id, p.withMetadata(desired))
}

func (p *acceptanceMetadataProvider) Diff(desired *provider.Resource, remote provider.Object) ([]provider.AttrDiff, error) {
	return p.Provider.Diff(p.withMetadata(desired), remote)
}

func (p *acceptanceMetadataProvider) NormalizeStateConfig(desired *provider.Resource) (provider.Object, error) {
	normalizer, ok := p.Provider.(interface {
		NormalizeStateConfig(*provider.Resource) (provider.Object, error)
	})
	if !ok {
		return nil, fmt.Errorf("acceptance provider does not normalize state config")
	}
	return normalizer.NormalizeStateConfig(p.withMetadata(desired))
}

func (p *acceptanceMetadataProvider) withMetadata(desired *provider.Resource) *provider.Resource {
	if desired == nil || desired.Config == nil {
		return desired
	}
	cfg := make(provider.Object, len(desired.Config)+1)
	for key, value := range desired.Config {
		cfg[key] = value
	}
	metadata := map[string]any{}
	if existing, ok := desired.Config["metadata"].(map[string]any); ok {
		for key, value := range existing {
			metadata[key] = value
		}
	}
	metadata[claudeAcceptanceMarkerKey] = p.marker
	cfg["metadata"] = metadata
	return &provider.Resource{Addr: desired.Addr, Config: cfg}
}

func requireClaudeAcceptance(t *testing.T) {
	t.Helper()
	required := []string{"KASTOR_ACCEPTANCE", "ANTHROPIC_API_KEY", claudeAcceptanceMCPEnv}
	var missing []string
	for _, name := range required {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" || (name == "KASTOR_ACCEPTANCE" && value != "1") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("Claude acceptance requires %s", strings.Join(missing, ", "))
	}
}

func runAcceptanceCLI(t *testing.T, command, dir string) string {
	t.Helper()
	out, err := runCLI(t, command, "--target", claudeAcceptanceTarget, dir)
	if err != nil {
		t.Fatalf("%s: %v\n%s", command, err, out)
	}
	return out
}

func assertOutputContains(t *testing.T, output string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func loadAcceptanceState(t *testing.T, dir string) *state.File {
	t.Helper()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("load acceptance state: %v", err)
	}
	return st
}

func assertAcceptanceMetadata(t *testing.T, p provider.Provider, id, marker string) {
	t.Helper()
	remote, found, err := p.Read(context.Background(), id)
	if err != nil {
		t.Fatalf("read created Claude agent: %v", err)
	}
	if !found {
		t.Fatalf("created Claude agent %q was not found", id)
	}
	metadata, ok := remote["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("created Claude agent metadata = %T, want object", remote["metadata"])
	}
	for key, want := range map[string]any{
		"kastor_managed":          claudeAcceptanceAddr,
		claudeAcceptanceMarkerKey: marker,
	} {
		if got := metadata[key]; got != want {
			t.Errorf("created Claude agent metadata.%s = %v, want %v", key, got, want)
		}
	}
}

func replaceAcceptanceDescription(t *testing.T, dir, old, new string) {
	t.Helper()
	path := filepath.Join(dir, "acceptance.agent")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance agent: %v", err)
	}
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("acceptance agent description %q does not occur exactly once", old)
	}
	updated := strings.Replace(string(data), old, new, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("update acceptance agent: %v", err)
	}
}

func decodeAcceptanceConfig(t *testing.T, raw json.RawMessage) provider.Object {
	t.Helper()
	var cfg provider.Object
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode acceptance state config: %v", err)
	}
	return cfg
}

func TestAcceptanceMetadataProviderAddsMarkerWithoutMutation(t *testing.T) {
	decorated := &acceptanceMetadataProvider{marker: "run-123"}
	desired := &provider.Resource{
		Addr: claudeAcceptanceAddr,
		Config: provider.Object{
			"description": "test",
			"metadata":    map[string]any{"existing": "preserved"},
		},
	}

	gotResource := decorated.withMetadata(desired)
	got := gotResource.Config["metadata"].(map[string]any)
	if got[claudeAcceptanceMarkerKey] != "run-123" || got["existing"] != "preserved" {
		t.Errorf("decorated metadata = %v, want existing and acceptance markers", got)
	}
	original := desired.Config["metadata"].(map[string]any)
	if len(original) != 1 || original["existing"] != "preserved" {
		t.Errorf("decorator mutated caller config: %v", original)
	}
}
