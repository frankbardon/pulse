# CLAUDE.md

## Project Overview

Pulse is a self-describing tabular data processing engine. Ships as a Go library (`github.com/frankbardon/pulse`) and a CLI binary (`cmd/pulse/`). Library is primary; CLI is a thin adapter.

**Design principles:**

- **Library-first.** `pulse.go` facade (`New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`) is the public API. CLI never contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header. `descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `skills/` embeds 20 markdown skills via `//go:embed`. LLM agents call `skills.List()` / `skills.Get(name)` to inject domain guidance.
- **Nexus relationship.** Pulse is standalone. Nexus (upstream orchestrator) discovers capabilities via `pulse manifest --json` and loads skills from the embedded pack. Pulse has no dependency on Nexus.

For contributor recipes — adding an aggregator, attribute, filterer, grouper, window operator, feature operator, statistical test, synth distribution, I/O format, MCP tool, error code, or field type; porting; debugging predict; regenerating goldens; wiring an MCP client — read `skills/contributor-workflow.md`.

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if any trigger fires without the required update.

| If you change... | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator | `skills/aggregation-guide.md` + `descriptor/capabilities_aggregators.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered attribute | `skills/attribute-composition.md` + `descriptor/capabilities_attributes.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered filterer | `skills/aggregation-guide.md` (filtering section) + `descriptor/capabilities_filterers.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered grouper | `skills/grouper-design.md` + `descriptor/capabilities_groupers.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered window operator | `skills/window-operations.md` + `descriptor/capabilities_windows.go` | `TestSkillsCoverAllWindowTypes`, `TestManifestOperatorsComplete` |
| A registered feature operator | `skills/feature-engineering.md` + `descriptor/capabilities_features.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered statistical test (`TEST_*`) | `skills/statistical-testing.md` + `types/streamability.go` + `descriptor/capabilities_tests.go` | `TestStreamability_TestsKnown`, `TestManifestTestsComplete` |
| A registered tier-2 post-test variant | `descriptor/capabilities_tests.go` (`postTestCapabilities`) | `TestManifestPostTestsComplete` |
| A registered synth distribution kind | `skills/synthetic-data.md` + `descriptor/capabilities_distributions.go` | `TestSkillsCoverAllSynthDistributions`, `TestManifestDistributionsComplete` |
| An operator's streaming capability | `types/streamability.go` + `types/streamability_test.go` table | `TestRegistryStreamabilityMatchesTypes`, `TestManifestStreamableMatchesTypes` |
| An error code (added/removed/renamed) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + Fixups via `pulse_errors_lookup` | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A CLI leaf (added/removed/flag added) | `skills/getting-started.md` + `skills/contributor-workflow.md` if internal | `TestSkillsCoverAllCliLeaves` |
| A `--json` envelope or `format_version` value | CLAUDE.md "Output Format Contract" | `TestClaudeMdMentionsFormatVersion` |
| A `.pulse` file format change (header, new field type) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` | `TestSkillsCoverAllFieldTypes`, `TestClaudeMdMentionsFormatVersion` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| A new architectural decision | CLAUDE.md (relevant section) + PRD if applicable | reviewer enforcement |
| An environment variable | CLAUDE.md "Build / Env" + `skills/getting-started.md` | `TestClaudeMdMentionsAllEnvVars` |
| A registered MCP tool (added/removed) | `skills/mcp-integration.md` + `internal/mcp/mcptools/meta.go` | `TestSkillsCoverAllMCPTools`, `TestManifestMCPToolsComplete` |
| Managed-import sidecar shape (`imports.Sidecar`) | `skills/mcp-integration.md` (Managed imports + TTL section) + `CLAUDE.md` "Build / Env" env var list | reviewer enforcement |
| A new MCP action tool with field-name params | `internal/mcp/schema_bind.go` + `skills/mcp-integration.md` (Schema-bound enums section) | `TestMCPSchemaBinding_RemovesInvalidFields`, `TestMCPSchemaBinding_AllFieldsInFiltererEnum`, `TestMCPSchemaBinding_SampleAndFacetFieldEnum`, `TestMCPSchemaBinding_InspectSucceedsRegistersBindings`, `TestMCPSchemaBinding_BindOnOpenFalse` |
| The default operator table | CLAUDE.md "Smart defaults" + `skills/getting-started.md` | `TestDefaults_Applied` + reviewer enforcement |
| A natural-query parsing route | `internal/query/query.go` grammar + tests + `skills/query-router-prompt.md` + `skills/request-recipes.md` | `TestNaturalQuery_HeuristicGrammar` |
| A request example under `examples/` | `_meta` block (unique kebab name, category matching directory, tags from canonical taxonomy, operators alphabetized + matching body) | `TestExamples_AllParseAsRequest`, `TestExamples_UniqueNames`, `TestExamples_TagsFromTaxonomy`, `TestExamples_OperatorsMatchBody`, `TestExamples_CategoryMatchesDirectory`, `TestManifestExamplesPopulated` |
| Example tag taxonomy | `CanonicalTags` in `examples/library.go` + mdBook chapter `docs/src/examples/library.md` | `TestExamples_TagsFromTaxonomy` |

The Update Demand applies recursively to itself: new trigger rows require this table to be updated in the same PR. `TestUpdateDemandTableCovers` parses this section and asserts every component category and contract type has a row.

If you find yourself wanting to defer the doc/skill update to "a follow-up PR," stop. The follow-up will not happen, the next Claude Code session will read stale guidance and produce wrong code. Update in the same PR or do not merge.

## Architecture

```
pulse/
├── cmd/pulse/              # CLI binary — only binary
├── pulse.go                # Public facade
├── service/                # Orchestration: wires processing to encoding
├── processing/             # Aggregators, attributes, filterers, groupers
│   ├── window/             # WIN_* operators
│   └── feature/            # FEAT_* pre-filter feature engineers
├── encoding/               # .pulse binary codec
├── io/                     # Tabular ↔ .pulse adapters
│   ├── csv|tsv|ndjson|jsonarray|jsonshared/
│   └── arrow|parquet|excel/
├── fs/                     # afero-based filesystem abstraction
├── errors/                 # Typed error codes (CodedError system)
├── types/                  # Request/response structs + streamability table
├── descriptor/             # manifest, predict, inspect, envelope (no-execute)
├── skills/                 # //go:embed markdown skill pack
├── examples/               # //go:embed runnable request examples
├── synth/                  # Synthetic data generator
├── docs/                   # mdBook source (GitHub Pages)
└── internal/
    ├── cli/                # CLI internals
    └── mcp/                # MCP server (wraps pulse.Pulse)
        └── mcptools/       # Tool name + description metadata (no import cycle)
```

`pulse.go` re-exports `types.Request` → `pulse.Request`, `types.Response` → `pulse.Response`, `types.ComposedRequest` → `pulse.ComposedRequest`, plus `synth.Spec`/`Result`/`Options`/`Profile`/`ProfileOptions`.

CLI commands map 1:1 to manifest's command list: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `mcp`, plus `synth from-schema`, `synth from-profile`, `profile create`.

`internal/mcp/` registers ten tools (one per facade method plus `pulse_ask` one-shot that collapses inspect→predict→process) and two resource schemes (`pulse://`, `pulse-skill://`).

Documentation lives in `docs/` (mdBook, published to <https://frankbardon.github.io/pulse/>). Skills under `skills/` are the LLM-facing surface.

## Code Conventions

### Naming

- All identifiers, comments, docs are Pulse-native. No predecessor references (`TestNoOrbitPrefix`, `TestNoOrbitPrefixes`).
- Module path: `github.com/frankbardon/pulse`. `io/` sub-packages imported as `pio "..."` to avoid stdlib collision.
- Component types: SCREAMING_SNAKE — `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`, `WIN_LAG`, `FEAT_LOG`, `TEST_T`.
- Error codes: DOMAIN_CATEGORY — `ENCODING_INVALID`, `PROCESSING_CONFIG`, `SERVICE_VALIDATION`, `DATA_FILE`, `CLI_INPUT`, `PULSE_IMPORT_ROW_ERROR`.

### Error handling

Six error domains: `CLI`, `DATA`, `ENCODING`, `PROCESSING`, `PULSE`, `SERVICE`. Canonical list in `errors/codes.go` (`allCodes`). Every code must have an entry in `codeMetadata` (`errors/fixup_metadata.go`) with a `Message` + at least one `Fixup` template OR `FixupNotApplicable: true`. Per-code prose is reactive lookup via `pulse_errors_lookup` (MCP) / `pulse errors lookup CODE` (CLI). Manifest carries only the alphabetized code-name list.

Field descriptions in `.pulse` files are capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`). Low-quality descriptions (empty, <10 chars, generic words like "n/a"/"tbd"/"value") emit `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warnings (errors under `--strict`).

### Byte-layout invariants

`.pulse` binary format:

1. **9-byte header:** 8-byte magic `PULSE\x00\x00\x00` + 1-byte format version `0x01`. `encoding.MagicBytes`, `encoding.FormatVersion`, `encoding.HeaderSize = 9`.
2. **Schema block:** field descriptors (name, type byte, byte offset, bit position, optional description).
3. **Dictionary blocks:** inline after schema for `categorical_u8/u16/u32`.
4. **Record data:** fixed-width rows; size derived from schema.

19 field types (full table + sizes in `skills/cohort-schema-design.md`, enforced by `TestSkillsCoverAllFieldTypes`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `nullable_bool`, `nullable_u4`/`u8`/`u16`, `date`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `nullable_decimal128`, `point_f64`, `h3_cell`. Bit-packed types (`nullable_bool`, `nullable_u4`, `packed_bool`) return `ByteSize() == 0` — they share bytes with adjacent fields. Schema reader rejects unknown type bytes at parse time with `ENCODING_INVALID`.

### Smart defaults

When a request slot names a field but omits the operator `Type`, the engine infers it from the schema type. Rule table lives in `descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u*/f*/decimal*) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (component `"day"`) |
| `nullable_bool` / `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `point_f64` / `h3_cell` | `AGG_GEO_CENTROID` | `GROUP_H3_CELL` (resolution 7) |

Defaults apply only when `Field` is set and `Type` is empty; never override explicit `Type`; never cross categories; never default tier-1/tier-2 tests, filter expressions, attributes, windows, or features. Disable via `pulse.Options{DisableDefaults: true}` or `--no-defaults`. Predict always computes `DefaultsApplied`.

## Output Format Contract

### `--json` envelope

All `--json` CLI output and descriptor operations use `descriptor.Envelope`:

```json
{
  "format_version": "1.0",
  "data": { ... },
  "errors": [],
  "warnings": []
}
```

- `format_version` is always `"1.0"`. Changes MUST update this section (`TestClaudeMdMentionsFormatVersion`).
- `errors` / `warnings` use `{"code", "message", "details"}` entries. Empty array (never null) when absent.

Additive-only policy: bump `format_version` only on backward-incompatible envelope shape changes. New `data` fields don't trigger a bump; renames/removals do.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** All structured output goes through `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf` over `descriptor/envelope.go`, `manifest.go`, `predict.go`, `inspect.go`.
- **No hand-built XML/CDATA.** Use `encoding/xml` if XML is ever added.
- Use `descriptor.NewEnvelope(data)` — auto-sets `format_version`, empty `errors`, empty `warnings`.

### Manifest payload

`descriptor.BuildManifest()` returns a deterministic LLM-bootstrap blob — one fetch per session, client-cached. Reachable via `pulse manifest --json` and `pulse_manifest`. Top-level fields on `Manifest` (sort-stable, golden-checked at `descriptor/testdata/manifest.json`):

- `format_version` — `"1.0"`.
- `commands []Command` — CLI leaf catalog.
- `components Components` — six `[]Operator` slices (aggregators, attributes, filterers, groupers, windows, features). Each carries `name`, `category`, `description`, `params`, `accepts_types`, `emits_type` / `emits_type_note`, `streamable`, `streamable_hint`.
- `tests []TestMeta` (tier-1) + `post_tests []TestMeta` (tier-2 with non-empty `variant`, `family ∈ AllTestTypes()`).
- `synth_distributions []DistributionMeta`.
- `error_codes_count int` + `error_domains []string` + `error_codes []string` — slim, name-only. Per-code detail via `pulse_errors_lookup`.
- `mcp_tools []MCPTool`, `cohort_types []CohortFieldType` (with compatible-operator cross-refs), `skills []SkillMeta`.

Capability declarations live in `descriptor/capabilities_*.go`. MCP tool metadata lives in `internal/mcp/mcptools/meta.go` (descriptor mirrors it without import cycle).

### Predict / Inspect contracts

- **Predict structural ban:** `descriptor/predict.go` MUST NOT import `service/` or `processing/`. Enforced by `TestPredictNoExecutionImports`. Predict reads only header + schema, never records. For capability lookups, use `types/` constants (e.g., `types.AllAggregationTypes()`).
- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`. Dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`. Missing descriptions get a synthesized fallback with `description_source` flagged.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods on `types.AggregationType`/`AttributeType`/`FiltererType`/`GroupType`/`WindowType`/`FeatureType` plus schema gates (decimal, geo). Runtime parity via `processing.CanStreamRequest(req, schema)` (`TestPredict_Streamable_MatchesRuntime`).

### Streaming Process

Four orchestrator modes — single-pass, grouped, two-pass attributes (Welford-Pébaÿ), streaming features. Forced buffered: median/percentile/zscore aggregators, `ATTR_PERCENTILE`, `GROUP_QUANTILE`/`GROUP_DATE`, window operators, decimal/geo paths, tier-1 tests combined with groupers/features/two-pass attrs, all tier-2 tests. CLI streams via `pulse api process --stream` / `pulse api compose --stream` (NDJSON one row per line). Library: `pulse.ProcessStream(ctx, req) (RowIter, error)`.

### Parallel Compose

`pulse.ComposeParallel(ctx, req, opts)` fans out a `ComposedRequest` over a bounded worker pool. Order-preserving by slot index. `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}` (FailFast defaults true). CLI: `pulse api compose --parallel N [--no-fail-fast]`.

## Non-Skippable CI Gates

CLAUDE.md hygiene:
- `TestClaudeMdMentionsFormatVersion` — CLAUDE.md must mention the current `format_version` `"1.0"`.
- `TestClaudeMdMentionsAllEnvVars` — every `PULSE_*` env var in Go source must appear in CLAUDE.md.
- `TestClaudeMdMentionsAllNonSkippableGates` — every test name with these prefixes must be listed in CLAUDE.md.
- `TestUpdateDemandTableCovers` — Update Demand table must cover every component category and contract type.

Predecessor-reference hygiene:
- `TestNoOrbitPrefix` — no type-constant string contains predecessor references.
- `TestNoOrbitPrefixes` — no error-code string contains predecessor references.

Descriptor contracts:
- `TestPredictNoExecutionImports` — `descriptor/predict.go` must not import `service/` or `processing/`.
- `TestDescriptorNoFmtSprintf` — no `fmt.Sprintf` in `descriptor/envelope.go`/`manifest.go`/`predict.go`/`inspect.go`.
- `TestGoldensNotHandEdited` — golden files end with valid `// golden-hash: <sha256>` line.
- `TestPerPackageCoverageFloors` — package directories exist; documents target coverage floors per package.

Skill-coverage:
- `TestSkillsCoverAllComponents` — every aggregator/attribute/filterer/grouper in registries mentioned in its target skill.
- `TestSkillsCoverAllCliLeaves` — every CLI leaf appears in `skills/getting-started.md`.
- `TestSkillsCoverAllFieldTypes` — every field type appears in `skills/cohort-schema-design.md`.
- `TestSkillsCoverAllWindowTypes` — every `WIN_*` operator appears in `skills/window-operations.md`.
- `TestSkillsCoverAllMCPTools` — every registered MCP tool appears in `skills/mcp-integration.md`.
- `TestSkillsCoverAllSynthDistributions` — every distribution kind in `synth.AllDistributions()` appears in `skills/synthetic-data.md`.

Other contract gates (not in the prefix set but load-bearing): `TestManifestOperatorsComplete`, `TestManifestStreamableMatchesTypes`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete`, `TestManifestDistributionsComplete`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`, `TestManifestMCPToolsComplete`, `TestManifestExamplesPopulated`, `TestManifest_SkillsNotEmpty`, `TestCodesHaveFixups`, `TestRegistryStreamabilityMatchesTypes`, `TestPredict_Streamable_MatchesRuntime`, `TestStreamability_*Known` (Aggregations/Attributes/Filterers/Groups/Windows/Features/Tests), `TestCanStreamRequest_RegressionMatrix`, `TestCohortTypeCrossRefsDeterministic`, `TestDefaults_Applied`, `TestNaturalQuery_HeuristicGrammar`, `TestExamples_*`, `TestMCPSchemaBinding_*`, `TestErrorsLookup_*`, `TestMCPErrorsLookup_RoundTrip`.

## Build / Env

`make build` (default), `make test`, `make fmt`, `make vet`, `make lint`, `make cover`, `make clean`, `make docs`, `make docs-serve`, `make docs-clean`. A `.env` at repo root is auto-loaded by the Makefile.

**Environment variables:**

- `PULSE_DATA_DIR` — base directory for `.pulse` cohort files. Used by `fs.Default()` when no explicit `DataDir` or `afero.Fs` is provided. Only required env var for runtime operation. Embedders can bypass via `pulse.Options{DataDir}` or `pulse.Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdirectory under the Pulse fs root. Defaults to `imports`. Honoured by `imports.Manager` (and therefore by `pulse_import` / `pulse import auto`). `pulse.Options{ImportsDir}` overrides.
- `PULSE_IMPORT_TTL` — default TTL for managed imports when the caller doesn't pass one. Accepts Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin` for never-expire. Defaults to `7d`. `pulse.Options{ImportTTL}` overrides.

Hermetic testing: `fs.NewMemMap()` returns a `Config` backed by `afero.NewMemMapFs()`. No disk I/O.

## Skill Pack

20 skills under `skills/`, embedded via `//go:embed`. Each skill has YAML frontmatter:

```yaml
---
name: skill-name
description: What the skill teaches
type: guide   # or "reference"
applies_to: process, compose, predict
---
```

`applies_to` entries must be valid CLI leaves (`process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`).

Per-component target skill:

| Category | Skill |
|---|---|
| Aggregator (`AGG_*`) | `skills/aggregation-guide.md` |
| Attribute (`ATTR_*`) | `skills/attribute-composition.md` |
| Filterer (`FILTER_*`) | `skills/aggregation-guide.md` (filtering section) |
| Grouper (`GROUP_*`) | `skills/grouper-design.md` |
| Window (`WIN_*`) | `skills/window-operations.md` |
| Feature (`FEAT_*`) | `skills/feature-engineering.md` |
| Statistical test (`TEST_*`) | `skills/statistical-testing.md` |
| Synth distribution | `skills/synthetic-data.md` |
| CLI leaf | `skills/getting-started.md` |
| Field type | `skills/cohort-schema-design.md` |
| MCP tool | `skills/mcp-integration.md` |
| Error code | `errors/fixup_metadata.go` (surfaced via `pulse_errors_lookup`) |

**Current registered counts** (full lists in each skill, enforced by coverage gates): 18 aggregators, 6 attributes, 6 filterers, 6 groupers, 10 window operators, 8 feature operators, 20 statistical tests (18 tier-1 row tests + tier-2 variants), 12 synth distributions.

Adding a new skill: create `skills/<name>.md` with frontmatter, add entry to `skills/index.json`, bump the count in `TestSkillsList_ReturnsAll` and `TestSkillsNames`. Run `go test ./skills/...`.

## What NOT to Do

- **Do not import `service/` or `processing/` from `descriptor/`.** Predict/inspect/manifest are no-execute. `TestPredictNoExecutionImports` will fail.
- **Do not hand-edit golden files.** Regenerate via `go test ./descriptor/ -run 'Test.*Golden' -update`. `TestGoldensNotHandEdited` verifies hashes.
- **Do not add implementation without tests in the same PR.** TDD: write the test first, watch it fail, then implement.
- **Do not use `fmt.Sprintf` for JSON/XML.** Use `encoding/json` and `descriptor.NewEnvelope(data)`.
- **Do not defer skill or CLAUDE.md updates.** The follow-up PR will not happen. Next session reads stale guidance.
- **Do not add a component without updating the registry** (`processing/registry.go`) and `types.All*Types()`.
- **Do not bypass `afero.Fs`** for file access — defeats `fs.NewMemMap()` and the custom-storage extension hook.
- **Do not put business logic in `cmd/pulse/`.** CLI is a thin adapter — parse flags, construct library objects, call methods, format output.
