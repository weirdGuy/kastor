model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}

# Codegen target -> exercises the module-walk skip of target output paths (#6)
target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}

# Codegen target -> `kastor build` emits one eve project per root agent
target "eve" {
  type   = "codegen"
  output = "./gen/eve"
}

# Platform target -> `kastor plan` / `kastor apply` against the built-in
# ephemeral in-memory platform: no credentials, no network. Swap for
# `target "claude_agents"` once the Claude Managed Agents provider ships
# (SPEC.md section 8).
target "memory" {
  type = "platform"
}

# The MCP server tool.web_search binds to. Declaring it is what makes
# mcp://search-server/<tool> resolvable (SPEC.md section 3.6). The auth block
# names *where* the credential lives -- kastor never reads the value, and
# `kastor doctor` reports the variable as unset if it is.
mcp_server "search-server" {
  url = "https://mcp.tavily.com/mcp"

  auth {
    ref = "env://TAVILY_API_KEY"
  }
}
