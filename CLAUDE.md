# CLAUDE.md

## Project Overview

Pulse is a self-describing tabular data processing engine. Ships as a Go library (`github.com/frankbardon/pulse`) and a CLI binary (`cmd/pulse/`). Library is primary; CLI is a thin adapter.

**Design principles:**

- **Library-first.** `pulse.go` facade (`New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`) is the public API. CLI never contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header. `descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `skills/` embeds 22 markdown skills via `//go:embed`. LLM agents call `skills.List()` / `skills.Get(name)` to inject domain guidance.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators (AGG/ATTR/FILTER/GROUP/WIN/FEAT/TEST/SYNTH) + expr functions + lookup tables at `pulse.New()` time. Registered extensions are first-class — predict, manifest, MCP schema-binding, and the runtime treat them identically to built-ins. See `skills/extension-points.md`.
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
| A registered regression operator (`REG_*`) | `skills/regression-modeling.md` + `descriptor/capabilities_regressions.go` | `TestSkillsCoverAllRegressions`, `TestManifestRegressionsComplete` |
| A regression modifier (`Resample` / `Selection` enum value) | `skills/regression-modeling.md` + capability metadata | `TestManifestRegressionsComplete` |
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
| `pulse.Options.Extensions` API (Registration struct shape, ExprFunction, LookupTable, naming rules) | `skills/extension-points.md` + CLAUDE.md "Extension Points" section | `TestExtensions_NameInvalidRegex`, `TestExtensions_ProbeAggregator_StreamableMismatch`, `TestExtensions_Manifest_EmissionPopulatesAllCategories`, `TestExtensions_Predict_AcceptsCustomFeatureType`, `TestMCPSchemaBinding_IncludesCustomAggregator` |
| Extension registration validation rule (regex, reserved namespace, ParamMeta shape) | `extensions_validate.go` + `skills/extension-points.md` + CLAUDE.md "Extension Points" section | `TestExtensions_NameInvalidRegex`, `TestExtensions_NameReservedNamespace`, `TestExtensions_ParamMetaInvalidJSONType` |
| `FacetRequest` / `FacetResult` shape | `types/facet.go` + `skills/facet-design.md` + `descriptor/capabilities_facet.go` + `descriptor/facet.go` (`ValidateFacet`) | `TestFacetSchema_*`, `TestManifestFacetCapability` |
| Facet streamability conditions | `descriptor/capabilities_facet.go` (`StreamableConditions`) + `skills/facet-design.md` | reviewer enforcement |
| `pulse.Options.ProjectBufferedFields` flag or `processing.NeededFields` extraction (new operator slot, expr identifier source, Params-referenced field) | CLAUDE.md "Projected buffered decode" subsection + `skills/extension-points.md` (FieldInputs hook + extractor surface) | `TestNeededFields_*`, `TestProjection_BufferedMatchesFullDecode_*`, `TestProjection_ByteCursorAlignmentWhenSkipping`, `TestReadRecordProjected_*` |
| `FieldInputsFunc` hook on a registration struct (added/removed) | `extensions.go` (registration struct field) + `extensions_runtime.go` (wire into ExtensionRegistry.FieldInputs) + `skills/extension-points.md` | `TestNeededFields_ExtensionWithFieldInputs`, `TestNeededFields_UnknownExtensionWidens` |

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

`internal/mcp/` registers eleven tools (one per facade method plus `pulse_ask` one-shot that collapses inspect→predict→process and `pulse_facet_schema` for multi-field rich facets) and two resource schemes (`pulse://`, `pulse-skill://`).

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

17 field types (full table + sizes in `skills/cohort-schema-design.md`, enforced by `TestSkillsCoverAllFieldTypes`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `nullable_bool`, `nullable_u4`/`u8`/`u16`, `date`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `nullable_decimal128`. Bit-packed types (`nullable_bool`, `nullable_u4`, `packed_bool`) return `ByteSize() == 0` — they share bytes with adjacent fields. Schema reader rejects unknown type bytes at parse time with `ENCODING_INVALID`.

### Smart defaults

When a request slot names a field but omits the operator `Type`, the engine infers it from the schema type. Rule table lives in `descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u*/f*/decimal*) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (component `"day"`) |
| `nullable_bool` / `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

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
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods on `types.AggregationType`/`AttributeType`/`FiltererType`/`GroupType`/`WindowType`/`FeatureType` plus schema gates (decimal). Runtime parity via `processing.CanStreamRequest(req, schema)` (`TestPredict_Streamable_MatchesRuntime`).

### Streaming Process

Four orchestrator modes — single-pass, grouped, two-pass attributes (Welford-Pébaÿ), streaming features. Forced buffered: median/percentile/zscore aggregators, `ATTR_PERCENTILE`, `GROUP_QUANTILE`/`GROUP_DATE`, window operators, decimal paths, tier-1 tests combined with groupers/features/two-pass attrs, all tier-2 tests. CLI streams via `pulse api process --stream` / `pulse api compose --stream` (NDJSON one row per line). Library: `pulse.ProcessStream(ctx, req) (RowIter, error)`.

### Projected buffered decode

`pulse.Options.ProjectBufferedFields` (opt-in, defaults `false`) enables per-request field projection on the streaming iterator. When enabled, `processing.NeededFields(req, schema, ext)` walks every request slot — Aggregations.Field, Attributes (Field, Target, Predictors, expr-AST identifiers via `expr-lang/expr/parser` + `expr-lang/expr/ast` for `ATTR_FORMULA` / `FILTER_EXPRESSION`), Filterers.Field, Groups.Field, Windows (Field + PartitionBy + OrderBy), Features (Field + `stratify` / `target` from Params for `FEAT_TRAIN_TEST_SPLIT` / `FEAT_TARGET_ENCODE`), Tests (Field, Field2, SplitBy, Rows, Cols, SubjectField, OrderBy), Regressions (Target + Predictors), Sort.Field — and returns the `FieldSet` the request actually reads. The iterator's `ReadRecordWithWideProjected` then advances byte cursors for every field but skips map writes outside the set. Per-record `values`/`nulls`/`wide` map allocations drop proportional to the projection ratio; decode CPU is unchanged. Bit-packed neighbours stay correct because every field still consumes its on-wire bytes, only the map writes are guarded.

Extension operators surface a per-registration `FieldInputs FieldInputsFunc` hook. When set, the projection extractor calls it with the operator's `Params` and includes the returned field names. When absent on a custom operator, the extractor widens to `*` (every field) — projection then falls back to the full-decode path so the runtime stays correct. Built-in operators are fully introspectable; only extension-resolved operators can widen.

### Parallel Compose

`pulse.ComposeParallel(ctx, req, opts)` fans out a `ComposedRequest` over a bounded worker pool. Order-preserving by slot index. `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}` (FailFast defaults true). CLI: `pulse api compose --parallel N [--no-fail-fast]`.

### Facet endpoints

Two facet entry points. `pulse.Facet(ctx, path, field)` is the simple distinct-values returner — categorical fast path through the dictionary, numeric fields stream the file. `pulse.FacetSchema(ctx, *FacetRequest)` is the multi-field rich endpoint: per-value counts (sorted descending by count, ties ascending by value), null tallies, Welford online numeric stats (count/sum/min/max/mean/stddev), optional `NumericPercentiles` (forces a buffered per-field sort), optional `IncludeHistogram` with caller-supplied `HistogramRange` (single-pass), optional `DiscreteTopK` truncation with `TruncatedAt` warning, and optional `AdditiveFields` contribution counts that strip the field's own clauses from a copy of the base filter. Capability descriptor lives on `Manifest.Facet`. `descriptor.ValidateFacet(data, req)` is the no-execute predict equivalent. CLI: `pulse api facet` falls back to the simple shape unless any rich flag (`--request`, repeat `--field`, `--top-k`, `--percentile`, `--histogram`, `--additive`) is set; MCP exposes both `pulse_facet` and `pulse_facet_schema`.

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
- `TestSkillsCoverAllRegressions` — every `REG_*` operator appears in `skills/regression-modeling.md`.

Extension API contract:
- `TestExtensions_ValidRegistrationPasses` — round-trip a valid registration through `pulse.New`.
- `TestExtensions_NameInvalidRegex` — name validation rejects malformed registrations.
- `TestExtensions_NameWrongCategoryPrefix` — an `AggregatorRegistration` cannot smuggle an `ATTR_*`-prefixed name (and so on).
- `TestExtensions_NameReservedNamespace` — `BUILTIN/STANDARD/CORE/PULSE` namespaces rejected.
- `TestExtensions_CollisionWithBuiltin` — registering a built-in name returns `PULSE_EXTENSION_NAME_COLLISION`.
- `TestExtensions_DuplicateWithinCategory` — duplicate registration in same category rejected.
- `TestExtensions_DuplicateAcrossCategoriesAllowed` — same suffix is fine across category prefixes.
- `TestExtensions_ParamMetaInvalidJSONType` / `TestExtensions_ParamMetaEmptyName` / `TestExtensions_ParamMetaRequiredWithDefault` — ParamMeta validation.
- `TestExtensions_AttributeModeRequired` / `TestExtensions_AttributeModeUnknown` — attribute Mode enforcement.
- `TestExtensions_TestTierMissingFactory` / `TestExtensions_TestTierBothFactoriesSet` — test tier ↔ factory pairing.
- `TestExtensions_ExprFunctionEmptyName` / `TestExtensions_ExprFunctionNilFn` / `TestExtensions_ExprFunctionDuplicate` — expr-function validation.
- `TestExtensions_LookupTableRowsOK` / `TestExtensions_LookupTableFuncOK` / `TestExtensions_LookupTableNeitherOrBoth` — exactly-one-of Rows/Lookup.
- `TestExtensions_ProbeAggregator_StreamableMismatch` — Streamable=true with buffered-only factory.
- `TestExtensions_ProbeAggregator_NonStreamableAccepted` — non-streamable registration accepts buffered-only factory.
- `TestExtensions_ProbeAggregator_FactoryPanicCaught` / `TestExtensions_ProbeAggregator_FactoryReturnsError` / `TestExtensions_ProbeAggregator_FactoryReturnsNil` — probe error surface.
- `TestExtensions_ProbeAttribute_RowLocalMismatch` / `TestExtensions_ProbeAttribute_TwoPassMismatch` / `TestExtensions_ProbeAttribute_BufferedAcceptsAnyComputer` — attribute Mode ↔ interface contract.
- `TestExtensions_RegistryInstalledOnService` / `TestExtensions_ZeroValueProducesNilRegistry` / `TestExtensions_RegistryIsolationAcrossInstances` / `TestExtensions_RegistryFallsThroughToBuiltins` / `TestExtensions_OnlyExprEntriesYieldsRegistry` / `TestExtensions_AttributeStreamabilityFromMode` — Service-side wiring.
- `TestExtensionRegistry_NilFallsThroughToBuiltin` / `TestExtensionRegistry_OverlayWinsOverBuiltin` / `TestExtensionRegistry_CustomAggregatorResolves` / `TestExtensionRegistry_IsStreamableOverridesBuiltin` / `TestExtensionRegistry_IsStreamableFallsBackToTypeSwitch` / `TestExtensionRegistry_IsolationBetweenRegistries` / `TestExtensionRegistry_CustomNamesEnumerateOverlayOnly` — overlay-registry semantics.
- `TestExtensions_AggregatorRoundTrip_Streaming` / `_Buffered` / `_OverlayOverridesBuiltin` — aggregator end-to-end.
- `TestExtensions_AttributeRoundTrip_RowLocal` / `_Buffered` — attribute end-to-end.
- `TestExtensions_FiltererRoundTrip` / `TestExtensions_GrouperRoundTrip` / `TestExtensions_WindowRoundTrip` / `TestExtensions_FeatureRoundTrip` / `TestExtensions_TestRoundTrip_Tier1` / `TestExtensions_TestRoundTrip_Tier2` — remaining categories.
- `TestExtensions_ExprFunction_AvailableInFormula` / `TestExtensions_LookupTable_AvailableInFormula` / `TestExtensions_LookupTable_AvailableInFilterExpression` / `TestExtensions_LookupTable_UnknownReturnsCodedError` / `TestExtensions_LookupTable_MissReturnsCodedError` / `TestExtensions_LookupTable_FuncBackedResolves` — expr env round-trip.
- `TestExtensions_Manifest_EmissionPopulatesAllCategories` / `TestExtensions_Manifest_EmptyWhenNoExtensions` / `TestExtensions_Manifest_DeterministicSort` — manifest emission.
- `TestExtensions_Predict_AcceptsCustomFeatureType` / `TestExtensions_Predict_FlagsUnknownCustomFeature` / `TestExtensions_Predict_StreamableFlagFromSnapshot` / `TestExtensions_Predict_BufferedCustomAggregatorBlocksStreaming` / `TestExtensions_Predict_DescriptorImportContractHolds` — predict integration.
- `TestMCPSchemaBinding_IncludesCustomAggregator` / `TestMCPSchemaBinding_BackwardCompatBindNoCustomNames` / `TestMCPSchemaBinding_DedupAndSort` — MCP schema binding.

Other contract gates (not in the prefix set but load-bearing): `TestManifestOperatorsComplete`, `TestManifestStreamableMatchesTypes`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete`, `TestManifestDistributionsComplete`, `TestManifestRegressionsComplete`, `TestRegressionStreamabilityMatchesTypes`, `TestRegressionTypesKnown`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`, `TestManifestMCPToolsComplete`, `TestManifestExamplesPopulated`, `TestManifest_SkillsNotEmpty`, `TestCodesHaveFixups`, `TestRegistryStreamabilityMatchesTypes`, `TestPredict_Streamable_MatchesRuntime`, `TestStreamability_*Known` (Aggregations/Attributes/Filterers/Groups/Windows/Features/Tests), `TestCanStreamRequest_RegressionMatrix`, `TestCohortTypeCrossRefsDeterministic`, `TestDefaults_Applied`, `TestNaturalQuery_HeuristicGrammar`, `TestExamples_*`, `TestMCPSchemaBinding_*`, `TestErrorsLookup_*`, `TestMCPErrorsLookup_RoundTrip`, `TestFacetSchema_*`, `TestManifestFacetCapability`, `TestValidateFacet_*`, `TestShardArchiveMagicDispatch`.

## Build / Env

`make build` (default), `make test`, `make fmt`, `make vet`, `make lint`, `make cover`, `make clean`, `make docs`, `make docs-serve`, `make docs-clean`. A `.env` at repo root is auto-loaded by the Makefile.

**Environment variables:**

- `PULSE_DATA_DIR` — base directory for `.pulse` cohort files. Used by `fs.Default()` when no explicit `DataDir` or `afero.Fs` is provided. Only required env var for runtime operation. Embedders can bypass via `pulse.Options{DataDir}` or `pulse.Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdirectory under the Pulse fs root. Defaults to `imports`. Honoured by `imports.Manager` (and therefore by `pulse_import` / `pulse import auto`). `pulse.Options{ImportsDir}` overrides.
- `PULSE_IMPORT_TTL` — default TTL for managed imports when the caller doesn't pass one. Accepts Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin` for never-expire. Defaults to `7d`. `pulse.Options{ImportTTL}` overrides.

Hermetic testing: `fs.NewMemMap()` returns a `Config` backed by `afero.NewMemMapFs()`. No disk I/O.

## Extension Points

`pulse.Options.Extensions` is the public surface for embedders that need to inject domain-specific operators or expression-runtime extensions without forking the engine. Eight operator categories plus expression functions and lookup tables. Registration happens at `pulse.New()` time; restart to change the registered set.

**Naming policy:** custom operator names match `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$` — three uppercase ASCII segments separated by underscores. Namespaces `BUILTIN`, `STANDARD`, `CORE`, `PULSE` are reserved. Collision with a built-in name is rejected. Validation runs in this order at `pulse.New`: regex/reserved/collision/duplicate → probe-validation (factory invocation + interface check) → runtime registration.

**Probe-validation:** the engine constructs each registered factory once against a minimal synthetic schema. Streamable-flagged registrations must return the corresponding streaming interface (`OnlineAggregator` / `RowLocalAttribute` / `TwoPassAttribute`). Mismatch → `PULSE_EXTENSION_STREAMABLE_MISMATCH`. Factory panics are caught and surface as `PULSE_EXTENSION_FACTORY_PANIC`.

**Expression environment:** `ExprFunctions` are merged into the expr-lang environment used by `ATTR_FORMULA` and `FILTER_EXPRESSION`. `LookupTables` are reachable via the built-in `lookup(table, keys...)` function, which is auto-injected when at least one table is registered. Rows-backed tables join keys with `|`; function-backed tables receive the raw `[]string` slice. Unknown table → `PULSE_LOOKUP_TABLE_UNKNOWN`. Missing key → `PULSE_LOOKUP_MISS`.

**Manifest visibility:** the root manifest carries a top-level `extensions` block listing every registered operator + expr function + lookup table (with `has_rows_data` to distinguish static maps from function-driven tables). The schema-bound MCP tools (post-`pulse_inspect`) include custom names in their per-category enums.

**Snapshot pattern:** `descriptor.ExtensionsSnapshot` is the read-only projection passed into `descriptor.PredictOptions.Extensions` and `mcp.BindSessionToolsWithExtensions`. Built by `pulse.New` via `buildExtensionsSnapshot`, cached on the Service. Predict and the schema binder consume the snapshot only — descriptor stays free of `service/` and `processing/` imports (gated by `TestPredictNoExecutionImports`).

**FieldInputs hook (buffered-projection introspection):** every operator registration (`AggregatorRegistration`, `AttributeRegistration`, `FiltererRegistration`, `GrouperRegistration`, `WindowRegistration`, `FeatureRegistration`, `TestRegistration`) accepts an optional `FieldInputs FieldInputsFunc`. When set, `processing.NeededFields` calls it with the operator's raw `Params` and includes the returned field names in the projection set. When omitted on a custom operator, the projection extractor widens to "every field" so the runtime stays correct (the operator is opaque). Built-in operators are always introspectable; only extension-resolved operators can widen. The hook is plumbed via `buildRuntimeExtensions` into `processing.ExtensionRegistry.FieldInputs`, keyed by `StreamabilityKey(category, name)`.

The embedder-facing surface lives in `extensions.go` (types), `extensions_validate.go` (validation), `extensions_probe.go` (probe-validation), `extensions_runtime.go` (runtime registry conversion), and `extensions_snapshot.go` (manifest/predict snapshot). The runtime-side overlay lives in `processing/extensions.go`.

Full embedder-facing recipe in `skills/extension-points.md`.

## Skill Pack

23 skills under `skills/`, embedded via `//go:embed`. Each skill has YAML frontmatter:

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
| Regression (`REG_*`) | `skills/regression-modeling.md` |
| Synth distribution | `skills/synthetic-data.md` |
| CLI leaf | `skills/getting-started.md` |
| Field type | `skills/cohort-schema-design.md` |
| MCP tool | `skills/mcp-integration.md` |
| Facet endpoint (FacetSchema, FacetRequest/FacetResult) | `skills/facet-design.md` |
| Error code | `errors/fixup_metadata.go` (surfaced via `pulse_errors_lookup`) |
| Extension API surface (registration shape, expr funcs, lookup tables) | `skills/extension-points.md` |

**Current registered counts** (full lists in each skill, enforced by coverage gates): 16 aggregators, 9 attributes, 5 filterers, 5 groupers, 10 window operators, 9 feature operators, 20 statistical tests (18 tier-1 row tests + tier-2 variants), 12 synth distributions, 3 regressions.

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
