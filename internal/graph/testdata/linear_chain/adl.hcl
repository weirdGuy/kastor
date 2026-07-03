model "m" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "dev" {
  type   = "codegen"
  output = "./gen/dev"
}
