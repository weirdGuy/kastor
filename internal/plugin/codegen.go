package plugin

import (
	"context"
	"fmt"

	"github.com/getkastordev/kastor/internal/build"
	protocol "github.com/getkastordev/kastor/protocol/v1"
)

// CodegenClient is the protocol subset needed for generation.
type CodegenClient interface {
	Generate(context.Context, *protocol.GenerateRequest) (*protocol.GenerateResponse, error)
}

// Codegen adapts a protocol client to the core build.Generator contract.
type Codegen struct {
	Client  CodegenClient
	Context context.Context
}

var _ build.Generator = (*Codegen)(nil)

func (g *Codegen) Generate(job *build.Job) ([]build.File, error) {
	if g == nil || g.Client == nil {
		return nil, fmt.Errorf("external codegen plugin is not started")
	}
	ctx := g.Context
	if ctx == nil {
		ctx = context.Background()
	}
	response, err := g.Client.Generate(ctx, &protocol.GenerateRequest{
		Module: ModuleIR(job.Module, job.Graph),
		Target: TargetIR(job.Target),
	})
	if err != nil {
		return nil, err
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Severity == protocol.SeverityError {
			return nil, fmt.Errorf("%s: %s", diagnostic.Addr, diagnostic.Summary)
		}
	}
	files := make([]build.File, len(response.Files))
	for i, file := range response.Files {
		files[i] = build.File{Path: file.Path, Data: file.Data, Preserve: file.Preserve}
	}
	return files, nil
}
