model "fast" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "eve" {
  type   = "codegen"
  output = "./gen/eve"
}

target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}

target "claude_agents" {
  type = "platform"

  auth {
    api_key_env = "ANTHROPIC_API_KEY"
  }
}

mcp_server "actions" {
  url = "https://mcp.example.com/actions"
}
