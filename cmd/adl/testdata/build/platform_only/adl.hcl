target "openai_assistants" {
  type = "platform"

  auth {
    api_key_env = "OPENAI_API_KEY"
  }
}
