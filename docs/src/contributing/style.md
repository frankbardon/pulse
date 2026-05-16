# Style Guide

**Audience:** anyone writing code or docs in the Pulse repository.

This page summarises the conventions enforced by review and by CI. The
authoritative source is the "Code Conventions" section of
[`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md);
copy that file's rules when in doubt.

## Go style

- Standard `gofmt` / `go vet` cleanliness — `make lint` is the gate.
- Module path is `github.com/frankbardon/pulse`. The standard-library
  `io` collision is handled by aliasing the project's package as
  `pio "github.com/frankbardon/pulse/io"`.
- Library-first: business logic lives in library packages, never in
  `cmd/pulse/`. The CLI parses flags, calls the library, formats output.
- All file I/O routes through the injected `afero.Fs` — never
  `os.Open`/`os.ReadFile` directly in library code, because that
  defeats `fs.NewMemMap()` for tests and the extension hook for custom
  storage backends.

## Naming

- **Component types** use `SCREAMING_SNAKE_CASE`:
  `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`,
  `WIN_LAG`, `FEAT_LOG`, `TEST_T`.
- **Error codes** use `DOMAIN_CATEGORY` format, organised by the six
  domains listed in CLAUDE.md (`ENCODING`, `PROCESSING`, `SERVICE`,
  `DATA`, `CLI`, `PULSE`).
- **Field types** use lowercase snake (`u8`, `nullable_bool`,
  `categorical_u16`, `decimal128`).

## Structural bans

These are enforced by non-skippable CI gates:

| Ban | Enforced by |
|---|---|
| `descriptor/` MUST NOT import `service/` or `processing/` | `TestPredictNoExecutionImports` |
| `descriptor/` MUST NOT use `fmt.Sprintf` for JSON construction | `TestDescriptorNoFmtSprintf` |
| Golden files in `descriptor/testdata/` MUST NOT be hand-edited | `TestGoldensNotHandEdited` |
| No predecessor-project string prefixes (legacy "Orbit" naming) in error codes or constants | `TestNoOrbitReferences`, `TestNoOrbitPrefix` |
| `CLAUDE.md` MUST mention every `PULSE_*` env var, every non-skippable gate, the current `format_version` | `TestClaudeMd*` family |

See the [Pull Request Process](pr-process.md) for how these surface
during review.

## Comments and prose

- Public Go symbols carry a godoc-shaped comment opening with the
  symbol name.
- Skill files use YAML frontmatter (`name`, `description`, `type`,
  `applies_to`) and are LLM-facing — keep them in MCP voice (tool
  calls, JSON payloads). The human-facing equivalent is this site;
  cross-link from each side.
- mdBook chapters open with a one-sentence summary and an
  **Audience** line. See any of the already-authored chapters in
  this site for the tone.

## The Update Demand

The single most important convention: if your code change ships without
the corresponding `CLAUDE.md` and skill updates, CI will fail. The
[Update Demand](../internals/update-demand.md) chapter is the
authoritative table of triggers and the gates that enforce them. Read
it before opening a PR that touches a registered surface (new
aggregator, new error code, new CLI flag, new field type, ...).
