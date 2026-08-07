mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"

  auth {
    ref = "env://TOKEN"
  }
}
