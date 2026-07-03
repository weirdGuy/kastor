// Package module loads every ADL file in a directory tree into one module
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
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/weirdGuy/agentform/internal/parser"
	"github.com/weirdGuy/agentform/internal/schema"
)

// Module is a fully loaded and reference-resolved directory tree of ADL
// files. Block slices follow lexical file order, source order within a file,
// so downstream output stays deterministic.
type Module struct {
	Root    string
	Agents  []*schema.Agent
	Tools   []*schema.Tool
	Prompts []*schema.Prompt
	Models  []*schema.Model
	Targets []*schema.Target

	symbols map[string]*Symbol
}

// Symbol is one addressable block in the module's symbol table.
type Symbol struct {
	Addr  string // block address, e.g. "agent.weather"
	Kind  string // agent | tool | prompt | model | target
	File  string // declaring file, relative to the module root
	Block any    // *schema.Agent, *schema.Tool, *schema.Prompt, *schema.Model, or *schema.Target
}

// Lookup resolves a block address against the module's symbol table.
func (m *Module) Lookup(addr string) (*Symbol, bool) {
	sym, ok := m.symbols[addr]
	return sym, ok
}

// Load walks the directory tree rooted at root, parses every ADL file
// (.agent, .tool, .prompt, .adl, adl.hcl), and resolves all references.
// Hidden files and directories (dot-prefixed) are skipped. All errors —
// parse failures, cross-file duplicate addresses, unknown references — are
// collected and returned joined, so one run reports everything.
func Load(root string) (*Module, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("loading module: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("loading module: %s is not a directory", root)
	}

	mod := &Module{Root: root, symbols: map[string]*Symbol{}}

	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if hidden {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		errs = append(errs, mod.loadFile(path, rel)...)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking module directory: %w", walkErr)
	}

	for _, a := range mod.Agents {
		errs = append(errs, mod.resolveAgent(a)...)
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return mod, nil
}

// loadFile parses one ADL file (dispatched on its name) and registers its
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
	case ".adl", ".hcl":
		if filepath.Ext(path) == ".hcl" && filepath.Base(path) != "adl.hcl" {
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
	check(a.SystemPrompt)
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
