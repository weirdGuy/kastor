model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "claude_agents" {
  type = "platform"
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
  }
}

mcp_server "airtable" {
  url = "https://mcp.airtable.com/mcp"

  auth {
    ref = "env://AIRTABLE_TOKEN"
  }
}
