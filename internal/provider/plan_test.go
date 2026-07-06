package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/graph"
	"github.com/weirdGuy/agentform/internal/provider"
	"github.com/weirdGuy/agentform/internal/provider/providertest"
	"github.com/weirdGuy/agentform/internal/schema"
	"github.com/weirdGuy/agentform/internal/state"
)

// newJob loads the weather fixture and assembles a Job against target.fake
// with empty state.
func newJob(t *testing.T) *provider.Job {
	t.Helper()
	mod := loadModule(t, "weather")
	g, err := graph.Build(mod)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}
	sym, ok := mod.Lookup("target.fake")
	if !ok {
		t.Fatal("fixture has no target.fake")
	}
	return &provider.Job{
		Module: mod,
		Graph:  g,
		Target: sym.Block.(*schema.Target),
		State:  &state.File{Version: state.Version, Targets: map[string]*state.TargetState{}},
	}
}

// seedAll creates every agent on the fake and records it in state exactly
// as an apply would have, returning addr → remote id. The fake's call log
// is reset afterwards so tests assert only their own calls.
func seedAll(t *testing.T, job *provider.Job, fake *providertest.Fake) map[string]string {
	t.Helper()
	ids := map[string]string{}
	ts := job.State.Target(job.Target.Name)
	for _, addr := range job.Graph.Order() {
		if !strings.HasPrefix(addr, "agent.") {
			continue
		}
		sym, _ := job.Module.Lookup(addr)
		cfg, err := provider.DesiredConfig(job.Module, sym.Block.(*schema.Agent))
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
		var deps []string
		for _, dep := range job.Graph.Dependencies(addr) {
			if strings.HasPrefix(dep, "agent.") {
				deps = append(deps, dep)
			}
		}
		ts.Resources[addr] = &state.Resource{ID: id, Config: raw, Dependencies: deps}
		ids[addr] = id
	}
	fake.Calls = nil
	return ids
}

// changeSummary flattens a plan to "action addr" lines for order assertions.
func changeSummary(p *provider.Plan) []string {
	var out []string
	for _, c := range p.Changes {
		out = append(out, string(c.Action)+" "+c.Addr)
	}
	return out
}

func TestBuildPlanFreshModuleCreatesInTopoOrder(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{"create agent.geocoder", "create agent.forecast", "create agent.weather"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	for _, c := range plan.Changes {
		if c.Reason != "not in state" {
			t.Errorf("%s: Reason = %q, want %q", c.Addr, c.Reason, "not in state")
		}
		if c.ID != "" {
			t.Errorf("%s: create has ID %q, want empty", c.Addr, c.ID)
		}
	}
	if len(plan.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", plan.Diagnostics)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("plan against empty state made provider calls: %v", fake.Calls)
	}
}

func TestBuildPlanInSyncIsAllNoops(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	ids := seedAll(t, job, fake)

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{"noop agent.geocoder", "noop agent.forecast", "noop agent.weather"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	for _, c := range plan.Changes {
		if c.ID != ids[c.Addr] {
			t.Errorf("%s: ID = %q, want %q", c.Addr, c.ID, ids[c.Addr])
		}
	}
	if len(plan.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", plan.Diagnostics)
	}

	// Plan is a pure read: only Read and Diff calls are allowed.
	for _, call := range fake.Calls {
		if !strings.HasPrefix(call, "read ") && !strings.HasPrefix(call, "diff ") {
			t.Errorf("plan issued mutating call %q", call)
		}
	}
}

func TestBuildPlanSpecChangeIsAnUpdate(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	ids := seedAll(t, job, fake)

	// Simulate an older applied config: remote and state both carry the
	// previous model id, the spec now says gpt-4o-mini.
	stale := fake.Objects[ids["agent.geocoder"]]
	stale["model"].(map[string]any)["id"] = "gpt-4o"
	ts := job.State.Target("fake")
	raw, _ := provider.MarshalConfig(stale)
	ts.Resources["agent.geocoder"].Config = raw

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{"update agent.geocoder", "noop agent.forecast", "noop agent.weather"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	upd := plan.Changes[0]
	wantDiffs := []provider.AttrDiff{{Path: "model.id", Old: "gpt-4o", New: "gpt-4o-mini"}}
	if diff := cmp.Diff(wantDiffs, upd.Diffs); diff != "" {
		t.Errorf("update diffs (-want +got):\n%s", diff)
	}
	// Remote still matches what was last applied — no drift.
	if len(upd.Drift) != 0 || len(plan.Diagnostics) != 0 {
		t.Errorf("unexpected drift: %+v diagnostics: %+v", upd.Drift, plan.Diagnostics)
	}
}

func TestBuildPlanRemovedFromSpecDeletesInReverseDependencyOrder(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	seedAll(t, job, fake)

	// Two resources tracked in state but no longer in the spec;
	// legacy_b depends on legacy_a, so b must be deleted first.
	ts := job.State.Target("fake")
	ts.Resources["agent.legacy_a"] = &state.Resource{ID: "fake-90", Config: json.RawMessage(`{}`)}
	ts.Resources["agent.legacy_b"] = &state.Resource{ID: "fake-91", Config: json.RawMessage(`{}`), Dependencies: []string{"agent.legacy_a"}}
	fake.Objects["fake-90"] = provider.Object{}
	fake.Objects["fake-91"] = provider.Object{}

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{
		"delete agent.legacy_b", "delete agent.legacy_a",
		"noop agent.geocoder", "noop agent.forecast", "noop agent.weather",
	}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	for _, c := range plan.Changes[:2] {
		if c.Reason != "removed from spec" {
			t.Errorf("%s: Reason = %q, want %q", c.Addr, c.Reason, "removed from spec")
		}
	}
}

func TestBuildPlanRemoteDeletedOutsideAdl(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	ids := seedAll(t, job, fake)
	delete(fake.Objects, ids["agent.forecast"])

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{"noop agent.geocoder", "create agent.forecast", "noop agent.weather"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	create := plan.Changes[1]
	if !strings.Contains(create.Reason, ids["agent.forecast"]) || !strings.Contains(create.Reason, "missing") {
		t.Errorf("Reason = %q, want mention of missing remote %s", create.Reason, ids["agent.forecast"])
	}

	if len(plan.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one drift warning", plan.Diagnostics)
	}
	d := plan.Diagnostics[0]
	if d.Severity != provider.SeverityWarning || d.Addr != "agent.forecast" {
		t.Errorf("diagnostic = %+v, want warning for agent.forecast", d)
	}
	if !strings.Contains(d.Summary, "deleted outside adl") {
		t.Errorf("Summary = %q, want mention of out-of-band deletion", d.Summary)
	}
}

func TestBuildPlanRemoteDriftConvergesBackWithWarning(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	ids := seedAll(t, job, fake)

	// Someone edited the remote object out-of-band; the spec did not change.
	fake.Objects[ids["agent.weather"]]["instructions"] = "hacked"

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{"noop agent.geocoder", "noop agent.forecast", "update agent.weather"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	upd := plan.Changes[2]
	if len(upd.Diffs) != 1 || upd.Diffs[0].Path != "instructions" {
		t.Errorf("Diffs = %+v, want one diff on instructions", upd.Diffs)
	}
	if len(upd.Drift) != 1 || upd.Drift[0].Path != "instructions" {
		t.Errorf("Drift = %+v, want one drift on instructions", upd.Drift)
	}

	if len(plan.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one drift warning", plan.Diagnostics)
	}
	d := plan.Diagnostics[0]
	if d.Severity != provider.SeverityWarning || d.Addr != "agent.weather" {
		t.Errorf("diagnostic = %+v, want warning for agent.weather", d)
	}
	if !strings.Contains(d.Summary, "changed outside adl") || !strings.Contains(d.Detail, "instructions") {
		t.Errorf("diagnostic %+v does not name the drifted attribute", d)
	}
}

func TestBuildPlanSpecAlignedWithManualChangeIsNoopWithDrift(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	seedAll(t, job, fake)

	// The remote matches the spec, but state recorded an older config: the
	// user edited the spec to match a manual remote change. No update is
	// needed; the drift is still reported and apply will refresh state.
	ts := job.State.Target("fake")
	var cfg provider.Object
	if err := json.Unmarshal(ts.Resources["agent.geocoder"].Config, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["model"].(map[string]any)["id"] = "gpt-4o"
	raw, _ := provider.MarshalConfig(cfg)
	ts.Resources["agent.geocoder"].Config = raw

	plan, err := provider.BuildPlan(context.Background(), fake, job)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}

	want := []string{"noop agent.geocoder", "noop agent.forecast", "noop agent.weather"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	if len(plan.Changes[0].Drift) != 1 || plan.Changes[0].Drift[0].Path != "model.id" {
		t.Errorf("Drift = %+v, want one drift on model.id", plan.Changes[0].Drift)
	}
	if len(plan.Diagnostics) != 1 || plan.Diagnostics[0].Severity != provider.SeverityWarning {
		t.Errorf("diagnostics = %+v, want one warning", plan.Diagnostics)
	}
}

func TestBuildPlanReadErrorNamesTheResource(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	ids := seedAll(t, job, fake)
	boom := errors.New("rate limited")
	fake.FailOn = map[string]error{"read " + ids["agent.forecast"]: boom}

	_, err := provider.BuildPlan(context.Background(), fake, job)
	if err == nil {
		t.Fatal("BuildPlan succeeded, want error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap the provider error", err)
	}
	for _, want := range []string{"agent.forecast", ids["agent.forecast"]} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestBuildPlanCorruptStateConfigFails(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	seedAll(t, job, fake)
	job.State.Target("fake").Resources["agent.geocoder"].Config = json.RawMessage(`{broken`)

	_, err := provider.BuildPlan(context.Background(), fake, job)
	if err == nil {
		t.Fatal("BuildPlan succeeded, want error")
	}
	if !strings.Contains(err.Error(), "agent.geocoder") {
		t.Errorf("error %q does not name the resource", err)
	}
}

func TestPlanCounts(t *testing.T) {
	p := &provider.Plan{Changes: []provider.Change{
		{Action: provider.ActionCreate}, {Action: provider.ActionCreate},
		{Action: provider.ActionUpdate},
		{Action: provider.ActionDelete},
		{Action: provider.ActionNoop}, {Action: provider.ActionNoop}, {Action: provider.ActionNoop},
	}}
	create, update, del, noop := p.Counts()
	if create != 2 || update != 1 || del != 1 || noop != 3 {
		t.Errorf("Counts() = %d,%d,%d,%d; want 2,1,1,3", create, update, del, noop)
	}
}

func TestBuildDestroyPlanDeletesEverythingReverseOrder(t *testing.T) {
	job := newJob(t)
	fake := providertest.New()
	seedAll(t, job, fake)
	fake.Calls = nil

	plan, err := provider.BuildDestroyPlan(job)
	if err != nil {
		t.Fatalf("BuildDestroyPlan: %v", err)
	}

	// Reverse dependency order: weather and forecast depend on geocoder
	// (weather also on forecast), so geocoder goes last.
	want := []string{"delete agent.weather", "delete agent.forecast", "delete agent.geocoder"}
	if diff := cmp.Diff(want, changeSummary(plan)); diff != "" {
		t.Errorf("changes (-want +got):\n%s", diff)
	}
	if len(fake.Calls) != 0 {
		t.Errorf("destroy plan made provider calls: %v", fake.Calls)
	}
}

func TestBuildDestroyPlanEmptyState(t *testing.T) {
	job := newJob(t)
	plan, err := provider.BuildDestroyPlan(job)
	if err != nil {
		t.Fatalf("BuildDestroyPlan: %v", err)
	}
	if len(plan.Changes) != 0 {
		t.Errorf("changes = %+v, want none", plan.Changes)
	}
}
