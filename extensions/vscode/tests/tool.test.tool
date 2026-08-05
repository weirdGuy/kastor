# SYNTAX TEST "source.kastor" "tool blocks"

tool "web_search" {
# <- entity.name.type.kastor
#^^^ entity.name.type.kastor
#     ^^^^^^^^^^ variable.other.enummember.kastor
  param "max_results" {
# ^^^^^ entity.name.type.kastor
#        ^^^^^^^^^^^ variable.other.enummember.kastor
    type    = number
#             ^^^^^^ storage.type.kastor
    default = 10
#             ^^ constant.numeric.integer.kastor
  returns {
# ^^^^^^^ entity.name.type.kastor
  source {
# ^^^^^^ entity.name.type.kastor
    kind = "mcp"
#   ^^^^ variable.other.readwrite.kastor
#           ^^^ support.constant.kastor
    uri  = "mcp://search-server/tavily_search"
#   ^^^ variable.other.readwrite.kastor
#           ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ string.quoted.double.kastor
    # A description mentioning a string and a number stays prose
#                                ^^^^^^ comment.line.number-sign.kastor
#                                             ^^^^^^ comment.line.number-sign.kastor
  description = "returns a string, kind of"
#                ^^^^^^^^^^^^^^^^^^^^^^^^^ string.quoted.double.kastor
