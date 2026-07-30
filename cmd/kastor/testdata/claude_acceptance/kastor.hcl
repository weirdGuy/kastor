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
