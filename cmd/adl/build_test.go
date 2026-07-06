package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// runBuildCmd executes "adl build <args>" and returns combined output and
// the execution error.
func runBuildCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"build"}, args...))
	err := cmd.Execute()
	// main.go prints the returned error to stderr; append it so tests see
	// the same combined output a user does.
	if err != nil {
		fmt.Fprintf(&out, "adl: %v\n", err)
	}
	return out.String(), err
}

// copyModule copies a testdata module into a temp directory so builds never
// write generated output into the repository.
func copyModule(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
	return dst
}

func TestBuildCommandErrors(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		args     []string
		wantCode int      // expected process exit code for the error
		wantOut  []string // substrings that must appear in the output
		skipOut  []string // substrings that must not appear
	}{
		{
			name:     "unknown target is a usage error",
			dir:      "testdata/valid",
			args:     []string{"--target", "nope"},
			wantCode: 2,
			wantOut:  []string{`target.nope`, "langgraph"},
			skipOut:  []string{"Built"},
		},
		{
			name:     "platform target directs to plan/apply",
			dir:      "testdata/build/platform_only",
			args:     []string{"--target", "openai_assistants"},
			wantCode: 2,
			wantOut:  []string{"target.openai_assistants", "platform", "adl plan"},
			skipOut:  []string{"Built"},
		},
		{
			name:     "invalid module never generates",
			dir:      "testdata/unknown_ref",
			wantCode: 1,
			wantOut:  []string{"agent.solo: unknown reference model.fastt"},
			skipOut:  []string{"Built"},
		},
		{
			name:     "zero codegen targets is an error not a no-op",
			dir:      "testdata/build/platform_only",
			wantCode: 2,
			wantOut:  []string{"no codegen targets"},
			skipOut:  []string{"Built"},
		},
		{
			name:     "codegen target without a generator fails",
			dir:      "testdata/build/no_generator",
			wantCode: 1,
			wantOut:  []string{"target.alpha", "no code generator", "langgraph"},
			// Builds run in lexicographic target-name order and stop at the
			// first failure, so target.zed is never attempted.
			skipOut: []string{"target.zed"},
		},
		{
			name:     "generator errors surface with the block address",
			dir:      "testdata/valid", // its only tool is kind=builtin: a codegen error
			args:     []string{"--target", "langgraph"},
			wantCode: 1,
			wantOut:  []string{"target.langgraph", "builtin"},
			skipOut:  []string{"Built"},
		},
		{
			name:     "missing directory fails",
			dir:      "testdata/does_not_exist",
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runBuildCmd(t, append([]string{tt.dir}, tt.args...)...)
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

func TestBuildCommandSingleTarget(t *testing.T) {
	dir := copyModule(t, "testdata/build/single")
	out, err := runBuildCmd(t, dir, "--target", "langgraph")
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Built target langgraph:") {
		t.Errorf("output missing success line:\n%s", out)
	}
	for _, f := range []string{".adlbuild", "models.py", filepath.Join("agents", "solo.py")} {
		if _, err := os.Stat(filepath.Join(dir, "gen", "langgraph", f)); err != nil {
			t.Errorf("expected generated file %s: %v", f, err)
		}
	}
}

func TestBuildCommandDefaultsToCwd(t *testing.T) {
	dir := copyModule(t, "testdata/build/single")
	t.Chdir(dir)
	out, err := runBuildCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Built target langgraph:") {
		t.Errorf("output missing success line:\n%s", out)
	}
}

// TestBuildCommandWeatherExample builds the repo's end-to-end example with no
// --target flag: all codegen targets build, the platform target is left to
// plan/apply, and the reported file count matches what lands on disk in the
// declared output directory.
func TestBuildCommandWeatherExample(t *testing.T) {
	dir := copyModule(t, filepath.Join("..", "..", "examples", "weather"))
	out, err := runBuildCmd(t, dir)
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}

	m := regexp.MustCompile(`Built target langgraph: (\d+) files → `).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("output missing countable success line:\n%s", out)
	}
	reported, _ := strconv.Atoi(m[1])

	outDir := filepath.Join(dir, "gen", "langgraph")
	for _, f := range []string{
		".adlbuild",
		"models.py",
		filepath.Join("agents", "weather.py"),
		filepath.Join("agents", "forecast.py"),
		filepath.Join("agents", "geocoder.py"),
		filepath.Join("tools", "web_search.py"),
	} {
		if _, err := os.Stat(filepath.Join(outDir, f)); err != nil {
			t.Errorf("expected generated file %s: %v", f, err)
		}
	}

	// Every visible file in the output dir must be accounted for by the
	// reported count (the marker is hidden and excluded).
	var onDisk int
	err = filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			onDisk++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking output dir: %v", err)
	}
	if reported != onDisk {
		t.Errorf("reported %d files, found %d on disk", reported, onDisk)
	}
}

func TestExitCode(t *testing.T) {
	if got := exitCode(errors.New("plain")); got != 1 {
		t.Errorf("plain error exit code = %d, want 1", got)
	}
	wrapped := fmt.Errorf("outer: %w", usageErrorf("bad flag"))
	if got := exitCode(wrapped); got != 2 {
		t.Errorf("wrapped usage error exit code = %d, want 2", got)
	}
}
