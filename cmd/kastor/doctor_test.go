package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkastordev/kastor/internal/provider"
	"github.com/getkastordev/kastor/internal/provider/providertest"
	"github.com/getkastordev/kastor/internal/schema"
	"github.com/getkastordev/kastor/internal/state"
)

const (
	doctorCredential = "cred_011CZkZDLs7fYzm1hXNPeRjv"
	doctorServerURL  = "https://mcp.hubspot.com"
	doctorKeyEnv     = "KASTOR_DOCTOR_CLI_KEY"
)

// readStateFile returns the raw state file, for asserting doctor left it alone.
func readStateFile(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, state.Filename))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	return string(data)
}

// registerVaultFake wires a fake with a credential vault under the
// legacy claude_agents target alias, replacing the real provider factory for the
// duration of the test so doctor is exercised end to end without network.
func registerVaultFake(t *testing.T, credentials map[string]providertest.Credential) *providertest.VaultFake {
	t.Helper()
	fake := providertest.NewWithVault(credentials)
	previous, existed := providerFactories["claude_agents"]
	providerFactories["claude_agents"] = func(*schema.Target) (provider.Provider, error) { return fake, nil }
	t.Cleanup(func() {
		if existed {
			providerFactories["claude_agents"] = previous
			return
		}
		delete(providerFactories, "claude_agents")
	})
	return fake
}

// healthyVault holds the credential the doctor fixture references, correctly
// pointed at the server the module declares.
func healthyVault() map[string]providertest.Credential {
	return map[string]providertest.Credential{
		doctorCredential: {DisplayName: "HubSpot Prod", MCPServerURL: doctorServerURL},
	}
}

// deployDoctorModule copies the fixture and applies it, so doctor has
// something deployed to check.
func deployDoctorModule(t *testing.T) string {
	t.Helper()
	t.Setenv(doctorKeyEnv, "sk-test")
	dir := copyModule(t, "testdata/doctor")
	if out, err := runCLI(t, "apply", dir); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	return dir
}

// KAS-63 acceptance, offline: an agent whose MCP connection is not
// authenticated is named along with the server and the fault, and the command
// exits 1. Authenticating it exits 0.
func TestDoctorNamesAnUnauthenticatedConnection(t *testing.T) {
	fake := registerVaultFake(t, nil) // the vault answers, and holds nothing
	dir := deployDoctorModule(t)

	out, err := runCLI(t, "doctor", dir)
	if err == nil {
		t.Fatalf("doctor exited 0 with an unauthenticated connection:\n%s", out)
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	for _, want := range []string{
		"agent.sales",                             // the agent
		"mcp_server.hubspot",                      // the server
		"credential does not exist",               // the fault
		doctorCredential,                          // and which credential
		"2 ok, 1 failed, 0 could not be verified", // the countable summary
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}

	// Authenticate it: the same module now reports ready.
	fake.Credentials = healthyVault()
	out, err = runCLI(t, "doctor", dir)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "target.claude_agents is ready") {
		t.Errorf("doctor output does not report readiness:\n%s", out)
	}
}

// KAS-63 acceptance: with the vault unreachable the output must read "could
// not verify" and must not claim the credential is missing.
func TestDoctorSaysCouldNotVerifyWhenTheVaultIsUnreachable(t *testing.T) {
	fake := registerVaultFake(t, healthyVault())
	dir := deployDoctorModule(t)
	fake.VaultErr = errors.New("dial tcp 1.2.3.4:443: connect: connection refused")

	out, err := runCLI(t, "doctor", dir)
	if err == nil {
		t.Fatalf("doctor exited 0 without verifying the credential:\n%s", out)
	}
	if got := exitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if !strings.Contains(out, "could not verify") {
		t.Errorf("doctor output does not say the check could not be performed:\n%s", out)
	}
	for _, forbidden := range []string{"does not exist", "credential is missing"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("doctor output claims the credential is absent (%q) when the vault never answered:\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, "0 failed, 1 could not be verified") {
		t.Errorf("summary does not count the unverified check separately:\n%s", out)
	}
}

// The id is the identifier and the display name rides alongside it, so opaque
// platform ids stay decodable from output (SPEC.md §5.3).
func TestDoctorPrintsCredentialIDWithDisplayName(t *testing.T) {
	fake := registerVaultFake(t, healthyVault())
	dir := deployDoctorModule(t)

	out, err := runCLI(t, "doctor", dir)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	want := doctorCredential + ` ("HubSpot Prod")`
	if !strings.Contains(out, want) {
		t.Errorf("doctor output missing %q:\n%s", want, out)
	}

	// A credential with no display name — the platform allows that, which is
	// why the name cannot be the identifier — prints as the bare id.
	fake.Credentials = map[string]providertest.Credential{
		doctorCredential: {MCPServerURL: doctorServerURL},
	}
	out, err = runCLI(t, "doctor", dir)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if strings.Contains(out, doctorCredential+" (") {
		t.Errorf("doctor invented a display name for a credential that has none:\n%s", out)
	}
	if !strings.Contains(out, doctorCredential) {
		t.Errorf("doctor output does not name the credential:\n%s", out)
	}
}

// A missing environment variable is a finding, and it is reported before the
// per-resource sections because it is a fact about the module.
func TestDoctorReportsUnsetEnvironmentVariables(t *testing.T) {
	registerVaultFake(t, healthyVault())
	dir := deployDoctorModule(t)

	t.Setenv(doctorKeyEnv, "")
	out, err := runCLI(t, "doctor", dir)
	if err == nil {
		t.Fatalf("doctor exited 0 with an unset variable:\n%s", out)
	}
	for _, want := range []string{"Environment:", doctorKeyEnv, "environment variable is not set"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Environment:") > strings.Index(out, "agent.sales") {
		t.Errorf("environment section comes after the resources:\n%s", out)
	}
}

// The other half of the acceptance: plan on the same module still succeeds
// with no vault access at all, because plan never contacts it. This is what
// the §6 revert bought.
func TestPlanNeedsNoVaultAccess(t *testing.T) {
	fake := registerVaultFake(t, healthyVault())
	dir := deployDoctorModule(t)
	fake.VaultErr = errors.New("the vault must not be contacted by plan")

	out, err := runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("plan failed with the vault unreachable: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No changes for target.claude_agents") {
		t.Errorf("plan output = %s, want a clean plan", out)
	}
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "check ") {
			t.Errorf("plan issued a readiness check: %v", fake.Calls)
		}
	}
}

// A block in the spec that was never applied has nothing deployed to check.
func TestDoctorOnAnUnappliedModule(t *testing.T) {
	registerVaultFake(t, healthyVault())
	t.Setenv(doctorKeyEnv, "sk-test")
	dir := copyModule(t, "testdata/doctor")

	out, err := runCLI(t, "doctor", dir)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if !strings.Contains(out, "agent.sales: not deployed on this target") {
		t.Errorf("doctor output does not name the undeployed block:\n%s", out)
	}
}

// doctor never writes state: the file it read must be byte-identical after.
func TestDoctorDoesNotWriteState(t *testing.T) {
	registerVaultFake(t, nil)
	dir := deployDoctorModule(t)

	before := readStateFile(t, dir)
	if out, err := runCLI(t, "doctor", dir); err == nil {
		t.Fatalf("expected findings:\n%s", out)
	}
	if after := readStateFile(t, dir); after != before {
		t.Errorf("doctor rewrote the state file:\nbefore: %s\nafter:  %s", before, after)
	}
}

// Selecting a target that is not a platform target is a usage error (exit 2),
// matching plan and apply.
func TestDoctorTargetSelectionErrors(t *testing.T) {
	registerVaultFake(t, healthyVault())
	t.Setenv(doctorKeyEnv, "sk-test")
	dir := copyModule(t, "testdata/doctor")

	out, err := runCLI(t, "doctor", "--target", "nope", dir)
	if err == nil {
		t.Fatalf("doctor accepted an undeclared target:\n%s", out)
	}
	if got := exitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", got)
	}
}
