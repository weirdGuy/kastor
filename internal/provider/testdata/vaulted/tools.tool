tool "crm_search" {
  description = "Search the CRM"

  param "query" {
    type = string
  }

  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://hubspot/search"
  }
}

tool "docs_lookup" {
  returns {
    type = string
  }

  source {
    kind = "mcp"
    uri  = "mcp://docs/lookup"
  }
}
