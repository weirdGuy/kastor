// Package module loads every Kastor file in a directory tree into one module
// (SPEC.md §2), builds the module-wide symbol table, and resolves all
// captured references against it. Reference shapes and kinds are validated
// at parse time; this pass only checks that every target exists.
package module

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/weirdGuy/kastor/internal/parser"
	"github.com/weirdGuy/kastor/internal/schema"
)

// Module is a fully loaded and reference-resolved directory tree of Kastor
// files. Block slices follow lexical file order, source order within a file,
// so downstream output stays deterministic.
type Module struct {
	Root       string
	Agents     []*schema.Agent
	Tools      []*schema.Tool
	Prompts    []*schema.Prompt
	Models     []*schema.Model
	Targets    []*schema.Target
	MCPServers []*schema.MCPServer

	symbols map[string]*Symbol
}

// Symbol is one addressable block in the module's symbol table.
type Symbol struct {
	Addr  string // block address, e.g. "agent.weather"
	Kind  string // agent | tool | prompt | model | target | mcp_server
	File  string // declaring file, relative to the module root
	Block any    // *schema.Agent, *schema.Tool, *schema.Prompt, *schema.Model, *schema.Target, or *schema.MCPServer
}

// Lookup resolves a block address against the module's symbol table.
func (m *Module) Lookup(addr string) (*Symbol, bool) {
	sym, ok := m.symbols[addr]
	return sym, ok
}

// Load parses every Kastor file in the module rooted at root (.agent, .tool,
// .prompt, .kastor, kastor.hcl — discovered via Files) and resolves all
// references. All errors — parse failures, cross-file duplicate addresses,
// unknown references — are collected and returned joined, so one run
// reports everything.
func Load(root string) (*Module, error) {
	files, err := Files(root)
	if err != nil {
		return nil, fmt.Errorf("loading module: %w", err)
	}

	mod := &Module{Root: root, symbols: map[string]*Symbol{}}

	var errs []error
	for _, rel := range files {
		errs = append(errs, mod.loadFile(filepath.Join(root, rel), rel)...)
	}

	for _, a := range mod.Agents {
		errs = append(errs, mod.resolveAgent(a)...)
	}
	for _, t := range mod.Tools {
		errs = append(errs, mod.resolveToolSource(t)...)
	}
	for _, s := range mod.MCPServers {
		errs = append(errs, mod.resolveMCPServer(s)...)
	}
	errs = append(errs, mod.checkAuthCoverage()...)
	errs = append(errs, mod.checkCredentialTargets()...)

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return mod, nil
}

// Files walks the directory tree rooted at root and returns the relative
// paths of every file that belongs to the module, in lexical walk order.
// Hidden (dot-prefixed) files and directories are skipped, as are the
// output directories of codegen targets declared in the module's project
// files — generated code is never module input. Non-Kastor files are
// included; callers filter by extension.
func Files(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	skip, err := outputDirs(root)
	if err != nil {
		return nil, fmt.Errorf("walking module directory: %w", err)
	}

	var files []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		hidden := strings.HasPrefix(d.Name(), ".") && path != root
		if d.IsDir() {
			if hidden || skip[filepath.Clean(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if hidden {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking module directory: %w", walkErr)
	}
	return files, nil
}

// outputDirs pre-scans the tree for project files and collects their
// codegen target output directories, resolved relative to the declaring
// file. A project file that fails to parse contributes nothing here — the
// parse error surfaces when the file itself is loaded or formatted.
func outputDirs(root string) (map[string]bool, error) {
	dirs := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		hidden := strings.HasPrefix(d.Name(), ".") && path != root
		if d.IsDir() {
			if hidden {
				return filepath.SkipDir
			}
			return nil
		}
		if hidden || !isProjectFile(path) {
			return nil
		}

		project, err := parser.ParseProjectFile(path)
		if err != nil {
			return nil
		}
		for _, t := range project.Targets {
			if t.Output == "" {
				continue
			}
			out := t.Output
			if !filepath.IsAbs(out) {
				out = filepath.Join(filepath.Dir(path), out)
			}
			dirs[filepath.Clean(out)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dirs, nil
}

// isProjectFile reports whether path is a project file (kastor.hcl or *.kastor).
func isProjectFile(path string) bool {
	return filepath.Ext(path) == ".kastor" || filepath.Base(path) == "kastor.hcl"
}

// loadFile parses one Kastor file (dispatched on its name) and registers its
// blocks. Files with other extensions are ignored. A duplicate address skips
// that block but keeps registering the rest of the file.
func (m *Module) loadFile(path, rel string) []error {
	var errs []error
	define := func(kind, addr string, block any) bool {
		if prev, taken := m.symbols[addr]; taken {
			errs = append(errs, fmt.Errorf("%s: declared in both %s and %s", addr, prev.File, rel))
			return false
		}
		m.symbols[addr] = &Symbol{Addr: addr, Kind: kind, File: rel, Block: block}
		return true
	}

	switch filepath.Ext(path) {
	case ".agent":
		agents, err := parser.ParseAgentFile(path)
		if err != nil {
			return []error{fileErr(rel, err)}
		}
		for _, a := range agents {
			if define("agent", a.Addr(), a) {
				m.Agents = append(m.Agents, a)
			}
		}
	case ".tool":
		tools, err := parser.ParseToolFile(path)
		if err != nil {
			return []error{fileErr(rel, err)}
		}
		for _, t := range tools {
			if define("tool", t.Addr(), t) {
				m.Tools = append(m.Tools, t)
			}
		}
	case ".prompt":
		prompt, err := parser.ParsePromptFile(path)
		if err != nil {
			return []error{fileErr(rel, err)}
		}
		if define("prompt", prompt.Addr(), prompt) {
			m.Prompts = append(m.Prompts, prompt)
		}
	case ".kastor", ".hcl":
		if !isProjectFile(path) {
			return nil
		}
		project, err := parser.ParseProjectFile(path)
		if err != nil {
			return []error{fileErr(rel, err)}
		}
		for _, mdl := range project.Models {
			if define("model", mdl.Addr(), mdl) {
				m.Models = append(m.Models, mdl)
			}
		}
		for _, tgt := range project.Targets {
			if define("target", tgt.Addr(), tgt) {
				m.Targets = append(m.Targets, tgt)
			}
		}
		for _, srv := range project.MCPServers {
			if define("mcp_server", srv.Addr(), srv) {
				m.MCPServers = append(m.MCPServers, srv)
			}
		}
	}
	return errs
}

// resolveAgent checks every reference captured on an agent against the
// symbol table. Reference kinds are guaranteed by the parser (model.* for
// model, etc.), so existence is the only question left. Once the system
// prompt resolves, its variables are validated against the agent's IO
// contract.
func (m *Module) resolveAgent(a *schema.Agent) []error {
	file := m.symbols[a.Addr()].File

	var errs []error
	check := func(ref string) {
		if _, ok := m.symbols[ref]; !ok {
			errs = append(errs, fmt.Errorf("%s: %s: unknown reference %s", file, a.Addr(), ref))
		}
	}

	check(a.Model)
	if a.SystemPrompt != "" {
		check(a.SystemPrompt)
	}
	for _, ref := range a.Tools {
		check(ref)
	}
	for _, ref := range a.DependsOn {
		check(ref)
	}

	// SPEC.md §3.2: every variable the system prompt requires must be
	// satisfiable from the agent's inputs/outputs. Skipped when the prompt
	// reference is unknown — that already produced its own error above.
	if sym, ok := m.symbols[a.SystemPrompt]; ok {
		for _, err := range schema.ValidatePromptVars(a, sym.Block.(*schema.Prompt)) {
			errs = append(errs, fmt.Errorf("%s: %w", file, err))
		}
	}

	for _, in := range a.Inputs {
		if in.DefaultRef == "" {
			continue
		}
		// Parser guarantees the shape agent.<name>.output.<name>.
		parts := strings.SplitN(in.DefaultRef, ".", 4)
		agentAddr, outName := parts[0]+"."+parts[1], parts[3]

		sym, ok := m.symbols[agentAddr]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: %s: input %q: unknown reference %s", file, a.Addr(), in.Name, in.DefaultRef))
			continue
		}
		target := sym.Block.(*schema.Agent)
		if !hasOutput(target, outName) {
			errs = append(errs, fmt.Errorf("%s: %s: input %q: %s has no output %q", file, a.Addr(), in.Name, agentAddr, outName))
		}
	}
	return errs
}

// resolveToolSource checks the server segment of an mcp:// source uri against
// the module's mcp_server blocks (SPEC.md §3.6). Like target.<name> the
// reference is resolved and validated but creates no graph edge — servers are
// not nodes (§4). The <tool> segment is the server's own name for the tool and
// is not checkable without contacting the server, so it is left to run time.
func (m *Module) resolveToolSource(t *schema.Tool) []error {
	if t.Source == nil || t.Source.Kind != "mcp" {
		return nil
	}
	file := m.symbols[t.Addr()].File

	server, _, err := schema.ParseMCPURI(t.Source.URI)
	if err != nil {
		return []error{fmt.Errorf("%s: %s: %w", file, t.Addr(), err)}
	}
	addr := "mcp_server." + server
	if sym, ok := m.symbols[addr]; ok && sym.Kind == "mcp_server" {
		return nil
	}
	return []error{fmt.Errorf("%s: %s: source uri %q names undeclared MCP server %q; declare mcp_server %q in the project file (declared servers: %s)",
		file, t.Addr(), t.Source.URI, server, server, joinOrNone(m.mcpServerNames()))}
}

// resolveMCPServer checks every target.<name> reference on a server's auth
// blocks against the symbol table.
func (m *Module) resolveMCPServer(s *schema.MCPServer) []error {
	file := m.symbols[s.Addr()].File

	var errs []error
	for i, auth := range s.Auth {
		for _, ref := range auth.Targets {
			sym, ok := m.symbols[ref]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: %s: auth block %d: unknown reference %s (declared targets: %s)",
					file, s.Addr(), i+1, ref, joinOrNone(m.targetNames())))
				continue
			}
			if sym.Kind != "target" {
				errs = append(errs, fmt.Errorf("%s: %s: auth block %d: %s is a %s block, not a target",
					file, s.Addr(), i+1, ref, sym.Kind))
			}
		}
	}
	return errs
}

// checkAuthCoverage enforces the rule that a server which declares any auth
// block must have a binding on every target it is bound on (SPEC.md §3.6):
// otherwise a server picks up authentication on one path and silently loses it
// on another. A server with no auth block at all is unauthenticated
// everywhere, which is the normal case for a public or local server.
//
// v0 has no per-target tool source blocks, so a tool binds on every declared
// target; a server referenced by any tool is therefore bound on all of them.
func (m *Module) checkAuthCoverage() []error {
	referenced := map[string][]string{} // server name → tool addresses, in module order
	for _, t := range m.Tools {
		if t.Source == nil || t.Source.Kind != "mcp" {
			continue
		}
		server, _, err := schema.ParseMCPURI(t.Source.URI)
		if err != nil {
			continue // already reported by resolveToolSource
		}
		referenced[server] = append(referenced[server], t.Addr())
	}

	var errs []error
	for _, s := range m.MCPServers {
		tools := referenced[s.Name]
		if len(s.Auth) == 0 || len(tools) == 0 {
			continue
		}
		file := m.symbols[s.Addr()].File
		for _, tgt := range m.Targets {
			if _, ok := s.AuthFor(tgt.Addr()); ok {
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %s: declares auth but has no binding for %s; add an auth block naming it, or remove the others (tools bound to this server: %s)",
				file, s.Addr(), tgt.Addr(), strings.Join(tools, ", ")))
		}
	}
	return errs
}

// claudeTargetName is the target label that selects the Claude Managed Agents
// provider (SPEC.md §3.5). eveTargetName is the codegen target whose MCP
// binding is an HTTP client, which is what puts it in the transport matrix.
const (
	claudeTargetName = "claude_agents"
	eveTargetName    = "eve"
)

// checkCredentialTargets enforces the (scheme, target) matrix of SPEC.md §3.6
// and the transport matrix above it, plus §3.5's rule that a target whose
// servers use connection:// must declare the vault those credentials live in.
//
// The rejected (scheme, target) pairs are exactly the ones that would force
// kastor to read a secret and transmit it: env:// on a platform target means
// kastor resolving the variable and sending its value, and connection:// off
// the platform means there is no platform store to match the id against.
func (m *Module) checkCredentialTargets() []error {
	referenced := map[string]bool{}
	for _, t := range m.Tools {
		if t.Source == nil || t.Source.Kind != "mcp" {
			continue
		}
		if server, _, err := schema.ParseMCPURI(t.Source.URI); err == nil {
			referenced[server] = true
		}
	}

	var errs []error
	for _, s := range m.MCPServers {
		if !referenced[s.Name] {
			continue // an unreferenced server is bound nowhere
		}
		file := m.symbols[s.Addr()].File

		for _, tgt := range m.Targets {
			// The transport matrix of §3.6: stdio is spawned by a generated
			// langgraph project, but a platform dials a URL and eve's MCP
			// binding is an HTTP client, so neither can reach a local process.
			switch {
			case s.Transport != "stdio":
			case tgt.Type == "platform":
				errs = append(errs, fmt.Errorf("%s: %s: transport \"stdio\" cannot be bound on %s; a platform dials a URL and cannot spawn a local process",
					file, s.Addr(), tgt.Addr()))
			case tgt.Name == eveTargetName:
				errs = append(errs, fmt.Errorf("%s: %s: transport \"stdio\" cannot be bound on %s; the generated connection is an HTTP client — put an HTTP bridge in front of the server and declare its url",
					file, s.Addr(), tgt.Addr()))
			}

			auth, ok := s.AuthFor(tgt.Addr())
			if !ok {
				continue
			}
			scheme, _, err := schema.ParseCredentialRef(auth.Ref)
			if err != nil {
				continue // already reported at parse time
			}
			switch scheme {
			case schema.SchemeEnv:
				// Rejected on claude_agents specifically, per §3.6's table:
				// the platform's agent object accepts no credential, so
				// honoring env:// would mean kastor reading the variable and
				// transmitting its value. Other platform targets are not
				// listed and are not rejected — target.memory dials nothing,
				// so a binding on it is unused rather than wrong.
				if tgt.Name == claudeTargetName {
					errs = append(errs, fmt.Errorf("%s: %s: auth ref %q cannot be bound on %s; the platform's agent object accepts no credential and kastor never reads a credential value — use connection://<credential_id>",
						file, s.Addr(), auth.Ref, tgt.Addr()))
				}
			case schema.SchemeConnection:
				if tgt.Name != claudeTargetName {
					errs = append(errs, fmt.Errorf("%s: %s: auth ref %q cannot be bound on %s; only %s holds platform connections — use env://<NAME>",
						file, s.Addr(), auth.Ref, tgt.Addr(), "target."+claudeTargetName))
					continue
				}
				if tgt.VaultID == "" {
					errs = append(errs, fmt.Errorf("%s: %s: auth ref %q needs a vault to resolve against; %s must declare \"vault_id\"",
						file, s.Addr(), auth.Ref, tgt.Addr()))
				}
			}
		}
	}
	return errs
}

func (m *Module) mcpServerNames() []string {
	names := make([]string, 0, len(m.MCPServers))
	for _, s := range m.MCPServers {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func (m *Module) targetNames() []string {
	names := make([]string, 0, len(m.Targets))
	for _, t := range m.Targets {
		names = append(names, t.Addr())
	}
	sort.Strings(names)
	return names
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "none declared"
	}
	return strings.Join(items, ", ")
}

func hasOutput(a *schema.Agent, name string) bool {
	for _, out := range a.Outputs {
		if out.Name == name {
			return true
		}
	}
	return false
}

// fileErr prefixes a parse error with the file it came from, unless it is an
// HCL diagnostic set, which already carries filename and position.
func fileErr(rel string, err error) error {
	var diags hcl.Diagnostics
	if errors.As(err, &diags) {
		return err
	}
	return fmt.Errorf("%s: %w", rel, err)
}
