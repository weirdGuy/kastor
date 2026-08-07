// Package provider is the target-agnostic plan/apply engine (SPEC.md §6):
// it renders a loaded module into desired resource configurations, compares
// them three ways (spec vs. state vs. remote) into a plan, and executes
// plans against a platform through the Provider contract. Per-platform
// reconcilers live in subpackages (provider/memory, ...) and implement
// Provider; nothing platform-specific appears in this package.
//
// The contract deliberately traffics only in serializable, provider-neutral
// values (block addresses, JSON value trees) so it can move behind a plugin
// boundary later without redesign.
package provider

import "context"

// Object is a JSON value tree using the encoding/json value model: every
// value is a string, float64, bool, nil, []any, or map[string]any. Desired
// configs, last-applied configs, and remote reads all use this model, so
// comparisons never trip over Go-side type differences.
type Object = map[string]any

// Resource is one managed resource: a block address plus its desired
// configuration. In v0 every agent block is one resource — models, prompts,
// and tools are folded into the agent's config (see DesiredConfig).
type Resource struct {
	Addr   string `json:"addr"`
	Config Object `json:"config"`
}

// AttrDiff is one attribute-level difference between a desired config and
// a remote object, at a dotted path like "model.id" or "tools[0].source.uri".
type AttrDiff struct {
	Path string `json:"path"`
	Old  any    `json:"old"` // nil when the attribute is being added
	New  any    `json:"new"` // nil when the attribute is being removed
}

// Provider is the contract every platform reconciler implements
// (SPEC.md §6). The engine holds providers to these rules:
//
//   - Read reports found=false for a resource deleted outside kastor; that is
//     data (drift), not an error.
//   - Create returns the platform's identifier for the new resource; the
//     engine records it in state immediately.
//   - Delete is idempotent: deleting an id that no longer exists remotely
//     succeeds, so a re-run after a partial failure converges.
//   - Diff is the comparison authority — only the provider knows how a
//     desired config maps onto its platform's attributes. An empty result
//     means "in sync". The engine calls it with the spec's desired config
//     (update-or-noop decision) and with the last-applied config from state
//     (drift detection).
//   - Diff must accept a nil remote, which means the object does not exist
//     on the platform. It must then validate the desired config exactly as
//     it would against an existing object — returning an error is how a
//     provider rejects a spec it cannot map onto its platform — and on
//     success return one AttrDiff per attribute a Create would set (Old nil).
//     The engine calls Diff this way for every resource it plans to create,
//     so a module that cannot apply fails at plan rather than at apply.
//   - Diff must be pure and deterministic; Read must not mutate anything.
//     kastor plan issues only Read and Diff calls.
type Provider interface {
	Read(ctx context.Context, id string) (remote Object, found bool, err error)
	Create(ctx context.Context, desired *Resource) (id string, err error)
	Update(ctx context.Context, id string, desired *Resource) error
	Delete(ctx context.Context, id string) error
	Diff(desired *Resource, remote Object) ([]AttrDiff, error)
}

// Status is the outcome of one readiness check. There are deliberately three
// of them, not two: StatusUnknown says the check could not be performed, which
// is a different fact from the check failing, and a user acts differently on
// each — an unreachable vault is an outage to wait out or a network path to
// open, an absent credential is a spec or platform change. Collapsing the two
// would make kastor doctor misleading in exactly the situation where it most
// needs to be trusted (SPEC.md §5.3).
type Status string

const (
	StatusOK      Status = "ok"      // verified, and it is right
	StatusFailed  Status = "failed"  // verified, and it is wrong
	StatusUnknown Status = "unknown" // could not verify
)

// Check is one readiness finding. Like AttrDiff it is neutral and
// serializable — no provider types — so the contract survives a move behind a
// plugin boundary, and the JSON tags are the future --json rendering (§9).
type Check struct {
	Kind   string `json:"kind"` // what was checked: "credential", "tool_permission", …
	Status Status `json:"status"`
	// Subject identifies the thing checked in the platform's own terms — a
	// credential id, a server name, a tool name. Subjects are opaque by
	// design (a credential's display name is nullable and non-unique, so it
	// cannot be the identifier), which is why SubjectName exists.
	Subject string `json:"subject,omitempty"`
	// SubjectName is a human-readable name for Subject when the platform has
	// one. Renderers print `Subject ("SubjectName")` so opaque ids stay
	// decodable from output without ever becoming the identifier.
	SubjectName string `json:"subject_name,omitempty"`
	Summary     string `json:"summary"`
	Detail      string `json:"detail,omitempty"`
}

// Checker is an optional Provider capability: readiness verification for
// kastor doctor (SPEC.md §5.3). A provider that can verify nothing simply does
// not implement it, and doctor reports remote existence alone for its target.
//
// Check is explicitly permitted the extra reads Diff is not — a credential
// vault is a different API surface entirely, and reaching it is the point.
// The asymmetry is not an exception to the contract above but a consequence of
// it: Diff's whole output vocabulary is drift, so a check that can never be
// drift does not belong there, whereas doctor is its own verb that never feeds
// a plan and never writes state, and so has no purity contract to protect.
//
// Check reports findings rather than returning an error for a failed check:
// an error has one channel and would collapse StatusFailed into
// StatusUnknown. An error return means the check could not be attempted at
// all — a malformed desired config, not a platform verdict.
type Checker interface {
	Check(ctx context.Context, desired *Resource, remote Object) ([]Check, error)
}
