kastor {
  required_plugins {
    langgraph = {
      source  = "github.com/getkastor/kastor-langgraph"
      version = "~> 0.1"
    }
  }
}

model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}

# Codegen target -> `kastor build` emits a runnable LangGraph project
target "langgraph" {
  type   = "codegen"
  plugin = "langgraph"
  output = "./gen/langgraph"
}

# The MCP server tool.fetch_url binds to. A stdio server is spawned by the
# generated project and inherits its environment, so it takes no auth block.
mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
