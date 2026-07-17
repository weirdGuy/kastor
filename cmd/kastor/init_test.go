package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// scaffoldFilenames is every file kastor init creates, the contract the
// ticket names: project file, one agent, one tool, one prompt, plus the MCP
// runtime config the tool needs and a README.
var scaffoldFilenames = []string{
	"kastor.hcl",
	"researcher.agent",
	"fetch_url.tool",
	"researcher_system.prompt",
	"mcp_servers.json",
	"README.md",
}

func TestInitCommandErrors(t *testing.T) {
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
			name: "second init over a scaffold is refused",
			setup: func(t *testing.T, dir string) {
				if out, err := runCmd(t, "init", dir); err != nil {
					t.Fatalf("first init failed: %v\noutput:\n%s", err, out)
				}
			},
			wantCode: 2,
			wantOut:  []string{"not empty", "--force"},
			skipOut:  []string{"Scaffolded"},
		},
		{
			name:     "eve target has no scaffold yet",
			args:     []string{"--target", "eve"},
			wantCode: 2,
			wantOut:  []string{`no scaffold for target "eve"`, "langgraph"},
			skipOut:  []string{"created"},
		},
		{
			name:     "unknown target is a usage error",
			args:     []string{"--target", "nope"},
			wantCode: 2,
			wantOut:  []string{`no scaffold for target "nope"`, "langgraph"},
			skipOut:  []string{"created"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			out, err := runCmd(t, append([]string{"init", dir}, tt.args...)...)
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

// TestInitCommandScaffoldWorks is the ticket's acceptance path: init into a
// new directory, then the scaffolded module must pass kastor validate and
// kastor build with zero edits, and be in canonical kastor fmt style.
func TestInitCommandScaffoldWorks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo") // init must create missing dirs
	out, err := runCmd(t, "init", dir)
	if err != nil {
		t.Fatalf("init Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Scaffolded a new module: 6 files") {
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
	if !strings.Contains(out, "1 agent, 1 tool, 1 prompt, 1 model, 1 target") {
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
func TestInitCommandIgnoresHiddenEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "init", dir)
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
func TestInitCommandForce(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "kastor.hcl")
	if err := os.WriteFile(stale, []byte("# stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCmd(t, "init", dir, "--force")
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

func TestInitCommandDefaultsToCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out, err := runCmd(t, "init")
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
func TestInitCommandDeterministic(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if out, err := runCmd(t, "init", dir); err != nil {
			t.Fatalf("init %s: %v\noutput:\n%s", dir, err, out)
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
