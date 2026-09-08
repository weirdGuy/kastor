// Package build is the codegen engine (SPEC.md §6): it runs a codegen
// target's Generator over a loaded module and syncs the generated files
// into the target's output directory. Per-framework generators live in
// subpackages (build/langgraph, ...); this package is target-agnostic.
//
// Determinism guarantee: the same module must always produce byte-identical
// output — same file set, same paths, same bytes. The engine contributes
// canonical file ordering; generators must hold up the rest (see Generator).
// buildtest.AssertDeterministic enforces the guarantee in generator tests.
package build

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkastordev/kastor/internal/graph"
	"github.com/getkastordev/kastor/internal/module"
	"github.com/getkastordev/kastor/internal/schema"
)

// File is one generated output file. Path is slash-separated and relative
// to the target's output directory; it must be clean, must not escape the
// output directory, and must not contain hidden (dot-prefixed) segments —
// hidden entries in the output directory belong to the user, never to the
// engine (see Write).
//
// Preserve marks a file the engine generates once and then hands over: a
// scaffold whose body is the user's to write, like the stub for a tool with
// source kind "runtime" (SPEC.md §3.3). Write stops overwriting such a file
// the moment its contents differ from the stub it last wrote. Data must still
// be a pure function of the spec — Preserve changes when a file is written,
// never what is generated.
type File struct {
	Path     string
	Data     []byte
	Preserve bool
}

// Job bundles the inputs a Generator consumes: the loaded module, its
// dependency graph, and the codegen target being built.
type Job struct {
	Module *module.Module
	Graph  *graph.Graph
	Target *schema.Target
}

// Generator produces the output files for one codegen framework.
//
// Generate must be a pure function of the Job: no clocks, no randomness, no
// environment reads, no map-iteration-order leakage. Identical inputs must
// yield byte-identical files. File order does not matter — Run canonicalizes
// it — but paths and contents do.
type Generator interface {
	Generate(job *Job) ([]File, error)
}

// Run invokes gen for a codegen target and canonicalizes the result: file
// paths are validated, duplicates are errors, and files are returned sorted
// by path so output order never depends on generator internals.
func Run(gen Generator, job *Job) ([]File, error) {
	addr := job.Target.Addr()
	if job.Target.Type != "codegen" {
		return nil, fmt.Errorf("%s: not a codegen target", addr)
	}

	files, err := gen.Generate(job)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}

	seen := map[string]bool{}
	for _, f := range files {
		if err := f.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", addr, err)
		}
		if seen[f.Path] {
			return nil, fmt.Errorf("%s: duplicate generated file %q", addr, f.Path)
		}
		seen[f.Path] = true
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// OutputDir resolves a codegen target's output directory the same way the
// module walker does when excluding generated code from input: relative
// paths are anchored at the directory of the file that declares the target.
func OutputDir(mod *module.Module, tgt *schema.Target) (string, error) {
	if tgt.Type != "codegen" {
		return "", fmt.Errorf("%s: not a codegen target", tgt.Addr())
	}
	sym, ok := mod.Lookup(tgt.Addr())
	if !ok {
		return "", fmt.Errorf("%s: not declared in module %s", tgt.Addr(), mod.Root)
	}
	if filepath.IsAbs(tgt.Output) {
		return filepath.Clean(tgt.Output), nil
	}
	return filepath.Join(mod.Root, filepath.Dir(sym.File), tgt.Output), nil
}

// validate enforces the File.Path rules documented on File.
func (f File) validate() error {
	p := f.Path
	switch {
	case p == "":
		return fmt.Errorf("invalid generated file path: empty")
	case strings.Contains(p, `\`):
		return fmt.Errorf("invalid generated file path %q: must be slash-separated", p)
	case path.IsAbs(p):
		return fmt.Errorf("invalid generated file path %q: must be relative", p)
	case p == ".." || strings.HasPrefix(p, "../"):
		return fmt.Errorf("invalid generated file path %q: escapes the output directory", p)
	case path.Clean(p) != p:
		return fmt.Errorf("invalid generated file path %q: not a clean path", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return fmt.Errorf("invalid generated file path %q: hidden segment %q", p, seg)
		}
	}
	return nil
}
