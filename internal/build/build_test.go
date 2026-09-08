package build_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/getkastordev/kastor/internal/build"
	"github.com/getkastordev/kastor/internal/graph"
	"github.com/getkastordev/kastor/internal/module"
	"github.com/getkastordev/kastor/internal/schema"
)

// stub is a Generator returning a fixed result, for exercising the engine
// contract without any real target.
type stub struct {
	files []build.File
	err   error
}

func (s stub) Generate(job *build.Job) ([]build.File, error) { return s.files, s.err }

func load(t *testing.T, dir string) *module.Module {
	t.Helper()
	mod, err := module.Load(filepath.Join("testdata", dir))
	if err != nil {
		t.Fatalf("Load(%s): unexpected error: %v", dir, err)
	}
	return mod
}

// job builds a Job for the named target of the simple testdata module.
func job(t *testing.T, target string) *build.Job {
	t.Helper()
	mod := load(t, "simple")
	g, err := graph.Build(mod)
	if err != nil {
		t.Fatalf("graph.Build: unexpected error: %v", err)
	}
	for _, tgt := range mod.Targets {
		if tgt.Name == target {
			return &build.Job{Module: mod, Graph: g, Target: tgt}
		}
	}
	t.Fatalf("target %q not found in simple testdata module", target)
	return nil
}

func TestRunCanonicalizesOrder(t *testing.T) {
	gen := stub{files: []build.File{
		{Path: "sub/util.py", Data: []byte("util\n")},
		{Path: "main.py", Data: []byte("main\n")},
		{Path: "graph.py", Data: []byte("graph\n")},
	}}

	got, err := build.Run(gen, job(t, "dev"))
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	want := []build.File{
		{Path: "graph.py", Data: []byte("graph\n")},
		{Path: "main.py", Data: []byte("main\n")},
		{Path: "sub/util.py", Data: []byte("util\n")},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Run files (-want +got):\n%s", diff)
	}
}

func TestRunAllowsEmptyOutput(t *testing.T) {
	got, err := build.Run(stub{}, job(t, "dev"))
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Run returned %d files, want 0", len(got))
	}
}

func TestRunRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name     string
		files    []build.File
		wantErrs []string // substrings the error must contain
	}{
		{
			name: "duplicate path",
			files: []build.File{
				{Path: "main.py"},
				{Path: "main.py"},
			},
			wantErrs: []string{"target.dev", `duplicate generated file "main.py"`},
		},
		{
			name:     "absolute path",
			files:    []build.File{{Path: "/etc/passwd"}},
			wantErrs: []string{"target.dev", `"/etc/passwd"`, "relative"},
		},
		{
			name:     "escapes the output directory",
			files:    []build.File{{Path: "../outside.py"}},
			wantErrs: []string{"target.dev", `"../outside.py"`, "escapes"},
		},
		{
			name:     "bare parent reference",
			files:    []build.File{{Path: ".."}},
			wantErrs: []string{"target.dev", `".."`, "escapes"},
		},
		{
			name:     "non-clean path",
			files:    []build.File{{Path: "./main.py"}},
			wantErrs: []string{"target.dev", `"./main.py"`, "clean"},
		},
		{
			name:     "empty path",
			files:    []build.File{{Path: ""}},
			wantErrs: []string{"target.dev", "empty"},
		},
		{
			name:     "backslash separator",
			files:    []build.File{{Path: `sub\main.py`}},
			wantErrs: []string{"target.dev", `"sub\\main.py"`, "slash-separated"},
		},
		{
			name:     "hidden path segment",
			files:    []build.File{{Path: ".env"}},
			wantErrs: []string{"target.dev", `".env"`, "hidden"},
		},
		{
			name:     "hidden intermediate directory",
			files:    []build.File{{Path: ".cache/main.py"}},
			wantErrs: []string{"target.dev", `".cache/main.py"`, "hidden"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files, err := build.Run(stub{files: tc.files}, job(t, "dev"))
			if err == nil {
				t.Fatalf("Run: expected error containing %q, got nil", tc.wantErrs)
			}
			if files != nil {
				t.Error("Run: files should be nil on error")
			}
			for _, want := range tc.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Run error = %q\nwant substring %q", err, want)
				}
			}
		})
	}
}

func TestRunRejectsPlatformTarget(t *testing.T) {
	_, err := build.Run(stub{}, job(t, "prod"))
	if err == nil {
		t.Fatal("Run: expected error for platform target, got nil")
	}
	for _, want := range []string{"target.prod", "not a codegen target"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run error = %q\nwant substring %q", err, want)
		}
	}
}

func TestRunWrapsGeneratorError(t *testing.T) {
	genErr := errors.New("boom")
	_, err := build.Run(stub{err: genErr}, job(t, "dev"))
	if err == nil {
		t.Fatal("Run: expected error, got nil")
	}
	if !errors.Is(err, genErr) {
		t.Errorf("Run error = %q, want errors.Is(err, genErr)", err)
	}
	if !strings.Contains(err.Error(), "target.dev") {
		t.Errorf("Run error = %q\nwant substring %q", err, "target.dev")
	}
}

func TestOutputDir(t *testing.T) {
	tests := []struct {
		name   string
		dir    string // testdata module
		target string
		want   string // relative to the module root
	}{
		{
			name:   "output relative to root project file",
			dir:    "simple",
			target: "dev",
			want:   "gen/dev",
		},
		{
			name:   "output relative to the declaring file's directory",
			dir:    "nested",
			target: "dev",
			want:   "sub/gen",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mod := load(t, tc.dir)
			var tgt *schema.Target
			for _, tg := range mod.Targets {
				if tg.Name == tc.target {
					tgt = tg
				}
			}
			if tgt == nil {
				t.Fatalf("target %q not found", tc.target)
			}

			got, err := build.OutputDir(mod, tgt)
			if err != nil {
				t.Fatalf("OutputDir: unexpected error: %v", err)
			}
			want := filepath.Join(mod.Root, filepath.FromSlash(tc.want))
			if got != want {
				t.Errorf("OutputDir = %q, want %q", got, want)
			}
		})
	}
}

func TestOutputDirErrors(t *testing.T) {
	mod := load(t, "simple")

	t.Run("platform target has no output directory", func(t *testing.T) {
		var prod *schema.Target
		for _, tg := range mod.Targets {
			if tg.Name == "prod" {
				prod = tg
			}
		}
		_, err := build.OutputDir(mod, prod)
		if err == nil || !strings.Contains(err.Error(), "target.prod") {
			t.Errorf("OutputDir error = %v, want mention of target.prod", err)
		}
	})

	t.Run("target not declared in the module", func(t *testing.T) {
		_, err := build.OutputDir(mod, &schema.Target{Name: "ghost", Type: "codegen", Output: "./gen"})
		if err == nil || !strings.Contains(err.Error(), "target.ghost") {
			t.Errorf("OutputDir error = %v, want mention of target.ghost", err)
		}
	})
}
