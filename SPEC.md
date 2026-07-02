# Agent Definition Language (ADL) — v0 Design Spec
 
> Declarative language for defining, compiling, and managing AI agents.
> "Terraform for agents": one spec → codegen for frameworks *or* lifecycle management on hosted platforms.
 
Status: **draft v0** · Syntax: **HCL** · Implementation: **Go**
 
---
 
## 1. Vision
 
Agents today are defined imperatively inside frameworks (LangGraph, CrewAI) or clicked together in platform UIs (OpenAI Assistants, Bedrock Agents). There is no vendor-neutral, versionable, reviewable source of truth.
 
ADL provides:
 
1. **A spec** — typed, declarative definitions of agents, tools, prompts, and models.
2. **A compiler** — generate runnable projects for target frameworks (`adl build`).
3. **A reconciler** — create/update/destroy agents on hosted platforms with plan/apply/state semantics (`adl plan`, `adl apply`).
Non-goals (v0): being a runtime, executing agents, evaluation/testing harnesses.
 
---
 
## 2. File Types
 
| Extension | Purpose | Contains |
|-----------|---------|----------|
| `.agent`  | Agent definition | model ref, prompt refs, tool refs, IO schema, deps |
| `.tool`   | Tool specification | interface (params, returns) + implementation source |
| `.prompt` | Prompt template | frontmatter (name, required variables) + raw body |
| `.adl` / `adl.hcl` | Project file | project meta, model blocks, targets, defaults |
 
All files in a directory tree form one **module** (like a Terraform module). Files reference each other by block address, not path.
 
---
 
## 3. Block Reference
 
### 3.1 `model` (in project file)
 
```hcl
model "fast" {
  provider = "openai"        # openai | anthropic | google | ollama | ...
  id       = "gpt-4o-mini"
  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}
```
 
Vendor-neutral: agents reference `model.fast`, never raw model strings. Swapping providers is a one-line change. (`id` rather than `name` for the provider's model identifier — `name` is reserved for block labels conceptually.)

**Rules:**
- `params` is an open key/value bag — keys are provider-specific and are validated by the provider/target, not at parse time (a typo like `temperatur` is only caught by the platform; accepted v0 tradeoff). Whole-number values are integers, fractional values floats.
- Project files do not support expressions or references in v0 — attribute values must be literals.
- Unknown attributes and blocks are hard errors (strict in v0: loosening later is painless, tightening later breaks users).
- Duplicate block names within a file are a parse error. Module-wide (cross-file) duplicate detection is owned by module loading (see issue #6).
 
### 3.2 `agent` (.agent file)
 
```hcl
agent "weather" {
  description = "Answers weather questions for a location and date"
 
  model         = model.fast
  system_prompt = prompt.weather_system
 
  tools = [tool.web_search]
 
  input "location" {
    type        = string
    description = "The location to get the weather for"
  }
 
  input "date" {
    type     = string
    optional = true
  }
 
  output "weather" {
    type = string
  }
 
  # Implicit dependency: created because we reference agent.forecast
  input "forecast_context" {
    type    = string
    default = agent.forecast.output.summary
  }
 
  # Explicit fallback when no reference exists (rare)
  depends_on = [agent.geocoder]
}
```
 
**Ownership rules:**
- Agent owns the **model** and the **IO contract**.
- Prompt owns only its **template body** and the **variables it requires**.
- Validation: every variable a prompt requires must be satisfiable from the agent's inputs/outputs; conflict = compile error.
### 3.3 `tool` (.tool file)
 
A tool is an **interface + implementation source**.
 
```hcl
tool "web_search" {
  description = "Search the web"
 
  param "query" {
    type        = string
    description = "The query to search for"
  }
 
  param "max_results" {
    type    = number
    default = 10
  }
 
  param "include_images" {
    type    = bool
    default = false
  }
 
  returns {
    type = string
  }
 
  # Exactly one implementation block:
  source {
    kind = "mcp"                       # mcp | http | builtin | runtime | script
    uri  = "mcp://search-server/web_search"
  }
}
```
 
**Source kinds:**
 
| kind | Meaning |
|------|---------|
| `mcp` | Tool served by an MCP server |
| `http` | REST endpoint (OpenAPI-style descriptor) |
| `builtin` | Provided by target platform (e.g. OpenAI's built-in web search) |
| `runtime` | Implemented in user code within the generated project (codegen emits a stub) |
| `script` | Inline/local script executed by generated glue code |
 
### 3.4 `prompt` (.prompt file)
 
Frontmatter + body. Body is the raw prompt; variables use `{{var}}`.
 
```
---
name = "weather_system"
requires = ["location"]
---
You are a weather assistant. The user is asking about {{location}}.
Answer concisely using the provided tools.
```
 
No model, no IO — prompts are pure templates (per decision #2).

**Rules:**
- Frontmatter starts at byte 0: the opening `---` must be the first line of the file, and a closing `---` line is required. The interior is HCL (so `#` comments work there), allowing exactly two attributes: `name` (required string) and `requires` (optional list of string). Unknown attributes are hard errors.
- Variable grammar: `{{ident}}` where `ident` matches `[A-Za-z_][A-Za-z0-9_]*`, with optional whitespace inside the braces (`{{ date }}`). Any other brace sequence — `{{1bad}}`, `{{not-a-var}}`, a lone `{{` — is literal body text, not an error, so prompts can embed JSON and code examples freely. There is no escape syntax for a literal well-formed `{{var}}` in v0.
- `requires` is an optional contract. Omitted → the variable set is inferred from the body. Present (including an explicit `requires = []`) → it must match the body's variables exactly, both directions: a body variable missing from `requires` and a `requires` entry never used in the body are both compile errors. Duplicate entries are a compile error.
- An empty (or whitespace-only) body is a compile error — there is no valid use for a zero-byte prompt.
- The body is everything after the closing delimiter line, preserved byte for byte.

### 3.5 `target` (project file)
 
Where the spec goes. Two categories mirror the two verbs:
 
```hcl
# Codegen target → `adl build`
target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}
 
# Managed platform target → `adl plan` / `adl apply`
target "openai_assistants" {
  type = "platform"
  auth {
    api_key_env = "OPENAI_API_KEY"
  }
}
```

**Rules:**
- `type` is a closed enum: `codegen` or `platform`. Unknown values are a compile error; new target types are additive spec changes.
- `codegen` targets require `output` and do not allow `auth`.
- `platform` targets do not allow `output`; `auth` is optional (ambient credentials — env vars, instance roles — are the common case).
- Fields that are meaningless for a target's type are errors, not ignored (configs rot through silent acceptance).
 
---
 
## 4. Dependency & Reference Semantics
 
- **References create the DAG.** `agent.forecast.output.summary` makes `weather` depend on `forecast`. Same rule for `model.*`, `tool.*`, `prompt.*`.
- `depends_on` is the explicit escape hatch for ordering without data flow (Terraform-style).
- Cycles are a compile error.
- Cross-module references (v1): `module.<name>.agent.<name>` — registry/import system deferred.
---
 
## 5. CLI
 
| Command | Function |
|---------|----------|
| `adl init` | Scaffold project |
| `adl validate` | Parse + type-check + resolve references |
| `adl build [-target X]` | Codegen for framework targets |
| `adl plan` | Diff spec vs. state file vs. remote platform |
| `adl apply` | Reconcile platform targets, update state |
| `adl destroy` | Remove managed remote agents |
| `adl fmt` | Canonical formatting |
 
State file (`adl.state.json`): maps block addresses → remote resource IDs, tracks last-applied config for drift detection.
 
---
 
## 6. Architecture (Go)
 
```
cmd/adl/            CLI (cobra)
internal/
  parser/           HCL decode (hashicorp/hcl/v2) → AST
  schema/           typed config structs, validation, reference resolution
  graph/            DAG construction, cycle detection, topo sort
  build/            codegen engine
    langgraph/      target: LangGraph (Python)
    crewai/         target: CrewAI (Python)
  provider/         platform reconcilers
    openai/         OpenAI Assistants API
    bedrock/        AWS Bedrock Agents
  state/            state file read/write, locking, diff
```
 
Providers implement a common interface (`Read/Create/Update/Delete/Diff`) — later extractable to a plugin system (go-plugin, like Terraform).
 
---
 
## 7. Deferred to v1+
 
- **Memory/state config** on agents (conversation memory, vector stores)
- **Guardrails** blocks (input/output filters, budgets, rate limits)
- Multi-agent orchestration graphs (routing, handoffs) beyond simple references
- Module registry / package management for sharing `.agent`/`.tool` files
- Secrets management beyond env vars
- Remote state backends
---
 
## 8. v0 Milestones
 
1. Parser + `adl validate` for `.agent`, `.tool`, `.prompt`, project file
2. `adl build` with **one** codegen target (pick LangGraph)
3. `adl plan/apply` with **one** platform provider (pick OpenAI Assistants)
4. Examples repo: the weather agent end-to-end on both paths
One codegen target + one provider proves the hybrid thesis. Everything else is expansion.