package plugin

import (
	"context"
	"fmt"

	"github.com/getkastordev/kastor/internal/provider"
	"github.com/getkastordev/kastor/internal/schema"
	protocol "github.com/getkastordev/kastor/protocol/v1"
)

// PlatformClient is the protocol subset needed for reconciliation.
type PlatformClient interface {
	Metadata() protocol.Metadata
	Read(context.Context, *protocol.ReadRequest) (*protocol.ReadResponse, error)
	Create(context.Context, *protocol.CreateRequest) (*protocol.CreateResponse, error)
	Update(context.Context, *protocol.UpdateRequest) error
	Delete(context.Context, *protocol.DeleteRequest) error
	Diff(context.Context, *protocol.DiffRequest) (*protocol.DiffResponse, error)
	Check(context.Context, *protocol.CheckRequest) (*protocol.CheckResponse, error)
}

// Platform adapts protocol-v1 lifecycle RPCs to the core provider contract.
type Platform struct {
	Client PlatformClient
	Target *schema.Target
}

var (
	_ provider.Provider = (*Platform)(nil)
	_ provider.Checker  = (*Platform)(nil)
)

func (p *Platform) Read(ctx context.Context, id string) (provider.Object, bool, error) {
	response, err := p.client().Read(ctx, &protocol.ReadRequest{Target: TargetIR(p.Target), ID: id})
	if err != nil {
		return nil, false, err
	}
	return response.Remote, response.Found, nil
}

func (p *Platform) Create(ctx context.Context, desired *provider.Resource) (string, error) {
	response, err := p.client().Create(ctx, &protocol.CreateRequest{
		Target: TargetIR(p.Target), Desired: resourceIR(desired),
	})
	if err != nil {
		return "", err
	}
	return response.ID, nil
}

func (p *Platform) Update(ctx context.Context, id string, desired *provider.Resource) error {
	return p.client().Update(ctx, &protocol.UpdateRequest{
		Target: TargetIR(p.Target), ID: id, Desired: resourceIR(desired),
	})
}

func (p *Platform) Delete(ctx context.Context, id string) error {
	return p.client().Delete(ctx, &protocol.DeleteRequest{Target: TargetIR(p.Target), ID: id})
}

func (p *Platform) Diff(desired *provider.Resource, remote provider.Object) ([]provider.AttrDiff, error) {
	response, err := p.client().Diff(context.Background(), &protocol.DiffRequest{
		Target: TargetIR(p.Target), Desired: resourceIR(desired), Remote: remote,
	})
	if err != nil {
		return nil, err
	}
	diffs := make([]provider.AttrDiff, len(response.Diffs))
	for i, diff := range response.Diffs {
		diffs[i] = provider.AttrDiff{Path: diff.Path, Old: diff.Old, New: diff.New}
	}
	return diffs, nil
}

func (p *Platform) Check(ctx context.Context, desired *provider.Resource, remote provider.Object) ([]provider.Check, error) {
	if !p.client().Metadata().Capabilities.Check {
		return nil, fmt.Errorf("plugin %q does not advertise readiness checks", p.client().Metadata().Source)
	}
	response, err := p.client().Check(ctx, &protocol.CheckRequest{
		Target: TargetIR(p.Target), Desired: resourceIR(desired), Remote: remote,
	})
	if err != nil {
		return nil, err
	}
	checks := make([]provider.Check, len(response.Checks))
	for i, check := range response.Checks {
		checks[i] = provider.Check{
			Kind: check.Kind, Status: provider.Status(check.Status), Subject: check.Subject,
			SubjectName: check.SubjectName, Summary: check.Summary, Detail: check.Detail,
		}
	}
	return checks, nil
}

func (p *Platform) client() PlatformClient {
	if p == nil || p.Client == nil {
		panic("external platform plugin is not started")
	}
	return p.Client
}

func resourceIR(resource *provider.Resource) *protocol.Resource {
	if resource == nil {
		return nil
	}
	return &protocol.Resource{Addr: resource.Addr, Config: resource.Config}
}
