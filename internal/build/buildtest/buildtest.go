// Package buildtest provides test helpers that hold generators to the
// engine's determinism guarantee (see package build). Every per-target
// generator's test suite is expected to run its fixtures through
// AssertDeterministic.
package buildtest

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/getkastordev/kastor/internal/build"
)

// runs is how many times AssertDeterministic re-generates. One re-run proves
// byte-identity; the extras exist to shake out map-iteration order, which
// Go randomizes per traversal, not per process.
const runs = 5

// AssertDeterministic runs gen through build.Run repeatedly and fails tb
// unless every run produces byte-identical files. It returns the canonical
// files so callers can follow up with golden-file assertions. Note the
// limits: drift within one process is caught (iteration order, randomness);
// clock-derived output may slip through if the runs land on the same
// timestamp, so keep golden files as the second line of defense.
func AssertDeterministic(tb testing.TB, gen build.Generator, job *build.Job) []build.File {
	tb.Helper()

	first, err := build.Run(gen, job)
	if err != nil {
		tb.Fatalf("Run: %v", err)
		return nil
	}

	for i := 1; i < runs; i++ {
		again, err := build.Run(gen, job)
		if err != nil {
			tb.Fatalf("Run #%d: %v", i+1, err)
			return nil
		}
		if msg := compare(first, again); msg != "" {
			tb.Fatalf("generator output is not deterministic (run 1 vs run %d): %s", i+1, msg)
			return nil
		}
	}
	return first
}

// compare reports the first difference between two canonical (sorted,
// validated) file sets, or "" when they are byte-identical.
func compare(a, b []build.File) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].Path != b[i].Path {
			return fmt.Sprintf("file #%d is %s vs %s", i+1, a[i].Path, b[i].Path)
		}
		if !bytes.Equal(a[i].Data, b[i].Data) {
			return a[i].Path + " differs in content"
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("file count %d vs %d", len(a), len(b))
	}
	return ""
}
