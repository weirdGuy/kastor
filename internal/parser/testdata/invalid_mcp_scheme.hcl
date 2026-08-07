mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref = "vault://secret/hubspot"
  }
}
