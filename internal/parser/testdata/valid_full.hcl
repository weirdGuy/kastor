kastor {
  required_plugins {
    langgraph = {
      source  = "github.com/getkastor/kastor-langgraph"
      version = "~> 0.1"
    }
    assistants = {
      source  = "example.com/acme/assistants"
      version = "1.2.0"
    }
  }
}

model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}

model "smart" {
  provider = "anthropic"
  id       = "claude-sonnet-5"
}

target "langgraph" {
  type   = "codegen"
  plugin = "langgraph"
  output = "./gen/langgraph"
}

target "openai_assistants" {
  type   = "platform"
  plugin = "assistants"

  config {
    api_key_env = "OPENAI_API_KEY"
  }
}
