kastor {
  required_plugins {
    anthropic = {
      source  = "github.com/getkastordev/kastor-anthropic"
      version = "~> 0.1"
    }
  }
}

model "fast" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "prod" {
  type   = "platform"
  plugin = "anthropic"

  config {
    api_key_env = "ANTHROPIC_API_KEY"
  }
}
