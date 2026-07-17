# Your Kastor module

A starter module scaffolded by `kastor init`: one agent that answers a
question by fetching web pages through an MCP tool. It validates and builds
with zero edits — make it yours from there.

| File | Purpose |
|------|---------|
| `kastor.hcl` | the model and the LangGraph codegen target |
| `researcher.agent` | the agent: typed input (`question`), output (`answer`), tool list |
| `fetch_url.tool` | tool interface backed by the MCP server tool `mcp://fetch/fetch` |
| `researcher_system.prompt` | the system prompt; requires exactly the agent's inputs |
| `mcp_servers.json` | runtime connection details for the `fetch` MCP server |

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
reference MCP fetch server declared in `mcp_servers.json`), and an OpenAI
API key (`model "fast"` is `openai` / `gpt-4o-mini` — swap the provider in
`kastor.hcl` and rebuild to use another vendor).

```sh
cd gen/langgraph
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt

export OPENAI_API_KEY=sk-...
export KASTOR_MCP_CONFIG=../../mcp_servers.json
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
