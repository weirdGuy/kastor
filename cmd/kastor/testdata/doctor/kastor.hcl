model "fast" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "claude_agents" {
  type     = "platform"
  vault_id = "vlt_011CZaBcDeFgHiJkLmNoPqRs"

  auth {
    api_key_env = "KASTOR_DOCTOR_CLI_KEY"
  }
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
  }
}
