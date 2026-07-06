// Package provider is the target-agnostic plan/apply engine (SPEC.md §6):
// it renders a loaded module into desired resource configurations, compares
// them three ways (spec vs. state vs. remote) into a plan, and executes
// plans against a platform through the Provider contract. Per-platform
// reconcilers live in subpackages (provider/openai, ...) and implement
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
//   - Read reports found=false for a resource deleted outside adl; that is
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
//   - Diff must be pure and deterministic; Read must not mutate anything.
//     adl plan issues only Read and Diff calls.
type Provider interface {
	Read(ctx context.Context, id string) (remote Object, found bool, err error)
	Create(ctx context.Context, desired *Resource) (id string, err error)
	Update(ctx context.Context, id string, desired *Resource) error
	Delete(ctx context.Context, id string) error
	Diff(desired *Resource, remote Object) ([]AttrDiff, error)
}
