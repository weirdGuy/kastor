package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionMatches(t *testing.T) {
	tests := []struct {
		version    string
		constraint string
		want       bool
	}{
		{"0.1.0", "~> 0.1", true},
		{"0.9.0", "~> 0.1", true},
		{"1.0.0", "~> 0.1", false},
		{"0.1.9", "~> 0.1.2", true},
		{"0.2.0", "~> 0.1.2", false},
		{"0.1.0", "0.1.0", true},
		{"nope", "~> 0.1", false},
	}
	for _, test := range tests {
		if got := versionMatches(test.version, test.constraint); got != test.want {
			t.Errorf("versionMatches(%q, %q) = %v, want %v", test.version, test.constraint, got, test.want)
		}
	}
}

func TestDiscoverUsesLocalOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin")
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KASTOR_PLUGIN_LANGGRAPH", path)
	got, err := Discover("langgraph", "github.com/getkastordev/kastor-langgraph")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("Discover() = %q, want %q", got, path)
	}
}
