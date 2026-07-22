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
  output = "./gen/langgraph"
}

# Codegen target -> `kastor build` emits one eve project per root agent
target "eve" {
  type   = "codegen"
  output = "./gen/eve"
}
