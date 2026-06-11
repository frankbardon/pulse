# Testing Conventions

**Audience:** contributors writing tests, regenerating goldens, or
trying to figure out which CI gate to run locally before pushing.

> **From CLAUDE.md, CI gates and Common Claude Code Workflows.**

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
# Full suite
go test ./...

# Single package
go test ./processing/...

# Verbose, specific test
go test ./service/... -v -run TestProcess

# Coverage report
make cover

# Fuzz the .pulse header
go test ./encoding/... -fuzz FuzzPulseFileHeader -fuzztime 30s
```

## Non-skippable CI gates

These tests guard structural invariants. If one of them fails, the
underlying conventions (not the test) are what need re-thinking.
Their full names appear in CLAUDE.md so the
`TestClaudeMdMentionsAllNonSkippableGates` self-check can find them.

| Gate | Guards |
|---|---|
| `TestPredictNoExecutionImports`         | `descriptor/predict.go` does not import `service/` or `processing/` |
| `TestDescriptorNoFmtSprintf`            | `descriptor/` never builds JSON via `fmt.Sprintf` |
| `TestGoldensNotHandEdited`              | `descriptor/testdata/*` hashes match the generator |
| `TestClaudeMdMentionsFormatVersion`     | CLAUDE.md references the current envelope `format_version` |
| `TestClaudeMdMentionsAllEnvVars`        | Every `PULSE_*` env var has a CLAUDE.md row |
| `TestClaudeMdMentionsAllNonSkippableGates` | This very table is the source — CLAUDE.md must list every gate by name |
| `TestUpdateDemandTableCovers`           | The Update Demand table covers every registered component category |
| `TestPerPackageCoverageFloors`          | Package directories exist and meet documented coverage floors |
| `TestNoOrbitReferences`, `TestNoOrbitPrefix`, `TestNoOrbitPrefixes` | No predecessor-project string prefixes leak in |
| `TestSkillsCoverAll*`                   | Skill files mention every registered component, error code, distribution, CLI leaf, field type, MCP tool |
| `TestSkillsManifestConsistent`          | `skills/index.json` matches the `.md` files and frontmatter |
| `TestSkillsFrontmatter_RequiredFields`  | Every skill has `name`, `description`, `type`, `applies_to` |
| `TestRegistryStreamabilityMatchesTypes` | Aggregator `OnlineAggregator` capability matches `AggregationType.Streamable()` |
| `TestPredict_Streamable_MatchesRuntime` | `PredictResult.Streamable` mirrors `processing.CanStreamRequest` |
| `TestStreamability_*Known`              | Every `All*Types()` entry has a streamability table row |
| `TestCanStreamRequest_RegressionMatrix` | Regression matrix on the exported `CanStreamRequest` helper |
| `TestManifest*Complete`                 | Manifest enumerates every registered operator, test, distribution, MCP tool, error code |
| `TestManifestStreamableMatchesTypes`    | Manifest `Streamable` flags mirror the type-level methods |
| `TestCodesHaveFixups`, `TestSkillsErrorCodeFixupsDocumented` | Each error code has a fixup template and the skill row to match |
| `TestDefaults_Applied`                  | Smart-default operator-type inference behaves as documented |

(See `CLAUDE.md` "CI gates" for the full prose; this table is the
quick-reference.)

## Running a subset of gates locally

```bash
# All descriptor contract gates
go test ./descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'

# Skill coverage gates
go test ./skills/ -run 'TestSkillsCoverAll|TestSkillsManifestConsistent|TestSkillsFrontmatter'

# CLAUDE.md gates
go test . -run 'TestClaudeMd|TestUpdateDemandTable'

# Predecessor-reference scrub
go test . -run TestNoOrbitReferences
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

If your change introduces a structural invariant, add a test for it
under the same naming convention (`TestX`), and add it to the table in
`CLAUDE.md` so `TestClaudeMdMentionsAllNonSkippableGates` recognises
it. The [Update Demand](../internals/update-demand.md) lists this as a
trigger row.
