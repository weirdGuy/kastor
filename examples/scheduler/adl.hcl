model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
  params {
    temperature = 0.2
  }
}

target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}
