# Kastor — v0 Design Spec
 
> Declarative language for defining, compiling, and managing AI agents.
> "Terraform for agents": one spec → codegen for frameworks *or* lifecycle management on hosted platforms.
 
Status: **draft v0** · Syntax: **HCL** · Implementation: **Go**
 
---
 
## 1. Vision
 
Agents today are defined imperatively inside frameworks (LangGraph, CrewAI) or clicked together in platform UIs (OpenAI Assistants, Bedrock Agents). There is no vendor-neutral, versionable, reviewable source of truth.
 
Kastor provides:
 
1. **A spec** — typed, declarative definitions of agents, tools, prompts, and models.
2. **A compiler** — generate runnable projects for target frameworks (`kastor build`).
3. **A reconciler** — create/update/destroy agents on hosted platforms with plan/apply/state semantics (`kastor plan`, `kastor apply`).
Non-goals (v0): being a runtime, executing agents, evaluation/testing harnesses.
 
---
 
## 2. File Types
 
| Extension | Purpose | Contains |
|-----------|---------|----------|
| `.agent`  | Agent definition | model ref, prompt refs, tool refs, IO schema, deps |
| `.tool`   | Tool specification | interface (params, returns) + implementation source |
| `.prompt` | Prompt template | frontmatter (name, required variables) + raw body |
| `.kastor` / `kastor.hcl` | Project file | project meta, model blocks, targets, defaults |
 
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

**Codegen provider mapping.** Codegen targets support a fixed provider set; a `provider` outside this table is a codegen error (not a parse error — the block itself stays valid, e.g. for platform targets that accept it). LangGraph mapping:

| provider | `init_chat_model` prefix | pip package | credentials |
|----------|--------------------------|-------------|-------------|
| `openai` | `openai` | `langchain-openai` | `OPENAI_API_KEY` |
| `anthropic` | `anthropic` | `langchain-anthropic` | `ANTHROPIC_API_KEY` |
| `google` | `google_genai` | `langchain-google-genai` | `GOOGLE_API_KEY` |
| `ollama` | `ollama` | `langchain-ollama` | none (local runtime) |

For codegen, `params` keys must additionally be valid Python keyword arguments (`max_tokens`, not `max-tokens`); a key that is not is a codegen error.
 
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
- `system_prompt` is **optional** (issue #7): an agent may omit it entirely, in which case the prompt-variable check is a no-op. When present it must be a `prompt.<name>` reference.
- An input `default` that references another agent's output (`default = agent.forecast.output.summary`) creates the dependency edge and is validated at compile time (the referenced output must exist), but **v0 codegen does not wire the data flow** — see §4.
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

**Codegen mapping (LangGraph target):**

| kind | Generated binding |
|------|-------------------|
| `mcp` | `@tool` function calling the named server tool through a generated MCP bridge |
| `http` | `@tool` function POSTing the tool's params as a JSON object to `uri` |
| `runtime` | `@tool` stub raising `NotImplementedError` until user code supplies the body |
| `builtin` | Codegen error, **permanently**: platform-provided tools have no local binding — `builtin` is only meaningful on platform targets |
| `script` | Codegen error, **for now**: glue-code execution is deferred (issue #36) |

**Rules:**
- `kind` is a closed enum (like `target.type`): `mcp | http | builtin | runtime | script`. Unknown kinds are compile errors.
- Exactly one `source` block and exactly one `returns` block per tool. Zero `param` blocks is fine.
- `uri` is required for `mcp`, `http`, and `script` sources; it is an error on `builtin` and `runtime` (they have no external location — the platform or generated stub is the implementation). Meaningless fields are errors, not ignored.
- For `mcp` sources the `uri` pins **identity only**: `mcp://<server>/<tool>` names the server and the tool on it — nothing more. Transport and connection details (command, endpoint, headers) are deployment configuration, not spec: generated projects read them at runtime from `mcp_servers.json` (langchain-mcp-adapters connection format), overridable via the `KASTOR_MCP_CONFIG` env var.
- Param and returns types are bare keywords, not strings: `type = string`, never `type = "string"`. Closed enum in v0: `string | number | bool` (compound types deferred to v1).
- `default` must be a literal whose type matches the declared `type`; a mismatch or an explicit `default = null` is a compile error. A param with a `default` is optional at call time; there is no separate `optional` attribute on tool params.
- `description` is optional on both the tool and its params at parse time (targets may enforce more).
- A `.tool` file may contain multiple `tool` blocks; duplicate names within a file are a parse error (module-wide duplicates are owned by module loading, issue #6).
 
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
# Codegen target → `kastor build`
target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}
 
# Managed platform target → `kastor plan` / `kastor apply`
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
- **A platform target's label selects its provider implementation**, exactly as a codegen target's label selects its generator: `target "openai_assistants"` binds to the OpenAI Assistants reconciler, `target "memory"` to the built-in in-memory platform. A label with no registered provider is an error naming the available providers. (A separate `provider` attribute is deliberately deferred until something forces it — e.g. two targets on the same platform kind in one module.)
- The `memory` platform is built in: an **ephemeral in-memory store** so plan/apply can be demonstrated and exercised — examples, onboarding, CI — with no credentials and no network. `auth` on it is an error (meaningless fields, again). Its remote objects die with the process, so a later invocation's plan truthfully reports previously applied resources as remote-missing drift.
 
---
 
## 4. Dependency & Reference Semantics
 
- **References create the DAG.** `agent.forecast.output.summary` makes `weather` depend on `forecast`. Same rule for `model.*`, `tool.*`, `prompt.*`.
- **References order and validate; v0 codegen does not move data.** A cross-agent reference is checked at compile time (the output must exist) and orders the DAG, but generated code exposes the referencing input as an ordinary caller-supplied parameter — the caller runs the upstream agent and passes the value. Wiring actual data flow is orchestration, deferred to v1 (§7).
- `depends_on` is the explicit escape hatch for ordering without data flow (Terraform-style).
- Cycles are a compile error.
- Cross-module references (v1): `module.<name>.agent.<name>` — registry/import system deferred.
---
 
## 5. CLI
 
| Command | Function |
|---------|----------|
| `kastor init` | Scaffold project |
| `kastor validate` | Parse + type-check + resolve references |
| `kastor build [-target X]` | Codegen for framework targets |
| `kastor plan` | Diff spec vs. state file vs. remote platform |
| `kastor apply` | Reconcile platform targets, update state |
| `kastor destroy` | Remove managed remote agents |
| `kastor fmt` | Canonical formatting |
 
Exit codes (all commands): 0 clean, 1 validation/codegen/plan/apply errors, 2 usage/IO errors (including lock contention).

### 5.1 State file

`kastor.state.json`, at the module root, records what `kastor apply` manages: block addresses → remote resource IDs, plus the configuration last applied to each.

```json
{
  "version": 1,
  "serial": 4,
  "targets": {
    "openai_assistants": {
      "resources": {
        "agent.weather": {
          "id": "asst_abc123",
          "config": { "model": { "id": "gpt-4o-mini", "provider": "openai" } },
          "dependencies": ["agent.geocoder"]
        }
      }
    }
  }
}
```

**Rules:**
- `version` is the state format version. Unknown versions are rejected, never guessed at (same stance as language versioning, §9). `serial` increases by one on every write, ordering snapshots.
- The **unit of remote management is the agent**: each `agent` block is one resource; its model, prompt, and tools are folded into the resource's config (the "agent closure"). Standalone remote tool/prompt objects are deferred.
- `config` is the **full last-applied config** (canonical JSON, not a hash) — drift reports can then name the attributes that changed without refetching anything.
- `dependencies` records the resource's managed (agent) dependencies so a resource that has been removed from the spec can still be destroyed in reverse dependency order — the module graph no longer knows it.
- Serialization is deterministic: stable key order, byte-identical output for equal state. Writes are atomic (temp file + rename) and happen **after every applied operation**, not once at the end — an interrupted apply loses nothing, and a re-run plans exactly the remainder.
- Like Terraform state, the file is environment-specific and is not meant to be committed.

**Locking:** plan/apply/destroy take a local lock file (`.kastor.state.lock`) for their duration; contention errors name the holding pid and the recovery step (delete the stale file). Remote state backends and remote locking are deferred (§7).

### 5.2 Plan/apply semantics

`kastor plan` is a **three-way comparison** — spec vs. state vs. remote — and a **pure read**: it issues only `Read`/`Diff` provider calls and never touches the state file.

Per resource, in the module's topological order (deletes first, in reverse dependency order):

| Situation | Plan |
|-----------|------|
| in spec, not in state | create ("not in state") |
| in state, remote object missing | create ("remote object … missing") + drift warning |
| in spec and state, remote differs from spec | update, with attribute-level diffs |
| in spec and state, remote matches | no-op |
| in state, not in spec | delete ("removed from spec") |

**Drift** (remote changed outside kastor) is detected by diffing the *last-applied* config against the remote and reported as a warning naming the changed attributes; apply converges the remote back to the spec. When the user instead edits the spec to match a manual remote change, the plan is a no-op and apply silently refreshes the stale state entry (no remote call), so the warning does not recur.

**Plan output.** One line per pending change, in execution order: `+` create, `~` update, `-` delete, with the reason in parentheses on creates and deletes, and one indented `path: old → new` line per attribute diff under updates (values render as compact JSON, truncated so a prompt body cannot flood the plan). An attribute diff is `{path, old, new}` with dotted paths (`model.id`, `tools[0].source.uri`); `old` is null when an attribute is being added, `new` null when it is being removed. Warnings precede the summary. The summary line is countable and per-target — `Plan for target.<name>: N to create, M to update, K to delete, J unchanged.` — or `No changes for target.<name>: remote matches the spec (N resources).` when nothing is pending. The whole plan (target, ordered changes, attribute diffs, diagnostics) is one serializable tree, so the `--json` rendering (§9) is a second renderer over the same data, not a second pipeline.

`kastor apply` executes the plan in order and stops at the first failure; everything applied up to that point is already saved in state, and the error states what failed, what had been applied, and — if a resource was created remotely but saving state failed — the remote id, so nothing is orphaned silently. `kastor apply` does not prompt for confirmation in v0. `kastor destroy` deletes everything in state in reverse dependency order.

Diagnostics from plan/apply are structured (severity, block address, summary, detail) so a machine-readable `--json` rendering (§9) is a renderer, not a redesign.
 
---
 
## 6. Architecture (Go)
 
```
cmd/kastor/         CLI (cobra)
internal/
  parser/           HCL decode (hashicorp/hcl/v2) → AST
  schema/           typed config structs, validation
  module/           directory walk → symbol table, cross-file reference resolution
  graph/            DAG construction, cycle detection, topo sort
  build/            codegen engine
    langgraph/      target: LangGraph (Python)
    crewai/         target: CrewAI (Python)
  provider/         platform reconcilers
    memory/         built-in in-memory platform (demos, examples, CI)
    openai/         OpenAI Assistants API
    bedrock/        AWS Bedrock Agents
  state/            state file read/write, locking, diff
```
 
Providers implement a common interface (`Read/Create/Update/Delete/Diff`) — later extractable to a plugin system (go-plugin, like Terraform).

**Provider contract** (`internal/provider`): the engine renders each agent's closure into a neutral, serializable config (a JSON value tree — no core Go types, no provider types), so the interface can move behind a plugin boundary without redesign. Contract rules:

- `Read(id)` reports found=false for a resource deleted outside kastor — that is drift data, not an error.
- `Create(resource)` returns the platform's id; the engine records it in state immediately.
- `Delete(id)` is idempotent: deleting an already-missing remote object succeeds, so re-runs after partial failures converge.
- `Diff(desired, remote)` is the comparison authority — only the provider knows how the neutral config maps onto its platform's attributes. Empty result = in sync. The engine also diffs the last-applied config against the remote for drift detection.
- `Diff` must be pure and deterministic; `Read` must not mutate. `kastor plan` issues only these two.

The plan/apply engine is target-agnostic and consumes exactly what `kastor validate` assembles (loaded module, dependency graph, topological order) plus the state file — the same shape as the codegen engine's `Generate(job)` contract.
 
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
 
1. Parser + `kastor validate` for `.agent`, `.tool`, `.prompt`, project file
2. `kastor build` with **one** codegen target (pick LangGraph)
3. `kastor plan/apply` with **one** platform provider (pick OpenAI Assistants)
4. Examples repo: the weather agent end-to-end on both paths
One codegen target + one provider proves the hybrid thesis. Everything else is expansion.

## 9. Consumers & design constraints

Kastor has two consumer classes, in deliberate sequence:

1. **Humans (v0–v1).** Developers hand-writing specs, reading diagnostics,
   reviewing diffs in PRs. Human ergonomics — readable HCL, `kastor fmt`, clear
   error text, this document — are first-class permanently. Nothing in the
   AI-consumer direction may regress the human path.

2. **AI agents as the primary consumer (v1+).** The long-term thesis: people
   build personalized agents *by instructing an AI*, and that AI expresses the
   result as a Kastor module — validated, versioned, diffable, reviewable by a
   human. Kastor is the stable, typed substrate that makes AI-built agents
   trustworthy rather than ad hoc.

Every design decision is evaluated against both consumers. Concretely, the
AI-consumer thesis imposes these standing constraints:

- **Diagnostics are machine-readable and self-repair-oriented.** Every error
  states what was found, what was expected, and where (file:line + block
  address). A structured output mode (`--json`) is required, not polish:
  validation errors are the AI's self-correction loop.
- **The language is versioned.** Modules may declare the spec version they
  target; parsers reject versions they don't understand rather than
  misinterpreting them. Syntax changes are additive within a version.
- **Documentation is generated, not hand-maintained.** The schema structs are
  the source of truth; reference docs for the syntax derive from them, so an
  AI reading the docs and the parser enforcing the language can never
  disagree.
- **Determinism everywhere** (already a project convention): same input, same
  output — byte-identical builds, stable ordering, reproducible plans. AI
  workflows compound nondeterminism; the toolchain must contribute none.
- **The toolchain itself is agent-operable.** The pipeline behind the CLI is
  equally exposable via an MCP server (`validate`/`build`/`plan`/`apply` plus
  schema/introspection tools), so an AI can drive the full lifecycle without
  shelling out or screen-scraping text output.

Sequencing: these constraints shape decisions from now on, but implementation
lands after the core thesis is proven (§8 — one codegen target, one platform
provider). The MCP server and generated docs are v1 milestones, not v0 scope.
