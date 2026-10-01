# 01 — Release pipeline & versioning

**Decided:**
- No CHANGELOG file; GitHub Releases are the record.
- No upgrade guide.
- A release builds binaries for common platforms when a new version tag is pushed, and only after CI is green for that commit.
- The version reported by binaries must be the real tag.
- (U01 interview, 2026-10-01) Plain `go build` matrix + `gh`, not GoReleaser. Finalize owns the GitHub Release and its notes; the workflow only uploads assets, creating a Release with generated notes only when none exists. No release-note labels. The first tag is `v1.0.0-alpha.0`. Strategy: [roadmap README](../README.md#branching--release-strategy).

## Problems today

| Problem | Evidence |
|---|---|
| Binaries never carry a real version | `cmd/pulse/main.go` declares `var version = "dev"`; the Makefile's `LDFLAGS=-s -w` never sets it |
| The MCP server always claims to be 1.0.0 | `mcpserve/mcpserve.go`: `const defaultVersion = "1.0.0"`, used whenever `Options.Version` is empty. Every pre-1.0 build has been advertising 1.0.0 to MCP clients |
| No release automation | `.github/workflows/` contains only `ci.yml` and `docs.yml` |

## Version: one source of truth

1. **New leaf package `internal/buildinfo`**, exposed through the facade as `pulse.Version()`:
   - a `var version = ""` that release builds set via `-ldflags "-X …/internal/buildinfo.version=v1.2.3"`;
   - when unset (e.g. a `go install github.com/frankbardon/pulse/cmd/pulse@v1.2.3` build), it falls back to `runtime/debug.ReadBuildInfo()`, which reports the module version for `go install` builds;
   - for a plain local `go build`, it falls back to `"devel"` plus the VCS revision from build info.
2. **Every place that reports a version reads it:**
   - `pulse --version` and `pulse version`;
   - the `mcpserve` default and the `gosdk.Config.Version` default (replacing the hard-coded `"1.0.0"`);
   - the manifest, as a new additive `pulse_version` field (`format_version` stays `"1.1"`; that field versions the *envelope shape*, not the binary).
3. `make build` passes the version from `git describe --tags --always --dirty`, so local builds are identifiable too.
4. **Gate.** A test asserts there is no hard-coded semantic-version literal in `mcpserve`, `mcp/gosdk` or `cmd/pulse`, which prevents the `"1.0.0"` regression from coming back.

## Release workflow

**Trigger:** a pushed tag matching `v*.*.*`.

**CI-must-pass guarantee:**
- The release workflow's first job runs the same `make lint` + `make test` matrix as `ci.yml`, by calling it as a reusable workflow (`workflow_call`). The build job `needs:` it.
- A tag on a commit whose checks fail therefore produces no release.
- Optionally, a repository ruleset can also restrict who may push `v*` tags.

**Build** with a plain `go build` matrix (GoReleaser was considered and rejected):

| OS | Arch |
|---|---|
| linux | amd64, arm64 |
| darwin | amd64, arm64 |
| windows | amd64, arm64 |

- `CGO_ENABLED=0`, as the Makefile already enforces, so cross-compiling is trivial.
- Each archive contains the `pulse` binary plus `LICENSE` and `README.md`.
- A `checksums.txt` file (SHA-256) is attached.
- Optional: SBOM and keyless signing (cosign). Cheap to add, but not required by the decisions above.

**Publish:**
- Upload to the GitHub Release that `flow-finalize` created (`gh release upload --clobber`). If none exists, create one with GitHub's generated notes.
- A tag with a pre-release suffix (`v1.0.0-rc.1`) is marked as a pre-release.

**Docs:** `docs.yml` keeps publishing the mdBook site from `main`. Versioned docs are out of scope.

## Deliverables

- [ ] `internal/buildinfo` + `pulse.Version()`; ldflags injection; `ReadBuildInfo` fallback
- [ ] `pulse version` / `--version`; `mcpserve` and `gosdk` default to the real version; manifest `pulse_version`
- [ ] Gate against hard-coded version literals
- [ ] `ci.yml` made callable (`workflow_call`); `release.yml` on `v*` tags, needing the CI job
- [ ] Six-platform binary matrix, archives, checksums, GitHub Release with generated notes
