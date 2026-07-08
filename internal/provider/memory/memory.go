// Package memory is the built-in in-memory platform provider: a real,
// registered plan/apply target whose "platform" is a map in process memory.
// It exists so the reconcile path can be demonstrated and exercised —
// examples, onboarding, CI — without credentials or a network.
//
// The platform is ephemeral: remote objects vanish when the process exits.
// A plan run after an apply from an earlier invocation therefore reports
// every tracked resource as deleted outside kastor (drift) and recreates it
// on the next apply — the truthful answer for a platform that forgot
// everything, not a bug.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/schema"
)

// Provider implements provider.Provider over a map in process memory. Its
// remote objects are stored verbatim as the desired configs that created
// them, so Diff is a generic structural comparison over the JSON value
// model (DiffObjects).
type Provider struct {
	// Objects maps remote id → stored object (a deep copy of the config
	// that created or last updated it).
	Objects map[string]provider.Object

	nextID int
}

// New returns an empty in-memory platform. Remote ids are assigned as
// mem-1, mem-2, … in creation order.
func New() *Provider {
	return &Provider{Objects: map[string]provider.Object{}}
}

// Factory adapts New to the CLI's provider registry, validating the target
// block first: auth is meaningless on an in-memory platform, and meaningless
// fields are errors, not ignored (SPEC.md §3.5).
func Factory(tgt *schema.Target) (provider.Provider, error) {
	if tgt.Auth != nil {
		return nil, fmt.Errorf("auth block found, but the in-memory platform takes no credentials — remove auth from the target block")
	}
	return New(), nil
}

// Read implements provider.Provider.
func (p *Provider) Read(_ context.Context, id string) (provider.Object, bool, error) {
	obj, ok := p.Objects[id]
	if !ok {
		return nil, false, nil
	}
	return deepCopy(obj), true, nil
}

// Create implements provider.Provider.
func (p *Provider) Create(_ context.Context, desired *provider.Resource) (string, error) {
	p.nextID++
	id := fmt.Sprintf("mem-%d", p.nextID)
	p.Objects[id] = deepCopy(desired.Config)
	return id, nil
}

// Update implements provider.Provider.
func (p *Provider) Update(_ context.Context, id string, desired *provider.Resource) error {
	if _, ok := p.Objects[id]; !ok {
		return fmt.Errorf("no remote object %s", id)
	}
	p.Objects[id] = deepCopy(desired.Config)
	return nil
}

// Delete implements provider.Provider. Deleting a missing id succeeds, as
// the contract requires.
func (p *Provider) Delete(_ context.Context, id string) error {
	delete(p.Objects, id)
	return nil
}

// Diff implements provider.Provider via DiffObjects: the stored objects are
// exactly the neutral configs that created them, so the generic structural
// comparison is this platform's authoritative diff.
func (p *Provider) Diff(desired *provider.Resource, remote provider.Object) ([]provider.AttrDiff, error) {
	return DiffObjects(desired.Config, remote), nil
}

// DiffObjects structurally compares two JSON value trees: maps diff by
// sorted key union, same-length arrays element-wise, anything else as a
// leaf. Old is the remote value, New the desired one. Output order is
// deterministic (sorted key order). Exported so providertest's fake diffs
// with the same algorithm and the two can never disagree.
func DiffObjects(desired, remote provider.Object) []provider.AttrDiff {
	var diffs []provider.AttrDiff
	diffValue("", desired, remote, &diffs)
	return diffs
}

// diffValue appends the differences between desired and remote at path.
func diffValue(path string, desired, remote any, out *[]provider.AttrDiff) {
	switch d := desired.(type) {
	case map[string]any:
		r, ok := remote.(map[string]any)
		if !ok {
			appendLeaf(path, desired, remote, out)
			return
		}
		keys := map[string]bool{}
		for k := range d {
			keys[k] = true
		}
		for k := range r {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			dv, inD := d[k]
			rv, inR := r[k]
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			switch {
			case !inR:
				*out = append(*out, provider.AttrDiff{Path: sub, Old: nil, New: dv})
			case !inD:
				*out = append(*out, provider.AttrDiff{Path: sub, Old: rv, New: nil})
			default:
				diffValue(sub, dv, rv, out)
			}
		}
	case []any:
		r, ok := remote.([]any)
		if !ok || len(r) != len(d) {
			appendLeaf(path, desired, remote, out)
			return
		}
		for i := range d {
			diffValue(fmt.Sprintf("%s[%d]", path, i), d[i], r[i], out)
		}
	default:
		appendLeaf(path, desired, remote, out)
	}
}

// appendLeaf records a leaf-level difference, if there is one.
func appendLeaf(path string, desired, remote any, out *[]provider.AttrDiff) {
	if !reflect.DeepEqual(desired, remote) {
		*out = append(*out, provider.AttrDiff{Path: path, Old: remote, New: desired})
	}
}

// deepCopy clones a JSON value tree so the store never shares structure
// with callers.
func deepCopy(obj provider.Object) provider.Object {
	data, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Sprintf("memory: object is not a JSON value tree: %v", err))
	}
	var out provider.Object
	if err := json.Unmarshal(data, &out); err != nil {
		panic(fmt.Sprintf("memory: %v", err))
	}
	return out
}
