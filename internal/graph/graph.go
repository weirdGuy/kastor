// Package graph builds the dependency DAG over a loaded module's blocks
// (SPEC.md §4, §6): references and depends_on entries become edges, cycles
// are compile errors, and a deterministic topological order is exposed for
// build/apply consumption.
package graph

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/weirdGuy/agentform/internal/module"
)

// Graph is the dependency graph over every block address in a module.
type Graph struct {
	deps  map[string][]string // addr → sorted, deduped direct dependencies
	order []string            // topological order, dependencies first
}

// Build constructs the graph from a loaded, reference-resolved module.
// References (SPEC.md §4) and depends_on entries both become edges; deep
// references like agent.forecast.output.summary collapse to an edge on the
// owning block (agent.forecast). Every cycle is a compile error naming its
// full path.
func Build(mod *module.Module) (*Graph, error) {
	g := &Graph{deps: map[string][]string{}}

	for _, m := range mod.Models {
		g.deps[m.Addr()] = nil
	}
	for _, p := range mod.Prompts {
		g.deps[p.Addr()] = nil
	}
	for _, t := range mod.Tools {
		g.deps[t.Addr()] = nil
	}
	for _, t := range mod.Targets {
		g.deps[t.Addr()] = nil
	}
	for _, a := range mod.Agents {
		refs := []string{a.Model, a.SystemPrompt}
		refs = append(refs, a.Tools...)
		refs = append(refs, a.DependsOn...)
		for _, in := range a.Inputs {
			if in.DefaultRef != "" {
				refs = append(refs, owner(in.DefaultRef))
			}
		}
		g.deps[a.Addr()] = dedupSorted(refs)
	}

	if err := g.sortTopologically(); err != nil {
		return nil, err
	}
	return g, nil
}

// Order returns the deterministic topological order, dependencies first.
// Ties break lexicographically by block address.
func (g *Graph) Order() []string {
	return g.order
}

// Dependencies returns the direct dependencies of a block address, sorted.
func (g *Graph) Dependencies(addr string) []string {
	return g.deps[addr]
}

// sortTopologically fills g.order via depth-first post-order, visiting roots
// and edges in address order so the result is deterministic. Every distinct
// cycle found on the way is reported; any cycle leaves g.order unset.
func (g *Graph) sortTopologically() error {
	nodes := make([]string, 0, len(g.deps))
	for addr := range g.deps {
		nodes = append(nodes, addr)
	}
	sort.Strings(nodes)

	const (
		unvisited = iota
		onPath
		done
	)
	state := map[string]int{}
	var path []string
	var order []string
	var errs []error

	var visit func(addr string)
	visit = func(addr string) {
		state[addr] = onPath
		path = append(path, addr)
		for _, dep := range g.deps[addr] {
			switch state[dep] {
			case unvisited:
				visit(dep)
			case onPath:
				errs = append(errs, cycleErr(path, dep))
			}
		}
		path = path[:len(path)-1]
		state[addr] = done
		order = append(order, addr)
	}
	for _, addr := range nodes {
		if state[addr] == unvisited {
			visit(addr)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return err
	}
	g.order = order
	return nil
}

// cycleErr formats the cycle closed by the edge from the top of path back to
// start, e.g. "dependency cycle: agent.a → agent.b → agent.a".
func cycleErr(path []string, start string) error {
	i := 0
	for path[i] != start {
		i++
	}
	cycle := append(append([]string{}, path[i:]...), start)
	return fmt.Errorf("dependency cycle: %s", strings.Join(cycle, " → "))
}

// owner collapses a deep reference (agent.forecast.output.summary) to the
// address of the block that owns it (agent.forecast).
func owner(ref string) string {
	parts := strings.SplitN(ref, ".", 3)
	return parts[0] + "." + parts[1]
}

func dedupSorted(refs []string) []string {
	sort.Strings(refs)
	out := refs[:0]
	for i, r := range refs {
		if i == 0 || refs[i-1] != r {
			out = append(out, r)
		}
	}
	return out
}
