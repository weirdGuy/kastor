package plugin

import (
	"context"

	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

// Client is the protocol surface used by the core adapters. Keeping the
// interface here makes executable-process ownership explicit and lets command
// tests exercise dispatch without constructing a real protocol.Client.
type Client interface {
	Metadata() protocol.Metadata
	Validate(context.Context, *protocol.ValidateRequest) (*protocol.ValidateResponse, error)
	Generate(context.Context, *protocol.GenerateRequest) (*protocol.GenerateResponse, error)
	Scaffold(context.Context, *protocol.ScaffoldRequest) (*protocol.ScaffoldResponse, error)
	Read(context.Context, *protocol.ReadRequest) (*protocol.ReadResponse, error)
	Create(context.Context, *protocol.CreateRequest) (*protocol.CreateResponse, error)
	Update(context.Context, *protocol.UpdateRequest) error
	Delete(context.Context, *protocol.DeleteRequest) error
	Diff(context.Context, *protocol.DiffRequest) (*protocol.DiffResponse, error)
	Check(context.Context, *protocol.CheckRequest) (*protocol.CheckResponse, error)
	Close() error
}

var _ Client = (*protocol.Client)(nil)
