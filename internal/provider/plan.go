package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/weirdGuy/agentform/internal/graph"
	"github.com/weirdGuy/agentform/internal/module"
	"github.com/weirdGuy/agentform/internal/schema"
	"github.com/weirdGuy/agentform/internal/state"
)

// Action is what apply will do to one resource.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionNoop   Action = "noop"
)

// Change is one planned operation on one resource.
type Change struct {
	Addr   string     `json:"addr"`
	Action Action     `json:"action"`
	ID     string     `json:"id,omitempty"`     // remote id; empty for creates
	Reason string     `json:"reason,omitempty"` // why this action was chosen
	Diffs  []AttrDiff `json:"diffs,omitempty"`  // desired vs. remote (updates)
	Drift  []AttrDiff `json:"drift,omitempty"`  // last-applied vs. remote (informational)
}

// Severity classifies a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is a structured plan/apply finding: what was found, what was
// expected, and where. The JSON tags are the future --json rendering
// (SPEC.md §9); the CLI renders the same fields as text.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Addr     string   `json:"addr,omitempty"` // block address, empty when module-wide
	Summary  string   `json:"summary"`        // what was found
	Detail   string   `json:"detail,omitempty"`
}

// Plan is the result of the three-way comparison (spec vs. state vs.
// remote) for one platform target. Changes are in execution order: deletes
// first in reverse dependency order, then creates/updates/noops in the
// module's topological order.
type Plan struct {
	Target      string       `json:"target"`
	Changes     []Change     `json:"changes"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Counts tallies the plan's actions.
func (p *Plan) Counts() (create, update, del, noop int) {
	for _, c := range p.Changes {
		switch c.Action {
		case ActionCreate:
			create++
		case ActionUpdate:
			update++
		case ActionDelete:
			del++
		case ActionNoop:
			noop++
		}
	}
	return create, update, del, noop
}

// Job bundles what the engine consumes: the validated module, its graph,
// the platform target being reconciled, and the loaded state file — the
// same pipeline output adl validate assembles, plus state.
type Job struct {
	Module *module.Module
	Graph  *graph.Graph
	Target *schema.Target
	State  *state.File
}

// BuildPlan performs the three-way comparison for one platform target.
// It is a pure read: only Read and Diff are called on the provider, and
// the state file is never modified.
//
// Per resource: absent from state → create; tracked but the remote object
// is gone → create (with a drift warning); otherwise the provider's Diff
// against the desired config decides update vs. noop, and its Diff against
// the last-applied config detects drift (remote changed outside adl).
func BuildPlan(ctx context.Context, p Provider, job *Job) (*Plan, error) {
	plan := &Plan{Target: job.Target.Name}
	resources := stateResources(job)

	deletes, err := removedFromSpec(job, resources)
	if err != nil {
		return nil, err
	}
	plan.Changes = append(plan.Changes, deletes...)

	for _, addr := range agentOrder(job) {
		desired, err := desiredResource(job, addr)
		if err != nil {
			return nil, err
		}

		st, tracked := resources[addr]
		if !tracked {
			plan.Changes = append(plan.Changes, Change{Addr: addr, Action: ActionCreate, Reason: "not in state"})
			continue
		}

		remote, found, err := p.Read(ctx, st.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: reading remote object %s: %w", addr, st.ID, err)
		}
		if !found {
			plan.Changes = append(plan.Changes, Change{
				Addr:   addr,
				Action: ActionCreate,
				Reason: fmt.Sprintf("remote object %s missing", st.ID),
			})
			plan.Diagnostics = append(plan.Diagnostics, Diagnostic{
				Severity: SeverityWarning,
				Addr:     addr,
				Summary:  "remote object deleted outside adl",
				Detail:   fmt.Sprintf("state maps %s to %s, but the platform has no such object; apply will recreate it", addr, st.ID),
			})
			continue
		}

		lastApplied, err := decodeConfig(addr, st.Config)
		if err != nil {
			return nil, err
		}
		drift, err := p.Diff(&Resource{Addr: addr, Config: lastApplied}, remote)
		if err != nil {
			return nil, fmt.Errorf("%s: diffing last-applied config: %w", addr, err)
		}
		if len(drift) > 0 {
			plan.Diagnostics = append(plan.Diagnostics, Diagnostic{
				Severity: SeverityWarning,
				Addr:     addr,
				Summary:  "remote object changed outside adl",
				Detail:   "changed attributes: " + strings.Join(diffPaths(drift), ", "),
			})
		}

		diffs, err := p.Diff(desired, remote)
		if err != nil {
			return nil, fmt.Errorf("%s: diffing desired config: %w", addr, err)
		}

		change := Change{Addr: addr, Action: ActionNoop, ID: st.ID, Drift: drift}
		if len(diffs) > 0 {
			change.Action = ActionUpdate
			change.Diffs = diffs
		}
		plan.Changes = append(plan.Changes, change)
	}
	return plan, nil
}

// BuildDestroyPlan plans the deletion of every resource the state tracks
// for the target, in reverse dependency order. It needs no provider —
// Delete is idempotent, so no remote read can change the outcome.
func BuildDestroyPlan(job *Job) (*Plan, error) {
	plan := &Plan{Target: job.Target.Name}
	resources := stateResources(job)

	addrs := make([]string, 0, len(resources))
	for addr := range resources {
		addrs = append(addrs, addr)
	}
	for _, addr := range reverseDependencyOrder(addrs, resources) {
		plan.Changes = append(plan.Changes, Change{
			Addr:   addr,
			Action: ActionDelete,
			ID:     resources[addr].ID,
			Reason: "destroy",
		})
	}
	return plan, nil
}

// stateResources returns the tracked resources for the job's target; nil
// when the target has no state yet. Read-only — BuildPlan must not create
// state entries as a side effect.
func stateResources(job *Job) map[string]*state.Resource {
	ts, ok := job.State.Targets[job.Target.Name]
	if !ok {
		return nil
	}
	return ts.Resources
}

// agentOrder filters the module's topological order down to the managed
// resources (agents).
func agentOrder(job *Job) []string {
	var addrs []string
	for _, addr := range job.Graph.Order() {
		if strings.HasPrefix(addr, "agent.") {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

// desiredResource renders the desired resource for one agent address.
func desiredResource(job *Job, addr string) (*Resource, error) {
	sym, ok := job.Module.Lookup(addr)
	if !ok {
		return nil, fmt.Errorf("%s: not declared in module %s", addr, job.Module.Root)
	}
	cfg, err := DesiredConfig(job.Module, sym.Block.(*schema.Agent))
	if err != nil {
		return nil, err
	}
	return &Resource{Addr: addr, Config: cfg}, nil
}

// removedFromSpec plans deletes for resources tracked in state whose block
// no longer exists in the module, in reverse dependency order (using the
// dependencies recorded in state — the module's graph no longer knows these
// blocks).
func removedFromSpec(job *Job, resources map[string]*state.Resource) ([]Change, error) {
	var removed []string
	for addr := range resources {
		if _, ok := job.Module.Lookup(addr); !ok {
			removed = append(removed, addr)
		}
	}

	var changes []Change
	for _, addr := range reverseDependencyOrder(removed, resources) {
		changes = append(changes, Change{
			Addr:   addr,
			Action: ActionDelete,
			ID:     resources[addr].ID,
			Reason: "removed from spec",
		})
	}
	return changes, nil
}

// reverseDependencyOrder orders addrs so that every resource comes before
// the resources it depends on (reverse topological order over the
// dependencies recorded in state, restricted to addrs). Ties break
// lexicographically; dependency cycles cannot occur because the edges were
// recorded from an acyclic module graph.
func reverseDependencyOrder(addrs []string, resources map[string]*state.Resource) []string {
	sort.Strings(addrs)
	in := map[string]bool{}
	for _, addr := range addrs {
		in[addr] = true
	}

	visited := map[string]bool{}
	var order []string
	var visit func(addr string)
	visit = func(addr string) {
		visited[addr] = true
		for _, dep := range resources[addr].Dependencies {
			if in[dep] && !visited[dep] {
				visit(dep)
			}
		}
		order = append(order, addr)
	}
	for _, addr := range addrs {
		if !visited[addr] {
			visit(addr)
		}
	}

	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

// decodeConfig parses a resource's last-applied config out of the state
// file.
func decodeConfig(addr string, raw json.RawMessage) (Object, error) {
	var cfg Object
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s: state file holds a corrupt last-applied config: %w", addr, err)
	}
	return cfg, nil
}

// diffPaths lists a diff set's attribute paths, sorted.
func diffPaths(diffs []AttrDiff) []string {
	paths := make([]string, len(diffs))
	for i, d := range diffs {
		paths[i] = d.Path
	}
	sort.Strings(paths)
	return paths
}

// configStale reports whether the canonical desired config differs from the
// config recorded in state — used by apply to refresh state on noops after
// the user aligns the spec with a manual remote change.
func configStale(desired *Resource, recorded json.RawMessage) (bool, error) {
	want, err := MarshalConfig(desired.Config)
	if err != nil {
		return false, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, recorded); err != nil {
		return false, fmt.Errorf("%s: state file holds a corrupt last-applied config: %w", desired.Addr, err)
	}
	return !bytes.Equal(want, buf.Bytes()), nil
}
