model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"

  params {
    temperature = 0.2
    max_tokens  = 4096
  }
}

# Codegen target -> exercises the module-walk skip of target output paths (#6)
target "langgraph" {
  type   = "codegen"
  output = "./gen/langgraph"
}

# Platform target -> `kastor plan` / `kastor apply` against the built-in
# ephemeral in-memory platform: no credentials, no network. Swap for a real
# platform target (e.g. "openai_assistants", issue #16) once one ships.
target "memory" {
  type = "platform"
}
