// Package protocol defines the stable wire contract between Kastor core and
// executable target plugins. The v1 package contains only serializable data
// and public interfaces; plugins never need to import Kastor's internal
// parser, module, build, provider, or state packages.
package protocol

import (
	"context"
	"fmt"
	"strings"
)

// Version is the executable protocol version implemented by this package.
const Version = 1

// Kind identifies the operation family implemented by a plugin.
type Kind string

const (
	KindCodegen  Kind = "codegen"
	KindPlatform Kind = "platform"
)

// Metadata is returned by the mandatory handshake before any work is sent to
// a plugin process.
type Metadata struct {
	Protocol     int          `json:"protocol"`
	Source       string       `json:"source"`
	Version      string       `json:"version"`
	Kinds        []Kind       `json:"kinds"`
	Capabilities Capabilities `json:"capabilities"`
}

// Supports reports whether the plugin advertises an operation family.
func (m Metadata) Supports(kind Kind) bool {
	for _, advertised := range m.Kinds {
		if advertised == kind {
			return true
		}
	}
	return false
}

// Capabilities describes target validation and optional RPC support.
type Capabilities struct {
	LocalProcesses    bool                       `json:"local_processes,omitempty"`
	CredentialSchemes []string                   `json:"credential_schemes,omitempty"`
	Config            map[string]ConfigAttribute `json:"config,omitempty"`
	Check             bool                       `json:"check,omitempty"`
	Scaffold          bool                       `json:"scaffold,omitempty"`
}

// ConfigAttribute is one plugin-owned target configuration field.
type ConfigAttribute struct {
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// Severity classifies a structured plugin diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is returned for configuration or capability findings that can
// be attributed to a block address.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Addr     string   `json:"addr,omitempty"`
	Summary  string   `json:"summary"`
	Detail   string   `json:"detail,omitempty"`
}

// Module is the canonical, fully resolved IR sent to plugins. Slice order is
// deterministic and TopologicalOrder is dependencies-first with lexical tie
// breaking. Dependencies contains sorted direct dependency addresses.
type Module struct {
	Agents           []*Agent            `json:"agents,omitempty"`
	Tools            []*Tool             `json:"tools,omitempty"`
	Prompts          []*Prompt           `json:"prompts,omitempty"`
	Models           []*Model            `json:"models,omitempty"`
	Targets          []*Target           `json:"targets,omitempty"`
	MCPServers       []*MCPServer        `json:"mcp_servers,omitempty"`
	TopologicalOrder []string            `json:"topological_order,omitempty"`
	Dependencies     map[string][]string `json:"dependencies,omitempty"`
}

type Model struct {
	Name     string         `json:"name"`
	Provider string         `json:"provider"`
	ID       string         `json:"id"`
	Params   map[string]any `json:"params,omitempty"`
}

func (m *Model) Addr() string { return "model." + m.Name }

type Target struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Plugin string         `json:"plugin,omitempty"`
	Output string         `json:"output,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

func (t *Target) Addr() string { return "target." + t.Name }

func (t *Target) ConfigString(name string) (string, bool) {
	if t == nil || t.Config == nil {
		return "", false
	}
	value, ok := t.Config[name].(string)
	return value, ok
}

type Agent struct {
	Name             string         `json:"name"`
	Description      string         `json:"description,omitempty"`
	Model            string         `json:"model"`
	SystemPrompt     string         `json:"system_prompt,omitempty"`
	Tools            []string       `json:"tools,omitempty"`
	RequiresApproval []string       `json:"requires_approval,omitempty"`
	Inputs           []*AgentInput  `json:"inputs,omitempty"`
	Outputs          []*AgentOutput `json:"outputs,omitempty"`
	DependsOn        []string       `json:"depends_on,omitempty"`
}

func (a *Agent) Addr() string { return "agent." + a.Name }

func (a *Agent) ApprovalRequired(ref string) bool {
	for _, gated := range a.RequiresApproval {
		if gated == ref {
			return true
		}
	}
	return false
}

type AgentInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
	Default     any    `json:"default,omitempty"`
	DefaultRef  string `json:"default_ref,omitempty"`
}

type AgentOutput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type Tool struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Params      []*ToolParam `json:"params,omitempty"`
	Returns     *ToolReturns `json:"returns"`
	Source      *ToolSource  `json:"source"`
}

func (t *Tool) Addr() string { return "tool." + t.Name }

type ToolParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
}

type ToolReturns struct {
	Type string `json:"type"`
}

type ToolSource struct {
	Kind string `json:"kind"`
	URI  string `json:"uri,omitempty"`
}

type Prompt struct {
	Name     string   `json:"name"`
	Requires []string `json:"requires,omitempty"`
	Body     string   `json:"body"`
	Vars     []string `json:"vars,omitempty"`
}

func (p *Prompt) Addr() string { return "prompt." + p.Name }

type MCPServer struct {
	Name      string     `json:"name"`
	Transport string     `json:"transport"`
	URL       string     `json:"url,omitempty"`
	Command   string     `json:"command,omitempty"`
	Args      []string   `json:"args,omitempty"`
	Auth      []*MCPAuth `json:"auth,omitempty"`
}

func (s *MCPServer) Addr() string { return "mcp_server." + s.Name }

type MCPAuth struct {
	Ref     string   `json:"ref"`
	Targets []string `json:"targets,omitempty"`
}

func (s *MCPServer) AuthFor(targetAddr string) (*MCPAuth, bool) {
	var fallback *MCPAuth
	for _, auth := range s.Auth {
		if len(auth.Targets) == 0 {
			fallback = auth
			continue
		}
		for _, target := range auth.Targets {
			if target == targetAddr {
				return auth, true
			}
		}
	}
	if fallback != nil {
		return fallback, true
	}
	return nil, false
}

const (
	SchemeEnv        = "env"
	SchemeConnection = "connection"
)

func ParseCredentialRef(ref string) (scheme, value string, err error) {
	scheme, value, found := strings.Cut(ref, "://")
	if !found || scheme == "" || value == "" {
		return "", "", fmt.Errorf("credential ref %q is not a complete URI", ref)
	}
	if scheme != SchemeEnv && scheme != SchemeConnection {
		return "", "", fmt.Errorf("credential ref %q uses unknown scheme %q", ref, scheme)
	}
	return scheme, value, nil
}

func ParseMCPURI(uri string) (server, tool string, err error) {
	const prefix = "mcp://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", fmt.Errorf("MCP source uri is %q, expected mcp://<server>/<tool>", uri)
	}
	server, tool, found := strings.Cut(strings.TrimPrefix(uri, prefix), "/")
	if !found || server == "" || tool == "" || strings.Contains(tool, "/") {
		return "", "", fmt.Errorf("MCP source uri is %q, expected mcp://<server>/<tool>", uri)
	}
	return server, tool, nil
}

// File is one generated output. Kastor core remains responsible for path
// validation, canonical ordering, preserve semantics, and disk writes.
type File struct {
	Path     string `json:"path"`
	Data     []byte `json:"data"`
	Preserve bool   `json:"preserve,omitempty"`
}

type ValidateRequest struct {
	Module *Module `json:"module"`
	Target *Target `json:"target"`
}

type ValidateResponse struct {
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type GenerateRequest struct {
	Module *Module `json:"module"`
	Target *Target `json:"target"`
}

type GenerateResponse struct {
	Files       []File       `json:"files,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// ScaffoldRequest asks a plugin for its deterministic starter module. Name is
// a user-facing project name when one was supplied by the host.
type ScaffoldRequest struct {
	Name              string `json:"name,omitempty"`
	LocalName         string `json:"local_name"`
	Source            string `json:"source"`
	VersionConstraint string `json:"version_constraint"`
}

type ScaffoldResponse struct {
	Files []File `json:"files,omitempty"`
}

// Object uses encoding/json's value model.
type Object = map[string]any

type Resource struct {
	Addr   string `json:"addr"`
	Config Object `json:"config"`
}

type AttrDiff struct {
	Path string `json:"path"`
	Old  any    `json:"old"`
	New  any    `json:"new"`
}

type Status string

const (
	StatusOK      Status = "ok"
	StatusFailed  Status = "failed"
	StatusUnknown Status = "unknown"
)

type Check struct {
	Kind        string `json:"kind"`
	Status      Status `json:"status"`
	Subject     string `json:"subject,omitempty"`
	SubjectName string `json:"subject_name,omitempty"`
	Summary     string `json:"summary"`
	Detail      string `json:"detail,omitempty"`
}

type ReadRequest struct {
	Target *Target `json:"target"`
	ID     string  `json:"id"`
}

type ReadResponse struct {
	Remote Object `json:"remote,omitempty"`
	Found  bool   `json:"found"`
}

type CreateRequest struct {
	Target  *Target   `json:"target"`
	Desired *Resource `json:"desired"`
}

type CreateResponse struct {
	ID string `json:"id"`
}

type UpdateRequest struct {
	Target  *Target   `json:"target"`
	ID      string    `json:"id"`
	Desired *Resource `json:"desired"`
}

type DeleteRequest struct {
	Target *Target `json:"target"`
	ID     string  `json:"id"`
}

type DiffRequest struct {
	Target  *Target   `json:"target"`
	Desired *Resource `json:"desired"`
	Remote  Object    `json:"remote,omitempty"`
}

type DiffResponse struct {
	Diffs []AttrDiff `json:"diffs,omitempty"`
}

type CheckRequest struct {
	Target  *Target   `json:"target"`
	Desired *Resource `json:"desired"`
	Remote  Object    `json:"remote,omitempty"`
}

type CheckResponse struct {
	Checks []Check `json:"checks,omitempty"`
}

// Handler is the mandatory plugin surface. Operation-specific interfaces are
// discovered dynamically, allowing codegen-only and platform-only binaries.
type Handler interface {
	Metadata(context.Context) (Metadata, error)
}

type Validator interface {
	Validate(context.Context, *ValidateRequest) (*ValidateResponse, error)
}

type Generator interface {
	Generate(context.Context, *GenerateRequest) (*GenerateResponse, error)
}

type Scaffolder interface {
	Scaffold(context.Context, *ScaffoldRequest) (*ScaffoldResponse, error)
}

type PlatformProvider interface {
	Read(context.Context, *ReadRequest) (*ReadResponse, error)
	Create(context.Context, *CreateRequest) (*CreateResponse, error)
	Update(context.Context, *UpdateRequest) error
	Delete(context.Context, *DeleteRequest) error
	Diff(context.Context, *DiffRequest) (*DiffResponse, error)
}

type Checker interface {
	Check(context.Context, *CheckRequest) (*CheckResponse, error)
}
