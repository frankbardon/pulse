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
# Fast tier
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
| `TestGoldensNotHandEdited`              | `descriptor/testdata/*` hashes match the generator |
| `TestClaudeMdMentionsFormatVersion`, `TestClaudeMdSizeBudget` | CLAUDE.md names the current `format_version` and stays under its size ceiling |
| `TestEnvVarsDocumented`                 | Every `PULSE_*` env var has a row in [Options → Environment variables](../library/options.md#environment-variables) |
| `TestUpdateDemandTableCovers`           | The Update Demand reference covers every registered component category |
| `TestPerPackageCoverageFloors`          | The floor table `.github/coverage-floors.txt` is sound (real package dirs, 0..100, no duplicate); `make cover` / CI `coverage` measure against it |
| `TestNoOrbitPrefix`, `TestNoOrbitPrefixes` | No predecessor-project string prefixes leak in |
| `TestSkillsCoverAll*`                   | Skill files mention every registered component, error code, distribution, CLI leaf, field type, MCP tool |
| `TestSkillsManifestConsistent`          | Every skill's `applies_to` frontmatter names only real CLI leaves (there is no `skills/index.json` — the `embed.FS` walk is the manifest) |
| `TestSkillsFrontmatter_RequiredFields`  | Every skill has `name`, `description`, `type`, `applies_to` |
| `TestSkillExampleTagPointersResolve`    | Every `pulse_examples_search tags=[…]` pointer anywhere in a skill body matches at least one shipped example |
| `TestRegistryStreamabilityMatchesTypes` | Aggregator `OnlineAggregator` capability matches `AggregationType.Streamable()` |
| `TestPredict_Streamable_MatchesRuntime` | `PredictResult.Streamable` mirrors `internal/processing`'s `CanStreamRequest` |
| `TestStreamability_*Known`              | Every `All*Types()` entry has a streamability table row |
| `TestCanStreamRequest_RegressionMatrix` | Regression matrix on the exported `CanStreamRequest` helper |
| `TestManifest*Complete`                 | Manifest enumerates every registered operator, test, distribution, MCP tool, error code |
| `TestManifestStreamableMatchesTypes`    | Manifest `Streamable` flags mirror the type-level methods |
| `TestCodesHaveFixups`, `TestSkillsErrorCodeFixupsDocumented` | Each error code has a fixup template and the skill row to match |
| `TestDefaults_Applied`                  | Smart-default operator-type inference behaves as documented |
| `TestDocsLinksResolve`                  | Every relative link and `#anchor` in `docs/src/**` and the repo-root `*.md` files resolves as published: inside `docs/src`, a `SUMMARY.md` chapter, mdBook heading slugs. Known breaks sit in the shrink-only allowlist `internal/doccheck/allowlists/links.txt`; `make linkcheck-external` checks http(s) links by hand |
| `TestCliHelpMatchesFlagsDoc`            | The CLI command tree and `docs/src/cli/flags.md` name the same leaves and flags (global, persistent, per-leaf, aliases included), names only, both directions; known gaps sit in the shrink-only allowlist `internal/doccheck/allowlists/cli-flags.txt` |
| `TestFlagUsageOneWordingPerName`       | A flag name mounted on several CLI leaves carries one usage wording everywhere, unless `usageVariants` (`internal/cli/tree_test.go`) declares a per-leaf meaning with its exact wording count and a reason; no usage string is under 20 characters (urfave's `--help` / `--version` aside). The walk is the production one (`internal/cli` `Leaves`) |
| `TestNoRemovedNamesInDocs`              | No served text (`docs/src/**` minus the coverage page, root README / CONTRIBUTING / SECURITY, a default instance's skills, MCP tool descriptions, manifest, error messages and fixups, examples) names a spelling from the removed-name denylist `doccheck.RemovedNames` (whole word, a reason per entry; an `io.<Name>` entry also matches the import alias `pio.<Name>`); today's hits sit in the shrink-only allowlist `internal/doccheck/allowlists/removed-names.txt` |
| `TestNoRemovedNamesInPromptsAndReference` | Heavy tier (`mcp/gosdk`): the same denylist over every MCP prompt a default server mounts (description, argument descriptions, `prompts/get` body) and every file `ExportReference` writes; hits sit in `internal/doccheck/allowlists/removed-names-prompts.txt` |
| `TestDocsSnippetsRun`                   | Every `json` block in `docs/src/**` that is a request root (by its keys — the shared `internal/requestroot` rule plus a `cohort` object or `requests` / `base` / `sweep` — or by strict-decoding as one) runs on the example fixture cohorts (predict valid, process no error, output not asserted); a keyed root that fails the strict decode fails, naming the decode error; `jsonc` blocks are not read. Fence tags: `json norun` opts out, `json cohort=<fixture>[,…]` picks cohorts, untagged runnable blocks fail; `go compile` blocks must build (`TestDocsSnippetsCompile`, heavy tier). Known failures sit in the shrink-only allowlists `internal/doccheck/allowlists/snippets.txt` and `snippets-go.txt` |
| `TestDocsCoverageCurrent`               | The generated coverage matrix `docs/src/contract/coverage.md` matches a fresh build from its sources of truth (public API dump, CLI tree, manifest, MCP registrations, env vars, `Options` / Request / Response fields); lives in `internal/cli` (only it may walk the CLI tree); regenerate with `make docs` |
| `TestDocsCoverageNoGaps`                | No required coverage cell (per row kind: mdBook, skill pack, GoDoc, CLI help; verbatim mention) is empty unless the shrink-only allowlist `internal/doccheck/allowlists/coverage.txt` names it |
| `TestDocCoverageNotImportedByDescriptor` | The no-execute descriptor packages (`internal/descriptor`, `descriptor/`) never import the coverage generator `internal/doccoverage` |
| `TestProfileServedTextNoHiddenFeature` | Heavy. Under every single-feature-hidden feature profile, no served text (manifest, MCP tools / prompts / resources, skills, examples, `docs export` tree) names a hidden operator, hidden feature's MCP tool or hidden slot token, and no core tool description loses a sentence to the prose scrub (its clause is feature-fenced). Shrink-only allowlist `internal/doccheck/allowlists/served-text.txt` (empty) |
| `docs` CI job (not a Go test)          | `mdbook build docs` prints no `WARN` / `ERROR` line (this catches a broken page and a search index past mdBook's 10 MB warning), then `make doc-checks` runs the fast doc checks |
| `TestFacadeExamplesPresent`             | The pinned runnable GoDoc task examples (`facadeExamples` in `facade_examples_test.go`: root `example_*_test.go`, `observe`, `mcpserve`) all exist and carry a checked `// Output:` (compile-only ones are named); an unpinned example in those packages fails too, so the set only grows |

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
go test ./descriptor/ -run TestGoldensNotHandEdited   # confirms the new hash sticks
```

Never hand-edit a golden file — the gate will catch you.

## Adding a new gate

If your change introduces a structural invariant, add a test for it and
a row in the table above.
