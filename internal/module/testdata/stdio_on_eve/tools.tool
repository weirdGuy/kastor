tool "fetch_url" {
  returns { type = string }

  source {
    kind = "mcp"
    uri  = "mcp://fetch/fetch"
  }
}
