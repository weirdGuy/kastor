# ollama is a local runtime the AI Gateway cannot route: valid spec, no eve
# codegen mapping.
model "local" {
  provider = "ollama"
  id       = "llama3"
}

target "dev" {
  type   = "codegen"
  output = "./gen"
}
