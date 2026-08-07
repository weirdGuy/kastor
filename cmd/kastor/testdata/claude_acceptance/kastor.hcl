model "acceptance" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "claude_agents" {
  type = "platform"

  auth {
    api_key_env = "ANTHROPIC_API_KEY"
  }
}

# The URL is a placeholder: the acceptance run copies this module to a temp
# dir and rewrites it from KASTOR_MCP_KASTOR_ACCEPTANCE_URL, so the server the
# run dials stays a harness input while the spec still owns the address.
mcp_server "kastor-acceptance" {
  url = "https://mcp.invalid/acceptance"
}
