package langgraph_test

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/getkastordev/kastor/internal/build"
	"github.com/getkastordev/kastor/internal/build/buildtest"
	"github.com/getkastordev/kastor/internal/build/langgraph"
	"github.com/getkastordev/kastor/internal/graph"
	"github.com/getkastordev/kastor/internal/module"
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

// TestGenerateWeatherGolden generates the end-to-end weather example
// (SPEC.md §8 milestone 4) and compares every file against the goldens in
// testdata/weather. Run with -update to rewrite them.
func TestGenerateWeatherGolden(t *testing.T) {
	job := loadJob(t, filepath.Join("..", "..", "..", "examples", "weather"), "langgraph")
	files := buildtest.AssertDeterministic(t, langgraph.Generator{}, job)

	goldenDir := filepath.Join("testdata", "weather")
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
		t.Errorf("generated files differ from golden (-want +got); run `go test ./internal/build/langgraph -update` after reviewing:\n%s", diff)
	}
}

// TestGenerateMinimalModule pins the scaffold emitted for a module with no
// blocks at all: still deterministic, still a complete file set.
func TestGenerateMinimalModule(t *testing.T) {
	job := loadJob(t, filepath.Join("testdata", "minimal"), "dev")
	files := buildtest.AssertDeterministic(t, langgraph.Generator{}, job)

	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	want := []string{
		"README.md",
		"agents/__init__.py",
		"main.py",
		"models.py",
		"prompts/__init__.py",
		"requirements.txt",
		"tools/__init__.py",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("generated file set (-want +got):\n%s", diff)
	}
}

// TestGenerateRuntimeToolIsPreserved pins the ownership half of the runtime
// stub contract: the stub file — and only the stub file — is generated in
// preserve mode, and its TODO tells the user what that means, naming the
// sidecar path build.Write would actually use.
func TestGenerateRuntimeToolIsPreserved(t *testing.T) {
	job := loadJob(t, filepath.Join("testdata", "runtime_tool"), "dev")
	files := buildtest.AssertDeterministic(t, langgraph.Generator{}, job)

	var preserved []string
	stub := ""
	for _, f := range files {
		if f.Preserve {
			preserved = append(preserved, f.Path)
		}
		if f.Path == "tools/lookup.py" {
			stub = string(f.Data)
		}
	}
	if diff := cmp.Diff([]string{"tools/lookup.py"}, preserved); diff != "" {
		t.Errorf("preserve-mode files (-want +got):\n%s", diff)
	}

	for _, want := range []string{
		"leaves the file alone",
		"lookup.py" + build.SidecarSuffix,
		"NotImplementedError",
	} {
		if !strings.Contains(stub, want) {
			t.Errorf("generated stub does not mention %q:\n%s", want, stub)
		}
	}
	if strings.Contains(stub, "regenerates") {
		t.Errorf("generated stub still advertises being regenerated:\n%s", stub)
	}
}

// generatedFile returns one generated file's contents, failing if the
// generator did not emit it.
func generatedFile(t *testing.T, files []build.File, path string) string {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return string(f.Data)
		}
	}
	t.Fatalf("generator did not emit %s", path)
	return ""
}

// TestGenerateMCPConfigHoldsNoCredential pins the split SPEC.md §3.3 draws
// through the generated project: mcp_servers.json is connection config and
// nothing else — no credential value and no auth.ref — while the ref's
// *variable name* lives in the bridge, which reads it in the user's own
// process at call time. A ref in the config file would be the same defect
// state-file-side §5.1 forbids, one directory over.
func TestGenerateMCPConfigHoldsNoCredential(t *testing.T) {
	job := loadJob(t, filepath.Join("..", "..", "..", "examples", "weather"), "langgraph")
	files := buildtest.AssertDeterministic(t, langgraph.Generator{}, job)

	config := generatedFile(t, files, "mcp_servers.json")
	for _, want := range []string{`"search-server"`, `"streamable_http"`, `"https://mcp.tavily.com/mcp"`, "Do not edit"} {
		if !strings.Contains(config, want) {
			t.Errorf("mcp_servers.json missing %q:\n%s", want, config)
		}
	}
	// The whole point: the ref, its scheme, and the variable it names are
	// all absent from the file, and so is any header the bridge builds.
	for _, forbidden := range []string{"env://", "auth", "TAVILY_API_KEY", "Authorization", "Bearer"} {
		if strings.Contains(config, forbidden) {
			t.Errorf("mcp_servers.json contains %q — connection config only (SPEC.md §3.3):\n%s", forbidden, config)
		}
	}

	// The auth wiring is a server → variable-name map in the bridge.
	support := generatedFile(t, files, "mcp_support.py")
	for _, want := range []string{`"search-server": "TAVILY_API_KEY"`, "os.environ.get(variable)", "Bearer"} {
		if !strings.Contains(support, want) {
			t.Errorf("mcp_support.py missing %q:\n%s", want, support)
		}
	}
	if strings.Contains(support, "env://TAVILY_API_KEY\"") {
		t.Error("mcp_support.py stores the ref rather than the variable it names")
	}
}

// TestGenerateMCPConfigStdio pins the other transport: a stdio server is
// spawned by the generated project, so its config carries command and args
// and no url — and it takes no auth, having inherited the environment.
func TestGenerateMCPConfigStdio(t *testing.T) {
	job := loadJob(t, filepath.Join("testdata", "stdio_server"), "dev")
	files := buildtest.AssertDeterministic(t, langgraph.Generator{}, job)

	config := generatedFile(t, files, "mcp_servers.json")
	for _, want := range []string{`"fetch"`, `"transport": "stdio"`, `"command": "uvx"`, `"args": ["mcp-server-fetch"]`} {
		if !strings.Contains(config, want) {
			t.Errorf("mcp_servers.json missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, `"url"`) {
		t.Errorf("stdio server config carries a url:\n%s", config)
	}
	if support := generatedFile(t, files, "mcp_support.py"); !strings.Contains(support, "_AUTH_ENV: dict[str, str] = {}") {
		t.Errorf("unauthenticated module did not emit an empty auth map:\n%s", support)
	}
}

// TestGenerateErrors covers the specs the langgraph target must reject:
// each fixture is a valid Kastor module that has no langgraph mapping.
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
			name:     "unknown model provider",
			dir:      "bad_provider",
			wantErrs: []string{"model.mystery", `"watsonx"`, "supported: anthropic, google, ollama, openai"},
		},
		{
			name:     "model params key not a python identifier",
			dir:      "bad_param_key",
			wantErrs: []string{"model.fast", `"max-tokens"`, "keyword argument"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := loadJob(t, filepath.Join("testdata", tc.dir), "dev")
			_, err := build.Run(langgraph.Generator{}, job)
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
