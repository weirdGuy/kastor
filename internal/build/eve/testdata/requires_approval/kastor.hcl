model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "eve" {
  type   = "codegen"
  output = "./gen"
}
