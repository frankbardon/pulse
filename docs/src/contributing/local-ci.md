# Running CI Gates Locally

**Audience:** contributors who want to mirror the CI surface on a
local machine before pushing, or want to narrow down which gate
failed in a remote run.

The full `make lint && make test-heavy` cycle is the default
pre-push check. This page is the targeted-subset reference for when
you know which contract you changed and want to verify only that
contract's gates.

## Test tiers

The suite has two tiers, split by a build tag:

| Tier | Files | Run with |
|---|---|---|
| Fast | every `*_test.go` without a build tag | `make test` / `go test ./...` |
| Heavy | `*_heavy_test.go` files whose first line is `//go:build heavy` | `make test-heavy` / `go test -tags heavy ./...` (runs both tiers) |

```bash
make test         # fast tier only: the quick local loop
make test-heavy   # both tiers: everything, the pre-push default
```

The rule is time-based. A test that takes 1 s or more on a warm
cache goes in a heavy file. That covers large-cohort worker-invariance
and parallel-decode arms, peak-heap memory tables and calibration
runs, bootstrap / MCMC / oracle loops, and whole-repo scans. Everything
else stays in the fast tier. Helpers that both tiers use stay in
untagged files, because a fast-tier file cannot see a heavy-only
symbol. A test whose expensive part is one subtest or one size can
split that part into its own heavy test that calls a shared helper.
`TestReturnSkipsComputation_LongArms` and
`TestComponents_ParallelMergeMatchesSerial100K` work this way.

There is no `-short` mode. Use the heavy tag instead of a
`testing.Short()` guard.

`make lint` checks both tiers. `go vet` runs once without the tag and
once with `-tags heavy`. `staticcheck` runs both ways and merges the
results with `-merge`, so a helper that only heavy tests use is not
reported as unused. `gofmt` already covers every file. Gates that
walk `*_test.go` files by name (the non-skippable prefix gates, the
`sites` pins, the panic-suite isolation gate) see heavy files like
any other. The `go list -deps -test` boundary gates pass
`-tags heavy`, so they also cover the heavy tier's import graph.

## All tests

```bash
make test-heavy
```

Runs everything (both tiers). Slow but exhaustive. The recommended
pre-push default.

## Descriptor contract gates

The descriptor surface (predict, manifest, inspect, envelope) has
structural invariants that catch hand-edits and import cycles:

```bash
go test ./descriptor/ ./internal/descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'
```

- `TestPredictNoExecutionImports` — `internal/descriptor/predict.go` must not
  import `internal/service/` or `internal/processing/`.
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

## Contrib modules

`contrib/otelpulse` and `contrib/prompulse` (observability adapters) are
separately published Go modules (own `go.mod` + `go.sum`, `replace =>
../..` for development).
The core module never depends on them, nor on any OpenTelemetry or
Prometheus module (`TestCoreModuleNoObservabilityDeps`).

```bash
make contrib        # per module: go mod tidy -diff, vet, staticcheck, test
make contrib-tidy   # re-tidy every contrib module
```

`make contrib` runs without `-mod=mod`, so a stale contrib `go.mod` or
`go.sum` fails locally and in CI. A root dependency bump therefore needs
`make contrib-tidy` in the same PR. Contrib modules are released in
lockstep with the root tag by `scripts/release-contrib.sh` (called by
`release.yml`); run it without `--push` for a dry-run tag plan.

## When CI runs

`ci.yml` does not run on pushes or pull requests. Local gates cover a
branch while it is built; CI runs once, by hand, on the finished branch
before merge:

```bash
gh workflow run ci.yml --ref <branch>
gh run watch "$(gh run list --workflow ci.yml --branch <branch> --limit 1 --json databaseId --jq '.[0].databaseId')"
```

Its `lint` and `test (1.26)` check runs land on the branch head, which
branch protection requires before merge. A `v*.*.*` tag push
(`release.yml`) reuses a passing `ci.yml` run whose head commit has
the tagged commit's tree, runs the suite itself only when there is
none, and then builds the archives with `make dist`. A tag containing
`-` is published as a pre-release.
`api-compat.yml` still runs on pull requests (it diffs against the PR
base and takes under a minute).

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
