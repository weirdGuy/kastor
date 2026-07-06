model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}

target "fake" {
  type = "platform"
}
