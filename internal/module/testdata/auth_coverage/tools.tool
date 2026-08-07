tool "crm_search" {
  returns { type = string }

  source {
    kind = "mcp"
    uri  = "mcp://hubspot/search"
  }
}
