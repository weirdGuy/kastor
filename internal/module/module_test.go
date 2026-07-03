package module_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/module"
	"github.com/weirdGuy/agentform/internal/schema"
)

func TestLoadValidModule(t *testing.T) {
	mod, err := module.Load(filepath.Join("testdata", "valid_module"))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	// Blocks appear in lexical file-walk order, source order within a file.
	wantAgents := []string{"agent.geocoder", "agent.forecast", "agent.weather"}
	if diff := cmp.Diff(wantAgents, agentAddrs(mod.Agents)); diff != "" {
		t.Errorf("agents (-want +got):\n%s", diff)
	}
	wantModels := []string{"model.fast", "model.smart"}
	if diff := cmp.Diff(wantModels, modelAddrs(mod.Models)); diff != "" {
		t.Errorf("models (-want +got):\n%s", diff)
	}
	wantPrompts := []string{"prompt.forecast_system", "prompt.geocoder_system", "prompt.weather_system"}
	if diff := cmp.Diff(wantPrompts, promptAddrs(mod.Prompts)); diff != "" {
		t.Errorf("prompts (-want +got):\n%s", diff)
	}
	wantTools := []string{"tool.web_search"}
	if diff := cmp.Diff(wantTools, toolAddrs(mod.Tools)); diff != "" {
		t.Errorf("tools (-want +got):\n%s", diff)
	}
	wantTargets := []string{"target.langgraph"}
	if diff := cmp.Diff(wantTargets, targetAddrs(mod.Targets)); diff != "" {
		t.Errorf("targets (-want +got):\n%s", diff)
	}

	sym, ok := mod.Lookup("agent.forecast")
	if !ok {
		t.Fatal(`Lookup("agent.forecast") not found`)
	}
	if sym.Kind != "agent" {
		t.Errorf("Lookup kind = %q, want %q", sym.Kind, "agent")
	}
	if want := filepath.Join("agents", "pipeline.agent"); sym.File != want {
		t.Errorf("Lookup file = %q, want %q", sym.File, want)
	}
	if _, ok := sym.Block.(*schema.Agent); !ok {
		t.Errorf("Lookup block type = %T, want *schema.Agent", sym.Block)
	}

	if _, ok := mod.Lookup("model.nope"); ok {
		t.Error(`Lookup("model.nope") found, want missing`)
	}
}

func TestFiles(t *testing.T) {
	got, err := module.Files(filepath.Join("testdata", "walk_skip"))
	if err != nil {
		t.Fatalf("Files: unexpected error: %v", err)
	}

	// Lexical walk order; dot-directories and the codegen target output
	// directory (gen/, from adl.hcl) are skipped. Non-ADL files are still
	// listed — callers filter by extension.
	want := []string{
		"README.md",
		"adl.hcl",
		"solo.agent",
		"solo_system.prompt",
		filepath.Join("sub", "search.tool"),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Files (-want +got):\n%s", diff)
	}
}

func TestLoadSkipsTargetOutputDirs(t *testing.T) {
	mod, err := module.Load(filepath.Join("testdata", "walk_skip"))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if _, ok := mod.Lookup("agent.leftover"); ok {
		t.Error("agent.leftover found: generated output directory was not skipped")
	}
	if _, ok := mod.Lookup("agent.solo"); !ok {
		t.Error("agent.solo not found")
	}
}

func TestLoadEmptyDir(t *testing.T) {
	mod, err := module.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if len(mod.Agents)+len(mod.Tools)+len(mod.Prompts)+len(mod.Models)+len(mod.Targets) != 0 {
		t.Error("empty directory should load as an empty module")
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := module.Load(filepath.Join("testdata", "does_not_exist")); err == nil {
		t.Fatal("Load: expected error for missing directory")
	}
}

func TestLoadRootMustBeDirectory(t *testing.T) {
	root := filepath.Join("testdata", "valid_module", "adl.hcl")
	_, err := module.Load(root)
	if err == nil {
		t.Fatal("Load: expected error for file as module root")
	}
	if want := "not a directory"; !strings.Contains(err.Error(), want) {
		t.Errorf("Load error = %q, want substring %q", err, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		wantErrs []string // substrings the error must contain
	}{
		{
			name: "unknown model and tool references are all reported",
			dir:  "unknown_refs",
			wantErrs: []string{
				`solo.agent: agent.solo: unknown reference model.missing`,
				`solo.agent: agent.solo: unknown reference tool.missing`,
			},
		},
		{
			name: "output reference to a missing output names the agent",
			dir:  "unknown_output",
			wantErrs: []string{
				`pair.agent: agent.b: input "ctx": agent.a has no output "y"`,
			},
		},
		{
			name: "depends_on referencing an unknown agent",
			dir:  "unknown_dep",
			wantErrs: []string{
				`lonely.agent: agent.lonely: unknown reference agent.ghost`,
			},
		},
		{
			name: "unsatisfied prompt variables are all reported",
			dir:  "unsatisfied_var",
			wantErrs: []string{
				`greeter.agent: agent.greeter: system_prompt prompt.greeter_system: variable "city" is not an input or output of the agent`,
				`greeter.agent: agent.greeter: system_prompt prompt.greeter_system: variable "mood" is not an input or output of the agent`,
			},
		},
		{
			name: "agent declared in two files",
			dir:  "dup_agent",
			wantErrs: []string{
				`agent.weather: declared in both first.agent and second.agent`,
			},
		},
		{
			name: "model declared in two project files, including .adl extension",
			dir:  "dup_model",
			wantErrs: []string{
				`model.fast: declared in both adl.hcl and extra.adl`,
			},
		},
		{
			name: "parse errors carry the offending file name",
			dir:  "parse_error",
			wantErrs: []string{
				`bad.agent`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := module.Load(filepath.Join("testdata", tc.dir))
			if err == nil {
				t.Fatalf("Load: expected error containing %q, got nil", tc.wantErrs)
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load error = %q\nwant substring %q", err, want)
				}
			}
		})
	}
}

func agentAddrs(agents []*schema.Agent) []string {
	addrs := make([]string, len(agents))
	for i, a := range agents {
		addrs[i] = a.Addr()
	}
	return addrs
}

func modelAddrs(models []*schema.Model) []string {
	addrs := make([]string, len(models))
	for i, m := range models {
		addrs[i] = m.Addr()
	}
	return addrs
}

func promptAddrs(prompts []*schema.Prompt) []string {
	addrs := make([]string, len(prompts))
	for i, p := range prompts {
		addrs[i] = p.Addr()
	}
	return addrs
}

func toolAddrs(tools []*schema.Tool) []string {
	addrs := make([]string, len(tools))
	for i, tl := range tools {
		addrs[i] = tl.Addr()
	}
	return addrs
}

func targetAddrs(targets []*schema.Target) []string {
	addrs := make([]string, len(targets))
	for i, tg := range targets {
		addrs[i] = tg.Addr()
	}
	return addrs
}
