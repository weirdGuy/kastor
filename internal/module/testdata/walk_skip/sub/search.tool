tool "search" {
  description = "Search"

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
