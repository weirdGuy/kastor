# Kastor

**Kastor is a source-of-truth layer for AI agents.**

Define agents, tools, prompts, models, and targets in HCL. Validate the spec. Compile it to runnable framework code, or reconcile hosted agents with Terraform-style `plan` / `apply` / `state`.

```sh
kastor validate examples/weather
kastor build examples/weather
kastor plan examples/weather
```

Agents today are often split across framework code, prompt files, tool files, platform UI settings, and environment configuration. Kastor's idea is that agents need a versionable, reviewable, declarative contract before they become serious software.

The full design lives in [SPEC.md](SPEC.md).

## Status

Kastor is an early proof of concept.

Working today:

- scaffold a new module with `kastor init`
- parse `.agent`, `.tool`, `.prompt`, and `kastor.hcl`
- validate references and prompt variables
- build runnable LangGraph and eve projects
- run `kastor plan` / `kastor apply` / `kastor destroy` against the built-in in-memory platform
- reconcile hosted [Claude Managed Agents](#quickstart-hosted-claude-agents) with `target "claude_agents"`
- local state file, three-way diffs, and drift detection
- examples: [weather agent](examples/weather), [content scheduler](examples/scheduler), [support triage](examples/support-triage)

Planned for v0:

- structured `--json` rendering for diagnostics and plans

Kastor is **not** an agent runtime.

## Demo

![Kastor building the agent from files](./docs/assets/demo-1.gif)

## How it works

```text
.agent + .tool + .prompt + kastor.hcl
                │
                ▼
        kastor validate
                │
      ┌─────────┴─────────┐
      ▼                   ▼
kastor build        kastor plan/apply
framework code      hosted agents
(LangGraph, eve)    (Claude Managed Agents)
```

Kastor has two paths:

- `kastor build` compiles a Kastor module into runnable framework code.
- `kastor plan` / `kastor apply` reconciles long-lived hosted agents with state, diffs, and drift detection.

## Example

An agent in Kastor is a small declarative spec:

```hcl
agent "weather" {
  description = "Answers weather questions for a location and date"

  model         = model.fast
  system_prompt = prompt.weather_system
  tools         = [tool.web_search]

  input "location" {
    type        = string
    description = "The location to get weather for"
  }

  input "date" {
    type     = string
    optional = true
  }

  output "weather" {
    type = string
  }
}
```

The generated code is not the source of truth. The Kastor module is.

## Quickstart: start your own module

`kastor init` scaffolds a minimal working module — one agent, one MCP tool, one prompt, a model, and a LangGraph codegen target — that validates and builds with zero edits:

```sh
kastor init demo
cd demo
kastor validate
kastor build
```

The scaffolded agent answers a question by fetching web pages through the reference MCP fetch server (run via [`uvx`](https://docs.astral.sh/uv/), no API key needed). The scaffold's `README.md` walks through running the generated project end to end.

`init` refuses a directory that already contains visible files; `--force` overwrites only the scaffold's own file names and keeps everything else.

## Quickstart: no credentials required

This path validates the example and runs `plan` / `apply` against the built-in in-memory platform target. It does not create remote resources and does not require API keys.

```sh
go build -o kastor ./cmd/kastor
./kastor validate examples/weather/
./kastor plan examples/weather/
./kastor apply examples/weather/
```

Example plan output:

```console
$ kastor plan examples/weather/
  + agent.forecast (not in state)
  + agent.geocoder (not in state)
  + agent.weather (not in state)

Plan for target.memory: 3 to create, 0 to update, 0 to delete, 0 unchanged.
```

`kastor plan` is a pure read: it never touches remote resources or the state file. Updates show attribute-level diffs, and out-of-band remote changes surface as drift warnings.

## Quickstart: hosted Claude agents

This is the hosted path: `target "claude_agents"` reconciles agents in your
Anthropic organization through Claude Managed Agents. Unlike `memory`, `apply`
here creates real remote objects, and `destroy` **archives them irreversibly** —
read [Destroying a Claude agent](#destroying-a-claude-agent) before you run it.

Prerequisites:

- an Anthropic API key with access to Managed Agents
- an endpoint URL for every MCP server the module's `mcp` tools name

Write a module — one project file, one agent, two tools, one prompt:

```hcl
# kastor.hcl
model "haiku" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "claude_agents" {
  type = "platform"

  auth {
    api_key_env = "ANTHROPIC_API_KEY"
  }
}
```

```hcl
# researcher.agent
agent "researcher" {
  description = "Answers research questions with web search and a hosted MCP server"

  model         = model.haiku
  system_prompt = prompt.researcher_system

  tools = [tool.web_search, tool.tavily_search]
}
```

```hcl
# research.tool
tool "web_search" {
  description = "Claude's hosted web search"

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}

tool "tavily_search" {
  description = "Search the web through Tavily's hosted MCP server"

  param "query" {
    type = string
  }

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://search-server/tavily_search"
  }
}
```

```text
# researcher_system.prompt
---
name     = "researcher_system"
requires = []
---
You are a research assistant. Answer concisely and cite your sources.
```

Two kinds of environment variable:

```sh
export ANTHROPIC_API_KEY=sk-ant-YOUR-KEY
export KASTOR_MCP_SEARCH_SERVER_URL="https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-YOUR-KEY"
```

`ANTHROPIC_API_KEY` is the default credential; the `auth` block above only names
it explicitly. Any other variable works — `api_key_env = "ANTHROPIC_API_KEY_PROD"`
— and the `auth` block may be omitted entirely.

MCP endpoints stay out of the spec, exactly as they do for codegen: the `mcp://`
URI pins server and tool identity only. For each server named in a URI, kastor
reads `KASTOR_MCP_<SERVER>_URL` — the server name uppercased, with every
character outside `A-Z0-9` replaced by `_`. So `mcp://search-server/tavily_search`
needs `KASTOR_MCP_SEARCH_SERVER_URL`. A missing one fails the plan naming the
variable it wanted.

Plan, then apply:

```console
$ kastor plan
  + agent.researcher (not in state)

Plan for target.claude_agents: 1 to create, 0 to update, 0 to delete, 0 unchanged.

$ kastor apply
  + agent.researcher (not in state)

Plan for target.claude_agents: 1 to create, 0 to update, 0 to delete, 0 unchanged.

Applied target.claude_agents: 1 created, 0 updated, 0 deleted.
```

Kastor writes the remote id to `kastor.state.json` and stamps
`metadata.kastor_managed = "agent.researcher"` on the remote agent. That marker is
an ownership assertion: kastor refuses to compare — and therefore to update — a
remote agent that does not carry it, so an agent someone created in the Console
can never be silently overwritten by an apply.

Edit the spec and re-apply, and the change lands as an attribute-level update.
Change the agent in the Console instead, and the next `plan` reports drift and
plans the update that converges it back to the spec.

### What `claude_agents` does not support

The Managed Agents resource is narrower than the Kastor agent block, and Kastor
treats fields that are meaningless for a target as errors rather than ignoring
them (SPEC.md §3.5). On this target:

| Spec | Result |
| --- | --- |
| `source` kind `http`, `script`, or `runtime` | Error. These are client-executed tools, and kastor is not a runtime. Wrap the implementation in an MCP server and declare it `kind = "mcp"`. |
| `source` kind `builtin` outside the hosted toolset | Error. The hosted set is `bash`, `edit`, `glob`, `grep`, `read`, `web_fetch`, `web_search`, `write`. |
| `model` with `provider` other than `"anthropic"` | Error. |
| `params { temperature = ... }`, `max_tokens`, anything but `speed` | Error. The platform's model object exposes `id` and `speed` only, so `speed` is the one param that maps; omitted, it is `standard`. |
| `input` / `output` blocks | Sent nowhere. Managed Agents has no IO-contract field; the blocks stay valid spec and still drive references and validation, but they are not part of the remote object and never appear in a diff. |

These are **plan-time** errors, not validation errors — see the caveat below.

### Destroying a Claude agent

`kastor destroy` deletes the state entry and **archives** the remote agent.
Archive is irreversible on this platform: the agent cannot be restored, and it
stays listed in the Anthropic Console permanently. A later `kastor apply` does
not resurrect it — it creates a new agent with a new id.

`destroy` reads before archiving, so an agent that is already gone or already
archived is a no-op, and an archived agent reads as absent — which is why a plan
after destroy proposes a create rather than an update.

`destroy` does not prompt for confirmation in v0. On this target, run
`kastor plan` first and read the `-` lines.

### One caveat: these errors arrive at plan, not validate

`kastor validate` is target-agnostic — it parses, resolves references, and checks
prompt variables, and it knows nothing about any provider. The table above is
enforced by the provider, when it renders an agent for the platform.

That rendering happens during `plan`, for every resource — including one kastor
has not created yet, where there is no remote object to compare against. So a
module that could never apply fails the plan, naming the resource and the target:

```console
$ kastor validate
Success! Module is valid: 1 agent, 1 tool, 1 prompt, 1 model, 1 target.

$ kastor plan
kastor: agent.probe: cannot be created on target.claude_agents: tool.rest: source kind "http" cannot be mapped to Claude Managed Agents; custom tools are client-executed and kastor is not a runtime; use an MCP-server wrapper with source kind "mcp"

$ echo $?
1
```

A clean plan therefore does mean "this module maps onto this target". What it
still cannot promise is that the platform will accept it — credentials, quotas,
and model availability are only known to the API. When one of those fails, apply
stops at the first failure and state records everything applied before it, so a
re-run plans exactly the remainder.

## Quickstart: generate and run LangGraph

This path compiles the weather agent to a runnable LangGraph project.

Prerequisites:

- Go 1.26+
- Python 3.11+
- an OpenAI API key
- a [Tavily](https://tavily.com) API key, because the example's search tool runs against Tavily's hosted MCP server

Compile the spec to a LangGraph project:

```sh
go build -o kastor ./cmd/kastor
./kastor validate examples/weather/
./kastor build examples/weather/
```

`kastor build` writes the generated project to `examples/weather/gen/langgraph` — the target's declared `output`.

Generated output is not committed. It is reproducible from the spec, and codegen determinism is enforced by tests.

Set up the generated project:

```sh
cd examples/weather/gen/langgraph
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
```

The example's `web_search` tool is pinned to an MCP server and tool by its spec URI:

```text
mcp://search-server/tavily_search
```

How to reach that server is deployment configuration, not spec. Create `mcp_servers.json` in the generated project's working directory, or point the `KASTOR_MCP_CONFIG` environment variable at a file elsewhere.

For Tavily's hosted server:

```json
{
  "search-server": {
    "transport": "streamable_http",
    "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-YOUR-KEY"
  }
}
```

The URL embeds your API key, which is why `mcp_servers.json` is gitignored. Treat it as a secret and never commit it.

The spec URI's last path segment, `tavily_search`, must name a tool the server actually advertises. If it does not, calls fail with `does not expose tool`.

Export the model credential. The example's `model "fast"` block uses provider `openai`:

```sh
export OPENAI_API_KEY=sk-...
```

Run the agent:

```sh
python3 main.py weather --inputs '{"location": "Lisbon", "date": "tomorrow"}'
```

It prints the agent's declared output contract as JSON:

```json
{
  "weather": "..."
}
```

The generated `README.md` inside `gen/langgraph` owns the run-the-project side in full: every agent's inputs and outputs, tool bindings, and MCP configuration.

One v0 caveat: `agent.weather`'s optional `forecast_context` input references `agent.forecast`'s output. That reference is validated at compile time and orders the dependency graph, but generated code does not run the upstream agent for you. If you want the context, run `forecast` yourself and pass its summary via `--inputs`.

## File types

A Kastor module is a directory tree containing declarative files:

| File type | Purpose |
| --- | --- |
| `.agent` | Agent definitions: model, prompt, tools, inputs, outputs, dependencies |
| `.tool` | Tool interface plus implementation source |
| `.prompt` | Prompt template plus required variables |
| `kastor.hcl` / `*.kastor` | Project file: models, targets, defaults |

References connect blocks by address, not by file path. For example, an agent references `model.fast`, `prompt.weather_system`, and `tool.web_search`.

References also build the dependency graph. A reference like `agent.forecast.output.summary` validates that the output exists and orders the graph.

## What Kastor is not

Kastor is not an agent runtime.

Frameworks like LangGraph still execute agents. Hosted platforms like Dify still run managed agents. Kastor sits above them as the declarative source-of-truth layer: model, prompts, tools, inputs, outputs, dependencies, and targets.

Kastor also does not try to standardize the full behavior or control loop of an agent. That layer is still changing quickly. The narrower bet is that the outer contract around agents should be reviewable, versionable, and diffable.

## Why not Terraform?

Terraform is great for managing remote resources. A Terraform provider for hosted agents may make sense later.

Kastor starts one layer earlier: the agent spec itself.

The same Kastor module should be able to:

- generate runnable framework code with `kastor build`
- reconcile hosted platform agents with `kastor plan` / `kastor apply`

That codegen path is why Kastor is a separate toolchain rather than only a Terraform module or provider.

## Why not just LangGraph?

LangGraph is a runtime/framework. Kastor is not trying to replace it.

Kastor defines the agent contract and generates a LangGraph project from that spec. The generated code is an output; the Kastor module is the source of truth.

## Install

Homebrew (macOS):

```sh
brew install weirdGuy/tap/kastor
```

Install script:

```sh
curl -fsSL https://raw.githubusercontent.com/weirdGuy/kastor/main/scripts/install.sh | sh
```

The install script verifies the release checksum, installs to `/usr/local/bin` or `~/.local/bin`, and never uses `sudo`.

With Go 1.26+:

```sh
go install github.com/weirdGuy/kastor/cmd/kastor@latest
```

Or download an archive for your platform from the [releases page](https://github.com/weirdGuy/kastor/releases), verify it against `checksums.txt`, and put the `kastor` binary on your PATH.

## Development

```sh
go build ./...   # build everything
go test ./...    # run all tests
go vet ./...     # static checks
gofmt -l .       # formatting check
```

SPEC.md is the source of truth for design decisions. CLAUDE.md documents the day-to-day development conventions.

## Early feedback

I'm currently looking for feedback from people building agents in production or experimenting with agent tooling.

Useful feedback areas:

- whether the source-of-truth layer makes sense
- where the spec is too rigid or too loose
- what framework or hosted platform target should come next
- what would make the first-run experience smoother

To follow or discuss the project:

- star/watch the repo for updates
- open an issue for bugs or design feedback
- join the early Discord: [invite](https://discord.gg/bb4UwtJFe)
