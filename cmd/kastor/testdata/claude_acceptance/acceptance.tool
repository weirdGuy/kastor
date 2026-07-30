tool "read" {
  description = "Read a file from the managed agent workspace"

  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}

tool "echo" {
  description = "Echo a message through the acceptance MCP server"

  param "message" {
    type        = string
    description = "Message to echo"
  }

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://kastor-acceptance/echo"
  }
}
