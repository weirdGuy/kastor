# Documentation project instructions

## About this project

- This is the Mintlify documentation site for Kastor.
- Pages are MDX files with YAML frontmatter.
- Configuration lives in `docs.json`.
- `../SPEC.md` is the design source of truth.
- The Go CLI and parser are the source of truth for implemented behavior.

## Terminology

- Use "Kastor" consistently.
- Prefer "declarative agent definitions" over broad AI-platform language.
- Prefer "Terraform-style lifecycle" over vague platform claims.
- Use `kastor.hcl` or `*.kastor` for implemented project files.
- Use `kastor build` for the implemented compiler command.

## Style preferences

- Use active voice and second person ("you")
- Keep sentences concise: one idea per sentence
- Code formatting for file names, commands, paths, and code references
- Keep pages short and concrete
- Mark planned behavior as planned

## Content boundaries

- Claude Managed Agents (`target "claude_agents"`) ships: `plan`, `apply`, and `destroy` all work against it. Document its real constraints rather than softening them — unsupported tool kinds and model params are errors, `destroy` archives irreversibly, and those errors surface at apply, not at `kastor validate`. `target "memory"` is still the credential-free demo platform. Never present OpenAI Assistants or Bedrock Agents Classic as targets — both are sunset.
- Do not claim package-manager installation exists unless verified.
- Do not document `adl.hcl` as implemented unless the parser supports it.
- Do not document `kastor compile`; use `kastor build`.
- Do not put real API keys in examples.
