package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/weirdGuy/agentform/internal/module"
	"github.com/weirdGuy/agentform/internal/provider"
	"github.com/weirdGuy/agentform/internal/schema"
	"github.com/weirdGuy/agentform/internal/state"
)

// providerFactories maps a platform target's name to its provider factory:
// the target label doubles as the provider selector, exactly like codegen
// target names select generators (see cmd/adl/build.go). Issue #16
// registers "openai_assistants" here.
var providerFactories = map[string]func(*schema.Target) (provider.Provider, error){}

// platformJob is one reconcile unit: a platform target with its resolved
// provider, sharing the module, graph, and state with its siblings.
type platformJob struct {
	job      *provider.Job
	provider provider.Provider
}

// preparePlatform runs the shared front half of plan/apply/destroy:
// validate the module, select the platform targets, resolve their
// providers, take the state lock, and load the state file. The returned
// release function must be called (once) when the command is done.
func preparePlatform(stderr io.Writer, dir, targetName string) (jobs []*platformJob, release func() error, err error) {
	mod, g, err := compileModule(stderr, dir)
	if err != nil {
		return nil, nil, err
	}
	targets, err := selectPlatformTargets(mod, targetName)
	if err != nil {
		return nil, nil, err
	}

	// Resolve providers before locking: a missing provider needs no lock.
	var resolved []*platformJob
	for _, tgt := range targets {
		p, err := providerFor(tgt)
		if err != nil {
			return nil, nil, err
		}
		resolved = append(resolved, &platformJob{
			job:      &provider.Job{Module: mod, Graph: g, Target: tgt},
			provider: p,
		})
	}

	release, err = state.Lock(dir)
	if err != nil {
		return nil, nil, withExitCode(2, err)
	}
	st, err := state.Load(dir)
	if err != nil {
		release()
		return nil, nil, err
	}
	for _, pj := range resolved {
		pj.job.State = st
	}
	return resolved, release, nil
}

// selectPlatformTargets picks the platform targets to reconcile: the named
// one, or all of them in lexicographic name order when no name is given.
// Selection failures are usage errors (exit 2), mirroring adl build.
func selectPlatformTargets(mod *module.Module, name string) ([]*schema.Target, error) {
	if name != "" {
		for _, tgt := range mod.Targets {
			if tgt.Name != name {
				continue
			}
			if tgt.Type != "platform" {
				return nil, usageErrorf("target.%s is a %s target; adl plan/apply only reconcile platform targets — use adl build for it", name, tgt.Type)
			}
			return []*schema.Target{tgt}, nil
		}
		return nil, usageErrorf("target.%s: not declared in the module (platform targets: %s)", name, joinOrNone(platformNames(mod)))
	}

	var targets []*schema.Target
	for _, tgt := range mod.Targets {
		if tgt.Type == "platform" {
			targets = append(targets, tgt)
		}
	}
	if len(targets) == 0 {
		return nil, usageErrorf("module declares no platform targets; adl plan/apply need at least one target with type = \"platform\"")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

func platformNames(mod *module.Module) []string {
	var names []string
	for _, tgt := range mod.Targets {
		if tgt.Type == "platform" {
			names = append(names, tgt.Name)
		}
	}
	sort.Strings(names)
	return names
}

// providerFor resolves a platform target's provider from the registry.
func providerFor(tgt *schema.Target) (provider.Provider, error) {
	factory, ok := providerFactories[tgt.Name]
	if !ok {
		return nil, fmt.Errorf("%s: no platform provider named %q (available: %s)", tgt.Addr(), tgt.Name, joinOrNone(providerNames()))
	}
	p, err := factory(tgt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tgt.Addr(), err)
	}
	return p, nil
}

func providerNames() []string {
	names := make([]string, 0, len(providerFactories))
	for name := range providerFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// renderPlan prints a plan: one line per pending change with attribute
// diffs under updates, drift warnings, then the countable summary line.
func renderPlan(w io.Writer, p *provider.Plan) {
	create, update, del, noop := p.Counts()

	for _, c := range p.Changes {
		switch c.Action {
		case provider.ActionCreate:
			fmt.Fprintf(w, "  + %s (%s)\n", c.Addr, c.Reason)
		case provider.ActionUpdate:
			fmt.Fprintf(w, "  ~ %s\n", c.Addr)
			for _, d := range c.Diffs {
				fmt.Fprintf(w, "      %s: %s → %s\n", d.Path, renderValue(d.Old), renderValue(d.New))
			}
		case provider.ActionDelete:
			fmt.Fprintf(w, "  - %s (%s)\n", c.Addr, c.Reason)
		}
	}
	if create+update+del > 0 {
		fmt.Fprintln(w)
	}

	renderDiagnostics(w, p.Diagnostics)

	if create+update+del == 0 {
		fmt.Fprintf(w, "No changes for target.%s: remote matches the spec (%s).\n", p.Target, countNoun(noop, "resource"))
		return
	}
	fmt.Fprintf(w, "Plan for target.%s: %d to create, %d to update, %d to delete, %d unchanged.\n", p.Target, create, update, del, noop)
}

func renderDiagnostics(w io.Writer, diags []provider.Diagnostic) {
	for _, d := range diags {
		line := fmt.Sprintf("%s: %s", d.Addr, d.Summary)
		if d.Addr == "" {
			line = d.Summary
		}
		if d.Detail != "" {
			line += " (" + d.Detail + ")"
		}
		switch d.Severity {
		case provider.SeverityWarning:
			fmt.Fprintf(w, "Warning: %s\n", line)
		default:
			fmt.Fprintf(w, "Error: %s\n", line)
		}
	}
	if len(diags) > 0 {
		fmt.Fprintln(w)
	}
}

// renderValue formats one attribute value for plan output: compact JSON,
// truncated so a prompt body cannot flood the plan.
func renderValue(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	const limit = 60
	if len(data) > limit {
		return string(data[:limit-1]) + "…"
	}
	return string(data)
}

// releaseAndWarn releases the state lock, downgrading a release failure to
// a warning — the command's real result must not be masked by it.
func releaseAndWarn(stderr io.Writer, release func() error) {
	if err := release(); err != nil {
		fmt.Fprintf(stderr, "adl: warning: %v\n", err)
	}
}
