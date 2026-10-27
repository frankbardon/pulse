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

### Worker-invariance tests

A test that proves an answer does not depend on `DecodeWorkers`,
`ShardWorkers`, a single file against its shard archive, or streaming
against buffered runs on the shared harness in
`internal/testutil/invariance` (test-only). A family supplies one
request shape (batch its variants into one request where it can), a
`Run` that executes it under a knob cell and returns the
arm-invariant answer (`invariance.LayoutFree` blanks the cohort path
and shard count), and a `Check` that guards the reference against
degeneracy. The harness compares every cell against its group's
reference (`BitExact` unless the family documents a tolerance) and
fails a parallel cell that does not report its parallel arm.
`ArchiveGroup` and `BufferedCell` are cheap and belong in the fast
tier. The parallel-decode arm engages only at 100,000 records or more,
and no knob lowers that threshold, so `DecodeGroup` belongs in a heavy
test. The matrix, multi-grouper and grouped-components families
(`TestMatrixInvariance_*`, `TestMultiGrouperInvariance_*`,
`TestGroupedComponentsInvariance_*`) are the worked examples.

### Parallel by default

Every top-level `func TestXxx(t *testing.T)` in the root module, in
both tiers, starts with `t.Parallel()`. A test that cannot run in
parallel says why, with a `// serial: <reason>` comment above the
function or inside its body, for example `// serial: uses t.Setenv`.
`TestTestsParallelByDefault` (in `internal/tools/citest`) parses every
test file and fails on a test that has neither, or on a `// serial:`
comment with no reason. `TestMain`, benchmarks, fuzz targets and
examples are exempt. The nested modules (`internal/embeddersmoke`,
`contrib/*`) are out of scope: `make smoke` and `make contrib` run
them separately. Run the same check by hand:

```bash
go run ./internal/tools/citest parallel-check
```

### The 1 s budget

`make test` pipes `go test -count=1 -json -parallel 1 ./...` through
`citest filter -budget 1s -recheck`. The filter prints the usual
`go test` log and fails if any top-level test took 1 s or more. It
lists each offender with its time and the fix (move it to a
`*_heavy_test.go` file or make it cheaper), and prints the five
slowest tests with their margin. `-parallel 1` keeps each test's time
close to its own cost, because a test fanned out with `t.Parallel()`
also waits on its neighbours. `-recheck` reruns each offender alone
and clears it when that run is under 1 s. A test's time is its
`Elapsed` plus the time of every subtest that called `t.Parallel()`:
`go test` stops a parent's clock when its body returns, before those
subtests run, so without this a test that fans its work out into
parallel subtests reads near zero. A test failure still fails
`make test`. `make test-heavy` and CI apply no budget. CI only
reports the ten slowest tests in the job summary.

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
go test -tags heavy ./internal/apigolden/ # TestPublicAPIGolden (blocking, heavy tier)
make smoke                               # builds internal/embeddersmoke as an external module
```

- `TestPublicAPIGolden` dumps the exported shape of every public package,
  with each root alias expanded into its target's fields, tags and
  methods, and compares it to `internal/apigolden/testdata/public_api.txt`.
  If you meant to change the surface, regenerate with
  `go test -tags heavy ./internal/apigolden/ -run TestPublicAPIGolden -update` and
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

The jobs run in parallel, so the wall clock is the slowest job, not
the sum:

| Job (check name) | Runs |
|---|---|
| `lint` | `make lint`: gofmt check, `go vet` and `staticcheck`, each over both tiers |
| `docs` | `mdbook build docs` |
| `build` | `make build`, `make smoke`, `make contrib` |
| `test (1.26)` | the fast tier: `go test -count=1 -json ./...`, plus `citest heavy-check` |
| `test-heavy (1)` … `test-heavy (6)`, `test-heavy (catchall)` | the heavy tier, one shard per job |

Each shard runs only the heavy tests it owns, with `go test -tags heavy
-run '^(...)$'`. The fast tests run once, in `test (1.26)`.
`.github/heavy-shards.txt` assigns heavy tests to shards with one
`<shard> <package> <Test>` line per test. A heavy test the file does not
name runs in the `catchall` shard, so a new heavy test is never skipped.
To rebalance the shards, add the test to the file (a new heavy test needs a line there to run in a chosen shard).
`internal/tools/citest` does the work:

```bash
go run ./internal/tools/citest heavy-check       # list vs tree vs the ci.yml matrix
go run ./internal/tools/citest heavy-list        # <shard> <package> <Test>, every heavy test
go run ./internal/tools/citest heavy-run 3 -count=1   # one shard, as CI runs it
```

`heavy-check` fails on a stale or duplicate entry, and on a shard id
that is missing from (or extra in) the `test-heavy` matrix.
`TestRepoHeavyShardList_Valid` runs the same check in the fast tier.
`heavy-run` splits a shard into several `go test` runs when a name it
owns in one package also names a different test in another package, so
each heavy test runs in exactly one shard.

The test jobs pipe `-json` through `citest filter`. It prints `go
test`'s usual non-verbose log, plus the full output of each failed or
unfinished test. It also appends the 10 slowest tests to the job
summary. The report never fails a job; `pipefail` keeps `go test`'s
exit status. Before its test step, `test (1.26)` compiles every
fast-tier test binary on its own (`go test -run '^$' ./...`) and writes
the compile time to the job summary, so compile and run cost read
apart. Each Go job restores the `setup-go` module and build
cache. `lint` and `build` hash different files into their cache keys,
so they never seed the cache the test jobs use.

Branch protection requires the `lint` and `test (1.26)` check runs on
the branch head before merge. A `v*.*.*` tag push (`release.yml`)
reuses a passing `ci.yml` run whose head commit has the tagged commit's
tree. It runs the whole workflow itself (`workflow_call`) only when no
such run exists, and then builds the archives with `make dist`. A tag
containing `-` is published as a pre-release.
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
| Any exported identifier in a public package, or a root alias's target | `go test -tags heavy ./internal/apigolden/` (then `-update` only if the change is intended) + `make smoke` |
| A package move under `internal/io/**` / `internal/iocore` | `go test ./internal/iocore/ -run TestIOImportBoundary` |

The full set is documented in [Testing Conventions → Non-skippable
CI gates](testing.md#non-skippable-ci-gates) and enumerated by name
in [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md).
