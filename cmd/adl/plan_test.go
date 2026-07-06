package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weirdGuy/agentform/internal/provider"
	"github.com/weirdGuy/agentform/internal/provider/providertest"
	"github.com/weirdGuy/agentform/internal/schema"
	"github.com/weirdGuy/agentform/internal/state"
)

// runCLI executes "adl <args>" and returns combined output and the
// execution error, mirroring runBuildCmd.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err != nil {
		fmt.Fprintf(&out, "adl: %v\n", err)
	}
	return out.String(), err
}

// registerFake wires a persistent fake provider under the target name
// "fake" for the duration of the test, so successive CLI invocations see
// the same remote platform.
func registerFake(t *testing.T) *providertest.Fake {
	t.Helper()
	fake := providertest.New()
	providerFactories["fake"] = func(*schema.Target) (provider.Provider, error) { return fake, nil }
	t.Cleanup(func() { delete(providerFactories, "fake") })
	return fake
}

func TestPlanApplyDestroyEndToEnd(t *testing.T) {
	fake := registerFake(t)
	dir := copyModule(t, "testdata/platform")

	// 1. First plan: everything is a create; nothing is written.
	out, err := runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	for _, want := range []string{
		"+ agent.geocoder (not in state)",
		"+ agent.weather (not in state)",
		"Plan for target.fake: 2 to create, 0 to update, 0 to delete, 0 unchanged.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, state.Filename)); !os.IsNotExist(err) {
		t.Error("plan created a state file — plan must be a pure read")
	}
	if _, err := os.Stat(filepath.Join(dir, state.LockFilename)); !os.IsNotExist(err) {
		t.Error("plan left the lock file behind")
	}

	// 2. Apply creates both agents and writes state.
	out, err = runCLI(t, "apply", dir)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if want := "Applied target.fake: 2 created, 0 updated, 0 deleted."; !strings.Contains(out, want) {
		t.Errorf("apply output missing %q:\n%s", want, out)
	}
	if len(fake.Objects) != 2 {
		t.Errorf("remote has %d objects after apply, want 2", len(fake.Objects))
	}
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("loading state after apply: %v", err)
	}
	if n := len(st.Target("fake").Resources); n != 2 {
		t.Errorf("state tracks %d resources, want 2", n)
	}

	// 3. Replan: in sync.
	out, err = runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("replan: %v\n%s", err, out)
	}
	if want := "No changes for target.fake: remote matches the spec (2 resources)."; !strings.Contains(out, want) {
		t.Errorf("replan output missing %q:\n%s", want, out)
	}

	// 4. Drift: mutate the remote out-of-band; plan warns and converges.
	for _, obj := range fake.Objects {
		obj["model"].(map[string]any)["id"] = "tampered"
		break
	}
	out, err = runCLI(t, "plan", dir)
	if err != nil {
		t.Fatalf("plan after drift: %v\n%s", err, out)
	}
	for _, want := range []string{
		"~ agent.",
		`model.id: "tampered" → "gpt-4o-mini"`,
		"Warning:",
		"changed outside adl",
		"changed attributes: model.id",
		"Plan for target.fake: 0 to create, 1 to update, 0 to delete, 1 unchanged.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("drift plan output missing %q:\n%s", want, out)
		}
	}

	// 5. Apply converges the drift.
	out, err = runCLI(t, "apply", dir)
	if err != nil {
		t.Fatalf("apply after drift: %v\n%s", err, out)
	}
	if want := "Applied target.fake: 0 created, 1 updated, 0 deleted."; !strings.Contains(out, want) {
		t.Errorf("apply output missing %q:\n%s", want, out)
	}

	// 6. Destroy removes everything, remotely and from state.
	out, err = runCLI(t, "destroy", dir)
	if err != nil {
		t.Fatalf("destroy: %v\n%s", err, out)
	}
	for _, want := range []string{
		"- agent.weather",
		"- agent.geocoder",
		"Destroyed target.fake: 2 deleted.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("destroy output missing %q:\n%s", want, out)
		}
	}
	if len(fake.Objects) != 0 {
		t.Errorf("remote still has %d objects after destroy", len(fake.Objects))
	}
	st, err = state.Load(dir)
	if err != nil {
		t.Fatalf("loading state after destroy: %v", err)
	}
	if n := len(st.Target("fake").Resources); n != 0 {
		t.Errorf("state still tracks %d resources after destroy", n)
	}

	// 7. Destroy again: nothing left to do.
	out, err = runCLI(t, "destroy", dir)
	if err != nil {
		t.Fatalf("second destroy: %v\n%s", err, out)
	}
	if want := "Nothing to destroy for target.fake"; !strings.Contains(out, want) {
		t.Errorf("second destroy output missing %q:\n%s", want, out)
	}
}

func TestPlatformCommandErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		dir      string
		lockDir  bool // pre-create the lock file in a module copy
		wantCode int
		wantOut  []string
	}{
		{
			name:     "codegen target directs to adl build",
			args:     []string{"plan", "--target", "langgraph"},
			dir:      "testdata/valid",
			wantCode: 2,
			wantOut:  []string{"target.langgraph", "codegen", "adl build"},
		},
		{
			name:     "unknown target is a usage error",
			args:     []string{"plan", "--target", "nope"},
			dir:      "testdata/platform",
			wantCode: 2,
			wantOut:  []string{"target.nope", "not declared"},
		},
		{
			name:     "zero platform targets is a usage error",
			args:     []string{"apply"},
			dir:      "testdata/valid",
			wantCode: 2,
			wantOut:  []string{"no platform targets"},
		},
		{
			name:     "platform target without a registered provider",
			args:     []string{"plan"},
			dir:      "testdata/platform_no_provider",
			wantCode: 1,
			wantOut:  []string{"target.openai_assistants", "no platform provider"},
		},
		{
			name:     "invalid module never plans",
			args:     []string{"plan"},
			dir:      "testdata/unknown_ref",
			wantCode: 1,
			wantOut:  []string{"unknown reference"},
		},
		{
			name:     "held lock is reported with recovery hint",
			args:     []string{"plan"},
			dir:      "testdata/platform",
			lockDir:  true,
			wantCode: 2,
			wantOut:  []string{"locked", "delete"},
		},
	}

	registerFake(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.dir
			if tt.lockDir {
				dir = copyModule(t, tt.dir)
				if err := os.WriteFile(filepath.Join(dir, state.LockFilename), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runCLI(t, append(tt.args, dir)...)
			if err == nil {
				t.Fatalf("command succeeded, want error\noutput:\n%s", out)
			}
			if code := exitCode(err); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (error: %v)", code, tt.wantCode, err)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}
