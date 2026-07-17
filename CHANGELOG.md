# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Kastor is pre-1.0: the v0 language semantics may still change until the
v0 exit criteria ([KAS-36](https://linear.app/getkastor/issue/KAS-36)) are met.

## [Unreleased]

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

[Unreleased]: https://github.com/weirdGuy/kastor/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/weirdGuy/kastor/compare/v0.0.1-alpha...v0.1.0
[0.0.1-alpha]: https://github.com/weirdGuy/kastor/releases/tag/v0.0.1-alpha
