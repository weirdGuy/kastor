kastor {
  required_plugins {
    anthropic = {
      source  = "github.com/getkastor/kastor-anthropic"
      version = "~> 0.1"
    }
  }
}

model "fast" {
  provider = "openai"
  id       = "gpt-4o-mini"
}

target "prod" {
  type   = "platform"
  plugin = "anthropic"
}

mcp_server "hubspot" {
  url = "https://mcp.hubspot.com"

  auth {
    ref = "connection://cred_011CZkZDLs7fYzm1hXNPeRjv"
  }
}

mcp_server "airtable" {
  url = "https://mcp.airtable.com/mcp"

  auth {
    ref = "env://AIRTABLE_TOKEN"
  }
}
