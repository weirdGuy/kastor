tool "crm_search" {
  returns { type = string }

  source {
    kind = "mcp"
    uri  = "mcp://hubspot/search"
  }
}

tool "base_read" {
  returns { type = string }

  source {
    kind = "mcp"
    uri  = "mcp://airtable/read"
  }
}
