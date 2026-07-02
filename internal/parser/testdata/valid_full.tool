tool "web_search" {
  description = "Search the web"

  param "query" {
    type        = string
    description = "The query to search for"
  }

  param "max_results" {
    type    = number
    default = 10
  }

  param "include_images" {
    type    = bool
    default = false
  }

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://search-server/web_search"
  }
}
