tool "web_search" {
  param "max_results" {
    type    = number
    default = "ten"
  }

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}
