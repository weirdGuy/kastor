target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"

  auth {
    api_key_env = "OPENAI_API_KEY"
  }
}
