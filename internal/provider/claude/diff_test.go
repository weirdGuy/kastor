package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/kastor/internal/provider"
)

func TestDiffGoldenResponsesAreInSync(t *testing.T) {
	tests := []struct {
		addr     string
		spec     string
		response string
	}{
		{"agent.weather", "full_spec.json", "full_api_response.json"},
		{"agent.minimal", "minimal_spec.json", "minimal_api_response.json"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			desired := &provider.Resource{Addr: tt.addr, Config: loadObject(t, tt.spec)}
			if tt.addr == "agent.weather" {
				desired = fullResource(t)
			}
			diffs, err := New().Diff(
				desired,
				loadObject(t, tt.response),
			)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if len(diffs) != 0 {
				t.Errorf("Diff = %#v, want empty", diffs)
			}
		})
	}
}

func TestDiffScalarsAreSortedAndDirectional(t *testing.T) {
	desired := fullResource(t)
	remote := loadObject(t, "full_api_response.json")
	remote["description"] = "Old description"
	remote["system"] = "Old system"
	remote["model"].(map[string]any)["id"] = "claude-sonnet-4-5"

	got, err := New().Diff(desired, remote)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	want := []provider.AttrDiff{
		{Path: "description", Old: "Old description", New: "Answers engineering questions"},
		{Path: "model.id", Old: "claude-sonnet-4-5", New: "claude-opus-5"},
		{Path: "system", Old: "Old system", New: "Use the available tools and cite concrete evidence."},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Diff mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffReplaceArraysPerElementAndOrderSensitively(t *testing.T) {
	desired := fullResource(t)
	remote := loadObject(t, "full_api_response.json")
	tools := remote["tools"].([]any)
	tools[0], tools[1] = tools[1], tools[0]
	remote["skills"] = []any{map[string]any{"type": "custom", "skill_id": "skill_1"}}
	remote["mcp_servers"] = append(remote["mcp_servers"].([]any), map[string]any{
		"type": "url",
		"name": "linear",
		"url":  "https://mcp.linear.app/sse",
	})

	got, err := New().Diff(desired, remote)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	wantPaths := []string{
		"mcp_servers[1]",
		"skills[0]",
		"tools[0]",
		"tools[1]",
	}
	if diff := cmp.Diff(wantPaths, paths(got)); diff != "" {
		t.Errorf("array diff paths mismatch (-want +got):\n%s", diff)
	}
	for _, diff := range got {
		if strings.HasPrefix(diff.Path, "tools[") {
			if _, ok := diff.Old.(map[string]any); !ok {
				t.Errorf("%s Old = %T, want whole tool object", diff.Path, diff.Old)
			}
			if _, ok := diff.New.(map[string]any); !ok {
				t.Errorf("%s New = %T, want whole tool object", diff.Path, diff.New)
			}
		}
	}
}

func TestDiffEveryReplaceArrayIsOrderSensitive(t *testing.T) {
	desired := provider.Object{
		"tools":       []any{"tool-a", "tool-b"},
		"mcp_servers": []any{"server-a", "server-b"},
		"skills":      []any{"skill-a", "skill-b"},
	}
	remote := provider.Object{
		"tools":       []any{"tool-b", "tool-a"},
		"mcp_servers": []any{"server-b", "server-a"},
		"skills":      []any{"skill-b", "skill-a"},
	}

	got := diffObjects(desired, remote)
	wantPaths := []string{
		"mcp_servers[0]",
		"mcp_servers[1]",
		"skills[0]",
		"skills[1]",
		"tools[0]",
		"tools[1]",
	}
	if diff := cmp.Diff(wantPaths, paths(got)); diff != "" {
		t.Errorf("order-sensitive diff paths mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffChangeInsideToolRendersAtElement(t *testing.T) {
	desired := fullResource(t)
	remote := loadObject(t, "full_api_response.json")
	config := remote["tools"].([]any)[0].(map[string]any)["configs"].([]any)[0].(map[string]any)
	config["enabled"] = false

	got, err := New().Diff(desired, remote)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff := cmp.Diff([]string{"tools[0]"}, paths(got)); diff != "" {
		t.Errorf("tool diff path mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffMetadataOwnershipAndDeletion(t *testing.T) {
	t.Run("foreign keys ignored and owned keys compared", func(t *testing.T) {
		desired := fullResource(t)
		remote := loadObject(t, "full_api_response.json")
		metadata := remote["metadata"].(map[string]any)
		metadata["console_note"] = "changed foreign value"
		metadata["another_foreign_key"] = true
		metadata["team"] = "application"

		got, err := New().Diff(desired, remote)
		if err != nil {
			t.Fatalf("Diff: %v", err)
		}
		want := []provider.AttrDiff{{Path: "metadata.team", Old: "application", New: "platform"}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("metadata diff mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("null is a deletion tombstone", func(t *testing.T) {
		cfg := loadObject(t, "minimal_spec.json")
		cfg["metadata"] = map[string]any{"obsolete": nil}
		desired := &provider.Resource{Addr: "agent.minimal", Config: cfg}
		remote := loadObject(t, "minimal_api_response.json")
		remote["metadata"].(map[string]any)["obsolete"] = "old"

		got, err := New().Diff(desired, remote)
		if err != nil {
			t.Fatalf("Diff: %v", err)
		}
		want := []provider.AttrDiff{{Path: "metadata.obsolete", Old: "old", New: nil}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("metadata deletion mismatch (-want +got):\n%s", diff)
		}

		delete(remote["metadata"].(map[string]any), "obsolete")
		got, err = New().Diff(desired, remote)
		if err != nil || len(got) != 0 {
			t.Errorf("already-deleted metadata key: Diff = %#v, %v; want empty, nil", got, err)
		}
	})
}

func TestDiffMetadataKeysComeFromSpecAndLastAppliedCalls(t *testing.T) {
	current := loadObject(t, "minimal_spec.json")
	current["metadata"] = map[string]any{"current_key": "desired"}
	lastApplied := loadObject(t, "minimal_spec.json")
	lastApplied["metadata"] = map[string]any{"previous_key": "applied"}
	remote := loadObject(t, "minimal_api_response.json")
	remote["metadata"].(map[string]any)["current_key"] = "remote-current"
	remote["metadata"].(map[string]any)["previous_key"] = "remote-previous"
	remote["metadata"].(map[string]any)["foreign_key"] = "ignored"

	specDiff, err := New().Diff(&provider.Resource{Addr: "agent.minimal", Config: current}, remote)
	if err != nil {
		t.Fatalf("spec Diff: %v", err)
	}
	lastAppliedDiff, err := New().Diff(&provider.Resource{Addr: "agent.minimal", Config: lastApplied}, remote)
	if err != nil {
		t.Fatalf("last-applied Diff: %v", err)
	}

	if diff := cmp.Diff([]string{"metadata.current_key"}, paths(specDiff)); diff != "" {
		t.Errorf("spec metadata paths mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"metadata.previous_key"}, paths(lastAppliedDiff)); diff != "" {
		t.Errorf("last-applied metadata paths mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffManagedMarkerIsAnAssertionNotAnAttrDiff(t *testing.T) {
	desired := &provider.Resource{Addr: "agent.minimal", Config: loadObject(t, "minimal_spec.json")}
	remote := loadObject(t, "minimal_api_response.json")
	remote["metadata"].(map[string]any)[managedMarkerKey] = "agent.someone_else"

	diffs, err := New().Diff(desired, remote)
	if err == nil {
		t.Fatal("Diff succeeded for the wrong managed marker")
	}
	if diffs != nil {
		t.Errorf("Diffs = %#v, want nil on ownership assertion failure", diffs)
	}
	if !strings.Contains(err.Error(), "metadata.kastor_managed") || !strings.Contains(err.Error(), "agent.minimal") {
		t.Errorf("marker error = %q, want marker path and resource address", err)
	}
}

func TestDiffIgnoresEnvelopeTimestampsAndForeignModelFields(t *testing.T) {
	desired := fullResource(t)
	remote := loadObject(t, "full_api_response.json")
	remote["id"] = "agent_changed_envelope"
	remote["type"] = "new_agent_envelope"
	remote["version"] = float64(900)
	remote["created_at"] = "yesterday"
	remote["updated_at"] = "today"
	remote["archived_at"] = "tomorrow"
	remote["model"].(map[string]any)["server_default_added_later"] = true

	got, err := New().Diff(desired, remote)
	if err != nil || len(got) != 0 {
		t.Errorf("Diff = %#v, %v; want empty, nil", got, err)
	}
}

func TestDiffReportsMCPServerURLDrift(t *testing.T) {
	desired := fullResource(t)
	remote := loadObject(t, "full_api_response.json")
	remote["mcp_servers"].([]any)[0].(map[string]any)["url"] = "https://deployment.example.com/mcp"

	got, err := New().Diff(desired, remote)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff := cmp.Diff([]string{"mcp_servers[0]"}, paths(got)); diff != "" {
		t.Errorf("MCP URL diff path mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffIsPureAndDeterministic(t *testing.T) {
	desired := fullResource(t)
	remote := loadObject(t, "full_api_response.json")
	remote["description"] = "drift"
	remote["skills"] = []any{"skill_2", "skill_1"}

	desiredBefore := marshalObject(t, desired.Config)
	remoteBefore := marshalObject(t, remote)

	var first []provider.AttrDiff
	for i := 0; i < 10; i++ {
		got, err := New().Diff(desired, remote)
		if err != nil {
			t.Fatalf("Diff #%d: %v", i+1, err)
		}
		if i == 0 {
			first = got
		} else if diff := cmp.Diff(first, got); diff != "" {
			t.Fatalf("Diff is nondeterministic on run %d (-first +got):\n%s", i+1, diff)
		}
	}
	if got := marshalObject(t, desired.Config); got != desiredBefore {
		t.Errorf("Diff mutated desired:\nbefore %s\nafter  %s", desiredBefore, got)
	}
	if got := marshalObject(t, remote); got != remoteBefore {
		t.Errorf("Diff mutated remote:\nbefore %s\nafter  %s", remoteBefore, got)
	}
}

func paths(diffs []provider.AttrDiff) []string {
	out := make([]string, len(diffs))
	for i, diff := range diffs {
		out[i] = diff.Path
	}
	return out
}

func marshalObject(t *testing.T, object provider.Object) string {
	t.Helper()
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	return string(data)
}
