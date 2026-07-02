tool "web_search" {
  param "query" {
    type    = string
    default = null
  }

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}
