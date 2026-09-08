package provider_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/getkastordev/kastor/internal/graph"
	"github.com/getkastordev/kastor/internal/provider"
	"github.com/getkastordev/kastor/internal/provider/providertest"
	"github.com/getkastordev/kastor/internal/schema"
	"github.com/getkastordev/kastor/internal/state"
)

const (
	vaultedCredential = "cred_011CZkZDLs7fYzm1hXNPeRjv"
	vaultedURL        = "https://mcp.hubspot.com"
	vaultedKeyEnv     = "KASTOR_DOCTOR_TEST_KEY"
)

// newVaultedJob assembles a Job over the fixture whose MCP server references a
// connection:// credential, against target.claude_agents with empty state.
func newVaultedJob(t *testing.T) *provider.Job {
	t.Helper()
	mod := loadModule(t, "vaulted")
	g, err := graph.Build(mod)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}
	sym, ok := mod.Lookup("target.claude_agents")
	if !ok {
		t.Fatal("fixture has no target.claude_agents")
	}
	return &provider.Job{
		Module: mod,
		Graph:  g,
		Target: sym.Block.(*schema.Target),
		State:  &state.File{Version: state.Version, Targets: map[string]*state.TargetState{}},
	}
}

// seedResource records one agent on the fake and in state, as an apply would.
func seedResource(t *testing.T, job *provider.Job, fake *providertest.Fake, addr string) string {
	t.Helper()
	sym, ok := job.Module.Lookup(addr)
	if !ok {
		t.Fatalf("module has no %s", addr)
	}
	cfg, err := provider.DesiredConfig(job.Module, sym.Block.(*schema.Agent), job.Target)
	if err != nil {
		t.Fatalf("DesiredConfig(%s): %v", addr, err)
	}
	id, err := fake.Create(context.Background(), &provider.Resource{Addr: addr, Config: cfg})
	if err != nil {
		t.Fatalf("seeding %s: %v", addr, err)
	}
	raw, err := provider.MarshalConfig(cfg)
	if err != nil {
		t.Fatalf("MarshalConfig(%s): %v", addr, err)
	}
	job.State.Target(job.Target.Name).Resources[addr] = &state.Resource{ID: id, Config: raw}
	fake.Calls = nil
	return id
}

// setModuleEnv satisfies every variable the vaulted fixture needs, so a test
// about credentials or remotes is not perturbed by environment findings.
func setModuleEnv(t *testing.T) {
	t.Helper()
	t.Setenv(vaultedKeyEnv, "sk-test")
	t.Setenv("HUBSPOT_TOKEN", "hs-test")
}

// findCheck returns the first check of the given kind and subject.
func findCheck(t *testing.T, report *provider.Report, kind, subject string) provider.Check {
	t.Helper()
	for _, c := range report.Environment {
		if c.Kind == kind && c.Subject == subject {
			return c
		}
	}
	for _, res := range report.Resources {
		for _, c := range res.Checks {
			if c.Kind == kind && c.Subject == subject {
				return c
			}
		}
	}
	t.Fatalf("report has no %s check for %q", kind, subject)
	return provider.Check{}
}

func TestBuildReportEverythingReady(t *testing.T) {
	setModuleEnv(t)
	job := newVaultedJob(t)
	fake := providertest.NewWithVault(map[string]providertest.Credential{
		vaultedCredential: {DisplayName: "HubSpot Prod", MCPServerURL: vaultedURL},
	})
	id := seedResource(t, job, fake.Fake, "agent.sales")

	report, err := provider.BuildReport(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if !report.Ready() {
		t.Errorf("report is not ready: %+v", report)
	}
	_, failed, unknown := report.Counts()
	if failed != 0 || unknown != 0 {
		t.Errorf("counts = %d failed, %d unknown, want 0 and 0", failed, unknown)
	}
	if len(report.Resources) != 1 || report.Resources[0].ID != id {
		t.Fatalf("resources = %+v, want one entry for %s", report.Resources, id)
	}

	credential := findCheck(t, report, "credential", vaultedCredential)
	if credential.Status != provider.StatusOK {
		t.Errorf("credential status = %q, want ok", credential.Status)
	}
	// The display name rides alongside the id so output stays readable
	// without the name ever becoming the identifier (SPEC.md §5.3).
	if credential.SubjectName != "HubSpot Prod" {
		t.Errorf("credential SubjectName = %q, want %q", credential.SubjectName, "HubSpot Prod")
	}
}

// The distinction this whole feature turns on: a vault that does not answer
// and a vault that answers "no such credential" are different facts.
func TestBuildReportSeparatesUnverifiedFromMissing(t *testing.T) {
	setModuleEnv(t)

	t.Run("credential missing", func(t *testing.T) {
		job := newVaultedJob(t)
		fake := providertest.NewWithVault(nil) // vault answers, holds nothing
		seedResource(t, job, fake.Fake, "agent.sales")

		report, err := provider.BuildReport(context.Background(), fake, job)
		if err != nil {
			t.Fatalf("BuildReport: %v", err)
		}
		check := findCheck(t, report, "credential", vaultedCredential)
		if check.Status != provider.StatusFailed {
			t.Errorf("status = %q, want failed", check.Status)
		}
		if !strings.Contains(check.Summary, "does not exist") {
			t.Errorf("summary = %q, want it to say the credential does not exist", check.Summary)
		}
		if strings.Contains(strings.ToLower(check.Summary), "could not verify") {
			t.Errorf("summary %q reads as unverified, but the vault answered", check.Summary)
		}
		_, failed, unknown := report.Counts()
		if failed == 0 || unknown != 0 {
			t.Errorf("counts = %d failed, %d unknown; a missing credential is a failure, not an unknown", failed, unknown)
		}
	})

	t.Run("vault unreachable", func(t *testing.T) {
		job := newVaultedJob(t)
		fake := providertest.NewWithVault(map[string]providertest.Credential{
			vaultedCredential: {DisplayName: "HubSpot Prod", MCPServerURL: vaultedURL},
		})
		fake.VaultErr = errors.New("dial tcp: connection refused")
		seedResource(t, job, fake.Fake, "agent.sales")

		report, err := provider.BuildReport(context.Background(), fake, job)
		if err != nil {
			t.Fatalf("BuildReport: %v", err)
		}
		check := findCheck(t, report, "credential", vaultedCredential)
		if check.Status != provider.StatusUnknown {
			t.Errorf("status = %q, want unknown", check.Status)
		}
		if !strings.Contains(check.Summary, "could not verify") {
			t.Errorf("summary = %q, want it to say the check could not be performed", check.Summary)
		}
		for _, forbidden := range []string{"missing", "does not exist"} {
			if strings.Contains(strings.ToLower(check.Summary), forbidden) {
				t.Errorf("summary %q claims the credential is absent; the vault never answered", check.Summary)
			}
		}
		// Unknown still costs readiness: doctor did not establish that the
		// module is ready, and saying otherwise would overclaim.
		if report.Ready() {
			t.Error("report is ready despite an unverified credential")
		}
		_, failed, unknown := report.Counts()
		if failed != 0 || unknown == 0 {
			t.Errorf("counts = %d failed, %d unknown; an unreachable vault is unknown, not failed", failed, unknown)
		}
	})
}

func TestBuildReportRemoteMissingAndUnreadable(t *testing.T) {
	setModuleEnv(t)

	t.Run("deleted outside kastor is a failure", func(t *testing.T) {
		job := newVaultedJob(t)
		fake := providertest.NewWithVault(nil)
		id := seedResource(t, job, fake.Fake, "agent.sales")
		delete(fake.Objects, id)

		report, err := provider.BuildReport(context.Background(), fake, job)
		if err != nil {
			t.Fatalf("BuildReport: %v", err)
		}
		check := findCheck(t, report, provider.CheckRemote, id)
		if check.Status != provider.StatusFailed {
			t.Errorf("status = %q, want failed", check.Status)
		}
	})

	t.Run("a read that errors is unknown, not failed", func(t *testing.T) {
		job := newVaultedJob(t)
		fake := providertest.NewWithVault(nil)
		id := seedResource(t, job, fake.Fake, "agent.sales")
		fake.FailOn = map[string]error{"read " + id: errors.New("503 service unavailable")}

		report, err := provider.BuildReport(context.Background(), fake, job)
		if err != nil {
			t.Fatalf("BuildReport: %v", err)
		}
		check := findCheck(t, report, provider.CheckRemote, id)
		if check.Status != provider.StatusUnknown {
			t.Errorf("status = %q, want unknown — the platform did not answer", check.Status)
		}
		// Nothing downstream of an unanswered read is knowable, so the
		// provider must not have been asked to check anything.
		for _, call := range fake.Calls {
			if strings.HasPrefix(call, "check ") {
				t.Errorf("Check was called after a failed read: %v", fake.Calls)
			}
		}
	})
}

func TestBuildReportEnvironmentReadiness(t *testing.T) {
	job := newVaultedJob(t)
	fake := providertest.NewWithVault(nil)
	seedResource(t, job, fake.Fake, "agent.sales")

	t.Run("unset is a finding", func(t *testing.T) {
		t.Setenv(vaultedKeyEnv, "")
		report, err := provider.BuildReport(context.Background(), fake, job)
		if err != nil {
			t.Fatalf("BuildReport: %v", err)
		}
		check := findCheck(t, report, provider.CheckEnvironment, vaultedKeyEnv)
		if check.Status != provider.StatusFailed {
			t.Errorf("status = %q, want failed for an unset variable", check.Status)
		}
	})

	t.Run("set passes", func(t *testing.T) {
		t.Setenv(vaultedKeyEnv, "sk-test")
		report, err := provider.BuildReport(context.Background(), fake, job)
		if err != nil {
			t.Fatalf("BuildReport: %v", err)
		}
		check := findCheck(t, report, provider.CheckEnvironment, vaultedKeyEnv)
		if check.Status != provider.StatusOK {
			t.Errorf("status = %q, want ok", check.Status)
		}
	})
}

// Environment readiness is module-wide, not narrowed to the selected target:
// the question is "what does this module need before it will run", and the
// fixture's HUBSPOT_TOKEN binds on target.langgraph while doctor is pointed at
// target.claude_agents. Narrowing it would answer a question nobody asked.
func TestBuildReportReportsEnvRefsFromTheWholeModule(t *testing.T) {
	t.Setenv(vaultedKeyEnv, "sk-test")
	t.Setenv("HUBSPOT_TOKEN", "")
	job := newVaultedJob(t)
	fake := providertest.NewWithVault(nil)

	report, err := provider.BuildReport(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	var subjects []string
	for _, c := range report.Environment {
		subjects = append(subjects, c.Subject)
	}
	// The target's own api_key_env first, then env:// refs in name order.
	if diff := cmp.Diff([]string{vaultedKeyEnv, "HUBSPOT_TOKEN"}, subjects); diff != "" {
		t.Errorf("environment subjects (-want +got):\n%s", diff)
	}

	ref := findCheck(t, report, provider.CheckEnvironment, "HUBSPOT_TOKEN")
	if ref.Status != provider.StatusFailed {
		t.Errorf("status = %q, want failed for an unset variable", ref.Status)
	}
	// The detail says where the need comes from, so the fix is locatable.
	if !strings.Contains(ref.Detail, "mcp_server.hubspot") || !strings.Contains(ref.Detail, "target.langgraph") {
		t.Errorf("detail = %q, want it to name the server and the target it binds on", ref.Detail)
	}
}

// A provider that is not a Checker still produces a usable report, and says
// what it did not cover rather than implying full coverage.
func TestBuildReportWithoutACheckerSaysSo(t *testing.T) {
	setModuleEnv(t)
	job := newVaultedJob(t)
	fake := providertest.New() // no vault → not a Checker
	seedResource(t, job, fake, "agent.sales")

	report, err := provider.BuildReport(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	for _, res := range report.Resources {
		for _, c := range res.Checks {
			if c.Kind != provider.CheckRemote {
				t.Errorf("non-Checker provider produced a %s check: %+v", c.Kind, c)
			}
		}
	}
	if len(report.Diagnostics) == 0 {
		t.Fatal("report does not say that coverage is partial")
	}
	if !strings.Contains(report.Diagnostics[0].Summary, "remote existence only") {
		t.Errorf("diagnostic = %q, want it to name the limit", report.Diagnostics[0].Summary)
	}
}

// A block that was never applied has nothing deployed to check. That is a
// diagnostic, not a failed check: the answer is kastor apply, and plan already
// says so.
func TestBuildReportSkipsUnappliedBlocks(t *testing.T) {
	setModuleEnv(t)
	job := newVaultedJob(t)
	fake := providertest.NewWithVault(nil)

	report, err := provider.BuildReport(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(report.Resources) != 0 {
		t.Errorf("resources = %+v, want none", report.Resources)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Addr != "agent.sales" {
		t.Fatalf("diagnostics = %+v, want one naming agent.sales", report.Diagnostics)
	}
	if report.Diagnostics[0].Severity != provider.SeverityWarning {
		t.Errorf("severity = %q, want warning", report.Diagnostics[0].Severity)
	}
}

// doctor is read-only: the state file it was given must come back untouched.
func TestBuildReportNeverWritesState(t *testing.T) {
	setModuleEnv(t)
	job := newVaultedJob(t)
	fake := providertest.NewWithVault(map[string]providertest.Credential{
		vaultedCredential: {MCPServerURL: vaultedURL},
	})
	seedResource(t, job, fake.Fake, "agent.sales")

	before := job.State.Serial
	if _, err := provider.BuildReport(context.Background(), fake, job); err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if job.State.Serial != before {
		t.Errorf("state serial moved from %d to %d", before, job.State.Serial)
	}
	for _, call := range fake.Calls {
		for _, mutating := range []string{"create ", "update ", "delete "} {
			if strings.HasPrefix(call, mutating) {
				t.Errorf("doctor issued a mutating call: %v", fake.Calls)
			}
		}
	}
}
