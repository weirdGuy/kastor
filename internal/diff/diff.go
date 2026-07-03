// Package diff produces unified diffs for adl fmt's -diff output. It is a
// small line-based LCS implementation kept in-house because the approved
// dependency set has no diff library; it targets human review of
// config-sized files, not patch-perfect fidelity on huge inputs.
package diff

import (
	"bytes"
	"fmt"
	"strings"
)

// context is the number of unchanged lines shown around each change,
// matching the unified-diff default.
const context = 3

// op is one line of an edit script: kept (' '), deleted ('-'), or
// added ('+'). Lines keep their trailing newline; a missing one triggers
// the "\ No newline at end of file" marker on output.
type op struct {
	kind byte
	text string
	// 1-based line this op consumes in a and b; 0 when the op does not
	// consume a line on that side.
	aLine, bLine int
}

// Unified returns a unified diff from a to b with standard "--- a/path" /
// "+++ b/path" headers, or "" when the inputs are identical.
func Unified(path string, a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}

	ops := editScript(splitLines(a), splitLines(b))

	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", path, path)
	for _, h := range hunks(ops) {
		writeHunk(&out, h)
	}
	return out.String()
}

// splitLines splits src into lines, each keeping its trailing newline.
// A final line without a newline is kept as-is.
func splitLines(src []byte) []string {
	if len(src) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(src), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// editScript computes a line-based edit script from a to b via
// longest-common-subsequence. O(len(a)*len(b)) time and space, which is
// fine for the file sizes adl fmt handles.
func editScript(a, b []string) []op {
	// lcs[i][j] = LCS length of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	var ops []op
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{kind: ' ', text: a[i], aLine: i + 1, bLine: j + 1})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{kind: '-', text: a[i], aLine: i + 1})
			i++
		default:
			ops = append(ops, op{kind: '+', text: b[j], bLine: j + 1})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, op{kind: '-', text: a[i], aLine: i + 1})
	}
	for ; j < len(b); j++ {
		ops = append(ops, op{kind: '+', text: b[j], bLine: j + 1})
	}
	return ops
}

// hunks groups the edit script into hunks with `context` unchanged lines
// around each change, splitting where the gap between changes exceeds
// what two contexts can bridge.
func hunks(ops []op) [][]op {
	var groups [][]op
	start := -1 // index of first op in the current hunk, -1 when none open
	end := -1   // exclusive end of the current hunk
	for idx, o := range ops {
		if o.kind == ' ' {
			continue
		}
		if start >= 0 && idx-context > end {
			groups = append(groups, ops[start:min(end, len(ops))])
			start = -1
		}
		if start < 0 {
			start = max(idx-context, 0)
		}
		end = idx + context + 1
	}
	if start >= 0 {
		groups = append(groups, ops[start:min(end, len(ops))])
	}
	return groups
}

func writeHunk(out *strings.Builder, h []op) {
	var aStart, aCount, bStart, bCount int
	for _, o := range h {
		if o.aLine > 0 {
			if aStart == 0 {
				aStart = o.aLine
			}
			aCount++
		}
		if o.bLine > 0 {
			if bStart == 0 {
				bStart = o.bLine
			}
			bCount++
		}
	}
	// A side with no lines anchors just before the other side's position.
	if aCount == 0 {
		aStart = bStart - 1
	}
	if bCount == 0 {
		bStart = aStart - 1
	}

	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
	for _, o := range h {
		out.WriteByte(o.kind)
		out.WriteString(o.text)
		if !strings.HasSuffix(o.text, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
}
