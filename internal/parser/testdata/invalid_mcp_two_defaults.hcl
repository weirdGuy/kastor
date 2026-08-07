mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref = "env://A"
  }

  auth {
    ref = "env://B"
  }
}
