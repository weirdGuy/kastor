# Kastor — v0 Design Spec
 
> Declarative language for defining, compiling, and managing AI agents.
> "Terraform for agents": one spec → codegen for frameworks *or* lifecycle management on hosted platforms.
 
Status: **draft v0** · Syntax: **HCL** · Implementation: **Go**
 
---
 
## 1. Vision
 
Agents today are defined imperatively inside frameworks (LangGraph, CrewAI) or clicked together in platform UIs (Dify). There is no vendor-neutral, versionable, reviewable source of truth. The first generation of managed agent platforms (OpenAI Assistants, Bedrock Agents) was deprecated within ~3 years of launch — evidence that agent definitions need a vendor-neutral, versionable home that outlives any one platform.
 
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
| `.kastor` / `kastor.hcl` | Project file | project meta, model blocks, targets, MCP servers, defaults |
 
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

**Provider support is per target.** Any `provider` string parses — the block stays valid regardless, since a module may declare models several targets consume differently. A target that cannot map the provider fails when it consumes the block: `kastor build` for a codegen target, `kastor plan` for a platform target (a planned create is rendered through the provider, §5.2).

| provider | `langgraph` | `eve` | `claude_agents` |
|----------|-------------|-------|-----------------|
| `openai` | yes — `OPENAI_API_KEY` | yes — gateway namespace `openai` | error |
| `anthropic` | yes — `ANTHROPIC_API_KEY` | yes — gateway namespace `anthropic` | yes — `ANTHROPIC_API_KEY` |
| `google` | yes — `GOOGLE_API_KEY` | yes — gateway namespace `google` | error |
| `ollama` | yes — no credential (local runtime) | error — the Vercel AI Gateway cannot route a local runtime | error |
| anything else | error | error | error |

`eve` routes every model through the Vercel AI Gateway and pins it as `<namespace>/<id>`, so its credential is `AI_GATEWAY_API_KEY` (or project OIDC when deployed on Vercel) rather than a per-vendor key.

**LangGraph binding detail.** `langgraph` emits `init_chat_model("<prefix>:<id>", **params)`:

| provider | `init_chat_model` prefix | pip package |
|----------|--------------------------|-------------|
| `openai` | `openai` | `langchain-openai` |
| `anthropic` | `anthropic` | `langchain-anthropic` |
| `google` | `google_genai` | `langchain-google-genai` |
| `ollama` | `ollama` | `langchain-ollama` |

For LangGraph codegen, `params` keys must additionally be valid Python keyword arguments (`max_tokens`, not `max-tokens`); a key that is not is a codegen error. `claude_agents` accepts only `speed` and rejects every other key, including `temperature` and `max_tokens`, because the platform's model object exposes `id` and `speed` alone.
 
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
 
  # One implementation block per target; one unqualified block covers all:
  source {
    kind = "mcp"                       # mcp | http | builtin | runtime | script
    uri  = "mcp://search-server/web_search"
  }
}
```

Bindings may differ per target; identity and the param contract do not:

```hcl
tool "web_search" {
  description = "Search the web"

  param "query" { type = string }
  returns { type = string }

  source {
    kind    = "builtin"
    id      = "web_search"
    targets = [target.claude_agents]
  }

  source {
    kind    = "mcp"
    uri     = "mcp://search-server/web_search"
    targets = [target.langgraph, target.eve]
  }
}
```
 
**Source kinds:**
 
| kind | Meaning | Identifier |
|------|---------|------------|
| `mcp` | Tool served by an MCP server | `uri = "mcp://<server>/<tool>"` |
| `http` | REST endpoint (OpenAPI-style descriptor) | `uri` — the endpoint URL |
| `builtin` | Provided by target platform (e.g. the target platform's hosted web-search tool) | `id` — the platform's own name for the tool |
| `runtime` | Implemented in user code within the generated project (codegen emits a stub) | none — the generated stub is the implementation |
| `script` | Inline/local script executed by generated glue code | `uri` |

**Source-kind support is per target**, mirroring the model-provider matrix of §3.1. A tool block stays valid regardless of what any one target supports; a target that cannot bind the source *selected for it* (see binding selection, below) is an error.

| kind | `langgraph` | `eve` | `claude_agents` |
|------|-------------|-------|-----------------|
| `mcp` | yes | yes — surfaced through the server connection, no `tools/` file | yes — the server is sent as a URL connection |
| `http` | yes | yes | error |
| `runtime` | yes — generated stub | yes — generated stub | error |
| `builtin` | error, **permanently** — platform-provided tools have no local binding | error, **permanently** | yes — `id` must name a tool in the platform toolset |
| `script` | error — deferred (issue #36) | error — deferred (issue #36) | error |

This matrix belongs to the targets, not to the module: each generator and provider declares the source kinds it supports, and **`kastor validate` cross-checks every tool against every target the module declares** (§5). Targets never declare capabilities in HCL — the matrix is part of a target's implementation, and this table is its documentation.

**A declared target is a promise.** A module that cannot build or apply for a target it declares fails at `validate`, not at `build` or `plan` — which means a broken `langgraph` binding also stops `kastor plan` for a `claude_agents` target in the same module, since both commands run the same pipeline. That is the enforceable reading of "write once, target many": if a target is declared, the module is claimed to work there. A module that genuinely targets one destination declares one target; per-target `source` blocks are how a module keeps a target it would otherwise have to drop.

Validate checks only what is knowable from the spec. Facts that depend on the environment — a missing MCP endpoint variable, rejected credentials — remain the provider's to report through `Diff` (§6), so `kastor validate` still needs no credentials and no network.

**Codegen mapping (LangGraph target).** Applies to the source **selected for that target**:

| kind | Generated binding |
|------|-------------------|
| `mcp` | `@tool` function calling the named server tool through a generated MCP bridge, configured from the tool's `mcp_server` block (§3.6) |
| `http` | `@tool` function POSTing the tool's params as a JSON object to `uri` |
| `runtime` | `@tool` stub raising `NotImplementedError` until user code supplies the body |
| `builtin` | Unreachable: `builtin` has no local binding, and `validate` rejects a module that selects a `builtin` source for a codegen target |
| `script` | Unreachable for the same reason, **for now**: glue-code execution is deferred (issue #36) |

**Rules:**
- `kind` is a closed enum (like `target.type`): `mcp | http | builtin | runtime | script`. Unknown kinds are compile errors.
- **At least one** `source` block and exactly one `returns` block per tool. Zero `param` blocks is fine.
- A `source` block may declare `targets` — a list of `target.<name>` references — which restricts it to those targets. A `source` without `targets` is the tool's **default binding**; at most one per tool.
- **Binding selection.** For target T a tool binds the source whose `targets` names T, otherwise the default. The invariant is **exactly one binding per (tool, target)** — the same guarantee "exactly one `source`" used to give, one axis richer: two sources naming the same target is an error, and a tool with no binding for a declared target is an error. Both are reported by `kastor validate`, naming the tool, the target, and the agents that use the tool.
- `targets` entries are references to `target` blocks declared in the module; an unknown one is an error listing the declared targets. Unlike the references of §4 they create no graph edge — targets are not nodes.
- A tool's identity and contract are shared by all of its bindings: `description`, `param`, and `returns` are declared once and cannot vary per target. A tool whose parameters differ per target is two tools.
- Identity attributes are per-kind and exclusive. `uri` is required for `mcp`, `http`, and `script`, and an error on any other kind. `id` is required for `builtin` — it is the platform's own name for the tool (`id = "web_search"`), the same relationship `model.id` has to a `model` block's label — and an error on any other kind. `runtime` takes neither: the generated stub is the implementation. Meaningless fields are errors, not ignored.
- A `builtin` source's `id`, not the tool's block label, is what a platform resolves. The label stays a kastor address with no vendor meaning.
- A `runtime` stub is **generated once and then belongs to the user**: its body is user code, so `kastor build` writes the file only while it is still absent or byte-identical to the stub the last build wrote there, and never writes it again once the file has been edited. A spec change that a user's implementation cannot be merged into still has to reach them, so the build writes the new stub beside their file as a `<name>.kastor-new` sidecar and reports the pair; a stub whose tool leaves the spec entirely is kept, not deleted, and reported the same way. Each is reported once — the sidecar on disk is the durable message. Reverting a file to its current stub hands it back to kastor and clears the sidecar. Ownership is recorded in the output directory's `.kastorbuild` marker; a directory that has no such record is treated as the user's, since assuming otherwise is the only way to lose their work.
- For `mcp` sources the `uri` names a **declared server and a tool on it**: `mcp://<server>/<tool>`, where `<server>` must match an `mcp_server` block in the module (§3.6). An unknown server is a compile error naming the file, the tool's block address, and the servers the module declares. Like `target.<name>` the reference is resolved and validated but creates no graph edge (§4) — servers are not nodes. The `<tool>` segment is the server's own name for the tool and is not checkable without contacting the server, so a mismatch surfaces at run time, not at `validate`.
- **The langgraph target generates `mcp_servers.json`** from the module's `mcp_server` blocks — a generated artifact like every other file it writes: deterministic, marked do-not-edit, never hand-maintained. It holds connection config only; a credential value never appears in it, and neither does an `auth.ref`. Auth is injected by the generated bridge, which reads the environment in the user's own process at call time.
- `KASTOR_MCP_CONFIG` survives as a **local override**: pointing it at a file replaces the generated one wholesale for that run. It is a development escape hatch — for aiming a run at a local server instance — not how connection details are meant to arrive. It carries no validation: a server the override omits or renames fails at the call, not at build. It exists only on the codegen path, where there is no state file and no plan for a bad override to corrupt.
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
target "claude_agents" {
  type = "platform"
  auth {
    api_key_env = "ANTHROPIC_API_KEY"
  }
}
```

**Rules:**
- `type` is a closed enum: `codegen` or `platform`. Unknown values are a compile error; new target types are additive spec changes.
- `codegen` targets require `output` and do not allow `auth`.
- `platform` targets do not allow `output`; `auth` is optional (ambient credentials — env vars, instance roles — are the common case).
- Fields that are meaningless for a target's type are errors, not ignored (configs rot through silent acceptance).
- **A platform target's label selects its provider implementation**, exactly as a codegen target's label selects its generator: `target "claude_agents"` binds to the Claude Managed Agents reconciler, `target "memory"` to the built-in in-memory platform. A label with no registered provider is an error naming the available providers. (A separate `provider` attribute is deliberately deferred until something forces it — e.g. two targets on the same platform kind in one module.) A target's label is also how a tool's `source` block names it (§3.3).
- The `memory` platform is built in: an **ephemeral in-memory store** so plan/apply can be demonstrated and exercised — examples, onboarding, CI — with no credentials and no network. `auth` on it is an error (meaningless fields, again). Its remote objects die with the process, so a later invocation's plan truthfully reports previously applied resources as remote-missing drift.
- A `claude_agents` target may declare `vault_id` — the id of the Anthropic vault (`vlt_…`) holding the credentials its MCP servers reference. It is **required** when any `mcp_server` bound on this target carries a `connection://` auth ref (§3.6), and an error on every other target, `memory` and codegen targets alike (meaningless fields, again). It names a location, not a secret: the vault's contents are created and rotated outside kastor, and kastor only reads enough to verify a reference resolves.

### 3.6 `mcp_server` (project file)

An MCP server the module's tools bind to. Declaring the server is what makes
`mcp://<server>/<tool>` resolvable (§3.3).

```hcl
# Remote server; the target platform holds the credential
mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"
  auth {
    ref = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
  }
}

# Remote server; the credential is in the environment
mcp_server "airtable" {
  url = "https://mcp.airtable.com/mcp"
  auth {
    ref = "env://AIRTABLE_TOKEN"
  }
}

# Local server, spawned by the generated project
mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
```

**Rules:**
- `transport` is a closed enum — `http` | `stdio` — defaulting to `http`. Unknown values are compile errors.
- `http` requires `url` and allows `auth`. `stdio` requires `command`, allows `args` (list of string, default `[]`), and allows no `auth`: a spawned local process inherits the environment that spawned it. Fields meaningless for a transport are errors, not ignored (§3.5's stance).
- Project files carry no expressions (§3.1), so `url`, `command`, and `args` entries are literals.
- Duplicate names within a file are a parse error; module-wide duplicates are owned by module loading, like every other block.
- A declared server no tool references is not an error, the same as an unreferenced `model`. A platform is sent only the servers an agent's tools actually bind.

**Transport support is per target**, mirroring §3.1 and §3.3:

| transport | `langgraph` | `eve` | `claude_agents` |
|-----------|-------------|-------|-----------------|
| `http` | yes | yes | yes |
| `stdio` | yes — spawned by the generated project | error — the connection is an HTTP client; a stdio-only server needs an HTTP bridge in front of it | error — the platform dials a URL |

**Credential references.** `auth.ref` is a URI naming *where a credential lives*.
The scheme set is closed in v0:

| scheme | Meaning | `langgraph` | `eve` | `claude_agents` |
|--------|---------|-------------|-------|-----------------|
| `env://NAME` | The value of environment variable `NAME`, read by whatever dials the server | yes — the generated bridge sends `Authorization: Bearer` from `NAME` at call time | yes — the same, from the connection's headers callback | error — the platform's agent object accepts no credential; use `connection://` |
| `connection://<credential_id>` | A credential the target platform already holds, authenticated out of band | error — no platform holds connections on the codegen path | error | yes — kastor sends the server's name and URL only, and verifies the credential at plan |

- **Kastor is never the credential holder.** It implements no OAuth flow, stores no token, and refreshes nothing. Brokering a token exchange would require a durable secret store, and the only one kastor has is a plaintext state file (§5.1); it would also make `kastor plan`, defined as a pure read (§5.2), an operation that must refresh tokens. Obtaining and refreshing a credential belongs to the environment (`env://`) or to the platform (`connection://`). This is a permanent non-goal, not a v0 deferral.
- **In v0 kastor never reads a credential value.** Each supported (scheme, target) pair is resolve-free: on the codegen path the generated project reads `env://` itself at call time; on the platform path `connection://` is matched by the platform against its own store. The rejected pairs are exactly the ones that would force kastor to read a secret and transmit it.
- **State records the ref, never the resolved value** (§5.1). Changing `env://A` to `env://B` is a visible diff; rotating the secret behind `env://A` is invisible to kastor — correct, because kastor does not manage the secret.
- An unknown scheme is a compile error listing the known ones, so new resolvers are additive (§7).
- `auth` is optional. A server without it is unauthenticated on every target.
- An `auth` block may declare `targets` — a list of `target.<name>` references — exactly as a tool's `source` block does (§3.3). A block without `targets` is the server's default. Two `auth` blocks naming the same target is an error, as is more than one default. A server with **no** `auth` block at all is unauthenticated everywhere, which is the normal case for a public or local server; but once a server declares any `auth` block, every target it is bound on must have a binding — otherwise a server picked up authentication on one path and silently lost it on another. All three are reported by `kastor validate`, naming the server, the target, and the tools bound to it.
- Per-target `auth` is not an optimization. `env://` is codegen-only and `connection://` is platform-only by construction, so a module targeting both paths — the §8 milestone-4 shape — cannot express an authenticated server without it.

```hcl
mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref     = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
    targets = [target.claude_agents]
  }

  auth {
    ref     = "env://HUBSPOT_TOKEN"
    targets = [target.langgraph, target.eve]
  }
}
```

**On `claude_agents`, `connection://` is verified at plan.** The ref names a
credential **id** in the vault the target declares (`vault_id`, §3.5) — not a
display name, which the platform allows to be absent and to repeat. `kastor plan`
fetches it and fails if it does not exist, if it is archived, or if the
credential's own MCP server URL does not equal this block's `url`. So a typo'd,
archived, or misdirected credential is a plan error rather than a failure at the
agent's first tool call. Kastor still sends only the server's name and URL: the
credential stays on the platform and is never read.

**Where connection config lives.** Earlier drafts kept MCP transport entirely out
of the spec: connection details were deployment configuration. That holds on the
codegen path, where kastor's output is a project someone deploys into an
environment with its own config layer. It does not hold on the platform path,
where `kastor apply` *is* the deployment and a server's address has nowhere to
come from but the spec. Reading it from the environment instead made a target's
desired config depend on the operator's shell, wrote an environment-derived value
into the state file, and left drift on that attribute comparing one shell against
another. The line now sits here: a server's **identity, address, and the location
of its credential** are spec; a credential's **value** never is. On the codegen
path only, the generated runtime config may still be overridden locally (§3.3,
`KASTOR_MCP_CONFIG`).
 
---
 
## 4. Dependency & Reference Semantics
 
- **References create the DAG.** `agent.forecast.output.summary` makes `weather` depend on `forecast`. Same rule for `model.*`, `tool.*`, `prompt.*`. Two references are exceptions — `target.<name>` (`source.targets` and `auth.targets`, §3.3 and §3.6) and the server segment of an `mcp://` uri (§3.6): both are resolved and validated like any other reference but create no edge, because neither targets nor MCP servers are nodes in the agent graph.
- **References order and validate; v0 codegen does not move data.** A cross-agent reference is checked at compile time (the output must exist) and orders the DAG, but generated code exposes the referencing input as an ordinary caller-supplied parameter — the caller runs the upstream agent and passes the value. Wiring actual data flow is orchestration, deferred to v1 (§7).
- `depends_on` is the explicit escape hatch for ordering without data flow (Terraform-style).
- Cycles are a compile error.
- Cross-module references (v1): `module.<name>.agent.<name>` — registry/import system deferred.
---
 
## 5. CLI
 
| Command | Function |
|---------|----------|
| `kastor init` | Scaffold project |
| `kastor validate` | Parse + type-check + resolve references + check every tool against every declared target |
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
    "claude_agents": {
      "resources": {
        "agent.weather": {
          "id": "agent-abc123",
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
- **Credential references are stored; credential values never are.** A resource's `config` records an `auth.ref` (§3.6) verbatim — `env://AIRTABLE_TOKEN`, not the token behind it. Nothing in `kastor.state.json` is a secret, and no plan rendering of it can leak one. This is what keeps the state file safe to hand to a colleague while debugging, and it holds for every resolver added later.
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

**Creates are validated, not assumed:** for every planned create the engine calls the provider's `Diff` against an absent remote (§6), so a spec the target cannot express — an unsupported tool source kind, a foreign model provider — is a plan error naming the resource, not an apply-time surprise.

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
    eve/            target: Vercel eve (TypeScript)
    crewai/         target: CrewAI (Python)
  provider/         platform reconcilers
    memory/         built-in in-memory platform (demos, examples, CI)
    claude/         provider: Claude Managed Agents
  state/            state file read/write, locking, diff
```
 
Providers implement a common interface (`Read/Create/Update/Delete/Diff`) — later extractable to a plugin system (go-plugin, like Terraform).

**Provider contract** (`internal/provider`): the engine renders each agent's closure into a neutral, serializable config (a JSON value tree — no core Go types, no provider types), so the interface can move behind a plugin boundary without redesign. Contract rules:

- `Read(id)` reports found=false for a resource deleted outside kastor — that is drift data, not an error.
- `Create(resource)` returns the platform's id; the engine records it in state immediately.
- `Delete(id)` is idempotent: deleting an already-missing remote object succeeds, so re-runs after partial failures converge.
- `Diff(desired, remote)` is the comparison authority — only the provider knows how the neutral config maps onto its platform's attributes. Empty result = in sync. The engine also diffs the last-applied config against the remote for drift detection.
- `Diff` must accept a **nil remote**, meaning the object does not exist on the platform. It then validates the desired config exactly as it would against an existing object — returning an error is how a provider rejects a spec it cannot map — and otherwise returns one attribute diff per attribute a `Create` would set. The engine calls `Diff` this way for every planned create, so a module that cannot apply fails at plan.
- `Diff` must not mutate and must return the same result for the same platform state; `Read` must not mutate either. `kastor plan` issues only these two. `Diff` may issue **additional reads** to validate a desired config against platform-side objects that config references — verifying a `connection://` credential against the target's vault (§3.6) is the first — because rejecting an unsatisfiable spec at plan is precisely what `Diff` against a nil remote exists to do. Such a read must be side-effect free and must report a missing referenced object as a plan error, never as drift.

The plan/apply engine is target-agnostic and consumes exactly what `kastor validate` assembles (loaded module, dependency graph, topological order) plus the state file — the same shape as the codegen engine's `Generate(job)` contract.
 
---
 
## 7. Deferred to v1+
 
- **Memory/state config** on agents (conversation memory, vector stores)
- **Guardrails** blocks (input/output filters, budgets, rate limits)
- Multi-agent orchestration graphs (routing, handoffs) beyond simple references
- Module registry / package management for sharing `.agent`/`.tool` files
- **More credential resolvers** — `vault://`, `op://`, `aws-sm://` alongside v0's `env://` and `connection://` (§3.6). Purely additive: the scheme set is closed and rejects unknown schemes, so each new resolver is an addition within the language version. Each also brings a dependency and an auth story of its own, and would be the first scheme requiring kastor to *read* a secret — so each needs its own design pass. Kastor holding or brokering a credential is **not** on this list; it is a permanent non-goal (§3.6).
- **Per-environment variables** — staging vs. production values for a server's `url` and other spec attributes (the tfvars-shaped problem)
- Remote state backends
---
 
## 8. v0 Milestones
 
1. Parser + `kastor validate` for `.agent`, `.tool`, `.prompt`, project file
2. `kastor build` with **two** codegen targets: LangGraph and eve
3. `kastor plan/apply` with **one** platform provider: Claude Managed Agents
4. Examples repo: the weather agent end-to-end on both paths
Two codegen targets + one provider prove the hybrid thesis. Everything else is expansion.

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
lands after the core thesis is proven (§8 — two codegen targets, one platform
provider). The MCP server and generated docs are v1 milestones, not v0 scope.
