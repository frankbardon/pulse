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
go test ./descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'
```

- `TestPredictNoExecutionImports` — `descriptor/predict.go` must not
  import `service/` or `processing/`.
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
go test ./skills/ -run 'TestSkillsCoverAll|TestSkillsManifestConsistent|TestSkillsFrontmatter'
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
| An aggregator / attribute / filterer / grouper / window / feature | `go test ./skills/ -run TestSkillsCoverAllComponents && go test ./descriptor/ -run TestManifestOperatorsComplete` |
| A statistical test | `go test ./types/ -run TestStreamability_TestsKnown && go test ./descriptor/ -run 'TestManifestTestsComplete\|TestManifestPostTestsComplete'` |
| A synth distribution | `go test ./skills/ -run TestSkillsCoverAllSynthDistributions && go test ./descriptor/ -run TestManifestDistributionsComplete` |
| A regression operator | `go test ./skills/ -run TestSkillsCoverAllRegressions && go test ./descriptor/ -run TestManifestRegressionsComplete` |
| An error code | `go test ./errors/ -run 'TestCodesHaveFixups\|TestErrorsLookup' && go test ./descriptor/ -run 'TestManifestErrorCodesComplete\|TestManifest_ErrorCodesSlim'` |
| An MCP tool | `go test ./skills/ -run TestSkillsCoverAllMCPTools && go test ./descriptor/ -run TestManifestMCPToolsComplete && go test ./mcp/ -run TestMCPSchemaBinding` |
| A field type | `go test ./skills/ -run TestSkillsCoverAllFieldTypes && go test ./encoding/...` |
| The Update Demand table or a contract listed in it | `go test . -run TestUpdateDemandTableCovers` |

The full set is documented in [Testing Conventions → Non-skippable
CI gates](testing.md#non-skippable-ci-gates) and enumerated by name
in [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md).
