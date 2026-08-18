package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginruntime "github.com/weirdGuy/kastor/internal/plugin"
	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

// runCmd executes "kastor <args>" and returns combined output and the
// execution error, mirroring runBuildCmd for arbitrary subcommands.
func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err != nil {
		fmt.Fprintf(&out, "kastor: %v\n", err)
	}
	return out.String(), err
}

// scaffoldFilenames is every file kastor new creates: project file, one
// agent, one tool, one prompt, and a README. The MCP server the tool binds
// is an mcp_server block in the project file, not a hand-maintained
// mcp_servers.json — the build generates that (SPEC.md §3.3).
var scaffoldFilenames = []string{
	"kastor.hcl",
	"researcher.agent",
	"fetch_url.tool",
	"researcher_system.prompt",
	"README.md",
}

func useFakeScaffoldLifecycle(t *testing.T) {
	t.Helper()
	client := newFakePluginClient(langgraphPluginSource, protocol.KindCodegen)
	useFakePlugins(t, client)
	previous := installPlugins
	installPlugins = func(_ context.Context, root string, requirements []*schema.PluginRequirement, _ pluginruntime.InstallOptions) (*pluginruntime.InstallResult, error) {
		entry := &pluginruntime.LockedPlugin{
			Name: requirements[0].Name, Source: requirements[0].Source, Version: "0.1.0",
			Constraints: requirements[0].Version, Release: "v0.1.0", Protocol: protocol.Version,
			Platforms: map[string]string{}, Checksums: map[string]string{},
		}
		lock := &pluginruntime.LockFile{Plugins: []*pluginruntime.LockedPlugin{entry}}
		if err := pluginruntime.WriteLock(root, lock); err != nil {
			return nil, err
		}
		return &pluginruntime.InstallResult{Lock: lock, Installed: []string{entry.Name}}, nil
	}
	t.Cleanup(func() { installPlugins = previous })
}

func TestNewCommandErrors(t *testing.T) {
	useFakeScaffoldLifecycle(t)
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string) // plant preexisting state
		args     []string                       // appended after the dir argument
		wantCode int
		wantOut  []string
		skipOut  []string
	}{
		{
			name: "non-empty directory is refused",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: 2,
			wantOut:  []string{"not empty", "notes.txt", "--force"},
			skipOut:  []string{"Scaffolded"},
		},
		{
			name: "second new over a scaffold is refused",
			setup: func(t *testing.T, dir string) {
				if out, err := runCmd(t, "new", dir); err != nil {
					t.Fatalf("first new failed: %v\noutput:\n%s", err, out)
				}
			},
			wantCode: 2,
			wantOut:  []string{"not empty", "--force"},
			skipOut:  []string{"Scaffolded"},
		},
		{
			name:     "invalid source is a usage error",
			args:     []string{"--from", ""},
			wantCode: 2,
			wantOut:  []string{`plugin source "" has no usable`},
			skipOut:  []string{"created"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			out, err := runCmd(t, append([]string{"new", dir}, tt.args...)...)
			if err == nil {
				t.Fatalf("Execute() succeeded, want error\noutput:\n%s", out)
			}
			if code := exitCode(err); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (error: %v)", code, tt.wantCode, err)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, skip := range tt.skipOut {
				if strings.Contains(out, skip) {
					t.Errorf("output must not contain %q:\n%s", skip, out)
				}
			}
		})
	}
}

// TestNewCommandScaffoldWorks is the ticket's acceptance path: new into a
// new directory, then the scaffolded module must pass kastor validate and
// kastor build with zero edits, and be in canonical kastor fmt style.
func TestNewCommandScaffoldWorks(t *testing.T) {
	useFakeScaffoldLifecycle(t)
	dir := filepath.Join(t.TempDir(), "demo") // new must create missing dirs
	out, err := runCmd(t, "new", dir)
	if err != nil {
		t.Fatalf("new Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Created a new module: 5 files") {
		t.Errorf("output missing scaffold summary:\n%s", out)
	}
	for _, f := range scaffoldFilenames {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected scaffold file %s: %v", f, err)
		}
	}

	out, err = runCmd(t, "validate", dir)
	if err != nil {
		t.Fatalf("validate Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "1 agent, 1 tool, 1 prompt, 1 plugin, 1 model, 1 target") {
		t.Errorf("validate output missing module summary:\n%s", out)
	}

	out, err = runCmd(t, "build", dir)
	if err != nil {
		t.Fatalf("build Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Built target langgraph:") {
		t.Errorf("build output missing success line:\n%s", out)
	}
	for _, f := range []string{
		filepath.Join("agents", "researcher.py"),
		filepath.Join("tools", "fetch_url.py"),
		// The connection config the scaffold no longer ships by hand: the
		// build derives it from the mcp_server "fetch" block (SPEC.md §3.3),
		// so the scaffold runs with nothing to configure.
		"mcp_servers.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, "gen", "langgraph", f)); err != nil {
			t.Errorf("expected generated file %s: %v", f, err)
		}
	}

	if out, err := runCmd(t, "fmt", "--check", dir); err != nil {
		t.Errorf("scaffold is not fmt-canonical: %v\noutput:\n%s", err, out)
	}
}

// TestInitCommandIgnoresHiddenEntries: hidden entries belong to the user and
// must not block a scaffold — a fresh `git init` directory is the canonical
// case.
func TestNewCommandIgnoresHiddenEntries(t *testing.T) {
	useFakeScaffoldLifecycle(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "new", dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "kastor.hcl")); err != nil {
		t.Errorf("expected scaffold file kastor.hcl: %v", err)
	}
}

// TestInitCommandForce: --force scaffolds into a non-empty directory,
// overwriting only the scaffold's own file names and keeping everything
// else.
func TestNewCommandForce(t *testing.T) {
	useFakeScaffoldLifecycle(t)
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "kastor.hcl")
	if err := os.WriteFile(stale, []byte("# stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, "new", dir, "--force")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}

	if data, err := os.ReadFile(keep); err != nil || string(data) != "mine\n" {
		t.Errorf("unrelated file must survive --force: %q, %v", data, err)
	}
	if data, err := os.ReadFile(stale); err != nil || strings.Contains(string(data), "# stale") {
		t.Errorf("scaffold-named file must be overwritten by --force: %q, %v", data, err)
	}
}

func TestNewCommandDefaultsToCwd(t *testing.T) {
	useFakeScaffoldLifecycle(t)
	dir := t.TempDir()
	t.Chdir(dir)
	out, err := runCmd(t, "new")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "kastor.hcl")); err != nil {
		t.Errorf("expected scaffold file kastor.hcl in cwd: %v", err)
	}
	// The dir was the cwd, so there is nothing to cd into.
	if strings.Contains(out, "cd ") {
		t.Errorf("next steps must not tell the user to cd into the cwd:\n%s", out)
	}
}

// TestInitCommandDeterministic: same binary, same scaffold — byte for byte
// (repo convention, and the ticket's "same version → same scaffold").
func TestNewCommandDeterministic(t *testing.T) {
	useFakeScaffoldLifecycle(t)
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if out, err := runCmd(t, "new", dir); err != nil {
			t.Fatalf("new %s: %v\noutput:\n%s", dir, err, out)
		}
	}
	for _, f := range scaffoldFilenames {
		da, err := os.ReadFile(filepath.Join(a, f))
		if err != nil {
			t.Fatal(err)
		}
		db, err := os.ReadFile(filepath.Join(b, f))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(da, db) {
			t.Errorf("%s differs between two inits", f)
		}
	}
}

func TestInitCommandWritesEmptyLockForModuleWithoutPlugins(t *testing.T) {
	dir := t.TempDir()
	out, err := runCmd(t, "init", dir)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No plugins are required") || !strings.Contains(out, pluginruntime.LockFilename) {
		t.Fatalf("output = %s", out)
	}
	lock, err := pluginruntime.ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Plugins) != 0 {
		t.Fatalf("plugins = %#v", lock.Plugins)
	}
}

func TestSafeScaffoldPathRejectsTraversalAndReservedLock(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", `dir\outside`, pluginruntime.LockFilename, "/absolute"} {
		if _, err := safeScaffoldPath(t.TempDir(), name); err == nil {
			t.Errorf("safeScaffoldPath(%q) succeeded", name)
		}
	}
	root := t.TempDir()
	got, err := safeScaffoldPath(root, "nested/file.agent")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "nested", "file.agent") {
		t.Fatalf("path = %q", got)
	}
}
