model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

# Referenced by no agent: skipped on the eve target, listed in the README.
model "spare" {
  provider = "anthropic"
  id       = "claude-haiku-4-5"
}

target "dev" {
  type   = "codegen"
  output = "./gen"
}
