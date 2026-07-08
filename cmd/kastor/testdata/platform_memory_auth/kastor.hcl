model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

# Invalid: auth is meaningless on the in-memory platform and must be an
# error, not ignored.
target "memory" {
  type = "platform"

  auth {
    api_key_env = "NOPE"
  }
}
