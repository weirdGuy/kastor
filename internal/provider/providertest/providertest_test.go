package providertest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/provider"
	"github.com/weirdGuy/agentform/internal/provider/providertest"
)

func TestFakeDiff(t *testing.T) {
	tests := []struct {
		name    string
		desired provider.Object
		remote  provider.Object
		want    []provider.AttrDiff
	}{
		{
			name:    "equal configs produce no diffs",
			desired: provider.Object{"a": "x", "n": 1.0},
			remote:  provider.Object{"a": "x", "n": 1.0},
			want:    nil,
		},
		{
			name:    "changed leaf reports remote as old, desired as new",
			desired: provider.Object{"model": map[string]any{"id": "gpt-4o-mini"}},
			remote:  provider.Object{"model": map[string]any{"id": "gpt-4o"}},
			want:    []provider.AttrDiff{{Path: "model.id", Old: "gpt-4o", New: "gpt-4o-mini"}},
		},
		{
			name:    "attribute only in desired is an add",
			desired: provider.Object{"description": "new"},
			remote:  provider.Object{},
			want:    []provider.AttrDiff{{Path: "description", Old: nil, New: "new"}},
		},
		{
			name:    "attribute only in remote is a removal",
			desired: provider.Object{},
			remote:  provider.Object{"description": "gone"},
			want:    []provider.AttrDiff{{Path: "description", Old: "gone", New: nil}},
		},
		{
			name:    "same-length arrays diff element-wise",
			desired: provider.Object{"tools": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}},
			remote:  provider.Object{"tools": []any{map[string]any{"name": "a"}, map[string]any{"name": "c"}}},
			want:    []provider.AttrDiff{{Path: "tools[1].name", Old: "c", New: "b"}},
		},
		{
			name:    "different-length arrays diff as a whole",
			desired: provider.Object{"tags": []any{"a"}},
			remote:  provider.Object{"tags": []any{"a", "b"}},
			want:    []provider.AttrDiff{{Path: "tags", Old: []any{"a", "b"}, New: []any{"a"}}},
		},
		{
			name:    "multiple diffs come out in sorted key order",
			desired: provider.Object{"b": "2", "a": "1"},
			remote:  provider.Object{"b": "x", "a": "y"},
			want: []provider.AttrDiff{
				{Path: "a", Old: "y", New: "1"},
				{Path: "b", Old: "x", New: "2"},
			},
		},
	}

	fake := providertest.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fake.Diff(&provider.Resource{Addr: "agent.x", Config: tt.desired}, tt.remote)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Diff() (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFakeLifecycle(t *testing.T) {
	ctx := context.Background()
	fake := providertest.New()

	id1, err := fake.Create(ctx, &provider.Resource{Addr: "agent.a", Config: provider.Object{"v": "1"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id2, err := fake.Create(ctx, &provider.Resource{Addr: "agent.b", Config: provider.Object{"v": "2"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id1 != "fake-1" || id2 != "fake-2" {
		t.Errorf("ids = %s, %s; want fake-1, fake-2", id1, id2)
	}

	remote, found, err := fake.Read(ctx, id1)
	if err != nil || !found {
		t.Fatalf("Read(%s) = found %v, err %v", id1, found, err)
	}
	// Read hands out a copy: mutating it must not corrupt the store.
	remote["v"] = "mutated"
	again, _, _ := fake.Read(ctx, id1)
	if again["v"] != "1" {
		t.Error("Read returned a live reference into the fake's store")
	}

	if err := fake.Update(ctx, id1, &provider.Resource{Addr: "agent.a", Config: provider.Object{"v": "3"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, _, _ := fake.Read(ctx, id1)
	if updated["v"] != "3" {
		t.Errorf("after Update, v = %v, want 3", updated["v"])
	}

	if err := fake.Delete(ctx, id1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := fake.Read(ctx, id1); found {
		t.Error("object still found after Delete")
	}
	// Delete is idempotent by contract.
	if err := fake.Delete(ctx, id1); err != nil {
		t.Errorf("second Delete errored: %v", err)
	}

	wantCalls := []string{
		"create agent.a", "create agent.b",
		"read fake-1", "read fake-1",
		"update agent.a", "read fake-1",
		"delete fake-1", "read fake-1", "delete fake-1",
	}
	if diff := cmp.Diff(wantCalls, fake.Calls); diff != "" {
		t.Errorf("Calls (-want +got):\n%s", diff)
	}
}

func TestFakeFailOn(t *testing.T) {
	ctx := context.Background()
	fake := providertest.New()
	boom := errors.New("boom")
	fake.FailOn = map[string]error{"create agent.b": boom}

	if _, err := fake.Create(ctx, &provider.Resource{Addr: "agent.a", Config: provider.Object{}}); err != nil {
		t.Fatalf("Create(agent.a): %v", err)
	}
	if _, err := fake.Create(ctx, &provider.Resource{Addr: "agent.b", Config: provider.Object{}}); !errors.Is(err, boom) {
		t.Errorf("Create(agent.b) error = %v, want boom", err)
	}

	fake.FailOn = map[string]error{"update agent.a": boom}
	if err := fake.Update(ctx, "fake-1", &provider.Resource{Addr: "agent.a", Config: provider.Object{}}); !errors.Is(err, boom) {
		t.Errorf("Update error = %v, want boom", err)
	}

	fake.FailOn = map[string]error{"read fake-1": boom}
	if _, _, err := fake.Read(ctx, "fake-1"); !errors.Is(err, boom) {
		t.Errorf("Read error = %v, want boom", err)
	}
}

func TestFakeUpdateUnknownIDFails(t *testing.T) {
	fake := providertest.New()
	err := fake.Update(context.Background(), "fake-404", &provider.Resource{Addr: "agent.a", Config: provider.Object{}})
	if err == nil {
		t.Fatal("Update of unknown id succeeded, want error")
	}
}
