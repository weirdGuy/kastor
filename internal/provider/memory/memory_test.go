package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/schema"
)

func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	p := New()

	// Create assigns sequential ids and stores the config.
	id, err := p.Create(ctx, &provider.Resource{Addr: "agent.a", Config: provider.Object{"model": map[string]any{"id": "gpt-4o-mini"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "mem-1" {
		t.Errorf("first id = %q, want mem-1", id)
	}
	if id2, _ := p.Create(ctx, &provider.Resource{Addr: "agent.b", Config: provider.Object{}}); id2 != "mem-2" {
		t.Errorf("second id = %q, want mem-2", id2)
	}

	// Read returns the stored object.
	obj, found, err := p.Read(ctx, id)
	if err != nil || !found {
		t.Fatalf("read: found=%v err=%v", found, err)
	}
	if diff := cmp.Diff(provider.Object{"model": map[string]any{"id": "gpt-4o-mini"}}, obj); diff != "" {
		t.Errorf("read object mismatch (-want +got):\n%s", diff)
	}

	// Update replaces the stored object.
	if err := p.Update(ctx, id, &provider.Resource{Addr: "agent.a", Config: provider.Object{"model": map[string]any{"id": "gpt-4o"}}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	obj, _, _ = p.Read(ctx, id)
	if got := obj["model"].(map[string]any)["id"]; got != "gpt-4o" {
		t.Errorf("model.id after update = %v, want gpt-4o", got)
	}

	// Delete removes the object; reading it reports found=false, no error.
	if err := p.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, err := p.Read(ctx, id); found || err != nil {
		t.Errorf("read after delete: found=%v err=%v, want found=false err=nil", found, err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	if err := New().Delete(context.Background(), "mem-99"); err != nil {
		t.Errorf("deleting a missing id must succeed, got %v", err)
	}
}

func TestUpdateUnknownIDFails(t *testing.T) {
	err := New().Update(context.Background(), "mem-99", &provider.Resource{Addr: "agent.a", Config: provider.Object{}})
	if err == nil || !strings.Contains(err.Error(), "mem-99") {
		t.Errorf("update of unknown id: err = %v, want error naming mem-99", err)
	}
}

// The store must never share structure with callers: mutating a config
// after Create, or an object returned by Read, must not change the store.
func TestStoreIsIsolatedFromCallers(t *testing.T) {
	ctx := context.Background()
	p := New()

	cfg := provider.Object{"model": map[string]any{"id": "gpt-4o-mini"}}
	id, _ := p.Create(ctx, &provider.Resource{Addr: "agent.a", Config: cfg})
	cfg["model"].(map[string]any)["id"] = "mutated-after-create"

	got, _, _ := p.Read(ctx, id)
	if v := got["model"].(map[string]any)["id"]; v != "gpt-4o-mini" {
		t.Fatalf("store shares structure with the creator: model.id = %v", v)
	}

	got["model"].(map[string]any)["id"] = "mutated-after-read"
	again, _, _ := p.Read(ctx, id)
	if v := again["model"].(map[string]any)["id"]; v != "gpt-4o-mini" {
		t.Errorf("store shares structure with readers: model.id = %v", v)
	}
}

func TestDiffMatchesStoredObjects(t *testing.T) {
	ctx := context.Background()
	p := New()
	id, _ := p.Create(ctx, &provider.Resource{Addr: "agent.a", Config: provider.Object{"model": map[string]any{"id": "gpt-4o-mini"}}})
	remote, _, _ := p.Read(ctx, id)

	// In sync: empty diff.
	diffs, err := p.Diff(&provider.Resource{Addr: "agent.a", Config: provider.Object{"model": map[string]any{"id": "gpt-4o-mini"}}}, remote)
	if err != nil || len(diffs) != 0 {
		t.Errorf("in-sync diff = %v, %v; want empty, nil", diffs, err)
	}

	// Spec change: one attribute diff, remote as old, desired as new.
	diffs, err = p.Diff(&provider.Resource{Addr: "agent.a", Config: provider.Object{"model": map[string]any{"id": "gpt-4o"}}}, remote)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	want := []provider.AttrDiff{{Path: "model.id", Old: "gpt-4o-mini", New: "gpt-4o"}}
	if diff := cmp.Diff(want, diffs); diff != "" {
		t.Errorf("diff mismatch (-want +got):\n%s", diff)
	}
}

func TestFactory(t *testing.T) {
	p, err := Factory(&schema.Target{Name: "memory", Type: "platform"})
	if err != nil || p == nil {
		t.Errorf("Factory on a plain platform target: %v, %v; want a provider, nil", p, err)
	}

	// Negative: auth is meaningless on an in-memory platform and must be
	// rejected, not ignored.
	_, err = Factory(&schema.Target{Name: "memory", Type: "platform", Auth: &schema.Auth{APIKeyEnv: "NOPE"}})
	if err == nil || !strings.Contains(err.Error(), "auth") {
		t.Errorf("Factory with auth: err = %v, want error naming the auth block", err)
	}
}
