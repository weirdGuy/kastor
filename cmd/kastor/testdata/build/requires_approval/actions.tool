tool "safe_read" {
  description = "Read a record"

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://actions/read_record"
  }
}

tool "publish" {
  description = "Publish a record"

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://actions/publish_record"
  }
}
