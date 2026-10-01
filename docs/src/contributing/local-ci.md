# Running CI Gates Locally

**Audience:** contributors who want to mirror the CI surface on a
local machine before pushing, or want to narrow down which gate
failed in a remote run.

The full `make lint && make test` cycle is the default pre-push
check. This page is the targeted-subset reference for when you know
which contract you changed and want to verify only that contract's
gates.

## All tests

```bash
go test ./...
```

Runs everything. Slow but exhaustive. The recommended pre-push
default.

## Descriptor contract gates

The descriptor surface (predict, manifest, inspect, envelope) has
structural invariants that catch hand-edits and import cycles:

```bash
go test ./descriptor/ ./internal/descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'
```

- `TestPredictNoExecutionImports` — `internal/descriptor/predict.go` must not
  import `internal/service/` or `processing/`.
- `TestDescriptorNoFmtSprintf` — no `fmt.Sprintf` in
  `descriptor/envelope.go`, `manifest.go`, `predict.go`, `inspect.go`.
- `TestGoldensNotHandEdited` — every golden file under
  `descriptor/testdata/` must end with a valid
  `// golden-hash: <sha256>` line that matches the body.

## Skill-coverage gates

The skill pack under `skills/` is the LLM-facing surface. Every
registered component, error code, distribution, CLI leaf, field
type, MCP tool must be mentioned in its target skill by name:

```bash
go test ./internal/skills/ -run 'TestSkillsCoverAll|TestSkillsManifestConsistent|TestSkillsFrontmatter'
```

The specific gates this batch includes are listed in [Testing
Conventions → Non-skippable CI gates](testing.md#non-skippable-ci-gates).

## Predecessor-reference scrub

Pulse has a hard rule against leaking strings from its predecessor
project (legacy "Orbit" naming) into error codes or type constants:

```bash
go test . -run TestNoOrbit
```

Should always return zero matches before opening a PR.

## Public API gates

Every package outside `internal/` and `cmd/` is frozen Go API at
`v1.0.0`. Three guards keep the surface deliberate:

```bash
go test ./internal/apigolden/            # TestPublicAPIGolden (blocking)
make smoke                               # builds internal/embeddersmoke as an external module
```

- `TestPublicAPIGolden` dumps the exported shape of every public package,
  with each root alias expanded into its target's fields, tags and
  methods, and compares it to `internal/apigolden/testdata/public_api.txt`.
  If you meant to change the surface, regenerate with
  `go test ./internal/apigolden/ -run TestPublicAPIGolden -update` and
  review the diff as the API delta.
- `make smoke` compiles and tests `internal/embeddersmoke`, a nested module
  with its own `go.mod` that may only use public spellings. CI runs it.
- The `apidiff` job (`.github/workflows/api-compat.yml`) runs on PRs only.
  See [Pull Request Process](pr-process.md#public-api-changes).

## CLAUDE.md hygiene gates

`CLAUDE.md` is itself a tested artefact. Every `PULSE_*` env var
named in Go source must appear there; every non-skippable gate must
be listed; the current `format_version` must be mentioned:

```bash
go test . -run 'TestClaudeMd|TestUpdateDemandTable'
```

## Per-change quick reference

| If you changed... | Run |
|---|---|
| An aggregator / attribute / filterer / grouper / window / feature | `go test ./internal/skills/ -run TestSkillsCoverAllComponents && go test ./internal/descriptor/ -run TestManifestOperatorsComplete` |
| A statistical test | `go test ./types/ -run TestStreamability_TestsKnown && go test ./internal/descriptor/ -run 'TestManifestTestsComplete\|TestManifestPostTestsComplete'` |
| A synth distribution | `go test ./internal/skills/ -run TestSkillsCoverAllSynthDistributions && go test ./internal/descriptor/ -run TestManifestDistributionsComplete` |
| A regression operator | `go test ./internal/skills/ -run TestSkillsCoverAllRegressions && go test ./internal/descriptor/ -run TestManifestRegressionsComplete` |
| An error code | `go test ./errors/ -run 'TestCodesHaveFixups\|TestErrorsLookup' && go test ./internal/descriptor/ -run 'TestManifestErrorCodesComplete\|TestManifest_ErrorCodesSlim'` |
| An MCP tool | `go test ./internal/skills/ -run TestSkillsCoverAllMCPTools && go test ./internal/descriptor/ -run TestManifestMCPToolsComplete && go test ./internal/mcp/ -run TestMCPSchemaBinding` |
| A field type | `go test ./internal/skills/ -run TestSkillsCoverAllFieldTypes && go test ./encoding/... ./internal/encoding/...` |
| The Update Demand table or a contract listed in it | `go test . -run TestUpdateDemandTableCovers` |
| Any exported identifier in a public package, or a root alias's target | `go test ./internal/apigolden/` (then `-update` only if the change is intended) + `make smoke` |
| A package move under `internal/io/**` / `internal/iocore` | `go test ./internal/iocore/ -run TestIOImportBoundary` |

The full set is documented in [Testing Conventions → Non-skippable
CI gates](testing.md#non-skippable-ci-gates) and enumerated by name
in [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md).
