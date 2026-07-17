tool "fetch_url" {
  description = "Fetch a URL and return the page contents as markdown"

  param "url" {
    type        = string
    description = "The absolute URL to fetch"
  }

  returns {
    type = string
  }

  # The uri pins identity only: mcp://<server>/<tool>. How to reach the
  # server (command, endpoint) is deployment config -- see mcp_servers.json.
  source {
    kind = "mcp"
    uri  = "mcp://fetch/fetch"
  }
}
