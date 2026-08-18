model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

# Invalid: config is meaningless on the in-memory platform and must be an
# error, not ignored.
target "memory" {
  type = "platform"

  config {
    api_key_env = "NOPE"
  }
}
