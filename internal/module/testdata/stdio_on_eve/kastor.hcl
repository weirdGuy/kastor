kastor {
  required_plugins {
    eve = {
      source  = "github.com/getkastordev/kastor-eve"
      version = "~> 0.1"
    }
  }
}

model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "typescript" {
  type   = "codegen"
  plugin = "eve"
  output = "./gen/eve"
}

mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
