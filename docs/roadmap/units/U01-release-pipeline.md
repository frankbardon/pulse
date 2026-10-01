---
id: U01
slug: release-pipeline
title: "Every build knows its real version, and a pushed tag ships binaries"
track: API & release
size: S
status: not-started
depends_on: []
soft_depends_on: []
blocks: [U32]
todo_items: [5, 6, 7, 8, 9]
branch: release-pipeline
---

# U01 — release-pipeline

**Outcome:** Every build knows its real version, and a pushed tag ships binaries.

**Track:** API & release · **Size:** S · **Depends on:** none · **Unblocks:** [U32](U32-v1-release.md)

## Summary

Fix version reporting: today `cmd/pulse` reports `"dev"` because `LDFLAGS` never sets it, and `mcpserve` hard-codes `"1.0.0"`. Add a tag-triggered, CI-gated release workflow that publishes binaries for six platforms to GitHub Releases.

## References

**Theme documents (read before starting):**
- [api-and-release 01 — Release pipeline & versioning](../v1.0.0-api-and-release/01-release-pipeline.md) — whole document

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#5** (1. API surface & release pipeline › Release pipeline) `internal/buildinfo` + `pulse.Version()`; ldflags injection; `ReadBuildInfo` fallback; `make build` uses `git describe`
- [ ] **#6** (1. API surface & release pipeline › Release pipeline) `pulse version` / `--version`; `mcpserve` and `gosdk` default to the real version (remove hard-coded `"1.0.0"`); manifest `pulse_version`
- [ ] **#7** (1. API surface & release pipeline › Release pipeline) Gate against hard-coded version literals
- [ ] **#8** (1. API surface & release pipeline › Release pipeline) `ci.yml` callable; `release.yml` on `v*` tags gated on CI
- [ ] **#9** (1. API surface & release pipeline › Release pipeline) Binaries for linux / darwin / windows × amd64 / arm64, plus checksums; GitHub Release with generated notes; release labels

## Scope

**In scope**
- `internal/buildinfo` as the single version source, plus `pulse.Version()`
- ldflags injection in `make build` and in release builds; `debug.ReadBuildInfo` fallback
- `pulse version` / `--version`; manifest `pulse_version`; `mcpserve` / `gosdk` defaults read the real version
- `ci.yml` reusable via `workflow_call`; `release.yml` on `v*.*.*` tags
- 6-platform binary matrix, checksums, GitHub Release with generated notes, release labels

**Out of scope**
- CHANGELOG file and upgrade guide (decided: not wanted)
- Versioned docs site
- SBOM / signing (optional follow-up)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(release-pipeline/E<n>-S<m>): …`; close each epic with `milestone(release-pipeline/E<n>): vertical slice complete — <epic title>`.

### E1 — Binaries report their real version
- S1: `internal/buildinfo` + `pulse.Version()` with the ldflags → `ReadBuildInfo` → `devel+<rev>` fallback chain
- S2: `pulse version` / `--version`; `make build` injects `git describe`
- S3: `mcpserve` and `gosdk.Config` default to `pulse.Version()`; manifest `pulse_version` (additive); manifest golden regenerated
- S4: gate rejecting hard-coded semver literals in `mcpserve`, `mcp/gosdk`, `cmd/pulse`

### E2 — A pushed tag ships binaries, only after CI passes
- S1: make `ci.yml` callable (`workflow_call`) with no behaviour change on push/PR
- S2: `release.yml` on `v*.*.*`, whose build job `needs:` the CI job
- S3: 6-platform `CGO_ENABLED=0` build, archives with LICENSE/README, `checksums.txt`, generated release notes, pre-release on `-rc`/`-beta`
- S4: release-note labels (`feature`, `fix`, `breaking`, `docs`)

## Acceptance criteria

- [ ] `pulse --version` on a release artifact prints the tag; a `go install …@vX.Y.Z` build prints `vX.Y.Z`; a local build prints `git describe` output
- [ ] The MCP `initialize` response advertises the real version; no source file under `mcpserve`, `mcp/gosdk` or `cmd/pulse` contains a hard-coded semver
- [ ] Pushing a tag on a commit with failing tests produces **no** release; pushing on a green commit produces a release with 6 archives + checksums
- [ ] A `-rc.N` tag is marked as a pre-release
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New: version-literal gate (name it `TestNoHardcodedVersionLiterals`)
- Manifest golden regenerated (not hand-edited) for `pulse_version`

## Update Demand companions

- CLAUDE.md "Build / Env" mentions version injection
- `docs/src/cli/flags.md` row for `pulse version` if it is a new leaf
- `skills/session-bootstrap.md` if `pulse_version` is useful to agents

## Human inputs & decisions

- Confirm GoReleaser vs a plain `go build` matrix (the design allows either)

## Notes

- A dry-run tag on a fork or a `v0.x.y-rc` is the cheapest way to validate the workflow end to end.
