model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "langgraph" {
  type   = "codegen"
  output = "./gen"
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref     = "env://HUBSPOT_TOKEN"
    targets = [target.ghost]
  }
}
