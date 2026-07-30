// Package claude maps Kastor resources onto the Claude Managed Agents
// comparison model. Network-backed lifecycle operations intentionally remain
// unimplemented while the beta API's normalization and diff semantics settle.
package claude

import (
	"context"
	"errors"

	"github.com/weirdGuy/kastor/internal/provider"
)

// ErrLifecycleNotImplemented is returned by the network-backed operations
// that are outside this package's current normalization-and-Diff scope.
var ErrLifecycleNotImplemented = errors.New("claude managed agents lifecycle operations are not implemented")

// Provider implements provider.Provider for Claude Managed Agents.
type Provider struct{}

var _ provider.Provider = (*Provider)(nil)

// New returns a Claude Managed Agents provider.
func New() *Provider {
	return &Provider{}
}

// Read implements provider.Provider.
func (*Provider) Read(context.Context, string) (provider.Object, bool, error) {
	return nil, false, ErrLifecycleNotImplemented
}

// Create implements provider.Provider.
func (*Provider) Create(context.Context, *provider.Resource) (string, error) {
	return "", ErrLifecycleNotImplemented
}

// Update implements provider.Provider.
func (*Provider) Update(context.Context, string, *provider.Resource) error {
	return ErrLifecycleNotImplemented
}

// Delete implements provider.Provider.
func (*Provider) Delete(context.Context, string) error {
	return ErrLifecycleNotImplemented
}

// Diff implements provider.Provider. Both inputs are normalized into the
// Managed Agents comparison shape before the pure structural comparison.
func (*Provider) Diff(desired *provider.Resource, remote provider.Object) ([]provider.AttrDiff, error) {
	spec, echo, err := normalizeForDiff(desired, remote)
	if err != nil {
		return nil, err
	}
	return diffObjects(spec, echo), nil
}
