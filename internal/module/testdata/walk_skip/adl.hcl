model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "langgraph" {
  type   = "codegen"
  output = "./gen"
}
