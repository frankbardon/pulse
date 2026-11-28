# Testing Conventions

**Audience:** contributors writing tests, regenerating goldens, or
trying to figure out which CI gate to run locally before pushing.

## Style

- Table-driven tests are the default. Put cases in a `[]struct{...}`
  with a `name` field, run with `t.Run(tc.name, func(t *testing.T))`.
- Hermetic by construction: anything that touches the filesystem uses
  `fs.NewMemMap()` so tests don't depend on disk state.
- New code lands with tests in the same PR — TDD first, then
  implementation. A test that passes without the implementation is
  suspicious; the test is probably wrong.

## Running tests

```bash
# Fast tier (make test adds the 1 s budget)
go test ./...

# Full suite: both tiers (see Running CI Gates Locally → Test tiers)
go test -tags heavy ./...

# Single package
go test ./internal/processing/...

# Verbose, specific test
go test ./internal/service/... -v -run TestProcess

# Coverage floors, both tiers (fails on a package below its floor)
make cover

# Fuzz the .pulse header
go test ./encoding/... -fuzz FuzzPulseFileHeader -fuzztime 30s
```

## Where black-box tests live

The root package holds only in-package (`package pulse`) tests. The black-box
suite (`package integration_test`) lives in `internal/integration/`: a
`TestMain` there walks up to the module root's `go.mod` and changes into it
(so fixture paths like `docs/roadmap/...` resolve), forces panic recovery off,
and owns that package's `-update` flag. A root gate test moved here is run as
`go test ./internal/integration/ -run X`; `go test . -run X` matches nothing
and passes silently, so confirm a gate ran with `-list` or `-v`.

Black-box tests reach the engine handle and the panic-recovery switch through
`internal/testhook` (`Service`, `ForcePanicRecoveryOff`), which the root's
`testhook_install.go` fills at init; no root test-only export file exists.

One file stays at the root: `feature_parity_mcp_bridge_test.go`. It imports
`mcp/gosdk` and shares an in-package harness, so moving it would create an
import cycle. `TestRootTestGraphExcludesCLI` checks both packages.

## Structural gates

These tests guard structural invariants. If one of them fails, the
underlying conventions (not the test) are what need re-thinking. What
each checks in detail: `.claude/reference/update-demand/gates.md`.
How to run the documentation checks and work their allowlists:
[Documentation Checks](doc-checks.md).

| Gate | Guards |
|---|---|
| `TestPredictNoExecutionImports`         | `internal/descriptor/predict.go` does not import `internal/service/` or `internal/processing/` |
| `TestDescriptorNoFmtSprintf`            | `descriptor/` + `internal/descriptor/` never build JSON via `fmt.Sprintf` |
| `TestNoQuotedPathVerbs`                 | no message builder `%q`s (or `strconv.Quote`s) a filesystem path; it uses `pathq.Quote`, so a Windows path reads with single backslashes (`internal/pathq`) |
| `TestGoldensNotHandEdited`              | `descriptor/testdata/*` hashes match the generator |
| `TestClaudeMdMentionsFormatVersion`, `TestClaudeMdSizeBudget` | CLAUDE.md names the current `format_version` and stays under its size ceiling |
| `TestEnvVarsDocumented`                 | Every `PULSE_*` env var has a row in [Options → Environment variables](../library/options.md#environment-variables) |
| `TestUpdateDemandTableCovers`           | The Update Demand reference covers every registered component category |
| `TestPerPackageCoverageFloors`          | The floor table `.github/coverage-floors.txt` is sound (real package dirs, 0..100, no duplicate); `make cover` / CI `coverage` measure against it |
| `TestNoOrbitPrefix`, `TestNoOrbitPrefixes` | No predecessor-project string prefixes leak in |
| `TestSkillsCoverAll*`                   | Skill files mention every registered operator (components, regressions, matrix ops, overlays, windows), synth distribution, field type, CLI leaf, MCP tool and prompt; every operator declares a `Purpose`; every skill cross-reference resolves. Spread over `internal/skills`, `internal/descriptor`, `internal/integration` and `mcp/gosdk`: run `go test ./... -run TestSkillsCoverAll` |
| `TestSkillsManifestConsistent`          | Every skill's `applies_to` frontmatter names only real CLI leaves, or `mcp` (there is no `skills/index.json` — the `embed.FS` walk is the index) |
| `TestParamsTableSaysWhereKeyGoes`      | An op skill whose Params table lists a key that is a top-level slot field (a filterer's `values`, a test's `alpha`) says it is not under `params` |
| `TestSkillsFrontmatter_RequiredFields`  | Every skill has `name`, `description`, `type`, `applies_to` |
| `TestSkillExampleTagPointersResolve`    | Every `pulse_examples_search tags=[…]` pointer anywhere in a skill body matches at least one shipped example |
| `TestRegistryStreamabilityMatchesTypes` | Aggregator `OnlineAggregator` capability matches `AggregationType.Streamable()` |
| `TestPredict_Streamable_MatchesRuntime` | `PredictResult.Streamable` mirrors `internal/processing`'s `CanStreamRequest` |
| `TestStreamability_*Known`              | Every `All*Types()` entry has a streamability table row |
| `TestCanStreamRequest_RegressionMatrix` | Regression matrix on the exported `CanStreamRequest` helper |
| `TestManifest*Complete`                 | Manifest enumerates every registered operator, test, distribution, MCP tool, error code |
| `TestManifestStreamableMatchesTypes`    | Manifest `Streamable` flags mirror the type-level methods |
| `TestCodesHaveFixups`                   | Each error code has a `codeMetadata` Message and at least one Fixup (or `FixupNotApplicable`) |
| `TestEmittedCodeLiteralsRegistered`     | Every code-shaped string literal in non-test Go source is a registered code (`errors/codes.go`) |
| `TestRegisteredCodesEmittedOrReserved` | The converse: every registered code is emitted by non-test Go source (named as a value, not only matched against), or listed in the gate's `reservedCodes` with a reason |
| `TestRefusalMessagesUseWireKeys`        | Request-facing refusal prose names wire keys (`params.field`, `max_iters`), not Go field paths |
| `TestFixupPathsAreWireKeys`             | Every `Fixup.Path` segment in `errors/fixup_metadata.go` is an index (`*`, digits) or a wire key (a JSON tag, a manifest param name); a fix on a Go-only surface carries no Path |
| `TestPredict_DefaultsAppliedPathsAreWireKeys` | `defaults_applied[].path` is wire keys (`["crosstab", "cells", "1", "type"]`) |
| `TestDefaults_Applied`                  | Smart-default operator-type inference behaves as documented |
| `TestDocsLinksResolve`                  | Every relative link and `#anchor` in `docs/src/**` and the repo-root `*.md` files resolves as published: inside `docs/src`, a `SUMMARY.md` chapter, mdBook heading slugs. Allowlist `internal/doccheck/allowlists/links.txt`, locked empty; `make linkcheck-external` checks http(s) links by hand |
| `TestCliHelpMatchesFlagsDoc`            | The CLI command tree and the generated `docs/src/cli/reference.md` carry the same leaves and, per leaf, the same flags (aliases included), plus the root's global flags, both directions; every flag the concepts page `docs/src/cli/flags.md` names is mounted somewhere; allowlist `cli-flags.txt`, locked empty |
| `TestCLIReferenceCurrent`               | `docs/src/cli/reference.md` matches a fresh render of the command tree (`internal/cli` `CLIReference`, `internal/docgen` `RenderCLIReference`) byte for byte; `make docs` rewrites it |
| `TestFlagUsageOneWordingPerName`       | A flag name mounted on several CLI leaves carries one usage wording everywhere, unless `usageVariants` (`internal/cli/tree_test.go`) declares a per-leaf meaning with its exact wording count and a reason; no usage string is under 20 characters (urfave's `--help` / `--version` aside). The walk is the production one (`internal/cli` `Leaves`) |
| `TestNoRemovedNamesInDocs`              | No served text (`docs/src/**` minus the coverage page, root README / CONTRIBUTING / SECURITY, a default instance's skills, MCP tool descriptions, manifest, error messages and fixups, examples) names a spelling from the removed-name denylist `doccheck.RemovedNames` (whole word, a reason per entry; an `io.<Name>` entry also matches the import alias `pio.<Name>`); allowlist `internal/doccheck/allowlists/removed-names.txt`, locked empty |
| `TestNoRemovedNamesInPromptsAndReference` | Heavy tier (`mcp/gosdk`): the same denylist over every MCP prompt a default server mounts (description, argument descriptions, `prompts/get` body) and every file `ExportReference` writes; allowlist `internal/doccheck/allowlists/removed-names-prompts.txt`, locked empty |
| `TestDocsBaselinesEmpty`                | Every documentation-check allowlist under `internal/doccheck/allowlists/` holds header lines only: a doc check's finding is fixed, never baselined (fast tier, `make doc-checks`) |
| `TestInstallDocAssetNamesMatchRelease`  | The download table on `docs/src/getting-started/installation.md` names exactly the archives `scripts/release-dist.sh` builds (one per target, `.tar.gz` / Windows `.zip`) plus `checksums.txt`; a new target or a renamed archive fails until the page follows (fast tier, `make doc-checks`) |
| `TestDocsExampleTagTableMatchesCanonicalTags` | The tag table on `docs/src/examples/library.md` lists exactly `internal/examples.CanonicalTags`, each tag once; a tag added to or dropped from the taxonomy fails until the page follows (fast tier, `make doc-checks`) |
| `TestSupportedProtocolVersionsPinned`   | The MCP revisions the go-sdk serves equal the pinned list in `mcp/gosdk`, and `docs/src/mcp/protocol.md` names each revision and the go-sdk version `go.mod` pins; a bump that changes the set is a deliberate diff, and a dropped revision needs a documented deprecation first |
| `TestServe_NegotiatesEverySDKRevision`  | `mcpserve` narrows no revision: `initialize` echoes every SDK revision before `2026-07-28` and answers any other value with `2025-11-25` (`mcpserve/`) |
| `TestDocsSnippetsRun`                   | Every `json` block in `docs/src/**` that is a request root (by its keys — the shared `internal/requestroot` rule plus a `cohort` object or `requests` / `base` / `sweep` — or by strict-decoding as one) runs on the example fixture cohorts (predict valid, process no error, output not asserted); a keyed root that fails the strict decode fails, naming the decode error; `jsonc` blocks are not read. Fence tags: `json norun <reason>` opts out (a `norun` with no reason fails), `json cohort=<fixture>[,…]` picks cohorts, untagged runnable blocks fail; `go compile` blocks must build (`TestDocsSnippetsCompile`, heavy tier). Allowlists `internal/doccheck/allowlists/snippets.txt` and `snippets-go.txt`, locked empty |
| `TestDocsCoverageCurrent`               | The generated coverage matrix `docs/src/contract/coverage.md` matches a fresh build from its sources of truth (public API dump, CLI tree, manifest, MCP registrations, env vars, `Options` / Request / Response fields); lives in `internal/cli` (only it may walk the CLI tree); regenerate with `make docs` |
| `TestDocsCoverageNoGaps`                | No required coverage cell (per row kind: mdBook, skill pack, GoDoc, CLI help; verbatim mention) is empty (allowlist `internal/doccheck/allowlists/coverage.txt`, locked empty) |
| `TestDocCoverageNotImportedByDescriptor` | The no-execute descriptor packages (`internal/descriptor`, `descriptor/`) never import the coverage generator `internal/doccoverage` |
| `TestProfileServedTextNoHiddenFeature` | Heavy. Under every single-feature-hidden feature profile, no served text (manifest, MCP tools / prompts / resources, skills, examples, `docs export` tree) names a hidden operator, hidden feature's MCP tool or hidden slot token, and no core tool description loses a sentence to the prose scrub (its clause is feature-fenced). Allowlist `internal/doccheck/allowlists/served-text.txt`, locked empty |
| `docs` CI job (not a Go test)          | `mdbook build docs` prints no `WARN` / `ERROR` line (this catches a broken page and a search index past mdBook's 10 MB warning), then `make doc-checks` runs the fast doc checks |
| `TestFacadeExamplesPresent`             | The pinned runnable GoDoc task examples (`facadeExamples` in `facade_examples_test.go`: root `example_*_test.go`, `observe`, `mcpserve`) all exist and carry a checked `// Output:` (compile-only ones are named); an unpinned example in those packages fails too, so the set only grows |
| `TestEvalReferences`, `TestEvalIntentCoverage` | Every agent-eval task's reference submission (`internal/eval/tasks/`) passes its own assertions on freshly built fixture cohorts, and every intent in the taxonomy has at least one task (read from `IntentIDs()`, never a count); running an agent against them: [Agent Evaluation](agent-eval.md) |
| `TestEvalNoAnswerKeyLeak` | The eval answer key stays unserved: no task question appears in an example or skill, no question names an operator, no reference request equals an example body, and no package outside `internal/eval` imports it |

## Running a subset of gates locally

```bash
# All descriptor contract gates
go test ./descriptor/ ./internal/descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'

# Skill coverage gates
go test ./internal/skills/ -run 'TestSkillsCoverAll|TestSkillsManifestConsistent|TestSkillsFrontmatter'

# Doc gates
go test ./internal/integration/ ./internal/descriptor/ -run 'TestClaudeMd|TestEnvVars|TestUpdateDemandTable|TestComponentsContract'

# Predecessor-reference scrub
go test ./types/ ./errors/ -run TestNoOrbit
```

## Regenerating golden files

Golden files live in `descriptor/testdata/`. Each ends with a
`// golden-hash: <sha256>` line; `TestGoldensNotHandEdited` verifies
the hash. After a legitimate change to the generator:

```bash
go test ./descriptor/ -run 'Test.*Golden' -update
go test -count=1 ./descriptor/ -run TestGoldensNotHandEdited   # confirms the new hash sticks
```

Other generators (public API, fingerprint, reference oracles, the
guide) have their own commands: [Regenerating
Goldens](../internals/regenerating-goldens.md).

Never hand-edit a golden file — the gate will catch you.

## Adding a new gate

If your change introduces a structural invariant, add a test for it, a
row in the table above and prose in the matching
`.claude/reference/update-demand/gates*.md` slice. A fast documentation
check also joins `DOC_CHECKS` ([Documentation Checks](doc-checks.md)).
