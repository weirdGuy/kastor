tool "web_search" {
  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://search-server"
  }
}
