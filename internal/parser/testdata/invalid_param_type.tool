tool "web_search" {
  param "query" {
    type = integer
  }

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}
