package diff_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/diff"
)

func TestUnified(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{
			name: "identical inputs produce no diff",
			a:    "one\ntwo\n",
			b:    "one\ntwo\n",
			want: "",
		},
		{
			name: "single change with surrounding context",
			a:    "one\ntwo\nthree\nfour\nfive\nsix\nseven\n",
			b:    "one\ntwo\nthree\nFOUR\nfive\nsix\nseven\n",
			want: "--- a/x.agent\n" +
				"+++ b/x.agent\n" +
				"@@ -1,7 +1,7 @@\n" +
				" one\n" +
				" two\n" +
				" three\n" +
				"-four\n" +
				"+FOUR\n" +
				" five\n" +
				" six\n" +
				" seven\n",
		},
		{
			name: "addition at end of file",
			a:    "one\ntwo\n",
			b:    "one\ntwo\nthree\n",
			want: "--- a/x.agent\n" +
				"+++ b/x.agent\n" +
				"@@ -1,2 +1,3 @@\n" +
				" one\n" +
				" two\n" +
				"+three\n",
		},
		{
			name: "deletion to empty file",
			a:    "only\n",
			b:    "",
			want: "--- a/x.agent\n" +
				"+++ b/x.agent\n" +
				"@@ -1,1 +0,0 @@\n" +
				"-only\n",
		},
		{
			name: "empty file to content",
			a:    "",
			b:    "one\ntwo\n",
			want: "--- a/x.agent\n" +
				"+++ b/x.agent\n" +
				"@@ -0,0 +1,2 @@\n" +
				"+one\n" +
				"+two\n",
		},
		{
			name: "distant changes split into separate hunks",
			a:    "l01\nl02\nl03\nl04\nl05\nl06\nl07\nl08\nl09\nl10\nl11\nl12\nl13\nl14\nl15\nl16\nl17\nl18\nl19\nl20\n",
			b:    "l01\nX02\nl03\nl04\nl05\nl06\nl07\nl08\nl09\nl10\nl11\nl12\nl13\nl14\nl15\nl16\nl17\nX18\nl19\nl20\n",
			want: "--- a/x.agent\n" +
				"+++ b/x.agent\n" +
				"@@ -1,5 +1,5 @@\n" +
				" l01\n" +
				"-l02\n" +
				"+X02\n" +
				" l03\n" +
				" l04\n" +
				" l05\n" +
				"@@ -15,6 +15,6 @@\n" +
				" l15\n" +
				" l16\n" +
				" l17\n" +
				"-l18\n" +
				"+X18\n" +
				" l19\n" +
				" l20\n",
		},
		{
			name: "missing trailing newline is marked",
			a:    "one\ntwo",
			b:    "one\ntwo\n",
			want: "--- a/x.agent\n" +
				"+++ b/x.agent\n" +
				"@@ -1,2 +1,2 @@\n" +
				" one\n" +
				"-two\n" +
				"\\ No newline at end of file\n" +
				"+two\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff.Unified("x.agent", []byte(tt.a), []byte(tt.b))
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("Unified (-want +got):\n%s", d)
			}
		})
	}
}
