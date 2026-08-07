tool "fetch_url" {
  description = "Fetch a web page"

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://fetch/fetch"
  }
}
