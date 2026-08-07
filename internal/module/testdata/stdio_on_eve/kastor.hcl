model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "eve" {
  type   = "codegen"
  output = "./gen/eve"
}

mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
