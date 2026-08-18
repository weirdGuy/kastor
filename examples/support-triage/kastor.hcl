kastor {
  required_plugins {
    langgraph = {
      source  = "github.com/getkastor/kastor-langgraph"
      version = "~> 0.1"
    }
    eve = {
      source  = "github.com/getkastor/kastor-eve"
      version = "~> 0.1"
    }
  }
}

model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    temperature = 0.1
    max_tokens  = 1024
  }
}

# Codegen target -> `kastor build` emits a runnable LangGraph project
target "langgraph" {
  type   = "codegen"
  plugin = "langgraph"
  output = "./gen/langgraph"
}

# Codegen target -> `kastor build` emits one eve project per root agent
target "eve" {
  type   = "codegen"
  plugin = "eve"
  output = "./gen/eve"
}
