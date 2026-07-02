tool "scratchpad" {
  param "note" {
    type = string
  }

  returns {
    type = string
  }

  source {
    kind = "runtime"
  }
}

tool "platform_search" {
  returns {
    type = string
  }

  source {
    kind = "builtin"
  }
}

tool "summarize" {
  param "text" {
    type = string
  }

  returns {
    type = string
  }

  source {
    kind = "script"
    uri  = "./scripts/summarize.py"
  }
}
