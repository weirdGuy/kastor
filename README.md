# Kastor

**Kastor is a source-of-truth layer for AI agents.**

Define agents, tools, prompts, models, and targets in HCL. Validate the spec. Compile it to runnable framework code. Later, reconcile hosted agents with Terraform-style `plan` / `apply` / `state`.

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

- parse `.agent`, `.tool`, `.prompt`, and `kastor.hcl`
- validate references and prompt variables
- build runnable LangGraph projects
- run `kastor plan` / `kastor apply` / `kastor destroy` against the built-in in-memory platform
- local state file, three-way diffs, and drift detection
- examples: [weather agent](examples/weather), [content scheduler](examples/scheduler)

Planned for v0:

- hosted platform providers

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
(LangGraph)         (platform targets)
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

Frameworks like LangGraph still execute agents. Hosted platforms like OpenAI Assistants still run managed agents. Kastor sits above them as the declarative source-of-truth layer: model, prompts, tools, inputs, outputs, dependencies, and targets.

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

Homebrew:

```sh
brew tap weirdGuy/tap && brew install kastor
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
