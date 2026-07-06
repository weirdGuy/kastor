# Two codegen targets whose names match no known generator: builds run in
# lexicographic target-name order, so target.alpha must fail first.
target "zed" {
  type   = "codegen"
  output = "./gen/zed"
}

target "alpha" {
  type   = "codegen"
  output = "./gen/alpha"
}
