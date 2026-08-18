model "fast" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "claude_agents" {
  type = "platform"

  config {
    api_key_env = "KASTOR_DOCTOR_TEST_KEY"
    vault_id    = "vlt_011CZaBcDeFgHiJkLmNoPqRs"
  }
}

target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}

# The mixed-module shape per-target auth exists for: connection:// is
# platform-only and env:// is codegen-only, so one server authenticated on both
# paths cannot be expressed without them.
mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref     = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
    targets = [target.claude_agents]
  }

  auth {
    ref     = "env://HUBSPOT_TOKEN"
    targets = [target.langgraph]
  }
}

# A public server: no auth block, so the connection is unauthenticated by
# declaration rather than by accident.
mcp_server "docs" {
  url = "https://mcp.example.com/docs"
}
