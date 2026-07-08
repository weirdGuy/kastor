tool "create_draft" {
  description = "Create a scheduled draft in Typefully (X). Call once per post."

  param "content" {
    type        = string
    description = "The post text, verbatim. For a thread, separate tweets with four consecutive newlines."
  }

  param "publish_at" {
    type        = string
    default     = "next-free-slot"
    description = "ISO 8601 datetime with timezone (e.g. 2026-07-10T09:00:00Z), or 'next-free-slot'"
  }

  returns {
    type = string
  }

  # Runtime kind: the official Typefully MCP server (typefully_create_draft)
  # requires a nested requestBody object, which v0's scalar-only param types
  # can't declare. The generated stub is hand-implemented against the
  # Typefully v2 REST API instead — see create_draft_impl.py.
  source {
    kind = "runtime"
  }
}
