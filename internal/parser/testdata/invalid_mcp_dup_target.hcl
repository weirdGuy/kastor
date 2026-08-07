target "langgraph" {
  type   = "codegen"
  output = "./gen"
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref     = "env://A"
    targets = [target.langgraph]
  }

  auth {
    ref     = "env://B"
    targets = [target.langgraph]
  }
}
