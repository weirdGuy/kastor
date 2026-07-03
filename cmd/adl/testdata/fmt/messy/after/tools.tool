tool "web_search" {
  description = "Search the web"

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
