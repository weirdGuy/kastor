# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Kastor is pre-1.0: the v0 language semantics may still change until the
v0 exit criteria ([KAS-36](https://linear.app/getkastor/issue/KAS-36)) are met.

## [Unreleased]

### Added

- Claude Managed Agents platform provider: `target "claude_agents"` supports
  plan, apply, and destroy with optimistic version checks, archive-aware drift
  handling, classified retries, and API-key auth (KAS-38)
- Codegen target `eve` (Vercel eve, TypeScript): `kastor build` emits one
  eve project per root agent — `agent.ts` model config, `instructions.md`
  with the IO contract as convention, referenced agents as subagent
  directories, MCP tools as allow-listed connection files (endpoint URLs
  from `KASTOR_MCP_<SERVER>_URL`), http/runtime tools as `defineTool` files
  with Zod schemas. Both example modules build for it (KAS-32)

### Fixed

- `kastor plan` no longer green-lights a module the target cannot apply. A
  resource absent from state never reached the provider, so a spec with an
  unsupported tool source kind, a non-anthropic model provider, an unsupported
  model param, or a missing `KASTOR_MCP_<SERVER>_URL` planned clean as
  `+ agent.x (not in state)` and exited 0, then failed part-way through apply.
  Planned creates are now validated through the provider's `Diff` against an
  absent remote and fail the plan naming the resource and the target. The
  provider contract states what `Diff` must do when the remote object does not
  exist; `kastor validate` itself stays provider-agnostic (KAS-55)

- `kastor build` no longer overwrites an implemented `runtime` tool stub. Such
  a stub is generated once and then belongs to the user: the file is written
  only while it still matches the stub the last build wrote, is kept rather
  than deleted when its tool leaves the spec, and a spec change that lands
  under an implementation arrives as a `<name>.kastor-new` sidecar beside it
  plus a one-time warning naming both files (the build still succeeds, exit 0).
  Ownership is recorded in the output
  directory's `.kastorbuild` marker; a directory with no record is treated as
  the user's. Both codegen targets (langgraph, eve) are covered (KAS-24)

  **Upgrade note.** `.kastorbuild` was a one-line marker and is now a versioned
  manifest recording, per stub, the hash of what kastor last wrote and whether
  you have edited it. An output directory built by an older kastor has no
  records, so the first build after upgrading has no history: an untouched stub
  is still byte-identical to the fresh one and is recognized as kastor's, but a
  stub you implemented is treated as yours — correct, and the only direction
  that cannot destroy your code — and arrives with one `.kastor-new` sidecar and
  one warning, because kastor cannot tell your edits from a spec change. Diff
  the pair, delete the sidecar; from that build on the manifest has real records
  and it does not recur.

### Changed

- v0 platform provider selected: Claude Managed Agents. `target
  "claude_agents"` is the platform target label, authenticating from
  `ANTHROPIC_API_KEY`; the provider lands in `internal/provider/claude/`.
  Bedrock AgentCore and Dify are no longer under consideration (KAS-37)
- Docs audited against the shipped code (KAS-41). README, the docs site, and
  the Mintlify authoring rules no longer describe hosted providers as planned.
  Added: a worked `claude_agents` quickstart (module, `ANTHROPIC_API_KEY`,
  `KASTOR_MCP_<SERVER>_URL` per MCP server, plan/apply transcript); the
  irreversibility of `kastor destroy` on that target, which archives the agent
  and leaves it listed in the Console forever; what the target rejects — tool
  sources `http`, `script`, and `runtime`, model params other than `speed`, a
  non-`anthropic` model provider — and that `input`/`output` blocks have no
  counterpart in the remote object; and the fact that all of those are
  apply-time errors, since `kastor validate` is target-agnostic and `plan`
  calls no provider for a resource that is not yet in state

## [0.1.2] - 2026-07-17

### Fixed

- Homebrew cask publishing: the tap `token` field must be exactly
  `{{ .Env.VAR_NAME }}` — GoReleaser rejects any other interpolation, so
  v0.1.1 also shipped binaries but no cask (KAS-40)

## [0.1.1] - 2026-07-17

### Fixed

- Homebrew cask publishing: the tap token template used a function that does
  not exist in OSS GoReleaser, failing the release at publish time — v0.1.0
  shipped binaries but no cask (KAS-40)

## [0.1.0] - 2026-07-17

### Added

- `kastor init` — new-project scaffolding: a minimal working module with one
  agent, one MCP tool, one prompt, a model, and a LangGraph codegen target (KAS-39)
- Built-in in-memory platform provider: `target "memory"` works with
  `plan` / `apply` / `destroy`, no credentials required (#17)
- Support-triage canonical example — single agent, pure prompt (KAS-46)
- Documentation site at [docs.getkastor.dev](https://docs.getkastor.dev) and
  project landing page, including an installation page (KAS-40)
- `kastor version` reports the release version and commit; binaries are
  stamped at build time and `go install` builds fall back to Go build info (KAS-40)
- Homebrew install path: `brew install weirdGuy/tap/kastor`, published as a
  cask on release (KAS-40)
- This changelog (KAS-40)

### Changed

- SPEC.md realigned to the current agent-platform landscape, with a docs
  consistency sweep to match (#56, #58)
- Releases ship five binaries — darwin arm64/amd64, linux arm64/amd64,
  windows amd64; windows arm64 dropped (KAS-40)

### Fixed

- `internal/build` tests no longer fail when developer-local artifacts exist
  in example output directories (KAS-35)

## [0.0.1-alpha] - 2026-07-08

### Added

- HCL language core: `.agent`, `.tool`, and `.prompt` files plus `kastor.hcl`
  project files parsed into typed structs with aggregated diagnostics
- Module loading: directory walk, symbol table, cross-file reference
  resolution, prompt variables checked against agent inputs/outputs
- Dependency graph: DAG construction, cycle detection, deterministic
  topological sort
- `kastor validate` — full parse/resolve/graph pipeline
- `kastor build` — codegen engine with a LangGraph target (single agent with
  tools)
- `kastor plan` / `kastor apply` / `kastor destroy` — three-way
  spec/state/remote comparison, per-operation state persistence,
  `kastor.state.json` with versioning and a local lock file
- `kastor fmt` — canonical formatting via hclwrite
- Weather example module
- Release automation: GoReleaser + GitHub Actions on `v*` tags
- Apache License 2.0

[Unreleased]: https://github.com/weirdGuy/kastor/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/weirdGuy/kastor/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/weirdGuy/kastor/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/weirdGuy/kastor/compare/v0.0.1-alpha...v0.1.0
[0.0.1-alpha]: https://github.com/weirdGuy/kastor/releases/tag/v0.0.1-alpha
