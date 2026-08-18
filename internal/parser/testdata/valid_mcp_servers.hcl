target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}

target "claude_agents" {
  type = "platform"

  config {
    vault_id = "vlt_011CZaBcDeFgHiJkLmNoPqRs"
  }
}

# Remote server, credential held by the platform on one path and by the
# environment on the other -- the mixed-module shape per-target auth exists for.
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

# Local server, spawned by the generated project: no auth, no url.
mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
