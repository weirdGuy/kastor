package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// runFmtCmd executes "adl fmt <args>" and returns combined output and the
// execution error, mirroring runValidateCmd.
func runFmtCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"fmt"}, args...))
	err := cmd.Execute()
	if err != nil {
		fmt.Fprintf(&out, "adl: %v\n", err)
	}
	return out.String(), err
}

// copyTree copies the fixture tree at src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyTree(%s): %v", src, err)
	}
}

// treeFiles collects a tree as a map of slash-separated relative path →
// file content, for whole-tree comparison.
func treeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("treeFiles(%s): %v", root, err)
	}
	return files
}

func TestFmtCommand(t *testing.T) {
	tests := []struct {
		name    string
		src     string // fixture tree under testdata/fmt copied into a temp dir
		want    string // fixture tree the temp dir must match after the run
		flags   []string
		wantErr bool
		wantOut []string // substrings that must appear in the output
		skipOut []string // substrings that must not appear
	}{
		{
			name:    "rewrites files in place and prints their names",
			src:     "messy/before",
			want:    "messy/after",
			wantOut: []string{"adl.hcl", "solo.agent", "tools.tool"},
			skipOut: []string{"clean.tool", "solo_system.prompt", "leftover"},
		},
		{
			name:    "check reports files without writing",
			src:     "messy/before",
			want:    "messy/before",
			flags:   []string{"--check"},
			wantErr: true,
			wantOut: []string{"solo.agent", "3 files would be reformatted"},
		},
		{
			name:    "check passes on a formatted tree",
			src:     "messy/after",
			want:    "messy/after",
			flags:   []string{"--check"},
			skipOut: []string{"solo.agent", "adl.hcl", "tools.tool"},
		},
		{
			name:  "diff prints unified diffs and still writes",
			src:   "messy/before",
			want:  "messy/after",
			flags: []string{"--diff"},
			wantOut: []string{
				"--- a/solo.agent",
				"+++ b/solo.agent",
				"+  model         = model.fast",
				"-model=model.fast",
			},
		},
		{
			name:    "check with diff prints diffs without writing",
			src:     "messy/before",
			want:    "messy/before",
			flags:   []string{"--check", "--diff"},
			wantErr: true,
			wantOut: []string{"--- a/adl.hcl", "+++ b/adl.hcl"},
		},
		{
			name:    "syntax errors are reported and other files still format",
			src:     "syntax_error/before",
			want:    "syntax_error/after",
			wantErr: true,
			wantOut: []string{"bad.agent:", "ok.agent", "1 error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			copyTree(t, filepath.Join("testdata", "fmt", tt.src), tmp)

			out, err := runFmtCmd(t, append(tt.flags, tmp)...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Execute() error = %v, wantErr %v\noutput:\n%s", err, tt.wantErr, out)
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

			want := treeFiles(t, filepath.Join("testdata", "fmt", tt.want))
			got := treeFiles(t, tmp)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("tree after fmt (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFmtIdempotent asserts fmt(fmt(x)) == fmt(x): a second run reports no
// changes and leaves every file untouched.
func TestFmtIdempotent(t *testing.T) {
	tmp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "fmt", "messy", "before"), tmp)

	if out, err := runFmtCmd(t, tmp); err != nil {
		t.Fatalf("first fmt: %v\noutput:\n%s", err, out)
	}
	first := treeFiles(t, tmp)

	out, err := runFmtCmd(t, tmp)
	if err != nil {
		t.Fatalf("second fmt: %v\noutput:\n%s", err, out)
	}
	if out != "" {
		t.Errorf("second fmt reported changes:\n%s", out)
	}
	if diff := cmp.Diff(first, treeFiles(t, tmp)); diff != "" {
		t.Errorf("second fmt modified files (-first +second):\n%s", diff)
	}
}

func TestFmtDefaultsToCwd(t *testing.T) {
	tmp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "fmt", "messy", "before"), tmp)
	t.Chdir(tmp)

	out, err := runFmtCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}
	got, err := os.ReadFile("solo.agent")
	if err != nil {
		t.Fatal(err)
	}
	if want := "  model         = model.fast\n"; !strings.Contains(string(got), want) {
		t.Errorf("solo.agent not formatted, missing %q:\n%s", want, got)
	}
}
