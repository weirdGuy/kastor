// Package eve generates Vercel eve (TypeScript) agent projects from a
// loaded Kastor module — the second codegen target of SPEC.md §8 milestone 2
// (issue #55). eve's unit is "an agent is a directory", so the module's
// dependency structure, not its file list, shapes the output: every root
// agent (one no other agent references or depends on) becomes one
// self-contained eve project under <output>/<agent name>/, and the agents it
// references become subagent directories inside it.
//
// Block → eve mapping:
//
//	agent  "a" (root)       → <a>/package.json, tsconfig.json, README.md, agent/…
//	agent  "a" (referenced) → …/subagents/a/ inside each referencing agent
//	model  "m"              → agent.ts: defineAgent model "<provider>/<id>"
//	                          (AI Gateway string) + params as modelOptions
//	prompt "p" (system)     → instructions.md: body verbatim + generated
//	                          Inputs/Outputs sections (the IO contract has no
//	                          typed eve equivalent — it degrades to convention)
//	prompt "p" (unused)     → agent/skills/p.md in every root project
//	tool   "t"              → by source kind, below
//
// Tool source.kind mapping:
//
//	mcp     → connections/<server>.ts allow-listing the pinned tool; the
//	          endpoint URL is deployment config (KASTOR_MCP_<SERVER>_URL)
//	http    → tools/t.ts: defineTool + Zod schema POSTing params to the uri
//	runtime → tools/t.ts: defineTool + Zod schema, execute throws until the
//	          user supplies the body
//	builtin → codegen error: platform-provided tools have no local binding
//	script  → codegen error: deferred, not in v0 eve scope
//
// Tools and models no agent references are not emitted: a file in eve's
// tools/ directory is auto-wired into the agent (filename = tool, no
// registration), so emitting an unreferenced tool would grant a capability
// the spec never did. They are listed in the project README instead.
//
// Cross-agent data flow (input defaults like agent.x.output.y) is not wired
// in v0; the instructions direct the model to delegate to the subagent or
// take the value from the message. depends_on stays ordering-only (SPEC.md
// §4) but the depended-on agent is still emitted as a subagent so it ships.
package eve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/weirdGuy/kastor/internal/build"
	"github.com/weirdGuy/kastor/internal/module"
	"github.com/weirdGuy/kastor/internal/schema"
)

// Generator implements build.Generator for the eve target.
type Generator struct{}

var _ build.Generator = Generator{}

// Generate emits one eve project per root agent. A module with no agents
// generates nothing: an eve project is an agent, so there is no scaffold to
// emit without one.
func (Generator) Generate(job *build.Job) ([]build.File, error) {
	idx := buildIndex(job.Module)
	var files []build.File
	for _, root := range rootAgents(job.Module) {
		pfiles, err := emitProject(idx, root)
		if err != nil {
			return nil, err
		}
		files = append(files, pfiles...)
	}
	return files, nil
}

// index resolves block references by name across the module.
type index struct {
	agents  map[string]*schema.Agent
	tools   map[string]*schema.Tool
	prompts map[string]*schema.Prompt
	models  map[string]*schema.Model

	unusedPrompts []*schema.Prompt // not any agent's system_prompt; sorted
	unboundTools  []*schema.Tool   // referenced by no agent; sorted
	unboundModels []*schema.Model  // referenced by no agent; sorted
}

func buildIndex(mod *module.Module) *index {
	idx := &index{
		agents:  map[string]*schema.Agent{},
		tools:   map[string]*schema.Tool{},
		prompts: map[string]*schema.Prompt{},
		models:  map[string]*schema.Model{},
	}
	for _, a := range mod.Agents {
		idx.agents[a.Name] = a
	}
	for _, t := range mod.Tools {
		idx.tools[t.Name] = t
	}
	for _, p := range mod.Prompts {
		idx.prompts[p.Name] = p
	}
	for _, m := range mod.Models {
		idx.models[m.Name] = m
	}

	usedTools, usedModels, usedPrompts := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range mod.Agents {
		usedModels[strings.TrimPrefix(a.Model, "model.")] = true
		if a.SystemPrompt != "" {
			usedPrompts[strings.TrimPrefix(a.SystemPrompt, "prompt.")] = true
		}
		for _, ref := range a.Tools {
			usedTools[strings.TrimPrefix(ref, "tool.")] = true
		}
	}
	for _, p := range sortedCopy(mod.Prompts, (*schema.Prompt).Addr) {
		if !usedPrompts[p.Name] {
			idx.unusedPrompts = append(idx.unusedPrompts, p)
		}
	}
	for _, t := range sortedCopy(mod.Tools, (*schema.Tool).Addr) {
		if !usedTools[t.Name] {
			idx.unboundTools = append(idx.unboundTools, t)
		}
	}
	for _, m := range sortedCopy(mod.Models, (*schema.Model).Addr) {
		if !usedModels[m.Name] {
			idx.unboundModels = append(idx.unboundModels, m)
		}
	}
	return idx
}

// childEdge is one agent→agent dependency edge: via an input's cross-agent
// default reference, or — orderingOnly — via depends_on alone.
type childEdge struct {
	name         string
	orderingOnly bool
}

// childEdges lists an agent's direct agent dependencies, sorted by name. An
// agent both referenced and listed in depends_on counts as referenced.
func childEdges(a *schema.Agent) []childEdge {
	ordering := map[string]bool{}
	for _, in := range a.Inputs {
		if in.DefaultRef != "" {
			ordering[refOwner(in.DefaultRef)] = false
		}
	}
	for _, dep := range a.DependsOn {
		name := strings.TrimPrefix(dep, "agent.")
		if _, referenced := ordering[name]; !referenced {
			ordering[name] = true
		}
	}
	edges := make([]childEdge, 0, len(ordering))
	for _, name := range sortedKeys(ordering) {
		edges = append(edges, childEdge{name: name, orderingOnly: ordering[name]})
	}
	return edges
}

// rootAgents returns the agents no other agent references or depends on,
// sorted by name. The module graph is a DAG (cycles fail validation before
// codegen), so every module with agents has at least one root.
func rootAgents(mod *module.Module) []*schema.Agent {
	child := map[string]bool{}
	for _, a := range mod.Agents {
		for _, e := range childEdges(a) {
			child[e.name] = true
		}
	}
	var roots []*schema.Agent
	for _, a := range sortedCopy(mod.Agents, (*schema.Agent).Addr) {
		if !child[a.Name] {
			roots = append(roots, a)
		}
	}
	return roots
}

// treeAgent is one agent directory in a project, in emission (depth-first)
// order.
type treeAgent struct {
	agent        *schema.Agent
	dir          string // project-relative directory, e.g. "agent/subagents/forecast/"
	parent       string // referencing agent name; "" for the root
	orderingOnly bool   // the parent edge is depends_on only
}

// projectBuilder accumulates one root agent's project: its files plus the
// facts the README reports.
type projectBuilder struct {
	idx     *index
	root    *schema.Agent
	files   []build.File
	tree    []*treeAgent
	models  map[string]*schema.Model
	servers map[string]*mcpServer // merged across agent directories
	runtime []*schema.Tool
	http    []*schema.Tool
}

// emitProject emits the whole project directory for one root agent.
func emitProject(idx *index, root *schema.Agent) ([]build.File, error) {
	pb := &projectBuilder{
		idx:     idx,
		root:    root,
		models:  map[string]*schema.Model{},
		servers: map[string]*mcpServer{},
	}
	prefix := root.Name + "/"

	if err := pb.emitAgentDir(root, prefix+"agent/", "", false); err != nil {
		return nil, err
	}
	for _, p := range idx.unusedPrompts {
		pb.add(prefix+"agent/skills/"+p.Name+".md", genSkill(p))
	}

	pb.add(prefix+"package.json", genPackageJSON(root))
	pb.add(prefix+"tsconfig.json", genTsconfig())
	pb.add(prefix+"README.md", pb.genReadme())
	return pb.files, nil
}

func (pb *projectBuilder) add(path string, data []byte) {
	pb.files = append(pb.files, build.File{Path: path, Data: data})
}

// emitAgentDir emits one agent directory — agent.ts, instructions.md, tool
// and connection files — then recurses into its subagents. The module graph
// is acyclic, so the recursion terminates.
func (pb *projectBuilder) emitAgentDir(a *schema.Agent, dir, parent string, orderingOnly bool) error {
	pb.tree = append(pb.tree, &treeAgent{agent: a, dir: dir, parent: parent, orderingOnly: orderingOnly})

	model, ok := pb.idx.models[strings.TrimPrefix(a.Model, "model.")]
	if !ok {
		return fmt.Errorf("%s: unknown reference %s", a.Addr(), a.Model)
	}
	pb.models[model.Name] = model
	agentTS, err := genAgentTS(a, model, parent != "")
	if err != nil {
		return err
	}
	pb.add(dir+"agent.ts", agentTS)

	var prompt *schema.Prompt
	if a.SystemPrompt != "" {
		prompt, ok = pb.idx.prompts[strings.TrimPrefix(a.SystemPrompt, "prompt.")]
		if !ok {
			return fmt.Errorf("%s: unknown reference %s", a.Addr(), a.SystemPrompt)
		}
	}
	pb.add(dir+"instructions.md", genInstructions(a, prompt))

	var mcpTools []*schema.Tool
	for _, ref := range sortedCopy(a.Tools, func(s string) string { return s }) {
		tool, ok := pb.idx.tools[strings.TrimPrefix(ref, "tool.")]
		if !ok {
			return fmt.Errorf("%s: unknown reference %s", a.Addr(), ref)
		}
		if tool.Source.Kind == "mcp" {
			mcpTools = append(mcpTools, tool)
			continue
		}
		data, err := genTool(tool)
		if err != nil {
			return err
		}
		pb.add(dir+"tools/"+tool.Name+".ts", data)
		switch tool.Source.Kind {
		case "runtime":
			pb.runtime = appendUnique(pb.runtime, tool)
		case "http":
			pb.http = appendUnique(pb.http, tool)
		}
	}

	servers, err := groupMCPServers(mcpTools)
	if err != nil {
		return err
	}
	for _, s := range servers {
		pb.add(dir+"connections/"+s.Name+".ts", genConnection(s))
		pb.mergeServer(s)
	}

	for _, e := range childEdges(a) {
		child, ok := pb.idx.agents[e.name]
		if !ok {
			return fmt.Errorf("%s: unknown reference agent.%s", a.Addr(), e.name)
		}
		if err := pb.emitAgentDir(child, dir+"subagents/"+e.name+"/", a.Name, e.orderingOnly); err != nil {
			return err
		}
	}
	return nil
}

// mergeServer folds one agent directory's server binding into the project-
// wide view the README reports.
func (pb *projectBuilder) mergeServer(s *mcpServer) {
	merged := pb.servers[s.Name]
	if merged == nil {
		merged = &mcpServer{Name: s.Name}
		pb.servers[s.Name] = merged
	}
	for _, t := range s.Tools {
		merged.Tools = appendUnique(merged.Tools, t)
	}
	merged.Allow = dedupe(sortedStrings(append(merged.Allow, s.Allow...)))
	sort.Slice(merged.Tools, func(i, j int) bool { return merged.Tools[i].Name < merged.Tools[j].Name })
}

func appendUnique[T comparable](s []T, v T) []T {
	for _, have := range s {
		if have == v {
			return s
		}
	}
	return append(s, v)
}

func sortedStrings(s []string) []string {
	sort.Strings(s)
	return s
}
