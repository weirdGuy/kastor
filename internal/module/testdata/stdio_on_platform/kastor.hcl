kastor {
  required_plugins {
    anthropic = {
      source  = "github.com/getkastor/kastor-anthropic"
      version = "~> 0.1"
    }
  }
}

model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "prod" {
  type   = "platform"
  plugin = "anthropic"
}

mcp_server "fetch" {
  transport = "stdio"
  command   = "uvx"
  args      = ["mcp-server-fetch"]
}
