# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as a Go library (`github.com/frankbardon/pulse`) and a CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` is the public API — `New`, `Open`, `Process`, `Compose`, `ComposeParallel`, `ProcessStream`, `ProcessChain`, `Import`, `Export`, `Convert`, `Inspect`, `InspectEnvelope`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`, `CountRecords`, `Lookup`, `BuildIndex`, `VerifyIndex`, `ListIndexes`, `DropIndex`, `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`. **The CLI never contains business logic.**
- **Self-describing.** Every `.pulse` file carries its schema in the header. `descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `skills/` embeds an atomic-per-surface pack (`op-*` / `tool-*` / `type-*`) plus ~20 topical design skills via `//go:embed *.md`; the filesystem walk + frontmatter parse is the source of truth.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators, expr functions and named tables. Predict, manifest, MCP and runtime treat them identically to built-ins.
- **Harness-agnostic.** Pulse is standalone; a downstream harness discovers it via `pulse manifest --json` + the embedded skills. No reverse dependency, and no Pulse-side design decision may depend on a consumer's internals.

**Where the detail lives.** Contributor recipes are the mdBook Internals chapter under `docs/src/internals/` — one `adding-*.md` per extension point plus `regenerating-goldens.md`, `debugging-predict.md`, `wiring-mcp-client.md`, `extension-points.md`. Long-form contract prose lives under `.claude/reference/` — **see "Reference Docs" at the bottom for the index and which file each kind of work requires loading.**

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

One row per category: trigger → companions → gates. **The exhaustive per-slot table is `.claude/reference/update-demand.md` — load it before touching any contract below.** ATOMIC = `TestOperatorHasAtomicSkill` + `TestAtomicSkillHasRequiredSections` + `TestSkillTokenBudget`. A **bold filename** in column 2 is a `.claude/reference/` file that must be LOADED BEFORE the change, not merely updated after it.

| If you change... (category) | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator, attribute, filterer, grouper, feature operator, window operator, statistical test (`TEST_*`), regression (`REG_*`), synth distribution or overlay kind (`OVERLAY_*`) | `skills/op-<category>-<kebab>.md` + the matching `descriptor/capabilities_*.go` + an `examples/` `_meta.operators` tag | ATOMIC, the category's `TestSkillsCoverAll*` + `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag` |
| A registered MCP tool (add/remove) | `skills/tool-<kebab>.md` (strip `pulse_`) + `mcp/toolmeta/meta.go` | ATOMIC, `TestSkillsCoverAllMCPTools` |
| A registered field type, a `.pulse` format or shard-archive change, a sidecar file, an SPSS surface, or projected decode | **`byte-layout.md`** + `skills/type-<kebab>.md` + `skills/cohort-schema-design.md` + CLAUDE.md "Byte-layout invariants" | ATOMIC, `TestSkillsCoverAllFieldTypes`, `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + ≥1 Fixup | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A `--json` envelope, `format_version` (currently `"1.1"`), or any payload-reachable `types` slot | CLAUDE.md "Output Format Contract" + `docs/src/contract/payload-schema.md` + regenerate `descriptor/testdata/payload-schema.json` | `TestPayloadSchemaGolden`, `TestClaudeMdMentionsFormatVersion` |
| `ComposedResponse` shape, the Compose facade return type, the `compose --json` `data` wrapping, or `OverlayLayer.Warnings` | CLAUDE.md "Output Format Contract" + `skills/overlay-system.md` (Per-layer warnings section) + the reference row's sites | `TestComposedResponse_OverlayFreeByteIdentical` |
| A registered I/O format (`io/<fmt>/` adapter) | the eleven registry / CLI / capability sites in the reference row + `skills/tool-import.md` + `docs/src/internals/adding-io-format.md` | `TestFromExt_Matrix`, `TestManifestImportCapability` |
| A CLI leaf (add/remove) | the `docs/src/cli/flags.md` command index + `skills/session-bootstrap.md` for an agent-relevant flag | `TestSkillsCoverAllCliLeaves` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| An environment variable | CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| `Response.Components` shape, a per-operator `ComponentSchema`, an Extension registration's `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either arm | **`response-components.md`** + CLAUDE.md "Output Format Contract" + `skills/response-components.md` + the operator's atomic skill + `descriptor/capabilities_*.go` + `docs/src/internals/extension-points.md` + `skills/crosstab-guide.md`. **A record reaches an auxiliary margin only if it reached a CELL — state that rule wherever the figures appear** | `TestClaudeMdMentionsComponentsContract`, `TestExtensions_ComponentSchemaParity`, `TestCrosstab_BufferedAuxMarginMatchesFused` |
| The request-template document model, variable/target sets, or `$var` / `{{}}` / `$when` | **`request-templating.md`** + `skills/request-templating.md` + `docs/src/library/request-templating.md` + CLAUDE.md "Request templating" | `TestTemplatePackage_ImportBoundary` |
| Any `synth/` surface — capture, `SpecFromProfile`, `Spec`, generation, structural rules, `--suggest-rules`, fidelity report | **`synthetic-data.md`** + `skills/synthetic-data.md` + `skills/synth-models.md` + `skills/synth-structural-rules.md` | `TestSkillsCoverAllSynthDistributions` |
| A skill file's stem, frontmatter, required sections or budget | **`skill-pack.md`** + the file itself | ATOMIC |
| Any Request slot, Response slot, capability block, or Execution-mode wiring | the per-slot row in `update-demand.md` | per-slot suites cited there |

Between them the rows carry every word `TestUpdateDemandTableCovers` checks; keep it that way when editing one.

Defer the doc/skill update to "a follow-up PR" and the follow-up will not happen. Update in the same PR or do not merge.

## Architecture

```
cmd/pulse/     the only binary; buildApp() defines the CLI leaf tree
pulse.go       public facade          internal/cli/  flags, formatting, envelopes
service/       orchestration          processing/    operators, crosstab, joins
encoding/      .pulse codec           io/  imports/  tabular adapters + manager
types/         request/response       errors/        typed CodedError system
descriptor/    manifest/predict/inspect/schema — NO-EXECUTE, no service|processing imports
skills/        embedded skill pack    examples/      embedded runnable requests
synth/         generator              template/      request templating
mcp/           SDK-free core (+ gosdk/ adapter, toolmeta/ leaf metadata)
fs/            afero abstraction      docs/          mdBook source
```

`processing/window/` holds the `WIN_*` operators and `processing/feature/` the `FEAT_*` pre-filter engineers. `io/` adapters: `csv|tsv|ndjson|jsonarray|jsonshared|arrow|parquet|excel|spss`. `pulse.go` re-exports `types.Request` / `Response` / `ComposedRequest` as `pulse.*`, plus `synth.Spec`/`Result`/`Options`/`Profile`/`ProfileOptions`.

CLI commands map 1:1 to manifest commands: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `schema`, `mcp`, plus `synth from-schema`, `synth from-profile`, `profile create`, `shard {create,add,remove,list,compact,verify,extract}`, `index {build,list,verify,drop}`, `api {process,compose,facet,process-chain,lookup}`. `pulse schema` prints the payload JSON Schema RAW — not envelope-wrapped.

**MCP layer split.** `mcp/` is the SDK-free core (typed In/Out structs, reflected JSON schemas, typed handlers over `*pulse.Pulse`, strict-decode, bind classification — gated by `TestMCPCore_NoSDKImport`). `mcp/gosdk/` is the ONLY package importing the go-sdk; its `Register(server, p, cfg)` mounts the core catalog onto a caller-supplied server, and `pulse mcp` builds a bare server and calls it. `mcp/toolmeta/` holds the leaf metadata both `descriptor` and the core import. One tool per facade method plus skills/examples/errors/import/label tools — **the manifest is the source-of-truth count, never hardcode it** — and two resource schemes (`pulse://`, `pulse-skill://`); `pulse://schema` serves the payload JSON Schema as a RESOURCE, not a tool. Cohort resources are ENUMERATED by a startup walk of the data root, suppressible with `gosdk.Config.DisableCohortScan` / `mcpserve.Options.DisableCohortScan` / `pulse mcp --no-cohort-scan` — the `pulse://` template stays mounted, so a disabled scan costs enumeration only, never readability. Payload tools take the structured request at top level, outputs are typed-wrapped, coded errors surface as `{code, message, details}`.

Docs at <https://frankbardon.github.io/pulse/>. Skills under `skills/` are the LLM surface.

## Code Conventions

### Naming

- Pulse-native identifiers. No predecessor references (`TestNoOrbitPrefix`, `TestNoOrbitPrefixes`).
- Module path: `github.com/frankbardon/pulse`. `io/` sub-packages imported as `pio "..."`.
- Component types: SCREAMING_SNAKE — `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`, `WIN_LAG`, `FEAT_LOG`, `TEST_T`.
- Error codes: DOMAIN_CATEGORY — `ENCODING_INVALID`, `PROCESSING_CONFIG`, `SERVICE_VALIDATION`, `DATA_FILE`, `CLI_INPUT`, `PULSE_IMPORT_ROW_ERROR`.

### Error handling

Six domains: `CLI`, `DATA`, `ENCODING`, `PROCESSING`, `PULSE`, `SERVICE`. Canonical list in `errors/codes.go` (`allCodes`). Every code needs `codeMetadata` entry (`errors/fixup_metadata.go`) with `Message` + ≥1 `Fixup` template OR `FixupNotApplicable: true`. Per-code prose surfaced via `pulse_errors_lookup` (MCP) / `pulse errors lookup CODE` (CLI). Manifest carries name-only list.

Field descriptions in `.pulse` capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`). Low-quality descriptions emit `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warnings (errors under `--strict`).

### Byte-layout invariants

`.pulse` binary format:

1. **9-byte header:** 8-byte magic `PULSE\x00\x00\x00` + 1-byte format version `0x01` (`encoding.MagicBytes`, `FormatVersion`, `HeaderSize = 9`).
2. **Schema block:** per-field descriptor — name, type byte, **nullable flag byte** (immediately after the type byte; `1` = participates in the null bitmap), byte offset, bit position, optional description.
3. **Dictionary blocks:** inline after the schema, for every dictionary-bearing field.
4. **Record data:** fixed-width rows, stride derived from the schema.
5. **Per-record null bitmap (optional).** Present iff the schema has any nullable field (`Schema.HasBitmap()`); every record then carries a trailing `ceil(field_count / 8)` bytes. Field index `i` → byte `i/8`, bit `i%8` (LSB-first); `1` = null. Helpers: `encoding.ReadBitmap` / `WriteBitmap` / `BitmapIsNull` / `BitmapSetNull`, `Schema.BitmapByteSize()`.

18 field types (full table in `skills/cohort-schema-design.md`, per-type detail in `skills/type-<kebab>.md`): `u4`, `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `set_u8`/`u16`/`u32`/`u64`. Bit-packed types (`u4`, `packed_bool`) return `ByteSize() == 0` and share bytes with neighbours (`FieldType.IsBitPacked()`). `categorical_*` and `set_*` carry an inline dictionary (`FieldType.HasDictionary()`); a `set_*` value is a fixed-width bitmask where bit `i` ↔ dictionary entry `i`, and an empty mask is a valid "no selection" — distinct from null. **`date` is epoch DAYS (`uint32`); `datetime` (type byte `17`) is epoch SECONDS (`uint64`) — never interchangeable, and swapping them rescales every value by 86,400.** Nullability is orthogonal to type; an unknown type byte is `ENCODING_INVALID`; `decimal128` and `set_*` nulls ride the bitmap only, with no in-band sentinel.

**Shard archive variant.** A `.pulse` path resolves to either the single-file layout above or a **shard archive** — uncompressed Zip64 (Method 0, store-only) whose first four bytes are zip magic `PK\x03\x04` instead of `PULSE` magic, dispatched on those bytes at `pulse.Open`. **The single-file byte format is unchanged.** The archive holds a reserved `_schema.pulse` entry (header-only canonical schema + SHRD trailer carrying `aggregate_record_count` and `shard_count`) plus N standalone shard payloads. Per-shard cohesion is structurally strict and description-tolerant; categorical dictionaries union-merge, divergent shards are byte-rewritten with remapped indices (`PULSE_SHARD_DICT_WIDTH_OVERFLOW` on overflow, the stricter `PULSE_SHARD_DICT_DIVERGENCE` for `pulse shard verify`). The anchor `archive.pulse#shard.pulse` opens one shard as a one-shard cohort. Concurrency is caller-owned. Detail: `skills/cohort-schema-design.md` (Sharded cohorts).

**Contract: `.claude/reference/byte-layout.md` — load it before touching any of these. None is a `.pulse` layout, and every one fails SILENTLY when got wrong:** the sidecar point-lookup index (`cohort.pulse.<keyhash>.idx`, v3, THREE-outcome staleness), its discovery manifest (`cohort.pulse.indexes.json`), the SPSS metadata sidecar (`cohort.pulse.spss.json` — the only place the `code ↔ label ↔ dictionary-ID` triple lives), SPSS derived columns (an import can produce a cohort WIDER than the source dictionary — count columns from the returned schema), SPSS export via `pio.CohortWriter`, target-aware export predict via `io.CohortValidator`, and default-on projected buffered decode.

### Smart defaults

When a request slot names a field but omits `Type`, engine infers from schema type. Table in `descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u4/u8/u16/u32/u64, f32/f64, decimal128) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `datetime` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

`Field.Nullable` orthogonal — never changes inferred operator. Defaults apply only when `Field` set and `Type` empty; never override explicit `Type`; never cross categories; never default tests, filter expressions, attributes, windows, features. Disable via `pulse.Options{DisableDefaults: true}` or `--no-defaults`. Predict always computes `DefaultsApplied`.

**Date-family field types.** `GROUP_DATE`, `GROUP_DATE_RANGES` and `FILTER_DATE_RANGES` accept BOTH temporal types — `date` (epoch days, `uint32`) and `datetime` (epoch seconds, `uint64`). Everything downstream of the operator boundary speaks epoch DAYS only, so a `datetime` column is day-truncated exactly once at that boundary by `encoding.DateTimeToDay` — toward the past, never rounding, naive UTC (`1969-12-31T23:59:59Z` is day −1, not day 0). The single adapter is `processing/date_field.go`; **no call site open-codes a `/ 86400`.** `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` reject any other field type with `PROCESSING_CONFIG`; `GROUP_DATE` keeps its historical non-validating posture and reads a non-temporal `Field` as an epoch-day count.

**Labeled date ranges.** `GROUP_DATE_RANGES` (explicit-only — never a smart default) and `FILTER_DATE_RANGES` share one compiled `{label, start, end}` model (`processing.CompileDateRanges`). Range source is exactly one of inline `ranges` XOR a named `table:` (a registered `RangeTable`). Structured ranges cannot ride `Values []string`, so `types.Filterer` carries an additive `omitempty` `Params json.RawMessage` slot — payload-reachable, `format_version` stays `"1.1"`. Operator detail (params, unmatched bucket, `PULSE_RANGE_*` codes): `skills/op-group-date-ranges.md` + `skills/op-filter-date-ranges.md`.

## Output Format Contract

### `--json` envelope

All `--json` CLI output and every descriptor operation use `descriptor.Envelope` — `{format_version, data, request, errors, warnings}`, built with `descriptor.NewEnvelope(data)`.

- `format_version` is always `"1.1"`, bumped from `"1.0"` for the Compose facade lift. **Additive-only: bump only on a backward-incompatible shape change** — new `data` fields do not bump, renames and removals do. Any bump MUST update this section.
- `errors` / `warnings` are `{"code", "message", "details"}` entries, an empty array (never null) when absent. **A FATAL `*errors.CodedError` carries its own code** — the `import` / `convert` / `export` leaves route through `writeCodedErrorEnvelope`, which unwraps with `errors.As` and falls back to the leaf placeholder (`IMPORT_ERROR`, …) only for an UNCODED error. Stringifying a coded error into the placeholder makes `errors[0].code` unusable with `pulse errors lookup`.
- `request` is an opt-in echo of the *normalized* request, omitted unless `Options.EchoRequest` / `--echo-request`; its shape follows the operation (one of the five request roots). Streaming skips the echo. Additive `omitempty` — does NOT bump `format_version`.

**Compose envelope (`pulse api compose --json`).** Since the v1.1 lift, `data` is a `ComposedResponse` OBJECT — not the legacy `[]*Response` array — carrying `responses` (one `Response` per `ComposedRequest.Requests` slot, in input order) and `overlays` (one `OverlayLayer` per `ComposedRequest.Overlays` spec, omitted when there are none). Streaming (`--stream`) bypasses the envelope entirely and emits per-row `{"index", "row"}` NDJSON; Compose overlays surface only at terminal flush in non-streaming mode (`skills/streaming-and-watching.md`).

### Response.Components

Every `Response` carries an optional `Components *ResponseComponents` (additive `omitempty`; `format_version` stays `"1.1"`). Mirrors the request shape:

- `Aggregations []AggregationComponents` — one entry per aggregator slot; universal floor `{n, n_null}` + operator-specific `Operator map[string]any` keyed by the manifest schema.
- `Groupers []GrouperComponents` — universal floor `{total_n, n_null}` + operator-specific bucket layout.
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, row/column/grand-total margin counterparts, axis-key components. Mirrors `MatrixPayload` coordinate-for-coordinate. Auxiliary `crosstab.margin_aggregations` margin figures extend this bullet — reference below.
- `Filterers []FiltererComponents` — uniform `{n_in, n_out, n_null_input}` across all 11 filterers.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. Coexists with `Response.Metadata`: `Metadata` keeps non-numerical run facts (cohort filename); `Run` carries the typed counters.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers}`, each carrying a mergeability class — `Mergeable` / `Partial` / `None` (`types.ComponentsMergeability`). Streaming chunks emit running state for mergeable operators; non-mergeable ones surface only at terminal flush.

**Opt-out.** `Options.DisableComponents bool` (engine default) + `types.Request.DisableComponents *bool` (per-request, `nil` inherits engine); CLI `--no-components` on `pulse api process` / `process-chain` / `compose`. Disabled leaves `Components` `nil` and the wire form byte-identical to the pre-Components baseline — `format_version` is NOT bumped.

**Long form: `.claude/reference/response-components.md`** — the auxiliary `crosstab.margin_aggregations` figures (ADMISSION rule, `present` semantics, display-flag gate, allocation/emission gap), the full opt-out contract and the Compose per-slot / per-layer surface. Skill: `skills/response-components.md`.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** Use `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf`.
- **No hand-built XML/CDATA.** Use `encoding/xml`.
- Use `descriptor.NewEnvelope(data)` for the standard envelope.

### Payload JSON Schema

`descriptor.BuildPayloadSchema()` returns the formal, deterministic JSON Schema (draft 2020-12) for every public payload — the five request envelopes (`Request`, `ComposedRequest`, `ChainRequest`, `FacetRequest`, `SampleRequest`), the result shapes (`Response`, `ComposedResponse`, `ChainResponse`, `FacetResult`) and the universal output `Envelope` (whose `data` slot is intentionally open). Generated three ways so it cannot drift: reflection over the `types` structs, registry-injected enums (`types.All*Types()` / `AllOverlayKinds()` / `AllRegressionTypes()`), and hand-tuned strict unions (`OverlayRef`, `OverlayPayload`). v1 boundaries: operator `params` stay an open object and the small closed mode enums stay plain strings. Golden at `descriptor/testdata/payload-schema.json`; `$id` version held equal to the envelope `format_version`. Reachable as `pulse schema` (raw, not envelope-wrapped), the `pulse://schema` MCP resource, and the published `payload-schema.json` on the docs site. Full prose: `docs/src/contract/payload-schema.md`.

### Manifest payload

`descriptor.BuildManifest()` returns the deterministic LLM-bootstrap blob — one fetch per session, client-cached, reachable as `pulse manifest --json` and `pulse_manifest`. Top level: `format_version`, `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim), `mcp_tools`, `cohort_types`, `skills`, `extensions`, plus the capability blocks `Facet`, `Join`, `ProcessChain`, `Crosstab`, `Export`, `Import`, `Overlays`. Sort-stable; golden at `descriptor/testdata/manifest.json`. Declarations live in `descriptor/capabilities_*.go`; MCP tool metadata in `mcp/toolmeta/meta.go`.

### Predict / Inspect contracts

**Contract: `.claude/reference/predict-inspect.md` — load it before changing predict, inspect, `pulse_inspect` or `CountRecords`.** The always-load half:

- **Predict structural ban:** `descriptor/predict.go` MUST NOT import `service/` or `processing/`. Enforced by `TestPredictNoExecutionImports`. Reads only header + schema, never records.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via `processing.CanStreamRequest(req, schema)`.
- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`; dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`. `InspectResult.RecordCount` is derived from the file LENGTH, never by reading a record. **`Pulse.Inspect` drops `env.Warnings` on the floor** — `Pulse.InspectEnvelope` is the only way to reach the `FullDict` knob or the truncated-tail warning, and both the CLI leaf and `pulse_inspect` therefore read the envelope, never the result-only wrapper.
- **CountRecords header-fast:** returns the record total without decoding the payload. The single-file floor division exists ONCE, at `encoding.Schema.RecordCountForPayload`, which `descriptor.Inspect` calls too. **The two arms differ in OBSERVABILITY and only there, deliberately** — `Inspect` warns `ENCODING_INVALID` on a truncated tail, `CountRecords` floors SILENTLY because it also feeds the parallel-decode eligibility gate. Do not close that gap by making `CountRecords` error.

### Execution modes (pointers)

**Contract: `.claude/reference/execution-modes.md` — load it before engine work.** CLAUDE.md keeps identity + the named skill only.

- **Streaming Process** (`pulse.ProcessStream`, `--stream`) — four orchestrator modes; the forced-buffered list is `skills/streaming-and-watching.md`.
- **Projected buffered decode** — default-on, output-transparent; `Options{DisableProjection}` / `--no-project`.
- **Parallel Compose** (`pulse.ComposeParallel`, `--parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}`; post-slot overlay fold at `service/compose_overlay.go`. `skills/compose-requests.md`.
- **Parallel shards** (`Options.ShardWorkers`) / **parallel buffered Process** (`Options.DecodeWorkers`) — mergeable-only via `processing.CanMergeRequest`, orthogonal to each other. `skills/cohort-schema-design.md`.
- **ProcessChain** (`pulse.ProcessChain`) — source-rooted linear chain, mergeable-only at v1, dual-slot overlays. `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins`) — v1 is exactly one inner join per Request. `skills/join-design.md`.
- **Crosstab / fused crosstab** (`Request.Crosstab`; `processing.CanFuseCrosstab`) — composed row×column grid whose margins recompute from raw rows, plus an in-decode streaming arm. **Benchmark fused on peak heap, never `B/op`.** `skills/crosstab-guide.md`.
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); four FACET-host overlay kinds ride `FacetRequest.Overlays`. `skills/facet-design.md`.
- **Point lookup** (`pulse.Lookup`) — O(1) key-exact rows via a prebuilt sidecar index; single-file cohorts, equality-only, full-key. `skills/tool-lookup.md`.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates that **never mutate the base payload**. `skills/overlay-system.md`.

## Non-Skippable CI Gates

**The list is self-expanding.** `TestClaudeMdMentionsAllNonSkippableGates` scans every `*_test.go` for eight prefixes — `TestSkillsCover`, `TestClaudeMd`, `TestUpdateDemand`, `TestNoOrbit`, `TestGoldensNot`, `TestPredictNo`, `TestDescriptorNo`, `TestPerPackageCoverage` — and requires each match to be listed BY NAME below, including one added in a different package. Write the gate and its list entry together or the suite fails.

CLAUDE.md hygiene — `TestClaudeMdMentionsFormatVersion` (the literal `"1.1"` appears), `TestClaudeMdMentionsAllEnvVars` (every `PULSE_*` in Go source appears), `TestClaudeMdMentionsAllNonSkippableGates` (the prefix rule above), `TestClaudeMdMentionsComponentsContract` (`Response.Components` shape + universal floor + the `Response.Metadata` collision note), `TestClaudeMdSizeBudget` (**CLAUDE.md ≤ 50,000 bytes — displace prose into `.claude/reference/`, never raise the ceiling**), `TestUpdateDemandTableCovers` (every category word inside the Update Demand SECTION, not the whole file), `TestUpdateDemandTableCoversComponents` (the `Response.Components` / per-operator `ComponentSchema` / extension `ComponentSchema` rows).

Predecessor-reference hygiene — `TestNoOrbitPrefix` (no type constant), `TestNoOrbitPrefixes` (no error code) contains a predecessor reference.

Descriptor contracts — `TestPredictNoExecutionImports` (`descriptor/predict.go` imports no `service/` or `processing/`), `TestDescriptorNoFmtSprintf` (no `fmt.Sprintf` in `envelope.go`/`manifest.go`/`predict.go`/`inspect.go`), `TestGoldensNotHandEdited` (every golden ends with a valid `// golden-hash:` line), `TestPerPackageCoverageFloors` (package dirs exist; documents the coverage floors).

Skill coverage — each asserts an atomic skill file exists at the conventional stem: `TestSkillsCoverAllComponents` (aggregators / attributes / filterers / groupers / features → `op-<category>-<kebab>.md`), `TestSkillsCoverAllFieldTypes` (`type-*`), `TestSkillsCoverAllWindowTypes` (`op-win-*`), `TestSkillsCoverAllMCPTools` (`tool-*`, strip `pulse_`), `TestSkillsCoverAllSynthDistributions` (`op-synth-*`), `TestSkillsCoverAllRegressions` (`op-reg-*`), `TestSkillsCoverAllOverlayKinds` (`op-overlay-*`). Plus three that check content rather than existence:
- `TestSkillsCoverShardingTopics` — `skills/cohort-schema-design.md` carries a `Sharded` section.
- `TestSkillsCoverAllCliLeaves` — every runnable leaf in the real `buildApp()` tree (`pulse convert` included) is named verbatim under `skills/` or `docs/src/`; the `docs/src/cli/flags.md` command index is the minimum home. Naming only — the gate cannot judge whether the prose is adequate.
- `TestSkillsCoverAllOperatorComponents` — each aggregator/grouper/filterer's `ComponentSchema` keys appear under a `## Components` section in its atomic skill.
- `TestSkillsCoverAllCrossReferences` — every skill stem named in CLAUDE.md, `.claude/reference/*.md` or the pack resolves, and a section-qualified `` `skills/<stem>.md` (Section) `` pointer names a heading the target really carries. Deliberate non-skill kebab tokens ride the allowlist in `skill_xref_test.go`.

Atomic-skill structure / budget / example-tag — `TestAtomicSkillHasRequiredSections` (the required `##` set per family), `TestSkillTokenBudget` (per-family body size; soft-only today), `TestOperatorHasAtomicSkill` (every operator, MCP tool and field type has a file at its stem), `TestEveryOperatorHasAnExampleTag` (every operator name is tagged on at least one `examples/<dir>/*.json`).

Other load-bearing contract gates are **not** prefix-matched (they are enforced by their own packages) and are listed in `.claude/reference/update-demand.md` (Other load-bearing contract gates) — the `TestManifest*Complete` family, `TestStreamability_*`, `TestExtensions_*`, `TestExamples_*`, `TestShardArchive*` and the per-mode suites.

## Build / Env

`make build` (default), `test`, `fmt`, `vet`, `lint`, `cover`, `clean`, `docs`, `docs-serve`, `docs-clean`. A `.env` at repo root is auto-loaded. `make lint` = `go vet` + `staticcheck`, and must pass before any push.

**Environment variables** — one line each; `pulse.Options` always overrides:

- `PULSE_DATA_DIR` — base directory for `.pulse` cohorts, used by `fs.Default()`. The only required env var; bypass with `Options{DataDir}` or `Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdir under the fs root (default `imports`), honoured by `imports.Manager`.
- `PULSE_IMPORT_TTL` — default TTL for managed imports: Go duration (`24h`), day form (`7d`), or `pin`. Default `7d`.
- `PULSE_LABEL_TABLES_DIR` — directory whose `*.json` files auto-load as `LabelTables` at `pulse.New` time, keyed by filename. Detail: `skills/label-display.md`.
- `PULSE_RANGE_TABLES_DIR` — same shape for `RangeTables` (bare `{label,start,end}` array or a `{"description","ranges"}` wrapper; filename minus `.json` is the table name), validated through the shared range-compilation pass. A name declared both programmatically and on disk is a hard error.
- `PULSE_MCP_NO_COHORT_SCAN` — `pulse mcp` only (flag `--no-cohort-scan`): skip the startup walk that enumerates every `.pulse` file as an exact-match `pulse://` resource. The `pulse://` TEMPLATE is registered either way, so cohorts stay READABLE by URI — only the `resources/list` enumeration is withheld. Library equivalents: `gosdk.Config.DisableCohortScan`, `mcpserve.Options.DisableCohortScan`; both default to scanning.
- `PULSE_TEMPLATES_DIR` — request-template roots, `os.PathListSeparator`-separated in PATH-style precedence (first root wins; a same-named template under a later root is shadowed, not rejected). Unset with no `TemplateDirs` builds no store and lookups return `PULSE_TEMPLATE_NOT_FOUND`. **The hot-reload phase table — which malformed-file state hard-fails startup, which serves its last-good parse, which lists as broken — is the contract and lives in `.claude/reference/request-templating.md` (Hot-reload lifecycle).**

**Both table directories exclude Pulse's own sidecars by suffix before any parse attempt** — `.spss.json`, `.meta.json`, `.indexes.json`, the list at `isPulseSidecarName` in `sidecar_names.go`. **That is an exclusion of KNOWN artefacts, not tolerance of unparseable JSON:** any other `*.json` that fails to parse is still a hard `pulse.New` error naming the path, because a typo'd table must not silently become a table that is not there. `PULSE_TEMPLATES_DIR` does **not** yet carry the exclusion — its recursive walk and `Summary.Broken` lifecycle make that a separate design call.

**Knobs.** Concurrency (`pulse.Options`, both default `0` ⇒ `NumCPU`, negatives rejected at `pulse.New()`, orthogonal to each other): `ShardWorkers` — per-shard pool for archives, explicit `1` forces serial; `DecodeWorkers` — per-segment pool for single-file cohorts above `parallelDecodeRecordThreshold` (100K records). Overlay (`OverlaySpec.Options`): `DictPrefixFast bool` (default `false`) matches Compose multi-slot overlay schemas by byte-equal dictionary PREFIX probe and requires the embedder to have verified prefix-equal dicts; `MaxPanelTargets int` (default `16`) caps Targets on multi-reference Compose overlays, overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`.

Hermetic testing: `fs.NewMemMap()` returns a `Config` backed by `afero.NewMemMapFs()`. No disk I/O.

## Extension Points

`pulse.Options.Extensions` is the public surface for embedders injecting domain operators or expression-runtime extensions — eight operator categories plus expr functions and three named-table kinds, all registered at `pulse.New()` time and treated identically to built-ins by predict, manifest, MCP and runtime. **Full recipe: `docs/src/internals/extension-points.md`.**

- **Naming policy:** `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$`. Reserved namespaces `BUILTIN` / `STANDARD` / `CORE` / `PULSE`; a collision with a built-in is rejected.
- **Probe-validation:** each factory is constructed once against a minimal synthetic schema. A streamable-flagged registration not returning the streaming interface → `PULSE_EXTENSION_STREAMABLE_MISMATCH`; a factory panic → `PULSE_EXTENSION_FACTORY_PANIC`.
- **Expression environment:** `ExprFunctions` merge into the expr-lang env (`ATTR_FORMULA`, `FILTER_EXPRESSION`); `LookupTables` are reachable as `lookup(table, keys...)` — unknown → `PULSE_LOOKUP_TABLE_UNKNOWN`, missing key → `PULSE_LOOKUP_MISS`.
- **Auxiliary tables:** `LookupTables` (numeric `key→float64`), `LabelTables` (output-time `key→label` via `LabelBinding.Table`) and `RangeTables` (`{label,start,end}` sets named by the date-range operators). All three project into the manifest `extensions` block and are dir-loadable.
- **Snapshot pattern:** `descriptor.ExtensionsSnapshot` is the read-only projection `pulse.New` builds and passes into `descriptor.PredictOptions.Extensions` and `mcp.BindSessionToolsWithExtensions` — that is what keeps `descriptor/` free of `service/` and `processing/` imports.
- **Lazy table resolution:** a grouper factory's `(grp, schema)` signature cannot reach the registry, so a named `table:` resolves through the `processing.ExtensionAware` `SetExtensions` hook (`processing.ApplyGrouperExtensions` threads it through every construction site; filterers already receive it). Inline sources resolve eagerly.
- **FieldInputs hook:** every registration accepts an optional `FieldInputs FieldInputsFunc`, read by `processing.NeededFields` for projection. **Absent widens the retained set to `*`** — correct, but it silently forfeits the projection win.

Surface: `extensions*.go` at the root; runtime overlay `processing/extensions.go`.

## Request templating

Stored parameterised JSON that renders into a **validated typed request**. `template/` is the whole implementation (import ceiling: stdlib + `types` + `errors` only — never `descriptor/`, `processing/`, `service/`; gated by `TestTemplatePackage_ImportBoundary`). Facade: `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`. **No CLI leaf, no MCP tool** — library/embedding surface only. **It is NOT expr-lang:** `$var` / `{{}}` / `$when` are request-authoring parameters substituted BEFORE decode, while `ATTR_FORMULA` / `FILTER_EXPRESSION` are expr-lang over row fields at execution time, and there is no interop by design.

**Contract: `.claude/reference/request-templating.md` — load it before changing the document model, the variable or target sets, or the substitution syntax.** It carries the file wrapper and its rejected-unknown-key rule, the three substitution forms, the nine variable types, directory precedence and shadowing, the hot-reload phase table, the nine `PULSE_TEMPLATE_*` codes chosen by provenance, and why render never opens a cohort. Env var `PULSE_TEMPLATES_DIR`; skill `skills/request-templating.md`; docs `docs/src/library/request-templating.md`.

## Synthetic data

**Contract: `.claude/reference/synthetic-data.md` — you MUST load it before changing any `synth/` code, configuration or public surface**: profile capture, `SpecFromProfile` translation, generation, structural rules, the `--suggest-rules` detectors, the fidelity report. **Every failure it records was SILENT** — the run succeeded and a number was quietly wrong. It covers conditional pair capture; per-numeric linear models (selection, shrinkage, the composed draw, shape-fitted conditioning); float-fusion determinism; `bernoulli` / `discrete` marginals; correlation and residual-correlation capture, draw and recovery; the warning summary; and `Spec.Rules`.

`format_version` does NOT move for a synth change — `synth.Profile` / `synth.Spec` live in `synth/`, not `types/`, so they are unreachable from `descriptor.BuildPayloadSchema`. Skills: `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md`.

## Skill Pack

The pack under `skills/` is the LLM surface, embedded via `//go:embed *.md`. Two shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic, `kind: design`).

**Contract: `.claude/reference/skill-pack.md` — load it before adding or restructuring a skill file, changing frontmatter keys, changing a family's required `##` section set, or moving a budget.** It carries the frontmatter blocks, the required-section table per family, the budget table, the per-trigger stem table and the add-a-skill procedure. The always-load half:

- **The stem encodes the surface**, and frontmatter `name:` MUST equal the file stem: `op-<category>-<kebab>.md` per registered operator constant, `tool-<kebab>.md` per MCP tool (strip `pulse_`), `type-<kebab>.md` per `FieldType`.
- **Each family has a required `##` section set** (`TestAtomicSkillHasRequiredSections`, keyed off the `category:` frontmatter field) and a body budget (`TestSkillTokenBudget`: `op-*` ≤1200 chars, `tool-*`/`type-*` ≤2000, `kind: design` ≤6000, at `chars / 4 ≈ tokens`). Both tables are in the reference file.
- **There is no `skills/index.json`.** `skills.List()` walks the embedded `embed.FS` and parses frontmatter — the filesystem IS the manifest, so a new file with valid frontmatter is picked up automatically. Never create or bump an index.
- **Never hardcode a registered count** in docs. Counts come from `pulse_manifest`; the coverage gates reject drift.
- Cross-cutting topics that are not operator-keyed route to a topical skill: `Response.Components` → `skills/response-components.md`; request slot map / smart defaults → `request-envelope`; streaming / `Watch` / request hashing → `streaming-and-watching`; error-code prose → `errors/fixup_metadata.go` via `pulse_errors_lookup`; extensions → `docs/src/internals/extension-points.md`.

## What NOT to Do

- **Do not import `service/` or `processing/` from `descriptor/`.** Predict/inspect/manifest are no-execute; `TestPredictNoExecutionImports` fails.
- **Do not hand-edit golden files.** Regenerate: `go test ./descriptor/ -run 'Test.*Golden' -update`.
- **Do not add implementation without tests in the same PR.** TDD is a hard rule here.
- **Do not use `fmt.Sprintf` for JSON/XML.** Use `encoding/json` + `descriptor.NewEnvelope(data)`.
- **Do not defer a skill or CLAUDE.md update.** The follow-up PR will not happen and the next session reads stale guidance.
- **Do not raise `claudeMdSizeCeiling` to make CLAUDE.md fit.** Move the long form into `.claude/reference/` and leave the always-load half plus a pointer — that is the whole point of the gate.
- **Do not add a component without updating the registry** (`processing/registry.go`) + `types.All*Types()`.
- **Do not bypass `afero.Fs`** — it defeats `fs.NewMemMap()` and the custom-storage extension hook.
- **Do not put business logic in `cmd/pulse/`.** The CLI parses flags, constructs library objects, calls methods, formats output.
- **Do not bypass overlay typing via direct payload mutation.** Overlays are read-only siblings keyed to host coordinates — never mutate `Response.Data` / `Response.Crosstab.Matrix` in a handler; the fold writes `Response.Overlays[i]` only.

## Reference Docs

`.claude/reference/*.md` is where CLAUDE.md's long form lives. **This is the complete index.** CLAUDE.md carries only the always-load half of each contract, so a change to one of these surfaces is not adequately informed by CLAUDE.md alone — load the named file BEFORE the work, not after. Most are also named as required companions by the Update Demand table, which makes loading them binding rather than advisory.

| File | Holds | Load before... |
|---|---|---|
| `update-demand.md` | the exhaustive per-slot trigger table + the non-prefix-matched gate list | touching ANY contract; add a row here when introducing a new Request slot, Response slot, capability block or execution-mode wiring |
| `byte-layout.md` | the sidecar index and its manifest, the SPSS metadata sidecar, derived columns, SPSS export, target-aware export predict, projected decode | changing the `.pulse` format, a field type, shard-archive layout, any sidecar file, an SPSS surface, or the projection contract |
| `execution-modes.md` | full wiring prose per mode, including every overlay host | engine work in `service/` or `processing/`, or adding an execution mode |
| `response-components.md` | the `crosstab.margin_aggregations` auxiliary figures (ADMISSION rule, `present` semantics, display-flag gate, allocation/emission gap), the opt-out, the Compose surface | changing `Response.Components`, a per-operator `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either arm |
| `predict-inspect.md` | inspect's envelope-vs-result split, the truncated-tail warning, `CountRecords`' deliberate silence, the predict import ban | changing `descriptor/predict.go` or `inspect.go`, `Pulse.Inspect` / `InspectEnvelope`, `pulse_inspect`, or `CountRecords` |
| `request-templating.md` | the file wrapper, the three substitution forms, the nine variable types, directory precedence, the hot-reload phase table, the nine `PULSE_TEMPLATE_*` codes | changing the request-template document model, the variable or target sets, or the substitution syntax |
| `synthetic-data.md` | the whole `synth/` contract — capture, models, marginals, correlation and residual recovery, structural rules, the detectors, the fidelity report | ANY `synth/` change. Every failure it records was SILENT: the run succeeded and a number was quietly wrong |
| `skill-pack.md` | frontmatter blocks, the required `##` set per family, the budget table, the per-trigger stem table, the add-a-skill procedure | adding or restructuring a skill file, changing frontmatter keys, changing a family's required sections, or moving a budget |

**Putting prose there instead of here is the intended direction of flow.** `TestClaudeMdSizeBudget` caps CLAUDE.md at 50,000 bytes precisely so a new contract DISPLACES long form into that directory rather than accumulating in the always-loaded file; raising the ceiling is not the fix. `.claude/reference/*.md` is inside `TestSkillsCoverAllCrossReferences`' corpus, so relocating prose carries its cross-references with it — a heading renamed in moved text still breaks the pointer that names it.
