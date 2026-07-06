model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "openai_assistants" {
  type = "platform"
}
