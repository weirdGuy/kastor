package plugin

import (
	"context"
	"fmt"

	"github.com/weirdGuy/kastor/internal/build"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

// Codegen adapts a protocol client to the core build.Generator contract.
type Codegen struct {
	Client *protocol.Client
}

var _ build.Generator = (*Codegen)(nil)

func (g *Codegen) Generate(job *build.Job) ([]build.File, error) {
	if g == nil || g.Client == nil {
		return nil, fmt.Errorf("external codegen plugin is not started")
	}
	response, err := g.Client.Generate(context.Background(), &protocol.GenerateRequest{
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
