package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/provider/providertest"
	"github.com/weirdGuy/kastor/internal/schema"
	"github.com/weirdGuy/kastor/internal/state"
)

// registerFakeAs wires a persistent fake provider under an arbitrary target
// name, restoring whatever was registered there before. Registering one under
// "claude_agents" is what lets this test drive the platform path — including a
// connection:// ref and a vault_id — with no API key and no network.
func registerFakeAs(t *testing.T, name string) *providertest.Fake {
	t.Helper()
	fake := providertest.New()
	previous, existed := providerFactories[name]
	providerFactories[name] = func(*schema.Target) (provider.Provider, error) { return fake, nil }
	t.Cleanup(func() {
		if existed {
			providerFactories[name] = previous
			return
		}
		delete(providerFactories, name)
	})
	return fake
}

// TestStateRecordsCredentialRefNeverValue is the §5.1 invariant with teeth:
// state stores the *reference* to a credential and never the credential.
//
// It asserts against the serialized kastor.state.json rather than the
// in-memory struct on purpose. The struct is what today's code happens to
// build; the file is what actually persists to disk, gets read by the next
// plan, and would leak. A later change that resolves a ref anywhere on the
// apply path — caching a token "so we don't re-read the environment every
// plan" is the plausible one — still has to serialize the result to be worth
// anything, and this test fails the moment it does.
//
// The positive assertions matter as much as the negative one: without them a
// refactor that dropped mcp_servers from the config entirely would pass by
// storing nothing at all, which proves nothing about credential handling.
func TestStateRecordsCredentialRefNeverValue(t *testing.T) {
	// A value distinctive enough that finding it anywhere in the file is
	// unambiguous, and shaped like a real bearer token.
	const secret = "pat-kastor-KAS65-do-not-persist-9f3c1e7a"
	t.Setenv("KASTOR_TEST_HUBSPOT_TOKEN", secret)

	registerFakeAs(t, "fake")
	registerFakeAs(t, "claude_agents")
	dir := copyModule(t, "testdata/mcp_credentials")

	// plan first: it must also succeed with no vault reachable and no
	// credential readable, which is the §3.6/§6 guarantee the amendment
	// moved verification out of Diff to protect.
	out, err := runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, state.Filename)); !os.IsNotExist(err) {
		t.Error("plan created a state file — plan must be a pure read")
	}

	out, err = runCLI(t, "apply", dir)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(dir, state.Filename))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	serialized := string(raw)

	if strings.Contains(serialized, secret) {
		t.Errorf("%s contains the resolved credential value — state records the ref, never the value (SPEC.md §5.1):\n%s",
			state.Filename, serialized)
	}
	// The token's variable is set in this process, so a resolver anywhere on
	// the apply path would have had something to find. That it did not is the
	// claim; these confirm the refs themselves did reach state, so the
	// absence above is about the value and not about missing config.
	for _, want := range []string{
		"env://KASTOR_TEST_HUBSPOT_TOKEN",
		"connection://cred_011CZkZDLs7fYzm1hXNPeRjv",
	} {
		if !strings.Contains(serialized, want) {
			t.Errorf("%s does not record the auth ref %q:\n%s", state.Filename, want, serialized)
		}
	}
	// The vault is a location, not a secret, but it is also not this
	// resource's configuration — it belongs to the target block.
	if strings.Contains(serialized, "vlt_011CZkZDLs7fYzm1hXNPeRjvVAULT") {
		t.Errorf("%s records the target's vault_id in a resource's config:\n%s", state.Filename, serialized)
	}

	// A second plan must be clean: the ref round-trips through state as
	// itself, so an unresolved credential is not perpetual drift.
	out, err = runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("replan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No changes for target.fake") {
		t.Errorf("replan is not clean for target.fake:\n%s", out)
	}
	if !strings.Contains(out, "No changes for target.claude_agents") {
		t.Errorf("replan is not clean for target.claude_agents:\n%s", out)
	}
}

// TestPlanWithConnectionRefNeedsNoVault pins watch-item 1 of KAS-65 directly:
// a module whose MCP server carries a connection:// ref plans without the
// vault being reachable. Verification lives in `kastor doctor` (KAS-63), so
// there is nothing on this path to make a lookup — no flag needed to skip
// one, because none is attempted.
func TestPlanWithConnectionRefNeedsNoVault(t *testing.T) {
	registerFakeAs(t, "fake")
	registerFakeAs(t, "claude_agents")
	dir := copyModule(t, "testdata/mcp_credentials")

	// Neither the credential's variable nor an API key is set: a lookup of
	// any kind would fail rather than silently succeed from ambient config.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("KASTOR_TEST_HUBSPOT_TOKEN", "")

	out, err := runCLI(t, "plan", "--target", "claude_agents", dir)
	if err != nil {
		t.Fatalf("plan against claude_agents with a connection:// ref: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ agent.sales (not in state)") {
		t.Errorf("plan did not report the agent as a create:\n%s", out)
	}
}
