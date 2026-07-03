package graph_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/graph"
	"github.com/weirdGuy/agentform/internal/module"
)

func load(t *testing.T, dir string) *module.Module {
	t.Helper()
	mod, err := module.Load(filepath.Join("testdata", dir))
	if err != nil {
		t.Fatalf("Load(%s): unexpected error: %v", dir, err)
	}
	return mod
}

func TestBuildValid(t *testing.T) {
	tests := []struct {
		name      string
		dir       string
		wantOrder []string
		wantDeps  map[string][]string
	}{
		{
			name: "linear chain of output references",
			dir:  "linear_chain",
			wantOrder: []string{
				"model.m", "prompt.sys", "tool.t",
				"agent.a", "agent.b", "agent.c",
				"target.dev",
			},
			wantDeps: map[string][]string{
				"agent.a":    {"model.m", "prompt.sys", "tool.t"},
				"agent.b":    {"agent.a", "model.m", "prompt.sys"},
				"agent.c":    {"agent.b", "model.m", "prompt.sys"},
				"model.m":    nil,
				"target.dev": nil,
			},
		},
		{
			name: "diamond joins both branches before the sink",
			dir:  "diamond",
			wantOrder: []string{
				"model.m", "prompt.sys",
				"agent.a", "agent.b", "agent.c", "agent.d",
			},
			wantDeps: map[string][]string{
				"agent.d": {"agent.b", "agent.c", "model.m", "prompt.sys"},
			},
		},
		{
			name: "depends_on alone creates an ordering edge",
			dir:  "depends_on_only",
			wantOrder: []string{
				"model.m", "prompt.sys",
				"agent.a", "agent.b",
			},
			wantDeps: map[string][]string{
				"agent.b": {"agent.a", "model.m", "prompt.sys"},
			},
		},
		{
			name: "agent without system_prompt contributes no prompt edge",
			dir:  "no_system_prompt",
			wantOrder: []string{
				"model.m", "agent.a",
				"prompt.sys", "agent.b",
			},
			wantDeps: map[string][]string{
				"agent.a": {"model.m"},
				"agent.b": {"agent.a", "model.m", "prompt.sys"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := graph.Build(load(t, tc.dir))
			if err != nil {
				t.Fatalf("Build: unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantOrder, g.Order()); diff != "" {
				t.Errorf("Order() (-want +got):\n%s", diff)
			}
			for addr, want := range tc.wantDeps {
				if diff := cmp.Diff(want, g.Dependencies(addr)); diff != "" {
					t.Errorf("Dependencies(%s) (-want +got):\n%s", addr, diff)
				}
			}
		})
	}
}

func TestBuildOrderIsDeterministic(t *testing.T) {
	mod := load(t, "diamond")
	first, err := graph.Build(mod)
	if err != nil {
		t.Fatalf("Build: unexpected error: %v", err)
	}
	for i := 0; i < 20; i++ {
		g, err := graph.Build(mod)
		if err != nil {
			t.Fatalf("Build: unexpected error: %v", err)
		}
		if diff := cmp.Diff(first.Order(), g.Order()); diff != "" {
			t.Fatalf("Order() differs across runs (-first +got):\n%s", diff)
		}
	}
}

func TestBuildCycles(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		wantErrs []string // substrings the error must contain
	}{
		{
			name:     "direct cycle via depends_on",
			dir:      "direct_cycle",
			wantErrs: []string{"dependency cycle: agent.a → agent.b → agent.a"},
		},
		{
			name:     "transitive cycle via output references",
			dir:      "transitive_cycle",
			wantErrs: []string{"dependency cycle: agent.a → agent.c → agent.b → agent.a"},
		},
		{
			name:     "self reference",
			dir:      "self_reference",
			wantErrs: []string{"dependency cycle: agent.a → agent.a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := graph.Build(load(t, tc.dir))
			if err == nil {
				t.Fatalf("Build: expected error containing %q, got nil", tc.wantErrs)
			}
			if g != nil {
				t.Error("Build: graph should be nil when a cycle is found")
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Build error = %q\nwant substring %q", err, want)
				}
			}
		})
	}
}
