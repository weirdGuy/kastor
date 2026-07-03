tool "deploy" {
  returns {
    type = string
  }

  source {
    kind = "script"
    uri  = "./scripts/deploy.sh"
  }
}
