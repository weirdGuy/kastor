# Kastor

Kastor is "Terraform for AI agents." Agents today are defined imperatively inside frameworks (LangGraph, CrewAI) or clicked together in platform UIs (OpenAI Assistants, Bedrock Agents) — there is no vendor-neutral, versionable, reviewable source of truth. Kastor provides one: a typed, declarative spec (`.agent`, `.tool`, `.prompt` files in HCL) and a Go toolchain with two paths — `kastor build` generates runnable projects for target frameworks, and `kastor plan` / `kastor apply` reconcile agents as long-lived resources on hosted platforms, with state, diffs, and drift detection.

The full design lives in [SPEC.md](SPEC.md).

## Status

Kastor is an early proof of concept.

Working today:
- parse `.agent`, `.tool`, `.prompt`, and `kastor.hcl`
- validate references and prompt variables
- build runnable LangGraph projects
- `kastor plan` / `kastor apply` / `kastor destroy` against the built-in in-memory platform — local state file, three-way diffs, drift detection
- examples: [weather agent](https://github.com/weirdGuy/kastor/tree/main/examples/weather), [content scheduler](https://github.com/weirdGuy/kastor/tree/main/examples/scheduler) agent

Planned for v0:
- hosted platform providers (OpenAI Assistants first, then AWS/Azure)

_This is not another agent runtime/framework._

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

And `kastor plan` shows what `kastor apply` would change on a platform target — a three-way comparison of the spec, the state file, and the remote platform. The weather example targets the built-in in-memory platform, so it works with no credentials:

```console
$ kastor plan examples/weather/
  + agent.forecast (not in state)
  + agent.geocoder (not in state)
  + agent.weather (not in state)

Plan for target.memory: 3 to create, 0 to update, 0 to delete, 0 unchanged.
```

Plan is a pure read — it never touches remote resources or the state file. Updates show attribute-level diffs, and out-of-band remote changes surface as drift warnings.

## Install

Homebrew:

```sh
brew tap weirdGuy/tap && brew install kastor
```

Install script (verifies the release checksum, installs to `/usr/local/bin` or `~/.local/bin`, never sudo):

```sh
curl -fsSL https://raw.githubusercontent.com/weirdGuy/kastor/main/scripts/install.sh | sh
```

With Go 1.26+:

```sh
go install github.com/weirdGuy/kastor/cmd/kastor@latest
```

Or download an archive for your platform from the [releases page](https://github.com/weirdGuy/kastor/releases), verify it against `checksums.txt`, and put the `kastor` binary on your PATH.

## Quickstart: build the weather example

Prerequisites: Go 1.26+, Python 3.11+, an OpenAI API key, and a [Tavily](https://tavily.com) API key (the example's search tool runs against Tavily's hosted MCP server).

Compile the spec to a LangGraph project:

```sh
go build ./cmd/kastor
./kastor validate examples/weather/
./kastor build examples/weather/
```

`kastor build` writes the generated project to `examples/weather/gen/langgraph` (the target's declared `output`). Generated output is not committed: it is reproducible from the spec, and codegen determinism is enforced by tests.

Set up the generated project:

```sh
cd examples/weather/gen/langgraph
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
```

The example's `web_search` tool is pinned to an MCP server and tool by its spec URI, `mcp://search-server/tavily_search`. How to *reach* that server is deployment configuration, not spec: create `mcp_servers.json` in the working directory (or point the `KASTOR_MCP_CONFIG` env var at a file elsewhere). For Tavily's hosted server:

```json
{
  "search-server": {
    "transport": "streamable_http",
    "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-YOUR-KEY"
  }
}
```

The URL embeds your API key, which is why `mcp_servers.json` is gitignored — treat it as a secret, never commit it. Also note the spec URI's last path segment (`tavily_search`) must name a tool the server actually advertises, or calls fail with "does not expose tool".

Export the model credential (the example's `model "fast"` block uses provider `openai`):

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

One v0 caveat (SPEC.md §3.2/§4): `agent.weather`'s optional `forecast_context` input references `agent.forecast`'s output. That reference is validated at compile time and orders the dependency graph, but generated code does not run the upstream agent for you — if you want the context, run `forecast` yourself and pass its summary via `--inputs`.

## Development

```sh
go build ./...   # build everything
go test ./...    # run all tests
```

SPEC.md is the source of truth for design decisions; CLAUDE.md documents the day-to-day conventions.

## Early feedback

I'm currently looking for feedback from people building agents in production or experimenting with agent tooling.

If you want to follow the project or discuss the design:

- Star/watch the repo for updates
- Open an issue for bugs or design feedback
- Join the early Discord: [invite](https://discord.gg/bb4UwtJFe)
