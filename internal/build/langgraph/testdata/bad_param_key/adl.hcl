model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    max-tokens = 4096
  }
}

target "dev" {
  type   = "codegen"
  output = "./gen"
}
