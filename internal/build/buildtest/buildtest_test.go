package buildtest_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkastordev/kastor/internal/build"
	"github.com/getkastordev/kastor/internal/build/buildtest"
	"github.com/getkastordev/kastor/internal/graph"
	"github.com/getkastordev/kastor/internal/module"
)

// recorder captures Fatalf calls so the helper's failure path can itself be
// tested. The embedded TB panics on any method not overridden here.
type recorder struct {
	testing.TB
	failed bool
	msg    string
}

func (r *recorder) Helper() {}
func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
}

// steady always generates the same file.
type steady struct{}

func (steady) Generate(*build.Job) ([]build.File, error) {
	return []build.File{{Path: "main.py", Data: []byte("main\n")}}, nil
}

// flaky generates different content on every call.
type flaky struct{ calls int }

func (f *flaky) Generate(*build.Job) ([]build.File, error) {
	f.calls++
	return []build.File{{Path: "main.py", Data: []byte(fmt.Sprintf("run %d\n", f.calls))}}, nil
}

func job(t *testing.T) *build.Job {
	t.Helper()
	mod, err := module.Load(filepath.Join("..", "testdata", "simple"))
	if err != nil {
		t.Fatalf("module.Load: %v", err)
	}
	g, err := graph.Build(mod)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}
	for _, tgt := range mod.Targets {
		if tgt.Name == "dev" {
			return &build.Job{Module: mod, Graph: g, Target: tgt}
		}
	}
	t.Fatal("target dev not found")
	return nil
}

func TestAssertDeterministicPasses(t *testing.T) {
	rec := &recorder{}
	files := buildtest.AssertDeterministic(rec, steady{}, job(t))
	if rec.failed {
		t.Fatalf("AssertDeterministic failed on a deterministic generator: %s", rec.msg)
	}
	if len(files) != 1 || files[0].Path != "main.py" {
		t.Errorf("AssertDeterministic files = %v, want the generated main.py", files)
	}
}

func TestAssertDeterministicCatchesDrift(t *testing.T) {
	rec := &recorder{}
	buildtest.AssertDeterministic(rec, &flaky{}, job(t))
	if !rec.failed {
		t.Fatal("AssertDeterministic passed a generator whose output drifts between runs")
	}
	for _, want := range []string{"main.py", "not deterministic"} {
		if !strings.Contains(rec.msg, want) {
			t.Errorf("failure message = %q\nwant substring %q", rec.msg, want)
		}
	}
}

func TestAssertDeterministicReportsGeneratorErrors(t *testing.T) {
	rec := &recorder{}
	buildtest.AssertDeterministic(rec, errGen{}, job(t))
	if !rec.failed || !strings.Contains(rec.msg, "boom") {
		t.Errorf("failed = %v, msg = %q; want failure mentioning the generator error", rec.failed, rec.msg)
	}
}

type errGen struct{}

func (errGen) Generate(*build.Job) ([]build.File, error) {
	return nil, fmt.Errorf("boom")
}
