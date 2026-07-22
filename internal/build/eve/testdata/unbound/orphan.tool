# Referenced by no agent: skipped on the eve target (a file in agent/tools/
# would be auto-wired), listed in the README.
tool "orphan" {
  description = "Never bound to an agent"

  returns {
    type = string
  }

  source {
    kind = "runtime"
  }
}
