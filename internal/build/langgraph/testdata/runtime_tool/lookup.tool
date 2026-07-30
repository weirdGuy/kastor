# A tool whose body is the user's code: the generated file is a stub that
# kastor writes once and preserves after that (SPEC.md §3.3).
tool "lookup" {
  description = "Look a city up in the user's own data store"

  param "city" {
    type        = string
    description = "The city to look up"
  }

  returns {
    type = string
  }

  source {
    kind = "runtime"
  }
}
