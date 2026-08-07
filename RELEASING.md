# Releasing kastor

Releases are tag-driven. Pushing a `v*` tag runs
[`.github/workflows/release.yml`](.github/workflows/release.yml), which runs the
full test suite and then GoReleaser
([`.goreleaser.yaml`](.goreleaser.yaml)): five platform/arch binaries (darwin
arm64/amd64, linux arm64/amd64, windows amd64), archives, `checksums.txt`, a
grouped changelog, the GitHub release, and — when the `TAP_GITHUB_TOKEN` secret
is present — a Homebrew cask push to `weirdGuy/homebrew-tap`.

Nothing below is automated. Work through it before you tag.

## Pre-tag checklist

1. **Be on `main`, up to date, clean.**

   ```sh
   git checkout main && git pull && git status --porcelain
   ```

2. **The full local suite passes.** CI runs the same four checks, but a red
   tree should never reach a tag.

   ```sh
   go build ./... && go test ./... && go vet ./... && gofmt -l .
   ```

3. **The examples still validate, build, and plan.** Passing tests are not the
   acceptance criterion for a release; the shipped binary's behavior on the
   shipped examples is.

   ```sh
   go build -o /tmp/kastor ./cmd/kastor
   for dir in examples/*/; do /tmp/kastor validate "$dir"; done
   /tmp/kastor build examples/weather && /tmp/kastor plan examples/weather
   ```

   `kastor doctor` is deliberately not in this loop: on `target.memory` its
   remote objects die with the process, so every run reports them missing and
   exits 1. Its live coverage is the acceptance run below.

4. **`CHANGELOG.md` is rolled.** The `[Unreleased]` section is renamed to the
   new version with today's date, a fresh empty `[Unreleased]` sits above it,
   and the link references at the bottom are updated. Every entry must describe
   something that actually shipped on `main` — check the log for tickets with no
   entry:

   ```sh
   git log --oneline "$(git describe --tags --abbrev=0)"..HEAD
   ```

5. **Version strings that live outside git tags are current.** The CLI's version
   is injected via ldflags and needs no edit, and `scripts/install.sh` resolves
   the latest tag from the GitHub API. The sample `kastor version` output in
   `mintlify/installation.mdx` and `mintlify/reference/cli.mdx` is hardcoded and
   does need bumping.

6. **The GoReleaser config still builds.** CI dry-runs this on every PR; run it
   locally if you touched the config. It publishes nothing.

   ```sh
   goreleaser check
   goreleaser release --snapshot --clean
   rm -rf dist
   ```

7. **Run the live Claude acceptance test** — see below.

## The live acceptance run

```sh
export KASTOR_ACCEPTANCE=1
export ANTHROPIC_API_KEY=...
export KASTOR_MCP_KASTOR_ACCEPTANCE_URL=...   # must expose an "echo" tool
export KASTOR_MCP_ACCEPTANCE_TOOL=...         # optional: name the tool it does expose
go test ./cmd/kastor -run TestClaudeManagedAgentsAcceptance -v -count=1
```

The test skips unless the first three variables are set, so a normal
`go test ./...` never runs it.

`KASTOR_MCP_KASTOR_ACCEPTANCE_URL` is a *harness* input, not a kastor
mechanism. Since KAS-63 a server's address is spec (SPEC.md §3.6): the run
copies the acceptance module to a temp directory and rewrites the `mcp_server`
block's `url` placeholder from the variable, so the module still owns the
address while the run stays pointable at whatever server you have.

`KASTOR_MCP_ACCEPTANCE_TOOL` exists because the live session step has to call a
tool that actually exists: the acceptance module declares `echo`, and a server
that serves something else needs its own tool named here (`tavily_search`, for
instance). Only the name matters — a tool block's params are not sent to
Managed Agents, because the MCP server owns the schema.

**Why this is manual and not in CI:**

- **It is the only check that catches a beta-API shape change.** Every other
  test in the suite runs against a fake or a recorded fixture, so the whole
  suite stays green if Claude Managed Agents changes a field on
  `managed-agents-2026-04-01`. This test is the one that talks to the real API
  and fails.
- **Plan output includes the MCP server URL**, which is deployment
  configuration and frequently carries a key in its query string. A CI log is
  public; that URL must not land in one.

The run also **starts one live session** against the created agent (KAS-57): it
provisions a cloud environment, asks the agent to call its MCP `echo` tool, and
asserts the platform evaluated that call as `allow`. This costs one short model
turn and is the only check that the tool permission kastor writes actually
reaches the deployed agent — a permission that is stored but ignored looks
identical to every CRUD assertion. The session and environment are deleted
afterwards; unlike the agent, both are reversible. The test logs the session's
Console trace URL, which is worth opening if the turn fails.

It also runs **`kastor doctor` against the live agent** (KAS-63) and asserts it
reports ready. The acceptance module's server declares no auth, so what this
covers is the remote read and the tool-permission check against a real agent
object — the credential path is covered offline against the fake vault, since
exercising it live would mean creating and expiring a real vault credential per
run. If you have a vault to hand, the fuller manual check is worth one pass:
add `vault_id` and a `connection://` ref to the copied module, run `doctor`
before authorizing the connection (expect `✗`, exit 1), authorize it and re-run
(expect exit 0), then re-run with the vault host blocked (expect `? could not
verify`, never "does not exist").

**Each run permanently archives an agent.** The test creates a real agent and
destroys it, and `destroy` on this target means *archive*, which is
irreversible: the agent cannot be restored and stays listed in the Anthropic
Console forever. Test agents are stamped with a `kastor_acceptance_test`
metadata marker and a timestamped run id so they are identifiable there. Run it
once per release, not in a loop.

## Tagging

Tag an annotated tag on the release commit and push the tag alone:

```sh
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

Pushing the tag is what starts the release. There is no way to undo a published
release cleanly — retracting one means a new patch tag, as v0.1.0 and v0.1.1
both demonstrated.

## After the release

1. The workflow run is green:
   `gh run list --workflow=release.yml --limit 1`
2. The GitHub release has five archives plus `checksums.txt`.
3. The Homebrew cask moved:
   `gh api repos/weirdGuy/homebrew-tap/contents/Casks/kastor.rb -q .content | base64 -d | head -3`
   The tap commit message is `Brew cask update for kastor version v<x.y.z>`.
4. The install script picks up the new tag:
   `curl -fsSL https://raw.githubusercontent.com/weirdGuy/kastor/main/scripts/install.sh | sh`

## Caveat: publish-stage template fields

Neither `goreleaser check` nor `goreleaser release --snapshot` evaluates
publish-stage templates — the cask's `token` and `skip_upload` among them. Both
v0.1.0 and v0.1.1 shipped binaries but no cask because a bad template there only
surfaces during a real release. Keep those fields to the exact documented forms
and change them as rarely as possible.
