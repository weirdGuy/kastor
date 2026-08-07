# Your Kastor module

A starter module scaffolded by `kastor init`: one agent that answers a
question by fetching web pages through an MCP tool. It validates and builds
with zero edits — make it yours from there.

| File | Purpose |
|------|---------|
| `kastor.hcl` | the model, the LangGraph codegen target, and the `fetch` MCP server |
| `researcher.agent` | the agent: typed input (`question`), output (`answer`), tool list |
| `fetch_url.tool` | tool interface backed by the MCP server tool `mcp://fetch/fetch` |
| `researcher_system.prompt` | the system prompt; requires exactly the agent's inputs |

## Validate and build

```sh
kastor validate
kastor build
```

Build writes a runnable LangGraph (Python) project to `gen/langgraph/`.
Generated output is reproducible — don't edit or commit it; edit the spec
and rebuild.

## Run the generated agent

Requires Python 3.11+, [`uvx`](https://docs.astral.sh/uv/) (runs the
reference MCP fetch server the `mcp_server "fetch"` block declares), and an
OpenAI API key (`model "fast"` is `openai` / `gpt-4o-mini` — swap the
provider in `kastor.hcl` and rebuild to use another vendor).

The build writes the server's connection config to
`gen/langgraph/mcp_servers.json`, so there is nothing to configure by hand:
the `fetch` server needs no credential, since a stdio server inherits the
environment that spawns it.

```sh
cd gen/langgraph
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt

export OPENAI_API_KEY=sk-...
python main.py researcher --inputs '{"question": "What is HCL and who maintains it?"}'
```

The agent returns its declared outputs as structured JSON:

```json
{
  "answer": "HCL (HashiCorp Configuration Language) is ..."
}
```

## Next steps

Quickstart and language reference: https://docs.getkastor.dev
