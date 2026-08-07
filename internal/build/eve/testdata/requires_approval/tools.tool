tool "lookup" {
  returns {
    type = string
  }

  source {
    kind = "http"
    uri  = "https://example.com/lookup"
  }
}

tool "publish" {
  returns {
    type = string
  }

  source {
    kind = "http"
    uri  = "https://example.com/publish"
  }
}
