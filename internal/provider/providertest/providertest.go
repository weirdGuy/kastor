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
	"strings"

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

// VaultFake is a Fake that also implements provider.Checker, backed by an
// in-memory credential vault. It is a separate type on purpose: a Check method
// on Fake would make *every* Fake a Checker, leaving no way to exercise the
// doctor engine's other branch — a provider that verifies nothing.
type VaultFake struct {
	*Fake
	// Credentials is the fake vault: credential id → what it holds. An id
	// absent from the map is the vault answering "no such credential".
	Credentials map[string]Credential
	// VaultErr, when non-nil, is the vault failing to answer at all rather
	// than returning a verdict. It must produce "could not verify", never
	// "credential missing": the whole point of the third status is that
	// those two are different facts (SPEC.md §5.3).
	VaultErr error
}

// Credential is one entry in the fake vault, mirroring the fields readiness
// depends on. Absent from Fake.Credentials means the vault answers "no such
// credential" — the finding, as distinct from VaultErr's non-answer.
type Credential struct {
	DisplayName  string // empty models the platform's nullable display name
	Archived     bool
	MCPServerURL string
	Expired      bool
}

// New returns an empty Fake. Remote ids are assigned as fake-1, fake-2, …
// in creation order.
func New() *Fake {
	return &Fake{Objects: map[string]provider.Object{}}
}

// NewWithVault returns a VaultFake holding the given credentials. Use New for
// a provider that implements no readiness checks at all.
func NewWithVault(credentials map[string]Credential) *VaultFake {
	if credentials == nil {
		credentials = map[string]Credential{}
	}
	return &VaultFake{Fake: New(), Credentials: credentials}
}

var (
	_ provider.Provider = (*Fake)(nil)
	_ provider.Checker  = (*VaultFake)(nil)
)

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

// Check implements provider.Checker against the fake vault, mirroring the
// shipped Claude provider's verdicts closely enough to exercise the doctor
// engine and its renderer offline: an unreachable vault is StatusUnknown, and
// only a vault that answers "no such credential" is StatusFailed.
func (f *VaultFake) Check(_ context.Context, desired *provider.Resource, _ provider.Object) ([]provider.Check, error) {
	if err := f.call("check", desired.Addr); err != nil {
		return nil, err
	}

	servers, _ := desired.Config["mcp_servers"].([]any)
	var checks []provider.Check
	for _, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := server["name"].(string)
		url, _ := server["url"].(string)
		ref, _ := server["auth_ref"].(string)
		id, found := strings.CutPrefix(ref, "connection://")
		if !found {
			continue
		}
		check := provider.Check{Kind: "credential", Subject: id}

		if f.VaultErr != nil {
			check.Status = provider.StatusUnknown
			check.Summary = "could not verify the credential against the vault"
			check.Detail = fmt.Sprintf("mcp_server.%s references %s; reading the vault failed: %v", name, ref, f.VaultErr)
			checks = append(checks, check)
			continue
		}

		credential, exists := f.Credentials[id]
		check.SubjectName = credential.DisplayName
		switch {
		case !exists:
			check.SubjectName = ""
			check.Status = provider.StatusFailed
			check.Summary = "credential does not exist in the vault"
			check.Detail = fmt.Sprintf("mcp_server.%s references %s, and the vault holds no credential with that id", name, ref)
		case credential.Archived:
			check.Status = provider.StatusFailed
			check.Summary = "credential is archived"
			check.Detail = fmt.Sprintf("mcp_server.%s references %s, which has been archived on the platform", name, ref)
		case credential.Expired:
			check.Status = provider.StatusFailed
			check.Summary = "connection is not authenticated: the OAuth grant has expired"
			check.Detail = fmt.Sprintf("mcp_server.%s references %s; re-authorize the connection on the platform", name, ref)
		case credential.MCPServerURL != "" && url != "" && credential.MCPServerURL != url:
			check.Status = provider.StatusFailed
			check.Summary = "credential authenticates a different server"
			check.Detail = fmt.Sprintf("mcp_server.%s declares url %q, but the credential authenticates %q", name, url, credential.MCPServerURL)
		default:
			check.Status = provider.StatusOK
			check.Summary = "credential resolves and authenticates this server"
			check.Detail = fmt.Sprintf("mcp_server.%s → %s", name, credential.MCPServerURL)
		}
		checks = append(checks, check)
	}
	return checks, nil
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
