model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "langgraph" {
  type   = "codegen"
  output = "./gen"
}

target "claude_agents" {
  type = "platform"

  config {
    vault_id = "vlt_011CZaBcDeFgHiJkLmNoPqRs"
  }
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref     = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
    targets = [target.claude_agents]
  }
}
