model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "dev" {
  type   = "codegen"
  output = "./gen"
}

# A stdio server is spawned by whatever dials it. An eve connection is an HTTP
# client, so this pair has no mapping (SPEC.md section 3.6 transport matrix).
mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
