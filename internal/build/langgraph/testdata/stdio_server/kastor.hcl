model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "dev" {
  type   = "codegen"
  output = "./gen"
}

# A stdio server is spawned by the generated project and inherits its
# environment, so it takes no auth block (SPEC.md section 3.6).
mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
