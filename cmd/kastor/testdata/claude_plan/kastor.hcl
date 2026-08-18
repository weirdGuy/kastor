model "haiku" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "claude_agents" {
  type = "platform"

  config {
    api_key_env = "ANTHROPIC_API_KEY"
  }
}
