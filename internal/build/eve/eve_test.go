package eve_test

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/kastor/internal/build"
	"github.com/weirdGuy/kastor/internal/build/buildtest"
	"github.com/weirdGuy/kastor/internal/build/eve"
	"github.com/weirdGuy/kastor/internal/graph"
	"github.com/weirdGuy/kastor/internal/module"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// loadJob loads the module at root and builds a Job for its named target.
func loadJob(t *testing.T, root, target string) *build.Job {
	t.Helper()
	mod, err := module.Load(root)
	if err != nil {
		t.Fatalf("Load(%s): unexpected error: %v", root, err)
	}
	g, err := graph.Build(mod)
	if err != nil {
		t.Fatalf("graph.Build: unexpected error: %v", err)
	}
	for _, tgt := range mod.Targets {
		if tgt.Name == target {
			return &build.Job{Module: mod, Graph: g, Target: tgt}
		}
	}
	t.Fatalf("target %q not found in module %s", target, root)
	return nil
}

// assertGolden generates the module and compares every file against the
// goldens in testdata/<name>. Run with -update to rewrite them.
func assertGolden(t *testing.T, job *build.Job, name string) {
	t.Helper()
	files := buildtest.AssertDeterministic(t, eve.Generator{}, job)

	goldenDir := filepath.Join("testdata", name)
	if *update {
		if err := os.RemoveAll(goldenDir); err != nil {
			t.Fatalf("clearing golden dir: %v", err)
		}
		for _, f := range files {
			path := filepath.Join(goldenDir, filepath.FromSlash(f.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("creating golden dir: %v", err)
			}
			if err := os.WriteFile(path, f.Data, 0o644); err != nil {
				t.Fatalf("writing golden %s: %v", f.Path, err)
			}
		}
		return
	}

	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Data)
	}

	want := map[string]string{}
	err := filepath.WalkDir(goldenDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(goldenDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		want[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading golden files: %v (run with -update to create them)", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("generated files differ from golden (-want +got); run `go test ./internal/build/eve -update` after reviewing:\n%s", diff)
	}
}

// TestGenerateWeatherGolden pins the multi-agent example: a root agent with
// a referenced subagent, an ordering-only (depends_on) subagent, and an MCP
// connection.
func TestGenerateWeatherGolden(t *testing.T) {
	job := loadJob(t, filepath.Join("..", "..", "..", "examples", "weather"), "eve")
	assertGolden(t, job, "weather")
}

// TestGenerateSupportTriageGolden pins the single-agent example: one root,
// no tools, a multi-field output contract.
func TestGenerateSupportTriageGolden(t *testing.T) {
	job := loadJob(t, filepath.Join("..", "..", "..", "examples", "support-triage"), "eve")
	assertGolden(t, job, "support_triage")
}

// TestGenerateMinimalModule pins the eve target's no-agent behavior: an eve
// project is an agent, so a module without agents generates nothing.
func TestGenerateMinimalModule(t *testing.T) {
	job := loadJob(t, filepath.Join("testdata", "minimal"), "dev")
	files := buildtest.AssertDeterministic(t, eve.Generator{}, job)
	if len(files) != 0 {
		var paths []string
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		t.Errorf("module without agents generated files: %v", paths)
	}
}

// TestGenerateUnbound pins the skip-plus-README decision for blocks no agent
// references: unbound tools and models are not emitted (a file in eve's
// tools/ would be auto-wired into the agent), unused prompts become skills.
func TestGenerateUnbound(t *testing.T) {
	job := loadJob(t, filepath.Join("testdata", "unbound"), "dev")
	files := buildtest.AssertDeterministic(t, eve.Generator{}, job)

	var paths []string
	readme := ""
	for _, f := range files {
		paths = append(paths, f.Path)
		if f.Path == "solo/README.md" {
			readme = string(f.Data)
		}
	}
	want := []string{
		"solo/README.md",
		"solo/agent/agent.ts",
		"solo/agent/instructions.md",
		"solo/agent/skills/greeting.md",
		"solo/package.json",
		"solo/tsconfig.json",
	}
	if diff := cmp.Diff(want, paths); diff != "" {
		t.Errorf("generated file set (-want +got):\n%s", diff)
	}
	for _, mention := range []string{"tool.orphan", "model.spare", "prompt.greeting", "Not emitted"} {
		if !strings.Contains(readme, mention) {
			t.Errorf("README does not mention %q", mention)
		}
	}
}

// TestGenerateErrors covers the specs the eve target must reject: each
// fixture is a valid Kastor module whose agent binds a block with no eve
// mapping. (Unbound blocks never error — they are skipped, see
// TestGenerateUnbound.)
func TestGenerateErrors(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		wantErrs []string // substrings the error must contain
	}{
		{
			name:     "builtin tool has no codegen mapping",
			dir:      "builtin_tool",
			wantErrs: []string{"tool.native_search", `"builtin"`, "platform"},
		},
		{
			name:     "script tool is not supported yet",
			dir:      "script_tool",
			wantErrs: []string{"tool.deploy", `"script"`, "not supported"},
		},
		{
			name:     "ollama is not gateway-routable",
			dir:      "bad_provider",
			wantErrs: []string{"model.local", `"ollama"`, "supported: anthropic, google, openai"},
		},
		{
			name:     "malformed mcp uri",
			dir:      "bad_mcp_uri",
			wantErrs: []string{"tool.web_search", "mcp://<server>/<tool>"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := loadJob(t, filepath.Join("testdata", tc.dir), "dev")
			_, err := build.Run(eve.Generator{}, job)
			if err == nil {
				t.Fatalf("Run: expected error containing %q, got nil", tc.wantErrs)
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Run error = %q\nwant substring %q", err, want)
				}
			}
		})
	}
}
