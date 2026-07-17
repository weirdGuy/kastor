model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}

# Codegen target -> `kastor build` emits a runnable LangGraph project
target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}
