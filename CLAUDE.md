# CLAUDE.md

## Project Overview

Pulse is a high-performance, self-describing tabular data processing engine. It ships as a Go library (`github.com/frankbardon/pulse`) and as a CLI binary (`cmd/pulse/`). The library is the primary deliverable; the CLI is a thin adapter over it.

**Design principles:**

- **Library-first.** The `pulse.go` facade (`pulse.New`, `pulse.Options`, `pulse.Process`, `pulse.Compose`, `pulse.Import`, `pulse.Export`, `pulse.Convert`, `pulse.Inspect`, `pulse.Predict`, `pulse.Sample`, `pulse.Facet`) is the public API. The CLI calls the library; it never contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header. The `descriptor/` package provides `manifest`, `predict`, and `inspect` operations that expose the system's capabilities and validate requests without executing them.
- **Skill-augmented.** The `skills/` package embeds 19 markdown skill files into the binary via `//go:embed`. LLM agents (and Nexus, the orchestration layer that consumes Pulse) can call `skills.List()` and `skills.Get(name)` at boot time to inject domain-specific guidance into their context.
- **Nexus relationship.** Pulse is a standalone processing engine. Nexus is the upstream orchestration agent that calls Pulse's library API or CLI. Pulse has no dependency on Nexus. Nexus discovers Pulse's capabilities via `pulse manifest --json` and loads skills from the embedded skill pack.

**Module path:** `github.com/frankbardon/pulse`

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. This is not a courtesy. It is a non-skippable CI failure if any of the trigger conditions below is met without the corresponding doc update.

**Trigger => required update:**

| If you change... | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator | `skills/aggregation-guide.md` (add or update the section for that aggregator) | `TestSkillsCoverAllComponents` |
| A registered attribute | `skills/attribute-composition.md` | `TestSkillsCoverAllComponents` |
| A registered filterer | `skills/aggregation-guide.md` (filtering section) | `TestSkillsCoverAllComponents` |
| A registered grouper | `skills/grouper-design.md` | `TestSkillsCoverAllComponents` |
| A registered window operator | `skills/window-operations.md` | `TestSkillsCoverAllWindowTypes` |
| An error code (added/removed/renamed) | Entry in `errors/fixup_metadata.go` (`codeMetadata`) — Message + Fixups visible via `pulse_errors_lookup` / `pulse errors lookup CODE` | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A CLI leaf (added/removed/flag added) | `CLAUDE.md` "Common Claude Code Workflows" + `skills/getting-started.md` if user-facing | `TestSkillsCoverAllCliLeaves` |
| A `--json` envelope or `format_version` | `CLAUDE.md` "Output Format Contract" | `TestClaudeMdMentionsFormatVersion` |
| A `.pulse` file format change (header layout, new field type) | `CLAUDE.md` "Code Conventions" + `skills/cohort-schema-design.md` | `TestClaudeMdMentionsFormatVersion`, `TestSkillsCoverAllFieldTypes` |
| A new non-skippable CI gate | `CLAUDE.md` (gate listed by name in the relevant section) | `TestClaudeMdMentionsAllNonSkippableGates` |
| A new architectural decision | `CLAUDE.md` (relevant section) + PRD if applicable | reviewer enforcement |
| An environment variable | `CLAUDE.md` "Build / Dev / Test Workflow" + `skills/getting-started.md` | `TestClaudeMdMentionsAllEnvVars` |
| A registered MCP tool (added/removed) | `skills/mcp-integration.md` (Tool surface table) + `internal/mcp/mcptools/meta.go` (name + description) | `TestSkillsCoverAllMCPTools`, `TestManifestMCPToolsComplete` |
| A new MCP action tool with field-name parameters | `internal/mcp/schema_bind.go` (add a per-tool JSON Schema builder + entry in `Bind`) + `skills/mcp-integration.md` (Schema-bound enums section) | `TestMCPSchemaBinding_RemovesInvalidFields`, `TestMCPSchemaBinding_AllFieldsInFiltererEnum`, `TestMCPSchemaBinding_SampleAndFacetFieldEnum`, `TestMCPSchemaBinding_InspectSucceedsRegistersBindings`, `TestMCPSchemaBinding_BindOnOpenFalse` |
| A registered feature operator | `skills/feature-engineering.md` (operator catalog) + capability declaration in `descriptor/capabilities_features.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered synth distribution kind | `skills/synthetic-data.md` (Supported distributions) + capability declaration in `descriptor/capabilities_distributions.go` | `TestSkillsCoverAllSynthDistributions`, `TestManifestDistributionsComplete` |
| A registered statistical test (`TEST_*`) | `skills/statistical-testing.md` (Operator catalog) + `types/streamability.go` + `types/streamability_test.go` + capability declaration in `descriptor/capabilities_tests.go` | `TestStreamability_TestsKnown`, `TestManifestTestsComplete` |
| A registered tier-2 post-test variant | Capability declaration in `descriptor/capabilities_tests.go` (`postTestCapabilities`) | `TestManifestPostTestsComplete` |
| A registered aggregator/attribute/filterer/grouper/window capability metadata | Capability declaration in `descriptor/capabilities_<category>.go` (params, accepts_types, emits_type, streamable_hint) | `TestManifestOperatorsComplete` |
| A new error code | Entry in `errors/fixup_metadata.go` (`codeMetadata`) — bootstrap manifest carries name only; per-code Message + Fixup detail is reactive lookup via `pulse_errors_lookup` | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim` |
| A new operator's streaming capability | `types/streamability.go` (case for the new type) + table in `types/streamability_test.go` | `TestRegistryStreamabilityMatchesTypes`, `TestStreamability_*Known`, `TestManifestStreamableMatchesTypes` |
| The default operator table | `CLAUDE.md` "Code Conventions → Smart defaults" + `skills/getting-started.md` ("Defaults" section) | `TestDefaults_Applied` + reviewer enforcement |
| A natural-query parsing route (new grammar shape) | `internal/query/query.go` grammar + `internal/query/query_test.go` fixtures + `skills/query-router-prompt.md` (router prompt grammar) + `skills/request-recipes.md` (target shapes) | `TestNaturalQuery_HeuristicGrammar` |
| A request example under `examples/` (added/edited) | `_meta` block at top of the file (kebab-name unique across the library, category matching directory, tags drawn from the canonical taxonomy in `examples/library.go`, operators alphabetized and matching the body) | `TestExamples_AllParseAsRequest`, `TestExamples_UniqueNames`, `TestExamples_TagsFromTaxonomy`, `TestExamples_OperatorsMatchBody`, `TestExamples_CategoryMatchesDirectory`, `TestManifestExamplesPopulated` |
| A change to the example tag taxonomy | `CanonicalTags` slice in `examples/library.go` + `skills/request-recipes.md` (pointer block, if the search story shifts) + mdBook chapter `docs/src/examples/library.md` | `TestExamples_TagsFromTaxonomy` |

**The Update Demand applies recursively to itself:** when a new trigger row is added (e.g., a new component category, a new contract), this table MUST be updated in the same PR. `TestUpdateDemandTableCovers` (non-skippable) parses this table and asserts every registered component category and contract type has a row.

If you find yourself wanting to defer the doc/skill update to "a follow-up PR," stop. The follow-up PR will not happen, and the next Claude Code session will read a stale CLAUDE.md and produce wrong code. Update in the same PR or do not merge.

## Architecture

### Package layout

```
pulse/
├── cmd/
│   └── pulse/              # CLI binary (the only binary)
├── pulse.go                # Public facade — pulse.New, pulse.Options
├── service/                # Orchestration layer; wires processing to encoding
├── processing/             # Aggregators, attributes, filterers, groupers, windows, features
│   ├── window/             # WIN_* operators (LAG, LEAD, RANK, RUNNING_*, EWMA, ...)
│   └── feature/            # FEAT_* pre-filter feature engineers (LOG, SQRT, BUCKETIZE, ...)
├── encoding/               # Dynamic schema + record codec (.pulse binary format)
├── io/                     # Bidirectional tabular <-> .pulse adapters
│   ├── csv/                # CSV reader + writer
│   ├── tsv/                # TSV reader + writer
│   ├── ndjson/             # NDJSON reader + writer
│   ├── jsonarray/          # JSON-array reader + writer (single top-level array of flat objects)
│   ├── jsonshared/         # Value coercion helpers shared by ndjson and jsonarray
│   ├── arrow/              # Arrow IPC / Feather V2 reader + writer; shared Arrow<->Pulse type maps
│   ├── parquet/            # Parquet reader + writer (delegates type maps to io/arrow)
│   └── excel/              # Excel reader + writer (Excelize)
├── fs/                     # afero-based filesystem abstraction + extension hook
├── errors/                 # Typed error codes (CodedError system)
├── types/                  # Request/response structs (JSON-serializable)
├── descriptor/             # Self-description: manifest, predict, inspect, envelope
├── skills/                 # Embedded markdown skill pack (//go:embed)
│   ├── index.json          # Manifest of all bundled skills
│   └── *.md               # Individual skill files with YAML frontmatter
├── examples/               # Embedded request-example library (//go:embed)
│   ├── library.go          # Search + Get facade over the embedded JSONs
│   └── <category>/*.json   # 71 runnable types.Request examples with _meta blocks
├── synth/                  # Synthetic data generator (from-schema, from-profile)
├── docs/                   # mdBook source for the human-facing site (published to GitHub Pages)
├── internal/
│   ├── cli/                # CLI internals (descriptor walker, json action)
│   └── mcp/                # MCP server: tool + resource handlers wrapping pulse.Pulse
│       └── mcptools/       # Leaf metadata package (tool names + descriptions) consumed by descriptor
```

User-facing CLI, library-embedding, format, internals, and operations documentation lives in `docs/` and is published to GitHub Pages at <https://frankbardon.github.io/pulse/>. Skills under `skills/` remain the LLM-facing surface and are unchanged.

### Library-first pattern

`pulse.go` is the public API surface. It wraps `service.Service` and provides: `New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`. Embedders use `pulse.New(pulse.Options{...})` and call methods directly. The `types.Request`, `types.Response`, and `types.ComposedRequest` are re-exported as `pulse.Request`, `pulse.Response`, `pulse.ComposedRequest`. Synth and Profile re-export `synth.Spec`, `synth.Result`, `synth.Options`, `synth.Profile`, and `synth.ProfileOptions`.

### CLI-as-thin-adapter

`cmd/pulse/main.go` is the only binary. It parses flags, constructs a `pulse.Pulse` instance, calls library methods, and formats output. It never contains processing logic. The CLI commands map 1:1 to the manifest's command list: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `mcp`, plus the synthesis-side leaves `synth from-schema`, `synth from-profile`, and `profile create`.

### MCP surface

`internal/mcp/` translates the Pulse facade into Model Context Protocol tools and resources. The library has no dependency on this package; only the `pulse mcp` CLI leaf imports it. Ten tools (one per facade method, plus the unified `pulse_ask` one-shot that collapses inspect→predict→process) and two resource schemes (`pulse://` for `.pulse` files, `pulse-skill://` for embedded skills) are registered at server start.

The canonical tool list is `mcp.RegisteredTools()` in `internal/mcp/tools.go`. Adding or removing a tool requires updating `skills/mcp-integration.md` in the same PR (`TestSkillsCoverAllMCPTools`).

### I/O subsystem

The `io/` package defines two interfaces:

- `io.Reader` — `ReadHeader() ([]string, error)`, `ReadRows(ctx, fn) error`, `Close() error`
- `io.Writer` — `WriteHeader(columns) error`, `WriteRow(values) error`, `Close() error`

Each format sub-package (`csv/`, `tsv/`, `ndjson/`, `parquet/`, `excel/`) implements both interfaces. `io.ResetReader` extends `Reader` with `Reset()` for schema inference followed by import.

Import, export, and convert are orchestrated by job structs (`ImportJob`, `ExportJob`, `ConvertJob`) that accept a reader/writer pair and an optional `afero.Fs`.

### Descriptor and skills

The `descriptor/` package provides three no-execution operations:

- **Manifest** (`BuildManifest()`) — deterministic self-description of all commands, components, field types, and skills.
- **Predict** (`Predict(fileData, req, opts)`) — validates a request against a `.pulse` file's schema without reading record data.
- **Inspect** (`Inspect(fileData, opts)`) — reads a `.pulse` file's header and schema, returning structured field information.

All three return an `Envelope` (see Output Format Contract below).

The `skills/` package embeds 19 skill files via `//go:embed *.md` and an `index.json` manifest. Each skill has YAML frontmatter with `name`, `description`, `type`, and `applies_to` fields. Skills are loaded with `skills.Get(name)` and listed with `skills.List()`.

## Code Conventions

### Naming patterns

- All identifiers, comments, and documentation are Pulse-native. No references to predecessor projects.
- Module path: `github.com/frankbardon/pulse`
- Package imports use the module path. The `io/` sub-packages are imported as `pio "github.com/frankbardon/pulse/io"` when needed to avoid collision with stdlib `io`.
- Component types use SCREAMING_SNAKE: `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`.
- Error codes use DOMAIN_CATEGORY format: `ENCODING_INVALID`, `PROCESSING_CONFIG`, `SERVICE_VALIDATION`, `DATA_FILE`, `CLI_INPUT`, `PULSE_IMPORT_ROW_ERROR`.

### Error handling

Errors use the `errors.Code` system. There are 6 domains with typed codes:

- **ENCODING:** `ENCODING_INVALID`, `ENCODING_IO`, `ENCODING_TYPE_MISMATCH`, `ENCODING_INTERNAL`
- **PROCESSING:** `PROCESSING_CONFIG`, `PROCESSING_STATE`, `PROCESSING_RUNTIME`, `PROCESSING_GROUP`, `PROCESSING_INTERNAL`
- **SERVICE:** `SERVICE_VALIDATION`, `SERVICE_RESOURCE`, `SERVICE_REGISTRY`, `SERVICE_INTERNAL`
- **DATA:** `DATA_FILE`, `DATA_PARSE`, `DATA_CONFIG`, `DATA_CALCULATION`, `DATA_INTERNAL`
- **CLI:** `CLI_INPUT`, `CLI_OUTPUT`, `CLI_COMMAND`, `CLI_INTERNAL`
- **PULSE:** `PULSE_IMPORT_SCHEMA_AMBIGUOUS`, `PULSE_IMPORT_ROW_ERROR`, `PULSE_EXPORT_ROW_ERROR`, `PULSE_IMPORT_CATEGORICAL_OVERFLOW`, `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`, `PULSE_IMPORT_DESCRIPTION_TOO_LONG`, `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`, `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`, `PULSE_WINDOW_INVALID`, `PULSE_FEAT_TARGET_LEAKAGE_RISK`, `PULSE_DECIMAL_OVERFLOW`, `PULSE_DECIMAL_PRECISION_LOSS`, `PULSE_DECIMAL_DIVIDE_BY_ZERO`, `PULSE_GEO_INVALID_POINT`, `PULSE_GEO_INVALID_POLYGON`, `PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS`, `PULSE_GEO_INVALID_RESOLUTION`, `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`, `PULSE_AGG_NOT_MEANINGFUL_FOR_GEO`, `PULSE_SYNTH_DISTRIBUTION_UNKNOWN`, `PULSE_SYNTH_CONSTRAINT_INFEASIBLE`, `PULSE_PROFILE_FIELD_UNSUPPORTED`, `PULSE_TEST_UNKNOWN_TYPE`, `PULSE_TEST_FIELD_NOT_NUMERIC`, `PULSE_TEST_INVALID_ALPHA`, `PULSE_TEST_INSUFFICIENT_N`, `PULSE_TEST_VARIANCE_ZERO`, `PULSE_TEST_SPLIT_GROUPS_LT_2`, `PULSE_TEST_CONTINGENCY_DEGENERATE`, `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`, `PULSE_TEST_FIELD2_NOT_NUMERIC`, `PULSE_TEST_SUCCESS_VALUE_MISSING`, `PULSE_TEST_CORRELATION_UNDEFINED`, `PULSE_TEST_PAIRED_LENGTH_MISMATCH`, `PULSE_TEST_TIES_DOMINATE`, `PULSE_TEST_SUBJECT_MISSING`, `PULSE_TEST_BALANCED_DESIGN_REQUIRED`, `PULSE_TEST_TUKEY_REQUIRES_K_GE_3`, `PULSE_TEST_SHAPIRO_N_BOUND`, `PULSE_TEST_FISHER_R_OR_C_GT_2`, `PULSE_QUERY_UNRESOLVED`, `PULSE_QUERY_AMBIGUOUS`

Every new error code MUST be added to the `allCodes` slice in `errors/codes.go` and the `codeMetadata` map in `errors/fixup_metadata.go` (enforced by `TestCodesHaveFixups`). Per-code prose is fetched on demand via `pulse_errors_lookup` (MCP) or `pulse errors lookup CODE` (CLI); the manifest carries only the alphabetized code-name list (`TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`).

### Accessor and component description style

- Field descriptions stored in `.pulse` files are capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`).
- Low-quality descriptions (empty, under 10 characters, or generic words like "n/a", "tbd", "unknown", "field", "data", "value", "column") trigger `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warnings (errors in `--strict` mode).
- Descriptions should be concise, third-person, present-tense sentences that describe what the field represents.

### Byte-layout invariants for .pulse files

The `.pulse` binary format has a fixed structure:

1. **9-byte header:** 8-byte magic (`PULSE\x00\x00\x00`) + 1-byte format version (currently `0x01`). Defined as `encoding.MagicBytes`, `encoding.FormatVersion`, `encoding.HeaderSize = 9`.
2. **Schema block:** field descriptors immediately after the header. Each field has a name, type byte, byte offset, bit position, and optional description suffix.
3. **Dictionary blocks:** categorical fields (`categorical_u8`, `categorical_u16`, `categorical_u32`) store their dictionaries inline in the header after the schema.
4. **Record data:** fixed-width rows follow the schema block. Record size is determined by the schema's field types.

### All 19 field types

| Type | Byte value | ByteSize | Notes |
|---|---|---|---|
| `u8` | 0 | 1 | Unsigned 8-bit integer |
| `u16` | 1 | 2 | Unsigned 16-bit integer |
| `u32` | 2 | 4 | Unsigned 32-bit integer |
| `u64` | 3 | 8 | Unsigned 64-bit integer |
| `f32` | 4 | 4 | 32-bit float |
| `f64` | 5 | 8 | 64-bit float |
| `nullable_bool` | 6 | 0 | Bit-packed, tri-state (null/true/false) |
| `nullable_u4` | 7 | 0 | Bit-packed, 4-bit nullable unsigned |
| `nullable_u8` | 8 | 1 | Nullable 8-bit unsigned |
| `nullable_u16` | 9 | 2 | Nullable 16-bit unsigned |
| `date` | 10 | 4 | Date as 32-bit value |
| `packed_bool` | 11 | 0 | Bit-packed boolean |
| `categorical_u8` | 12 | 1 | Categorical with up to 256 dictionary entries |
| `categorical_u16` | 13 | 2 | Categorical with up to 65,536 dictionary entries |
| `categorical_u32` | 14 | 4 | Categorical with up to 4,294,967,295 dictionary entries |
| `decimal128` | 15 | 16 | Fixed-point exact decimal; per-field `(precision, scale)` ≤ (38, 38) |
| `nullable_decimal128` | 16 | 16 | `decimal128` plus an `INT128_MIN` null sentinel |
| `point_f64` | 17 | 16 | Packed `(lat, lon)` f64 pair (LE) |
| `h3_cell` | 18 | 8 | Uber H3 cell index as `uint64` |

Bit-packed types (`nullable_bool`, `nullable_u4`, `packed_bool`) return `ByteSize() == 0` because they share bytes with adjacent fields.

Schema reader rejects unknown FieldType bytes at parse time with `ENCODING_INVALID`. Files written by future-version binaries that introduce new types fail loud at schema parse, not silent at row decode.

### Smart defaults

When a request slot names a field but omits the operator `Type`, the engine infers the operator from the named field's schema type. The rule table lives in `descriptor/defaults.go` (`defaultRules`) and is the single source of truth — predict echoes the inferred slots in `PredictResult.DefaultsApplied`, service applies them in place before Process / Compose execution.

| Field type | Default aggregation | Default grouper |
|---|---|---|
| `u8`..`u64`, `f32`, `f64`, `nullable_u4`/`u8`/`u16`, `decimal128`, `nullable_decimal128` | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| `categorical_u8`/`u16`/`u32` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (none — must be explicit) | `GROUP_DATE` (component `"day"`) |
| `nullable_bool`, `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `point_f64`, `h3_cell` | `AGG_GEO_CENTROID` | `GROUP_H3_CELL` (resolution 7) |

Behavioural rules:

1. Defaults apply only when `Field` is set and `Type` is empty. Explicit `Type` is never overridden.
2. Defaults never cross categories — a missing aggregator does not insert a grouper, and vice versa.
3. Tier-1 (`req.Tests`) and tier-2 (`req.PostTests`) statistical tests are not defaulted; hypothesis tests are too intent-bearing.
4. Filter expressions, feature pipelines, attributes, and windows are out of scope for defaulting.
5. Parameter defaults (`Interval`, `Params.resolution`, `Params.component`) fill in only when the caller leaves them unset.

Disabling defaults: pass `pulse.Options{DisableDefaults: true}` (library) or `--no-defaults` (CLI `process` / `compose`). Predict always computes `DefaultsApplied` regardless — the flag governs only what the runtime mutates.

## Output Format Contract

### --json envelope

All `--json` CLI output and all descriptor operations use the `descriptor.Envelope` struct:

```json
{
  "format_version": "1.0",
  "data": { ... },
  "errors": [],
  "warnings": []
}
```

- `format_version` is always `"1.0"` (the current value). Changes to this value MUST update this section of CLAUDE.md (enforced by `TestClaudeMdMentionsFormatVersion`).
- `data` contains the operation-specific result (e.g., `PredictResult`, `InspectResult`, `Manifest`).
- `errors` is an array of `{"code": "...", "message": "...", "details": {...}}` entries. Empty array (not null) when no errors.
- `warnings` is an array with the same shape. Empty array (not null) when no warnings.

### Additive-only format_version policy

`format_version` is bumped only when a backward-incompatible change is made to the envelope shape. New fields added to `data` do not require a version bump. Removing or renaming existing fields does.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** All JSON output MUST go through `encoding/json` marshaling. The `descriptor/` package is grep-gated: `TestDescriptorNoFmtSprintf` scans `envelope.go`, `manifest.go`, `predict.go`, and `inspect.go` for `fmt.Sprintf` and fails if found.
- **No hand-built XML/CDATA.** If any XML output is ever added, it must use `encoding/xml`, not string concatenation.
- Envelope construction uses `descriptor.NewEnvelope(data)` which sets `format_version`, empty `errors`, and empty `warnings` automatically.

### Manifest payload

`descriptor.BuildManifest()` returns the canonical Pulse self-description and is reachable via `pulse manifest --json` and the `pulse_manifest` MCP tool. The payload is designed as a single LLM-bootstrap blob: one fetch per session, cached client-side.

Top-level fields on `Manifest`:

- `format_version` — always `"1.0"`.
- `commands []Command` — CLI leaf catalog.
- `components Components` — six `[]Operator` slices (aggregators, attributes, filterers, groupers, windows, features). Each `Operator` carries `name`, `category`, `description`, `params`, `accepts_types`, `emits_type` / `emits_type_note`, `streamable`, `streamable_hint`.
- `tests []TestMeta` — tier-1 statistical tests. Each entry has `tier:1`, `family==name`, `params`, `requires`, `streamable` mirroring `types.TestType.Streamable()`.
- `post_tests []TestMeta` — tier-2 post-tests (natively-tier-2 entries `TEST_TREND` / `TEST_TUKEY_HSD` plus registered variants like `pearson_post`, `welch_one_way_post`). Each entry has `tier:2`, non-empty `variant`, `family` referencing the underlying `TestType`. `streamable` is always false. Tier-1 and tier-2 are peer slices, not nested.
- `synth_distributions []DistributionMeta` — one entry per `synth.AllDistributions()` value with `applies_to` and `params`.
- `error_codes_count int` + `error_domains []string` + `error_codes []string` — slim error coverage. The bootstrap manifest carries only the alphabetized code-name list and the six domain prefixes (`CLI`, `DATA`, `ENCODING`, `PROCESSING`, `PULSE`, `SERVICE`). Per-code Message + Fixup detail is depth-on-demand via the `pulse_errors_lookup` MCP tool or `pulse errors lookup CODE` CLI leaf — errors are reactive lookup, not authoring reference.
- `mcp_tools []MCPTool` — one entry per `internal/mcp/mcptools.Names()` value with `description`.
- `cohort_types []CohortFieldType` — one entry per field type with `compatible_aggregators`, `compatible_attributes`, `compatible_filterers`, `compatible_groupers`, `compatible_windows`, `compatible_features` cross-references derived deterministically from per-operator `accepts_types`.
- `skills []SkillMeta` — embedded skill index from `skills.List()`.

Operator-category slices are sorted by `Name`. Compatible-list cross-refs are sorted by operator name. The payload is golden-checked (`descriptor/testdata/manifest.json`).

Capability declarations for components, tests, and distributions live in `descriptor/capabilities_*.go`. Error coverage in the manifest is name-only (`descriptor/capabilities_errors.go` mirrors `errors.SortedCodeNames()`); per-code Message + Fixup detail lives in `errors/fixup_metadata.go` and is reachable via the `pulse_errors_lookup` MCP tool or `pulse errors lookup CODE` CLI leaf. MCP tool name + description metadata lives in `internal/mcp/mcptools/meta.go` so the descriptor package can mirror it without an import cycle. Tests `TestManifestOperatorsComplete`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete`, `TestManifestDistributionsComplete`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`, `TestManifestMCPToolsComplete`, `TestManifestStreamableMatchesTypes`, `TestCohortTypeCrossRefsDeterministic`, and `TestManifest_SkillsNotEmpty` enforce completeness and consistency.

## Predict / Inspect / Manifest Contracts

### Predict: no-execute structural ban

`descriptor/predict.go` validates a `types.Request` against a `.pulse` file's schema without ever executing the request. It reads only the header and schema (no record data). **Structural ban:** `predict.go` MUST NOT import `service/` or `processing/`. This is enforced by `TestPredictNoExecutionImports`, which grep-scans the source file for banned import paths:

- `"github.com/frankbardon/pulse/service"` — banned
- `"github.com/frankbardon/pulse/processing"` — banned

If predict ever needs to know about component capabilities, it must use `types/` constants (e.g., `types.AllAggregationTypes()`), not the processing registries.

### Inspect: header-only invariant

`descriptor/inspect.go` reads the `.pulse` file header and schema, returning field names, types, byte offsets, bit positions, descriptions, and categorical dictionaries. It MUST NOT read record data. The `Inspect` function accepts an `io.ReadSeeker` and calls only `encoding.ReadHeader` and `encoding.ReadSchema`.

Dictionary output is truncated to `DefaultDictionaryLimit` (100) entries unless `InspectOptions.FullDict` is true or a custom `DictionaryLimit` is set.

Fields without stored descriptions get a synthesized fallback (`"Categorical field: <name>"` or `"Numeric field: <name>"`), with `description_source` set to `"synthesized"` vs `"schema"`.

### Manifest: determinism

`descriptor.BuildManifest()` returns a deterministic `Manifest` struct. Every operator, test, distribution, error code, MCP tool, cohort type, and skill list is sorted lexically. The payload includes: commands, six `[]Operator` component slices (aggregators, attributes, filterers, groupers, windows, features), tier-1 tests, tier-2 post-tests (peer to tier-1), synth distributions, error codes, MCP tools, cohort field types with operator cross-references, and skill metadata. `format_version` is `"1.0"`. See "Manifest payload" under Output Format Contract above for the field-by-field shape.

### Predict: streamability flag

`PredictResult.Streamable bool` reports whether the request can execute via the streaming Process path (no buffered intermediate row set). `PredictResult.StreamableReasons []string` lists every gate that forced buffering — empty when `Streamable=true`.

Source of truth is per-type `Streamable() bool` methods on `types.AggregationType`, `types.AttributeType`, `types.FiltererType`, `types.GroupType`, `types.WindowType`, `types.FeatureType` (in `types/streamability.go`). Predict reads these methods plus schema-aware gates (decimal fields, geo aggregations) to compute the flag without importing `processing/`.

`processing.CanStreamRequest(req, schema) bool` is the exported runtime parity hook that descriptor tests use to confirm predict's flag matches the actual streaming gate. Drift between the two surfaces breaks `TestPredict_Streamable_MatchesRuntime` and `TestRegistryStreamabilityMatchesTypes`.

### Parallel compose

`pulse.ComposeParallel(ctx, req, opts)` fans out a `ComposedRequest` over a bounded worker pool. Order-preserving (slot-by-index) regardless of completion order. Workers share the engine's read-only registries; each `Process` call constructs fresh stateful operators per request.

`ComposeOptions`:
- `MaxWorkers` — defaults to `runtime.GOMAXPROCS(0)`; negatives clamp to 1.
- `PerRequestTimeout` — optional per-request deadline derived via `context.WithTimeout`.
- `FailFast` — defaults to `true` (cancel siblings on first error). Set `false` to aggregate every error into a single `SERVICE_INTERNAL` with `failed_indices`.

CLI: `pulse api compose --parallel N [--no-fail-fast]`.

### Streaming iterator

`pulse.ProcessStream(ctx, req) (RowIter, error)` returns a pull-based iterator over result rows. `RowIter.Next(ctx) (Row, bool, error)` / `Close() error` / `Metadata() *ResponseMetadata`. Today the iterator wraps `Process` and walks the materialized `Data` slice; consumers that adopt the API now will pick up true incremental emission without code changes when groupers/aggregators stream natively.

CLI: `pulse api process --stream` and `pulse api compose --stream` emit NDJSON one row per line.

### What streams today

The streaming Process path covers four orchestrator modes:

- **Single-pass streaming** (`processStreaming`): no-group requests with online aggregators (COUNT, SUM, AVG, STDDEV, VARIANCE, RANGE, FREQUENCY, MODE, SKEWNESS, KURTOSIS, DISTINCT_COUNT) on numeric (non-decimal) fields. Row-local attributes (FORMULA, DATE_PART, via `RowLocalAttribute.Row`) apply inline.
- **Grouped streaming** (`processStreamingGrouped`): groupers implementing `StreamingGrouper.KeyForRow` (CATEGORY, RANGE, ROUNDED, H3_CELL) drive per-key online aggregator buckets. Memory is O(distinct_groups × per-aggregator-state). One row per distinct key on stream exhaustion.
- **Two-pass streaming** (`processStreamingTwoPass`): attributes implementing `TwoPassAttribute` (ZSCORE, TSCORE, NORMALIZED) compute population stats via Welford-Pébaÿ pass 1, then emit per-row values in pass 2 after `iter.Reset()`. Mirrors the streaming feature pattern (`feature.StreamingComputer.PrePass + Finalize + EmitRow`). Memory is O(per-attribute-state); cost is 2× iter scan (typically OS-page-cached).
- **Streaming features** (every registered FEAT_* implements `feature.StreamingComputer`) compose with single-pass and grouped streaming.

Forced buffered:

- Median, percentile, ZScore *aggregators* (sort or summed deviations).
- `ATTR_PERCENTILE` (sorted view of every value — no streaming algorithm preserves exact rank).
- `GROUP_QUANTILE` / `GROUP_DATE` (finalize-time work over full set).
- Window operators (sorted post-aggregate row set).
- Decimal-typed field aggregations (precision-preserving path).
- Geo aggregations (typed buffered path).
- Two-pass attributes combined with features or groups (orchestration matrix not yet extended; tracked separately).
- Tier-1 statistical tests combined with groupers, features, or two-pass attributes (same orchestration limit; predict reports the gate).
- Tier-2 statistical tests (`req.PostTests`) regardless of TestType — they run after the result set is materialized.

### CI gates

- `TestPredictNoExecutionImports` — grep gate enforcing the no-execute ban on `predict.go`
- `TestDescriptorNoFmtSprintf` — grep gate banning `fmt.Sprintf` in all descriptor source files
- `TestGoldensNotHandEdited` — verifies golden files have valid SHA-256 hashes, preventing hand edits
- `TestClaudeMdMentionsFormatVersion` — asserts CLAUDE.md mentions the current `format_version` value
- `TestClaudeMdMentionsAllEnvVars` — asserts every `PULSE_*` env var in the codebase is listed in CLAUDE.md
- `TestClaudeMdMentionsAllNonSkippableGates` — asserts every non-skippable test name appears in CLAUDE.md
- `TestUpdateDemandTableCovers` — asserts the Update Demand table covers every registered component category and contract type
- `TestPerPackageCoverageFloors` — placeholder documenting target per-package coverage floors; verifies package directories exist
- `TestNoOrbitPrefixes` — verifies no error code string contains predecessor project references
- `TestNoOrbitPrefix` — verifies no type constant string contains predecessor project references
- `TestSkillsCoverAllMCPTools` — asserts every MCP tool registered in `internal/mcp` appears in `skills/mcp-integration.md`
- `TestSkillsCoverAllSynthDistributions` — asserts every distribution kind registered in `synth.AllDistributions()` appears in `skills/synthetic-data.md`
- `TestRegistryStreamabilityMatchesTypes` — for every registered aggregator, asserts `types.AggregationType.Streamable()` matches the runtime `OnlineAggregator` capability of the constructed instance
- `TestPredict_Streamable_MatchesRuntime` — cross-package parity gate: `PredictResult.Streamable` must equal `processing.CanStreamRequest(req, schema)` for every (request, schema) in the matrix
- `TestStreamability_AggregationsKnown`, `TestStreamability_AttributesKnown`, `TestStreamability_FilterersKnown`, `TestStreamability_GroupsKnown`, `TestStreamability_WindowsKnown`, `TestStreamability_FeaturesKnown` — exhaustiveness gates: every type listed in `All*Types()` must have an explicit entry in the per-type streamability table
- `TestCanStreamRequest_RegressionMatrix` — regression matrix on the exported `processing.CanStreamRequest` helper used by predict's parity gate
- `TestStreamability_TestsKnown` — exhaustiveness gate for `TestType.Streamable()` covering every entry in `types.AllTestTypes()`
- `TestManifest_HasMCPTool` — verifies `pulse_manifest` is registered in `internal/mcp.RegisteredTools()`
- `TestManifest_SkillsNotEmpty` — verifies `Manifest.Skills` is populated from `skills.List()` (not the previously-hardcoded empty slice)
- `TestManifestOperatorsComplete` — for every type in `types.All*Types()`, asserts a corresponding `Operator` exists in the manifest with non-empty `AcceptsTypes` and a curated `Description`
- `TestManifestStreamableMatchesTypes` — for every `Operator` in the manifest, asserts its `Streamable` flag mirrors `types.X.Streamable()`
- `TestManifestTestsComplete` — for every `TestType` in `types.AllTestTypes()`, asserts a tier-1 entry in `Manifest.Tests` (or a tier-2 entry for the natively-tier-2 families `TEST_TREND` / `TEST_TUKEY_HSD`)
- `TestManifestPostTestsComplete` — verifies every `Manifest.PostTests` entry has `Tier:2`, non-empty `Variant`, and a `Family` value present in `types.AllTestTypes()`
- `TestManifestDistributionsComplete` — for every entry in `synth.AllDistributions()`, asserts a `DistributionMeta` entry
- `TestManifestErrorCodesComplete` — for every entry in `errors.AllCodes()`, asserts the slim `manifest.error_codes []string` field carries that code name (length parity enforced separately)
- `TestManifest_ErrorCodesSlim` — asserts the manifest's error fields are name-only: alphabetized `error_codes []string` + `error_codes_count int` + `error_domains []string` (6 entries: CLI, DATA, ENCODING, PROCESSING, PULSE, SERVICE)
- `TestCodesHaveFixups` — for every entry in `errors.AllCodes()`, asserts an entry in `errors/fixup_metadata.go` (`codeMetadata`) carrying either at least one `Fixup` template or `FixupNotApplicable: true`
- `TestErrorsLookup_ByCode`, `TestErrorsByDomain_ReturnsAll`, `TestErrorsSearch_DescriptionMatch`, `TestErrorsSearch_FixupMatch`, `TestErrorsLookup_UnknownCodeReturnsFalse` — exercise the `errors.Lookup` / `errors.ByDomain` / `errors.Search` lookup surface that backs `pulse_errors_lookup`
- `TestMCPErrorsLookup_RoundTrip` — handler-level round-trip via the `pulse_errors_lookup` MCP tool (code / domain / query axes plus AND-intersection)
- `TestManifestMCPToolsComplete` — for every entry in `mcptools.Names()`, asserts an `MCPTool` entry
- `TestCohortTypeCrossRefsDeterministic` — verifies each `CohortFieldType`'s `Compatible*` slices are sorted lexically (required for golden stability)
- `TestMCPSchemaBinding_RemovesInvalidFields` — asserts the bound `pulse_process` schema's `request.attributes[].field` enum contains only numeric fields (categorical and geo fields excluded) for the test cohort
- `TestMCPSchemaBinding_AllFieldsInFiltererEnum` — asserts the bound filterer field enum equals the full set of cohort field names
- `TestMCPSchemaBinding_SampleAndFacetFieldEnum` — asserts bound `pulse_facet` constrains its `field` arg via enum; bound `pulse_sample` schema is well-formed
- `TestMCPSchemaBinding_InspectSucceedsRegistersBindings` — drives `handleInspect` against a `SessionWithTools` and asserts the five action tools (`pulse_process`, `pulse_predict`, `pulse_compose`, `pulse_sample`, `pulse_facet`) land in the session's tool map with bound input schemas
- `TestMCPSchemaBinding_BindOnOpenFalse` — asserts `BindOnOpen=false` suppresses session-tool registration on inspect
- `TestExamples_AllParseAsRequest` — every embedded example JSON in `examples/` parses as a `types.Request` (the `_meta` block is unknown-field-ignored)
- `TestExamples_UniqueNames` — every `_meta.name` is unique across the embedded library
- `TestExamples_TagsFromTaxonomy` — every tag on every example belongs to `examples.CanonicalTags`
- `TestExamples_OperatorsMatchBody` — declared `_meta.operators` equals the operator list auto-derived from the request body
- `TestExamples_CategoryMatchesDirectory` — `_meta.category` matches the parent directory of the file
- `TestExamplesSearch_QueryMatchesDescription` — substring search returns expected hits across description/name/operators
- `TestExamplesSearch_TagsAreANDed` — combining multiple tags narrows (never widens) the result set
- `TestExamplesGet_StripsMeta` — `examples.Get` returns the request JSON with the `_meta` block stripped, so callers can pass it straight to `pulse_process` / `pulse_predict`
- `TestManifestExamplesPopulated` — `Manifest.ExamplesCount > 0` and the categories/tags slices are non-empty and alphabetized

## Skill Pack Maintenance

### How to add a new skill

1. Create `skills/<skill-name>.md` with YAML frontmatter (see requirements below).
2. Add an entry to `skills/index.json` with matching `name`, `description`, `type`, and `applies_to` fields.
3. Update `TestSkillsList_ReturnsAll` and `TestSkillsNames` in `skills/skills_test.go` to reflect the new count.
4. Run `go test ./skills/...` to verify all consistency gates pass.

### Frontmatter requirements

Every skill file MUST begin with a YAML frontmatter block:

```yaml
---
name: skill-name
description: What the skill teaches
type: guide
applies_to: process, compose, predict
---
```

Required fields:
- `name` — must match the filename (without `.md`) and the `name` in `index.json`
- `description` — concise summary of the skill's purpose
- `type` — either `guide` or `reference`
- `applies_to` — comma-separated list of CLI leaf commands this skill is relevant to (must be valid leaves from the manifest: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`)

### Consistency CI gates

- `TestSkillsFrontmatter_RequiredFields` — every skill has `name`, `description`, `type`, `applies_to` in its frontmatter
- `TestSkillsManifestConsistent` — every skill in `index.json` has a matching `.md` file, frontmatter name matches, and `applies_to` entries reference valid CLI leaves
- `TestSkillsCoverAllComponents` — every aggregator, attribute, filterer, and grouper in the registries is mentioned in its target skill
- `TestSkillsCoverAllCliLeaves` — every CLI leaf command appears in `skills/getting-started.md`
- `TestSkillsCoverAllFieldTypes` — every field type appears in `skills/cohort-schema-design.md`
- `TestSkillsCoverAllWindowTypes` — every `WIN_*` operator in `types.AllWindowTypes` appears in `skills/window-operations.md`

### Per-component update rules

| Component category | Target skill file |
|---|---|
| Aggregator (`AGG_*`) | `skills/aggregation-guide.md` |
| Attribute (`ATTR_*`) | `skills/attribute-composition.md` |
| Filterer (`FILTER_*`) | At least one skill must mention it; typically `skills/aggregation-guide.md` or `skills/getting-started.md` |
| Grouper (`GROUP_*`) | `skills/grouper-design.md` |
| Window operator (`WIN_*`) | `skills/window-operations.md` |
| Feature operator (`FEAT_*`) | `skills/feature-engineering.md` |
| Synth distribution | `skills/synthetic-data.md` |
| Statistical test (`TEST_*`) | `skills/statistical-testing.md` |
| Error code | `errors/fixup_metadata.go` (per-code Message + Fixups) — surfaced via `pulse_errors_lookup` MCP tool, not the skill |
| CLI leaf command | `skills/getting-started.md` |
| Field type | `skills/cohort-schema-design.md` |

### Current registered components

**18 aggregators:** `AGG_AVERAGE`, `AGG_COUNT`, `AGG_DISTINCT_COUNT`, `AGG_FREQUENCY`, `AGG_GEO_BBOX`, `AGG_GEO_CENTROID`, `AGG_KURTOSIS`, `AGG_MAX`, `AGG_MEDIAN`, `AGG_MIN`, `AGG_MODE`, `AGG_PERCENTILE`, `AGG_RANGE`, `AGG_SKEWNESS`, `AGG_STDDEV`, `AGG_SUM`, `AGG_VARIANCE`, `AGG_ZSCORE`

**6 attributes:** `ATTR_DATE_PART`, `ATTR_FORMULA`, `ATTR_NORMALIZED`, `ATTR_PERCENTILE`, `ATTR_TSCORE`, `ATTR_ZSCORE`

**6 filterers:** `FILTER_EXCLUDE`, `FILTER_EXPRESSION`, `FILTER_GEO_WITHIN`, `FILTER_GEO_WITHIN_RADIUS_M`, `FILTER_INCLUDE`, `FILTER_RANGE`

**6 groupers:** `GROUP_CATEGORY`, `GROUP_DATE`, `GROUP_H3_CELL`, `GROUP_QUANTILE`, `GROUP_RANGE`, `GROUP_ROUNDED`

**10 window operators:** `WIN_DENSE_RANK`, `WIN_EWMA`, `WIN_LAG`, `WIN_LEAD`, `WIN_MOVING_AVG`, `WIN_PCT_CHANGE`, `WIN_RANK`, `WIN_ROW_NUMBER`, `WIN_RUNNING_AVG`, `WIN_RUNNING_SUM`

**8 feature operators:** `FEAT_BUCKETIZE`, `FEAT_DATE_FEATURES`, `FEAT_FREQUENCY_ENCODE`, `FEAT_LOG`, `FEAT_ONE_HOT`, `FEAT_SQRT`, `FEAT_TARGET_ENCODE`, `FEAT_TRAIN_TEST_SPLIT`

**12 synth distributions:** `bernoulli`, `constant`, `exponential`, `lognormal`, `monotonic_from`, `normal`, `pareto`, `poisson`, `regex`, `uniform`, `uniform_date`, `weighted_categorical`

**20 statistical tests:** `TEST_ANOVA_F`, `TEST_ANOVA_RM`, `TEST_ANOVA_WELCH`, `TEST_BROWN_FORSYTHE`, `TEST_CHISQ`, `TEST_FISHER_EXACT`, `TEST_KENDALL_TAU`, `TEST_KRUSKAL_WALLIS`, `TEST_KS`, `TEST_MANN_WHITNEY_U`, `TEST_PAIRED_T`, `TEST_PEARSON_R`, `TEST_PROP_Z`, `TEST_SHAPIRO_WILK`, `TEST_SPEARMAN_R`, `TEST_T`, `TEST_TREND`, `TEST_TUKEY_HSD`, `TEST_WELCH`, `TEST_WILCOXON_SR`. Tier-1 row tests: `TEST_T`, `TEST_WELCH`, `TEST_CHISQ`, `TEST_ANOVA_F`, `TEST_KS`, `TEST_PAIRED_T`, `TEST_PROP_Z`, `TEST_PEARSON_R`, `TEST_MANN_WHITNEY_U`, `TEST_WILCOXON_SR`, `TEST_KRUSKAL_WALLIS`, `TEST_SPEARMAN_R`, `TEST_KENDALL_TAU`, `TEST_ANOVA_WELCH`, `TEST_ANOVA_RM`, `TEST_BROWN_FORSYTHE`, `TEST_FISHER_EXACT`, `TEST_SHAPIRO_WILK` (registered in `processing/test.go`). Tier-2 post tests: `TEST_ANOVA_F` (from summary stats), `TEST_TREND` (Mann-Kendall), `TEST_TUKEY_HSD` (studentized-range p-values via numerical integration), `TEST_PEARSON_R` (Welford cross-product; variant `pearson_post`), `TEST_PAIRED_T` (variant `paired_two_sided_post`), `TEST_SPEARMAN_R` (variant `rank_pearson_post`), `TEST_KENDALL_TAU` (variant `tau_b_post`), `TEST_WILCOXON_SR` (variant `asymptotic_post`), `TEST_ANOVA_WELCH` (variant `welch_one_way_post`; consumes per-group summary stats), `TEST_BROWN_FORSYTHE` (variant `median_post`), `TEST_SHAPIRO_WILK` (variant `shapiro_francia_post`), `TEST_KS` (variant `two_sample_post`).

## Build / Dev / Test Workflow

### Make targets

| Command | What it does |
|---|---|
| `make build` | Builds the CLI binary to `bin/pulse` (default goal) |
| `make test` | Runs `go test ./...` |
| `make fmt` | Runs `go fmt ./...` |
| `make vet` | Runs `go vet ./...` |
| `make lint` | Runs `go vet` then `staticcheck ./...` |
| `make cover` | Runs tests with coverage, outputs `coverage.out` and prints function coverage |
| `make clean` | Removes `bin/` and `coverage.out` |
| `make docs` | Builds the mdBook site to `docs/book/` |
| `make docs-serve` | Serves the mdBook site locally with auto-reload (opens browser) |
| `make docs-clean` | Removes `docs/book/` |

A `.env` file at the repo root is auto-loaded and exported by the Makefile, so `PULSE_DATA_DIR` (and any other `PULSE_*` env vars) can live there for local development.

### Environment variables

- `PULSE_DATA_DIR` — the base directory for `.pulse` cohort files. Used by `fs.Default()` when no explicit `DataDir` or custom `afero.Fs` is provided. This is the only required environment variable for runtime operation. When embedding the library, you can bypass it entirely by passing `pulse.Options{DataDir: "/path"}` or `pulse.Options{FS: myFs}`.

### Running subsets of CI gates locally

```bash
# All tests
go test ./...

# Just the descriptor contract gates
go test ./descriptor/ -run 'TestPredictNoExecution|TestDescriptorNoFmtSprintf|TestGoldensNotHandEdited'

# Just the skill coverage gates
go test ./skills/ -run 'TestSkillsCoverAll|TestSkillsManifestConsistent|TestSkillsFrontmatter'

# Just the hygiene gate (no predecessor references)
go test . -run TestNoOrbitReferences

# Just the CLAUDE.md enforcement gates
go test . -run 'TestClaudeMd|TestUpdateDemandTable'
```

### Golden file regeneration

Golden files live in `descriptor/testdata/`. They are hash-protected: each golden file ends with a `// golden-hash: <sha256>` line. To regenerate after a legitimate change:

```bash
go test ./descriptor/ -run 'Test.*Golden' -update
```

This overwrites the golden files and recomputes the SHA-256 hashes. **Never hand-edit golden files** — `TestGoldensNotHandEdited` verifies the hash and will fail if the file was modified outside the test framework.

### Testing with in-memory filesystem

For tests that need filesystem access, use `fs.NewMemMap()` which returns a `Config` backed by `afero.NewMemMapFs()`. This avoids touching disk and makes tests hermetic.

## Common Claude Code Workflows

### Adding a new aggregator

1. Define the aggregation type constant in `types/types.go` (e.g., `AGG_MEDIAN`). Add it to `AllAggregationTypes()`.
2. Implement the aggregator in `processing/` — write the factory function and register it in `aggregatorRegistry` in `processing/registry.go`.
3. Write tests in `processing/aggregator_test.go`.
4. Add a section for the new aggregator in `skills/aggregation-guide.md`.
5. Run `go test ./skills/ -run TestSkillsCoverAllComponents` to verify the skill mentions the new aggregator.
6. Update this CLAUDE.md: add the new aggregator to the "Current registered components" list in the Skill Pack Maintenance section.
7. If the aggregator interacts with categorical fields in a special way, update `descriptor/predict.go`'s `numericAggregations` map.

### Adding a new I/O format

1. Create `io/<format>/` with a reader and writer implementing `io.Reader` and `io.Writer`.
2. Write tests in `io/<format>/<format>_test.go`.
3. Wire the format into `ImportJob` and `ExportJob` if needed.
4. Add or update a skill file (e.g., `skills/export-format-selection.md`) to document when to use the format.
5. If the format adds a CLI flag, update `skills/getting-started.md` and run `TestSkillsCoverAllCliLeaves`.

### Adding a new feature operator

1. Define the type constant in `types/types.go` (e.g., `FEAT_TARGET_ENCODE`). Add it to `AllFeatureTypes()`.
2. Implement the operator in `processing/feature/<name>.go`. Register via `init()` calling `register(types.FEAT_X, newX)`.
3. Add a section to `skills/feature-engineering.md` describing the operator, its params, and its output column naming.
4. Update `descriptor/predict_feature.go` to validate the new operator's params and emit the right output labels in `featureOutputLabels`.
5. Run `go test ./skills/ -run TestSkillsCoverAllComponents` and `go test ./descriptor/ -run TestPredict_Feature` to confirm coverage.
6. Update this CLAUDE.md: add the new operator to "Current registered components" -> "feature operators".

### Wiring Pulse into an MCP client

1. Build the binary: `make build`. The resulting `bin/pulse` must be on the client's `PATH` (or referenced absolutely).
2. Configure the client (Claude Desktop's `claude_desktop_config.json` or Claude Code's `~/.claude.json`) with an `mcpServers.pulse` entry running `pulse mcp` and exporting `PULSE_DATA_DIR`. The `--bind-on-open` flag (default true) controls whether successful `pulse_inspect` calls trigger registration of session-scoped tool variants whose JSON Schemas constrain field-name parameters to the cohort's actual fields. Pass `--bind-on-open=false` to disable for clients that bind themselves.
3. Restart the client. Pulse tools (`pulse_inspect`, `pulse_predict`, `pulse_process`, etc.) and resources (`pulse://*.pulse`, `pulse-skill://*`) appear in the tool/resource list.
4. See `skills/mcp-integration.md` for the full configuration recipe, including the "Schema-bound enums" section that describes the inspect trigger, the multi-file limitation (latest inspect wins), and the transport-support caveat (stdio sessions in mcp-go v0.52.0 do not implement `SessionWithTools`, so binding is a no-op there; SSE / Streamable HTTP transports honor it).

### Debugging a predict mismatch

1. Run `pulse predict --json < request.json` against your `.pulse` file.
2. Check the envelope's `errors` and `warnings` arrays.
3. Common issues: field name typo (error: `SERVICE_VALIDATION`), numeric aggregation on categorical field (warning: `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`), low-quality description (warning: `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`).
4. Use `pulse inspect --json <file.pulse>` to see the actual schema fields and types.
5. Predict reads only the header and schema. If it reports valid but execution fails, the bug is in the processing layer, not predict.

### Regenerating goldens

```bash
go test ./descriptor/ -run 'Test.*Golden' -update
```

Then verify: `go test ./descriptor/ -run TestGoldensNotHandEdited`

### Porting workflow checklist

When porting functionality into Pulse:

1. Identify the source behavior and the destination Pulse package.
2. Write Pulse-native tests first, in the destination package, covering the target behavior (with all identifiers renamed per the Zero-Predecessor-Reference Rule).
3. Run `go test ./...` and confirm the new tests fail with informative messages. If they pass, the test is wrong — fix the test, do not move on.
4. Port the implementation file, refactor for Pulse-native idioms.
5. Run the test suite again. Iterate until green.
6. Add or update the corresponding skill file(s) per the Update Demand. Run the skill-coverage gates locally.
7. Update CLAUDE.md if the change touches a contract, env var, format version, or registered surface.
8. Run the predecessor-reference grep gate and confirm zero matches before opening the PR.

## What NOT to Do

**Do not import `service/` or `processing/` from `descriptor/` commands.** Predict, inspect, and manifest are no-execute operations. They read headers and schemas only. Importing the service or processing package from descriptor breaks the structural ban and will fail `TestPredictNoExecutionImports`.

**Do not hand-edit golden files.** Golden files in `descriptor/testdata/` are hash-protected. Edit the code that produces the output, then regenerate with `go test ./descriptor/ -run 'Test.*Golden' -update`. Hand edits will fail `TestGoldensNotHandEdited`.

**Do not add implementation without tests in the same PR.** Tests come first (TDD). A PR that adds a new aggregator, field type, or I/O format without corresponding tests will not be merged. A PR that adds tests that pass without implementation is suspicious — the test is probably wrong.

**Do not use `fmt.Sprintf` for JSON or XML construction.** All structured output goes through `encoding/json` (or `encoding/xml`). The `descriptor/` package is grep-gated by `TestDescriptorNoFmtSprintf`. Use `descriptor.NewEnvelope(data)` for envelope construction and `json.Marshal` for serialization.

**Do not defer skill or CLAUDE.md updates to follow-up PRs.** The follow-up PR will not happen. The next Claude Code session will read stale guidance and produce wrong code. The Update Demand table above lists every trigger and its enforcing CI gate. If the gate exists, it will catch you. If it does not (reviewer enforcement rows), discipline is required.

**Do not add a component without updating the registry.** Every aggregator must be in `aggregatorRegistry`, every attribute in `attributeRegistry`, every filterer in `filtererRegistry`, every grouper in `grouperRegistry` (all in `processing/registry.go`). The corresponding type constant must be in `types/types.go` and its `All*Types()` function.

**Do not bypass the `afero.Fs` abstraction for file access.** All file I/O in the library goes through the `afero.Fs` interface injected via `fs.Config`. Direct `os.Open` or `os.ReadFile` calls defeat testing with `fs.NewMemMap()` and break the extension hook for custom storage backends.

**Do not put business logic in `cmd/pulse/`.** The CLI binary is a thin adapter. It parses flags, constructs library objects, calls library methods, and formats output. Processing logic, validation logic, and schema logic belong in their respective packages.
