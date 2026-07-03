package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// runValidateCmd executes "adl validate <dir>" and returns combined output
// and the execution error.
func runValidateCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"validate"}, args...))
	err := cmd.Execute()
	// main.go prints the returned error to stderr; append it so tests see
	// the same combined output a user does.
	if err != nil {
		fmt.Fprintf(&out, "adl: %v\n", err)
	}
	return out.String(), err
}

func TestValidateCommand(t *testing.T) {
	tests := []struct {
		name    string
		dir     string
		wantErr bool
		wantOut []string // substrings that must appear in the output
		skipOut []string // substrings that must not appear
	}{
		{
			name: "valid module succeeds with summary",
			dir:  "testdata/valid",
			wantOut: []string{
				"Success",
				"1 agent",
				"1 tool",
				"1 prompt",
				"1 model",
				"1 target",
			},
		},
		{
			name:    "unknown reference fails with block address",
			dir:     "testdata/unknown_ref",
			wantErr: true,
			wantOut: []string{
				"solo.agent",
				"agent.solo: unknown reference model.fastt",
			},
			skipOut: []string{"Success"},
		},
		{
			name:    "dependency cycle fails naming the cycle",
			dir:     "testdata/cycle",
			wantErr: true,
			wantOut: []string{
				"dependency cycle",
				"agent.a",
				"agent.b",
			},
		},
		{
			name:    "all diagnostics aggregated across files",
			dir:     "testdata/multi_error",
			wantErr: true,
			wantOut: []string{
				"broken.agent:3,", // parse error with file:line position
				"agent.solo: unknown reference model.missing",
				"2 error",
			},
		},
		{
			name:    "missing directory fails",
			dir:     "testdata/does_not_exist",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runValidateCmd(t, tt.dir)
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
		})
	}
}

func TestValidateCommandDefaultsToCwd(t *testing.T) {
	// No positional arg must be accepted (defaults to "."); an empty
	// directory is an empty module and still validates cleanly.
	t.Chdir(t.TempDir())
	out, err := runValidateCmd(t)
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Success") {
		t.Errorf("output missing %q:\n%s", "Success", out)
	}
}
