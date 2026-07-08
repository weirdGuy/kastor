// Package providertest provides an in-memory Provider for testing the
// plan/apply engine and CLI without a real platform (mirroring buildtest
// for the codegen engine). Its remote objects are stored verbatim as the
// desired configs that created them, and its Diff delegates to the shipped
// memory provider's structural comparison, so the fake and the real
// in-memory platform can never disagree.
package providertest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/provider/memory"
)

// Fake is an in-memory provider.Provider. Zero-configuration tests just
// call New; failure injection and drift simulation work by mutating FailOn
// and Objects directly.
type Fake struct {
	// Objects maps remote id → stored object (a deep copy of the config
	// that created or last updated it). Tests mutate entries to simulate
	// drift and delete entries to simulate out-of-band deletion.
	Objects map[string]provider.Object
	// Calls records every call in order, formatted like "create agent.a",
	// "read fake-1", "diff agent.a" — create/update/diff key on the block
	// address, read/delete on the remote id.
	Calls []string
	// FailOn maps a Calls entry to the error that call should return.
	FailOn map[string]error

	nextID int
}

// New returns an empty Fake. Remote ids are assigned as fake-1, fake-2, …
// in creation order.
func New() *Fake {
	return &Fake{Objects: map[string]provider.Object{}}
}

// call logs one provider call and returns its injected failure, if any.
func (f *Fake) call(op, key string) error {
	entry := op + " " + key
	f.Calls = append(f.Calls, entry)
	return f.FailOn[entry]
}

// Read implements provider.Provider.
func (f *Fake) Read(_ context.Context, id string) (provider.Object, bool, error) {
	if err := f.call("read", id); err != nil {
		return nil, false, err
	}
	obj, ok := f.Objects[id]
	if !ok {
		return nil, false, nil
	}
	return deepCopy(obj), true, nil
}

// Create implements provider.Provider.
func (f *Fake) Create(_ context.Context, desired *provider.Resource) (string, error) {
	if err := f.call("create", desired.Addr); err != nil {
		return "", err
	}
	f.nextID++
	id := fmt.Sprintf("fake-%d", f.nextID)
	f.Objects[id] = deepCopy(desired.Config)
	return id, nil
}

// Update implements provider.Provider.
func (f *Fake) Update(_ context.Context, id string, desired *provider.Resource) error {
	if err := f.call("update", desired.Addr); err != nil {
		return err
	}
	if _, ok := f.Objects[id]; !ok {
		return fmt.Errorf("no remote object %s", id)
	}
	f.Objects[id] = deepCopy(desired.Config)
	return nil
}

// Delete implements provider.Provider. Deleting a missing id succeeds, as
// the contract requires.
func (f *Fake) Delete(_ context.Context, id string) error {
	if err := f.call("delete", id); err != nil {
		return err
	}
	delete(f.Objects, id)
	return nil
}

// Diff implements provider.Provider by delegating to the memory provider's
// structural comparison (maps by sorted key union, same-length arrays
// element-wise, anything else as a leaf; Old is the remote value, New the
// desired one).
func (f *Fake) Diff(desired *provider.Resource, remote provider.Object) ([]provider.AttrDiff, error) {
	if err := f.call("diff", desired.Addr); err != nil {
		return nil, err
	}
	return memory.DiffObjects(desired.Config, remote), nil
}

// deepCopy clones a JSON value tree so the fake's store never shares
// structure with callers.
func deepCopy(obj provider.Object) provider.Object {
	data, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Sprintf("providertest: object is not a JSON value tree: %v", err))
	}
	var out provider.Object
	if err := json.Unmarshal(data, &out); err != nil {
		panic(fmt.Sprintf("providertest: %v", err))
	}
	return out
}
