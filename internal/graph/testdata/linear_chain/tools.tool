tool "t" {
  description = "A tool"

  param "query" {
    type = string
  }

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}
