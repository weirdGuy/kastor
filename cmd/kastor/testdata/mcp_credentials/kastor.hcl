model "fast" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

# Two platform targets, so one module exercises both credential schemes at
# once -- the multi-target shape SPEC.md section 3.6 exists for. Both are
# served by fake providers in the test, so nothing here dials anything.
target "fake" {
  type = "platform"
}

target "claude_agents" {
  type = "platform"

  config {
    vault_id = "vlt_011CZkZDLs7fYzm1hXNPeRjvVAULT"
  }
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  # env:// on the codegen-shaped path: the value lives in the environment and
  # kastor never reads it.
  auth {
    ref     = "env://KASTOR_TEST_HUBSPOT_TOKEN"
    targets = [target.fake]
  }

  # connection:// on the platform path: the platform already holds the
  # credential, and `kastor plan` / `kastor apply` never contact the vault.
  auth {
    ref     = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
    targets = [target.claude_agents]
  }
}
