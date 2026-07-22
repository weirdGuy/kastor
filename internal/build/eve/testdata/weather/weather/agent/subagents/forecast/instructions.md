Produce a one-paragraph weather forecast summary for {{location}}.
Be specific about temperature and precipitation. Note that literal
braces like {"not": "a variable"} are plain text, not template vars.

## Inputs

Inputs arrive in the user message; `{{name}}` placeholders above refer to them.

- `location` (string, required)

## Outputs

Reply with a single JSON object holding exactly these fields:

- `summary` (string)
