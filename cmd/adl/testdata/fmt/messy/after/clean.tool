tool "clean" {
  description = "Already formatted"

  param "q" {
    type = string
  }

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}
