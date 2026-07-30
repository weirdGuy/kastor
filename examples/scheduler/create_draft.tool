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
  # Typefully v2 REST API instead, in place at
  # gen/langgraph/tools/create_draft.py — kastor build writes that stub once
  # and keeps your implementation on every build after.
  #
  # That file is intentionally not tracked: examples/**/gen/ is ignored, and
  # committing one file back into an output directory would also mean
  # committing its .kastorbuild marker. The implementation is preserved as a
  # worked example in the docs instead (mintlify/reference/cli.mdx, "Generated
  # output is disposable"), so `kastor build` here regenerates an unimplemented
  # stub that raises NotImplementedError until you fill it in.
  source {
    kind = "runtime"
  }
}
