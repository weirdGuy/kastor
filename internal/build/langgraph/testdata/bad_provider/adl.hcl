model "mystery" {
  provider = "watsonx"
  id       = "granite"
}

target "dev" {
  type   = "codegen"
  output = "./gen"
}
