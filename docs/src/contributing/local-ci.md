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
examples are exempt. The nested module `internal/embeddersmoke` is out of scope: `make smoke`
runs it separately. Run the same check by hand:

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

## Coverage floors

```bash
make cover
```

Runs both tiers once with `-coverpkg="$(citest cover-pkgs)"
-coverprofile`. `citest cover-pkgs` prints the floored packages (the
`.github/coverage-floors.txt` dirs) as comma-separated import paths, and
`make cover`, `ci.yml` and `cache-warm.yml` all take the list from it
(`TestCoverpkgFlagsDeriveFromFloorTable` pins that). Every test binary
records those packages, so a package's figure counts statements covered
by any test in the repo (a package tested from elsewhere, such as
`encoding`, is measured fairly). Packages without a floor are not
instrumented: they cost compile time and profile size, and nothing reads
their figures. The figures equal a `-coverpkg=./...` run's, block for
block, because instrumenting other packages does not change which
statements of a floored package a test executes.
`citest cover-merge` dedupes the raw profile (every test binary repeats
every floored block) into `coverage.out`, and `citest cover-check` compares each
package with its floor in `.github/coverage-floors.txt`, the one source
of the floors. It prints a `package / floor / coverage / status` table
and fails on a package below its floor or absent from the profile.
`TestPerPackageCoverageFloors` validates the table itself: every entry
a real package directory, every floor in 0..100, no duplicate. CI's
`coverage` job runs the same check over the profiles of both
`coverage-fast` legs and every `test-heavy` shard.

```bash
go run ./internal/tools/citest cover-check -profile coverage.out   # re-check without re-running
go tool cover -html=coverage.out                                   # browse it
go run ./internal/tools/citest cover-pkgs                          # the -coverpkg list
go run ./internal/tools/citest cover-shard 1 2                     # coverage-fast leg 1 of 2's packages
```

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
go test ./types/ ./errors/ -run TestNoOrbit
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
- The `apidiff` job in `ci.yml` runs on each dispatch (not under a
  release). See [Pull Request Process](pr-process.md#public-api-changes).

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
| `fmt-vet` | `make fmt-check vet`: gofmt check and `go vet` over both tiers |
| `staticcheck (fast)`, `staticcheck (heavy)` | `make staticcheck-fast` / `staticcheck-heavy`: one tier each, results uploaded as an artifact |
| `lint` | needs the three above (fails, never skips, if one failed), then `make staticcheck-merge`; together the four jobs run exactly `make lint` |
| `docs` | `mdbook build docs` |
| `build` | `make build`, `make smoke` |
| `test (1.26)` | the fast tier: `go test -count=1 -json ./...` (no coverage), plus `citest heavy-check` |
| `coverage-fast (1)`, `coverage-fast (2)` | the fast tier again with `-coverpkg="$(citest cover-pkgs)" -coverprofile`, split in two by `citest cover-shard` |
| `test-heavy (1)` … `test-heavy (6)`, `test-heavy (catchall)` | the heavy tier, one shard per job |
| `test-windows (1)`, `test-windows (2)` | the fast tier on `windows-latest`, split by package (`citest fast-run`); no build cache |
| `test-macos` | the fast tier on `macos-latest` (arm64) |
| `smoke-windows`, `smoke-macos` | the `build` job per OS: build `cmd/pulse`, then the embedder smoke module |
| `vulncheck` | `make vulncheck`: pinned `govulncheck ./...`, `GOOS=linux` then `GOOS=windows`; a reachable finding fails it (see [Vulnerability scan](#vulnerability-scan)) |
| `coverage` | needs `coverage-fast` and `test-heavy` (fails, never skips, if one failed); `citest cover-check` over their merged profiles: [Coverage floors](#coverage-floors) |
| `apidiff` | the exported-API delta against the latest `v*` tag; advisory, not required, skipped under a release |

Each shard runs only the heavy tests it owns, with `go test -tags heavy
-run '^(...)$'`. The fast tests run in `test (1.26)`, and again,
instrumented, in `coverage-fast`. Coverage stays out of `test (1.26)`
because it is the critical path: on a cold cache the instrumented
compile plus run took it to 8m34s, past the 8-minute job cap.
`coverage-fast` splits the fast tier into two legs with `citest
cover-shard <leg> <legs>`: every package with a fast-tier test file,
heaviest first (by test-file bytes) onto the lighter leg. The legs
partition the fast tier, so their merged profile equals one whole-tier
run's, and they depend only on the tree, so a new package needs no list
entry.
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
apart. Each Go job except the `test-windows` shards, `lint`,
`coverage` and `vulncheck` restores the `setup-go` module and build
cache. The lint jobs and `build` hash different files into their cache
keys, so they never seed the cache the test jobs use; `cache-warm.yml`
does ([Warm build cache](#warm-build-cache)).

### Windows and macOS legs

The four OS legs are blocking, like the ubuntu jobs, and each fits an
8-minute wall on a cold cache (E1-S3 measured the numbers below; aim
6 minutes). They run the same fast tier as `test (1.26)`: no
`-parallel 1` and no budget, so a test that takes over 1 s on a slow
runner is fine there. `make test` keeps the budget locally. Each test
leg runs `go test` in one step. A separate compile step would link
every test binary twice, and linking ~145 binaries is most of a cold
Windows job.

`test-windows` runs as two jobs, one per id in its `fast-shard:`
matrix. `.github/fast-shards.txt` pins a few long-running packages
(`<shard> <package>`). Every other package of `go list ./...` goes to
the shard holding the fewest packages so far, so each package runs in
exactly one shard and a new package needs no line.
`TestRepoFastShardList_Valid` checks the list against the tree and the
matrix. Pin a package only to rebalance; a new shard id means a new
required check (see branch protection below).

```bash
go run ./internal/tools/citest fast-list            # <shard> <package>, every package
go run ./internal/tools/citest fast-run 1 -count=1  # one shard, as CI runs it
```

The Windows test shards run without the `setup-go` cache. Cold, one
unsharded job took over 7 minutes. Its build and module cache is 2 GB
and takes ~45 s just to restore, and `setup-go` saves a cache only
after a green run whose key missed, so a red run always started cold
anyway. Every shard run is cold and takes the same time. `test-macos`
and both smoke legs keep `setup-go`'s default cache: `test-macos` is
keyed on `go.sum` like `test (1.26)`, and the smoke legs on every
`go.sum` like `build`. They fit the wall cold too, and
`cache-warm.yml` warms all three from `main` ([Warm build
cache](#warm-build-cache)).

**Reproducing a Windows failure.** Start from the failing job's log.
Each shard also uploads its `go test -json` stream as the
`test-windows-<shard>-json` artifact. Then narrow it down:

- **Cross-check from macOS or Linux.** `GOOS=windows go vet ./...` and
  `GOOS=windows go test -c -o /dev/null ./<pkg>` catch build-tag and
  API mistakes, for example a `_unix.go` file with no Windows twin or a
  `syscall` name Windows lacks. They run nothing, so a path or
  file-locking bug still needs a Windows run.
- **Run it on Windows.** On a Windows machine with Go and Git Bash, run
  `go run ./internal/tools/citest fast-run <shard> -count=1 -run
  '^TestName$'` or a plain `go test -count=1 -run '^TestName$'
  ./<pkg>`. Without one, push the branch and dispatch `ci.yml`; the
  shard named in the failing check reruns only its own packages.
- **Common causes:** an OS path built with `path` instead of
  `filepath` (or the reverse: a slash-separated fixture path compared
  to an OS path), a file still open when `t.TempDir` cleanup or a
  rename runs (Windows refuses to delete an open or mapped file), and
  timer resolution, where a 1 ms deadline fires before the code it was
  meant to interrupt has started.
- **Line endings.** `.gitattributes` checks out every text file with LF
  (`* text=auto eol=lf`) and marks `.pulse` cohorts and archives
  `binary`. So a golden, fixture or embedded skill reads the same bytes
  on Windows. A CRLF-shaped failure means a new binary extension
  missing from `.gitattributes`, or text that reaches the engine from
  outside the repository; in that case normalise CRLF to LF where it
  enters, as the skill loader does.

`make lint` runs serially on one machine; CI splits it so the two
staticcheck tiers run in parallel with `fmt-vet`. Each staticcheck job
resolves `latest` to one version and records it, and `lint` merges with
that version (`STATICCHECK_VERSION`). Locally, `make lint` stays the one
command; `make staticcheck` runs the three staticcheck steps over a
temporary directory.

Branch protection requires these check runs on the branch head before
merge: `lint`, `test (1.26)`, `docs`, `build`, every `test-heavy (…)`
shard, `test-windows (1)`, `test-windows (2)`, `test-macos`,
`smoke-windows` and `smoke-macos` (and one approving review). A renamed
job or a new shard id must be updated in the protection settings in
the same change, or no run can satisfy the old name.

A `v*.*.*` tag push (`release.yml`) reuses a passing `ci.yml` run when
`scripts/release-ci-reuse.sh <tested-sha> <tag-sha>` accepts the run's
head commit. It accepts the same tree, or a tree where every changed
path is added or modified (none deleted or renamed) and is on the
script's allowlist. The allowlist holds only paths that no test, embed or
build reads: roadmap prose under `docs/roadmap/` (not `units/` or
`fixtures/`, which tests read), `docs/internals/` baselines, and
`CONTRIBUTING.md`, `CODE_OF_CONDUCT.md` and `SECURITY.md`. The script's
header records the evidence. A test, embed or build step that starts
reading an allowlisted path must take it off the list in the same
change. Any other change makes the release run the whole workflow itself
(`workflow_call`). Run the script locally to see which way a tag would
go: exit 0 reuses, exit 1 runs CI. Reuse covers only the test suite.
The release's own `vulncheck` job always rescans the tagged tree, and
the publish waits for it (see [Vulnerability scan](#vulnerability-scan)).

The archives build in parallel, one `dist` job per platform (`make dist
DIST_FLAGS="--target <os>/<arch> --no-checksums"`). Each `dist` job
also waits for the release's `vulncheck` job to pass. The `release` job
then gathers the archives, writes `checksums.txt` with
`scripts/release-dist.sh --checksums-only`, and publishes. The asset set
is the one a local `make dist VERSION=<tag>` builds. A tag containing
`-` is published as a pre-release, and a release never takes Latest.
The `apidiff` job is skipped under a release's `workflow_call`, since
the tagged tree is the release itself.

### Warm build cache

GitHub scopes an Actions cache to the ref that saved it. A run can
restore caches saved on its own branch or on the default branch
(`main`), never a sibling branch's. `ci.yml` runs only on branch
dispatches and tag releases, so without help `main` holds no Go cache
and every branch's first CI run compiles cold (the plain fast-tier
compile is roughly 250 s on a runner).

`.github/workflows/cache-warm.yml` fills that gap. It runs on every push
to `main` and on `workflow_dispatch`, and only compiles, so no test
runs. A `drop-stale` job first deletes every `setup-go-*` cache on the
ref (all OSes), so each warm job's restore misses and the job saves a
cache of this tree. Then one warm job per setup-go key runs, each
needing `drop-stale`:

| Job | Warms | Compiles |
|---|---|---|
| `warm` (ubuntu, default `go.sum` key) | `test (1.26)`, `coverage-fast`, `test-heavy` (and `apidiff`, same key) | `go build ./...`; `go test -count=1 -run '^$' ./...` (the `test (1.26)` compile); with `COVERPKG=$(citest cover-pkgs)`, `go test -count=1 -run '^$' -coverpkg="$COVERPKG" ./...` (the `coverage-fast` legs compile subsets of it) and the same with `-tags heavy` (the build `citest heavy-run` does) |
| `warm-macos` (macOS, default `go.sum` key) | `test-macos` | the CI helper, then `go test -count=1 -run '^$' ./...` |
| `warm-smoke-macos` (macOS, `**/go.sum` key) | `smoke-macos` | `make build`; in `internal/embeddersmoke`, `go vet ./...` and `go test -count=1 -run '^$' ./...` with `CGO_ENABLED=0` (as `make smoke` exports) |
| `warm-smoke-windows` (Windows, `**/go.sum` key) | `smoke-windows` | `go build -trimpath ./cmd/pulse` with `CGO_ENABLED=0`; the smoke module's `go vet` and test compile |

The `test-windows` shards run with `cache: false`, so nothing warms
them. The lint jobs and the ubuntu `build` job keep their own caches
unwarmed: they are cheap cold.

The settings have to match the test jobs exactly. `setup-go` keys its
cache as `setup-go-<OS>-<arch>-<image>-go-<resolved Go version>-<hash of
cache-dependency-path>` and restores by exact key only, so a different
`go-version` or dependency path gives a key the test jobs never read. A
`-coverpkg` compile is its own build-cache entry, so the warm compile
also has to use the same flags. Both workflows carry a KEEP IN SYNC
comment at those steps; change them together.
`TestRepoCacheWarm_MatchesCachedJobs` (`internal/tools/citest`) checks
the keys: every `ci.yml` job with a setup-go cache has a `cache-warm.yml`
job with the same `runs-on`, `go-version` and `cache-dependency-path`
(bar the lint and `build` jobs it exempts by name), and no warm job
seeds a key no `ci.yml` job reads. It cannot check compile flags; keep
those in sync by hand.

A saved cache cannot be overwritten, and `setup-go` skips the save when
its key already exists. The key changes only with `go.sum` or the Go
patch release, so without the first step `main`'s cache would keep the
first tree it saved and go stale. Deleting first means a short window
after each `main` push (the length of one cold compile) in which a branch
run misses the cache. Dispatching `cache-warm.yml` on a branch saves into
that branch's scope and also drops the branch's lint and `build` caches,
which rebuild on the next CI run.

## Vulnerability scan

```bash
make vulncheck   # needs network (vuln.go.dev)
```

`make vulncheck` installs `govulncheck` at the Makefile's
`GOVULNCHECK_VERSION` (the one pin, which the `vulncheck` job in
`ci.yml` also uses; bump it deliberately, never to `latest`) and runs
it in default symbol mode over `./...` twice: `GOOS=linux`, then
`GOOS=windows`. Only a finding the call graph reaches fails it; a
vulnerable module the code never calls is listed but passes. `./...`
does not include the nested `internal/embeddersmoke` module. Fix a
dependency finding with `go get <module>@<fixed>` and `go mod tidy`
(in the root and in `internal/embeddersmoke`). Standard-library
findings are judged against the running toolchain. CI's `"1.26"`
floats to the latest patch, so locally run it under that patch too,
for example `GOTOOLCHAIN=go1.26.10 make vulncheck`. A toolchain that
is not the latest patch of its minor can report standard-library
findings that CI does not.

**The release gate.** A tag push scans again. `release.yml` has its own
`vulncheck` job running the same `make vulncheck`, with no `needs:` and
no `if:`, so it starts beside `verify` on every tag. CI reuse (below,
[When CI runs](#when-ci-runs)) never satisfies it: a reused CI run's
scan is only as current as the vulnerability database was on the day
it ran, and a vulnerability published since then must still stop the
release. Both `dist` and `release` need the job and require
`needs.vulncheck.result == 'success'` in their `if:`, so a reachable
finding (or a scan that cannot run) blocks the publish.
`TestRepoReleaseVulncheck_Gated` (`internal/tools/citest`) checks that
shape: no `if:`, `needs:` or `continue-on-error` on the job, `make
vulncheck` as its command, every `contents: write` job needing it, and
`scripts/release-ci-reuse.sh` never mentioning it. To unblock a tag,
fix the finding on `main` and tag the fixed commit. Do not edit the
gate.

## Fuzzing

Every surface that reads untrusted bytes has a fuzz target: request JSON
decode, expression compile, each importer (CSV / TSV headers, NDJSON,
JSON array, JSON shared, Arrow, Parquet, Excel, SPSS), the `.pulse`
header, schema, dictionaries and full record read, and shard-archive
open.

**The contract.** A target hands each input to
`internal/testutil/fuzzcontract` (`fuzzcontract.Run`, or a tuned
`fuzzcontract.Contract{...}.Run`). The contract fails the input when:

1. **The surface panics.** The panic is recovered and reported with the
   input and the panicking goroutine's stack.
2. **The run is unbounded.** The input is capped at `MaxInput` (64 KiB by
   default) before the surface sees it, and one call must return within
   `Timeout` (10 s). A target that builds a Pulse instance also caps the
   engine with `fuzzcontract.Limits()`. `MaxAlloc` caps the heap one call
   may allocate. It catches a decoder that sizes a buffer from a length
   the input only declares; the importer targets set 1 GiB.
3. **An error is not coded.** Every non-nil error must satisfy
   `errors.As(err, **errors.CodedError)` with a registered code. A raw
   `encoding/json` or third-party library error that reaches a caller
   fails the contract.

Each target adds seeds with `f.Add` and may commit a corpus under its
package's `testdata/fuzz/<Target>/`. Seeds and corpus files run as
ordinary subtests in the fast tier, so keep them small.

**Running a target locally.** `-fuzz` takes one package and a pattern
that matches one target:

```bash
go test -run '^$' -fuzz '^FuzzNDJSONImport$' -fuzztime 2m -fuzzminimizetime 20x ./internal/io/ndjson
```

`-run '^$'` skips the package's other tests. Keep `-fuzzminimizetime`
short. The default of 60 s per new interesting input stalls the workers
on a slow target.

**Reproducing a crasher.** A failing input is written to
`<pkg>/testdata/fuzz/<Target>/<id>`, and `go test` prints its path.
Rerun that one input as an ordinary test:

```bash
go test -run '^FuzzNDJSONImport/<id>$' ./internal/io/ndjson
```

Fix the fault, then commit the crasher file unchanged as its regression
entry. It runs in every fast-tier run from then on.

**The weekly workflow.** `.github/workflows/fuzz.yml` runs every
Monday. It has one matrix leg per target, and each leg fuzzes for 10
minutes (`-fuzztime=10m`). It is advisory: it is not part of `ci.yml`
and not a required check. A failed leg uploads its new corpus files as
the `fuzz-crashers-<Target>` artifact and writes the reproduction
command to the job summary. Copy the files into the package's
`testdata/fuzz/<Target>/` and run that command. To run the workflow on
demand, optionally with a shorter fuzz time:

```bash
gh workflow run fuzz.yml --ref <branch> -f fuzztime=2m
```

**Keeping the matrix whole.** `go run ./internal/tools/citest
fuzz-check` fails when a `func FuzzXxx(f *testing.F)` in the module has
no `fuzz.yml` leg, or when a leg names no target.
`TestRepoFuzzMatrix_InSync` runs the same check in the fast tier. A new
target needs one matrix line, in the `- { pkg: "./dir", target: FuzzXxx }`
form the check parses.

## CLAUDE.md hygiene gates

`CLAUDE.md` is itself a tested artefact. Every `PULSE_*` env var
named in Go source must appear there; every non-skippable gate must
be listed; the current `format_version` must be mentioned:

```bash
go test ./internal/integration/ ./internal/descriptor/ -run 'TestClaudeMd|TestUpdateDemandTable'
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
| The Update Demand table or a contract listed in it | `go test ./internal/integration/ -run TestUpdateDemandTableCovers` |
| Any exported identifier in a public package, or a root alias's target | `go test -tags heavy ./internal/apigolden/` (then `-update` only if the change is intended) + `make smoke` |
| A package move under `internal/io/**` / `internal/iocore` | `go test ./internal/iocore/ -run TestIOImportBoundary` |

The full set is documented in [Testing Conventions → Non-skippable
CI gates](testing.md#non-skippable-ci-gates) and enumerated by name
in [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md).
