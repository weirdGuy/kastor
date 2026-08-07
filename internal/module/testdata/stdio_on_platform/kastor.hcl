model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "claude_agents" {
  type = "platform"
}

mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
