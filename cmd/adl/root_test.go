package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantOut string
	}{
		{
			name:    "version subcommand prints version",
			args:    []string{"version"},
			wantOut: "adl version dev",
		},
		{
			name:    "no args prints help",
			args:    []string{},
			wantOut: "Usage:",
		},
		{
			name:    "unknown subcommand errors",
			args:    []string{"frobnicate"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Execute() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestRootCommandUse(t *testing.T) {
	if got := newRootCmd().Use; got != "adl" {
		t.Errorf("root command Use = %q, want %q", got, "adl")
	}
}
