# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as a Go library (`github.com/frankbardon/pulse`) and a CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` is the public API — `New`, `Open` (+ `Cohort.Reader`), `NewCohortBuilder`, `Process`, `Compose`, `ComposeParallel`, `ProcessStream`, `ProcessChain`, `Import`, `Export`, `Convert`, `Inspect`, `InspectEnvelope`, `InspectBytes`, `Predict`, `PredictBytes`, `Sample`, `Facet`, `Synth`, `Profile`, `CountRecords`, `Lookup`, `BuildIndex`, `VerifyIndex`, `ListIndexes`, `DropIndex`, `CohortArtifacts`, `WidenSetField`, `Dedup`, `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`, `InitFeatureProfile`, `CheckFeatureProfile`, `DiffFeatureProfile`, `DescribeFeatureProfile`, `ExampleFeatureProfiles`, `ExampleFeatureProfile`, `Glossary`, `Intents`, `Ontology`, `Skills`, `Skill`, `Version`. **The CLI never contains business logic.**
- **Self-describing.** Every `.pulse` file carries its schema in the header. `internal/descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `internal/skills/` embeds an atomic-per-surface pack (`op-*` / `tool-*` / `type-*`) plus ~20 topical design skills via `//go:embed *.md`; the filesystem walk + frontmatter parse is the source of truth.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators, expr functions, named tables, skills and examples. Predict, manifest, MCP and runtime treat them identically to built-ins.
- **Embedder-first, consumer-agnostic.** Library embedders are a first-class audience: the public Go surface is sized for them, and real downstream usage catalogs are valid evidence for what it must cover. Pulse never depends on a consumer — no reverse imports, no consumer names in contract docs or code, and every public symbol is justified by a general embedder use case, not one consumer's convenience. Harnesses discover Pulse via `pulse manifest --json` + the embedded skills.

**Where the detail lives.** Contributor recipes are the mdBook Internals chapter under `docs/src/internals/` — one `adding-*.md` per extension point plus `regenerating-goldens.md`, `debugging-predict.md`, `wiring-mcp-client.md`, `extension-points.md`. Long-form contract prose lives under `.claude/reference/` — **see "Reference Docs" at the bottom for the index and which file each kind of work requires loading.**

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

One row per category: trigger → companions → gates. **The exhaustive per-slot table is `.claude/reference/update-demand.md` — load it before touching any contract below.** ATOMIC = `TestOperatorHasAtomicSkill` + `TestAtomicSkillHasRequiredSections` + `TestSkillTokenBudget`. A **bold filename** in column 2 is a `.claude/reference/` file that must be LOADED BEFORE the change, not merely updated after it.

| If you change... (category) | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator, attribute, filterer, grouper, feature operator, window operator, statistical test (`TEST_*`), regression (`REG_*`), synth distribution or overlay kind (`OVERLAY_*`) | `skills/op-<category>-<kebab>.md` + the matching `internal/descriptor/capabilities_*.go` + an `internal/examples/` `_meta.operators` tag | ATOMIC, the category's `TestSkillsCoverAll*` + `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag` |
| A registered MCP tool (add/remove) | `skills/tool-<kebab>.md` (strip `pulse_`) + `internal/mcp/toolmeta/meta.go` | ATOMIC, `TestSkillsCoverAllMCPTools` |
| A registered field type, a `.pulse` format or shard-archive change, a sidecar file, an SPSS surface, or projected decode | **`byte-layout.md`** + `skills/type-<kebab>.md` + `skills/cohort-schema-design.md` + CLAUDE.md "Byte-layout invariants" | ATOMIC, `TestSkillsCoverAllFieldTypes`, `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + ≥1 Fixup — and an `internal/descriptor/error_owners.go` owner entry | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete`, `TestErrorOwners_Complete` |
| A `--json` envelope, `format_version` (currently `"1.1"`), or any payload-reachable `types` slot | CLAUDE.md "Output Format Contract" + `docs/src/contract/payload-schema.md` + regenerate `descriptor/testdata/payload-schema.json` | `TestPayloadSchemaGolden`, `TestClaudeMdMentionsFormatVersion` |
| `ComposedResponse` shape, the Compose facade return type, the `compose --json` `data` wrapping, or `OverlayLayer.Warnings` | CLAUDE.md "Output Format Contract" + `skills/overlay-system.md` (Per-layer warnings section) + the reference row's sites | `TestComposedResponse_OverlayFreeByteIdentical` |
| A registered I/O format (`internal/io/<fmt>/` adapter) | the registry / factory / CLI / capability sites in the reference row + `skills/tool-import.md` + `docs/src/internals/adding-io-format.md` | `TestFromExt_Matrix`, `TestManifestImportCapability` |
| A CLI leaf (add/remove) | the `docs/src/cli/flags.md` command index + `skills/session-bootstrap.md` for an agent-relevant flag + a `commandBindings` entry (`internal/descriptor/features.go`) | `TestSkillsCoverAllCliLeaves`, `TestCommandBindings_Complete` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| An environment variable | CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| `Response.Components` shape, a per-operator `ComponentSchema`, an Extension registration's `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either arm | **`response-components.md`** + CLAUDE.md "Output Format Contract" + `skills/response-components.md` + the operator's atomic skill + `internal/descriptor/capabilities_*.go` + `docs/src/internals/extension-points.md` + `skills/crosstab-guide.md`. **A record reaches an auxiliary margin only if it reached a CELL — state that rule wherever the figures appear** | `TestClaudeMdMentionsComponentsContract`, `TestExtensions_ComponentSchemaParity`, `TestCrosstab_BufferedAuxMarginMatchesFused` |
| The request-template document model, variable/target sets, or `$var` / `{{}}` / `$when` | **`request-templating.md`** + `skills/request-templating.md` + `docs/src/library/request-templating.md` + CLAUDE.md "Request templating" | `TestTemplatePackage_ImportBoundary` |
| Any `synth/` / `internal/synth/` surface — capture, `SpecFromProfile`, `Spec`, generation, structural rules, `--suggest-rules`, fidelity report | **`synthetic-data.md`** + `skills/synthetic-data.md` + `skills/synth-models.md` + `skills/synth-structural-rules.md` | `TestSkillsCoverAllSynthDistributions` |
| A feature (operator, capability, I/O format, MCP tool or prompt) or the feature-profile model | **`feature-profiles.md`** + a `features.go` row (`Since` + dependency edges) + `docs/src/library/feature-profiles.md`; never a runtime skill | `TestFeaturesHaveSince`, `TestProfileDependenciesComplete` |
| A `multiplicity` block, `Options.DefaultMultiplicity`, its adjusted outputs, predict `p_values` or the fold | `update-demand.md` Multiplicity row (load first) + `skills/multiplicity-correction.md` + p-emitting atomic `## Params` | `TestMultiplicityNoneIsIdentity` |
| A registered operator (any category) → its `Purpose` (+ `Interpretation` if inferential); the intent taxonomy, glossary, virtual skills or extension guidance hook | **`guided-analysis.md`** + `builtinPurposes` / `builtinInterpretations` | `TestSkillsCoverAllPurposes`, `TestInterpretationCoversOutputs`, `TestManifestGuidanceBudget` |
| A skill file's stem, frontmatter, required sections or budget; a feature name in a skill (fence it) | **`skill-pack.md`** + the file itself | ATOMIC, `TestSkillsCoverFeatureFences` |
| `Request.Weight`, a per-slot `weight` (null ≠ absent), `Options.DefaultWeight`, an operator's weight class, or the weighted floor keys | **`weighting.md`** + `skills/weighting.md` + the operator's atomic skill + **`response-components.md`** + the payload-schema golden | the weighting row in `update-demand.md` |
| Any Request slot, Response slot, capability block, or Execution-mode wiring | the per-slot row in `update-demand.md` | per-slot suites cited there |

Between them the rows carry every word `TestUpdateDemandTableCovers` checks; keep it that way when editing one.

Never defer the doc/skill update to a follow-up PR; it will not happen.

## Architecture

**Public packages** (frozen at v1.0.0, `TestPublicAPIGolden`): root `pulse`, `types`, `errors`, `encoding` (schema nouns + ungrouped raw-byte primitives), `descriptor` (result/envelope types), `io` + `synth` (alias facades over `internal/io` / `internal/synth`), `mcp/gosdk`, `mcpserve`, `extend` (operator-authoring API). Everything else is under `internal/` — `internal/processing` (+ `feature`, `window`), `internal/service`, `internal/descriptor` (builders, `capabilities_*.go`; NO-EXECUTE), `internal/encoding`, `internal/io/<fmt>` adapters (`csv|tsv|ndjson|jsonarray|jsonshared|arrow|parquet|excel|spss`) over `internal/iocore` contracts, `internal/synth`, `internal/template`, `internal/mcp` (+ `toolmeta`), `internal/skills`, `internal/examples`, `internal/fs`, `internal/imports`, `internal/cli`. **Contract: `.claude/reference/architecture.md` — the full tree, the split-in-place vs alias-facade technique, root aliases, the `io` import boundary, and the MCP layer split; load it before moving a package or adding a public symbol.**

CLI commands map 1:1 to manifest commands: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `schema`, `mcp`, `widen`, `dedup`, `version`, plus `synth from-schema`, `synth from-profile`, `profile create`, `shard {create,add,remove,list,compact,verify,extract}`, `index {build,list,verify,drop}`, `features {init,check,diff,show}`, `api {process,compose,facet,process-chain,lookup}`. `pulse schema` prints the payload JSON Schema RAW — not envelope-wrapped.

**MCP:** `internal/mcp/` is the SDK-free core (`TestMCPCore_NoSDKImport`); `mcp/gosdk/` is the ONLY go-sdk importer (`Register(server, p, cfg)`). **The manifest is the source-of-truth tool count, never hardcode it**; `pulse://schema` is a RESOURCE, not a tool. **Tools, prompts and resources are registered per instance** — a profiled instance mounts only what it enables, so `gosdk.RegisteredTools()` / `RegisteredPrompts()` stay the GLOBAL lists. Detail: the MCP layer split section of `.claude/reference/architecture.md`.

Docs at <https://frankbardon.github.io/pulse/>. Skills under `skills/` are the LLM surface.

## Code Conventions

### Naming

- Pulse-native identifiers. No predecessor references (`TestNoOrbitPrefix`, `TestNoOrbitPrefixes`).
- Module path: `github.com/frankbardon/pulse`. The public `io` package is imported as `pio "..."`.
- Component types: SCREAMING_SNAKE — `AGG_COUNT`, `ATTR_ZSCORE`, `FILTER_INCLUDE`, `GROUP_CATEGORY`, `WIN_LAG`, `FEAT_LOG`, `TEST_T`.
- Error codes: DOMAIN_CATEGORY — `ENCODING_INVALID`, `PROCESSING_CONFIG`, `SERVICE_VALIDATION`, `DATA_FILE`, `CLI_INPUT`, `PULSE_IMPORT_ROW_ERROR`.

### Error handling

Six domains: `CLI`, `DATA`, `ENCODING`, `PROCESSING`, `PULSE`, `SERVICE`. Canonical list in `errors/codes.go` (`allCodes`). Every code needs `codeMetadata` entry (`errors/fixup_metadata.go`) with `Message` + ≥1 `Fixup` template OR `FixupNotApplicable: true`. Per-code prose surfaced via `pulse_errors_lookup` (MCP) / `pulse errors lookup CODE` (CLI). Manifest carries name-only list.

Field descriptions in `.pulse` capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`). Low-quality descriptions emit `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warnings (errors under `--strict`).

### Byte-layout invariants

`.pulse` binary format:

1. **9-byte header:** 8-byte magic `PULSE\x00\x00\x00` + 1-byte format version ∈ `{0x01, 0x02}` (`encoding.MagicBytes`, `FormatVersionV1`/`V2`, `HeaderSize = 9`). **`ReadHeader` RETURNS the version and every `ReadSchema(r, version)` must be handed it** (gated by `TestReadSchemaCallersThreadHeaderVersion`); any other byte is `ENCODING_INVALID` naming it. The version written is a function of schema content (`Schema.RequiredFormatVersion`, `WritePreamble`), never a flag — `0x02` iff the schema declares a parent group, so an ungrouped write stays `0x01` byte-identically. `0x01` cohorts stay readable forever (`encoding/testdata/format_v1.pulse`).
2. **Schema block:** per-field descriptor — name, type byte, **nullable flag byte** (immediately after the type byte; `1` = participates in the null bitmap), byte offset, bit position, optional description. Under `0x02` a `u64`-length-prefixed extension block of tagged sections follows the last descriptor; an unknown REQUIRED section is refused. Detail: `byte-layout.md` (Format version).
3. **Dictionary blocks:** inline after the schema, for every dictionary-bearing field.
4. **Record data:** fixed-width rows, stride derived from the schema.
5. **Per-record null bitmap (optional).** Present iff the schema has any nullable field (`Schema.HasBitmap()`); every record then carries a trailing `ceil(field_count / 8)` bytes. Field index `i` → byte `i/8`, bit `i%8` (LSB-first); `1` = null. Helpers: `encoding.ReadBitmap` / `WriteBitmap` / `BitmapIsNull` / `BitmapSetNull`, `Schema.BitmapByteSize()`.
6. **Parent groups (`0x02`).** N independent groups store each distinct member tuple ONCE in a schema-block dictionary (entry = members' raw on-wire bytes + member null bits, never strings; members keep their own dictionaries); each row carries a `u32` index per indexed group, a constant group one entry and no index. `RecordByteSize` / `BitmapByteSize` / `HasBitmap` are PHYSICAL while every field index stays LOGICAL (`Schema.Fields`); decode is identical to the ungrouped twin. Physical-byte rewriters refuse via `internal/encoding.RefuseGroups`. Written only on request: `--group KEY:MEMBER,...` (key-checked, `PULSE_GROUP_*`; per-group viability gate — too narrow dropped, low ratio warns `PULSE_DEDUP_LOW_RATIO`, `--strict` errors) and `--elide-constants` (FULL-pass constants). Detail: `byte-layout.md`.

20 field types (full table in `skills/cohort-schema-design.md`, per-type detail in `skills/type-<kebab>.md`): `u4`, `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `set_u8`/`u16`/`u32`/`u64`/`u128`/`u256`. Bit-packed types (`u4`, `packed_bool`) return `ByteSize() == 0` and share bytes with neighbours (`FieldType.IsBitPacked()`). `categorical_*` and `set_*` carry an inline dictionary (`FieldType.HasDictionary()`); a `set_*` value is a fixed-width bitmask where bit `i` ↔ dictionary entry `i`, and an empty mask is a valid "no selection" — distinct from null. **The wide rungs are `set_u128` (type byte `18`, stride 16 bytes, ≤128 members) and `set_u256` (type byte `19`, stride 32 bytes, ≤256) — 256 is a hard ceiling, inference picks the SMALLEST rung that fits, and both are too wide for the `uint64` `Read/WriteFieldValue` API, which refuses them with `ENCODING_TYPE_MISMATCH` rather than truncating (`FieldType.IsWideSet()`).** **`date` is epoch DAYS (`int32`); `datetime` (type byte `17`) is epoch SECONDS (`int64`) — never interchangeable, and swapping them rescales every value by 86,400.** Nullability is orthogonal to type; an unknown type byte is `ENCODING_INVALID`; `decimal128` and `set_*` nulls ride the bitmap only, with no in-band sentinel.

**Shard archive variant.** A `.pulse` path resolves to either the single-file layout above or a **shard archive** — uncompressed Zip64 (store-only) opening with zip magic `PK\x03\x04` instead of `PULSE`, dispatched at `pulse.Open`; **the single-file byte format is unchanged.** `.pulse.zst` is transport-only: reads refuse it (`PULSE_COHORT_COMPRESSED`). The archive holds a reserved `_schema.pulse` entry (canonical schema + SHRD trailer) plus N standalone shards; cohesion is structurally strict, categorical dictionaries union-merge, anchor `archive.pulse#shard.pulse` opens one shard. **A `set_*` field WIDENS (never narrows) to the narrowest rung that holds it, past `set_u256` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`; grouped (`0x02`) shards share ONE archive group layout (the archive's wins).** Every archive rewrite is atomic and reports a MANDATORY warning (`PULSE_SHARD_SET_WIDENED` / `PULSE_SHARD_GROUPS_REWRITTEN`). Long form incl. `pulse widen` / `pulse dedup` sidecar invalidation: `byte-layout.md`; `skills/cohort-schema-design.md` (Sharded cohorts).

**Contract: `.claude/reference/byte-layout.md` — load it before touching any of these; none is a `.pulse` layout and each fails SILENTLY when got wrong:** the sidecar point-lookup index + its discovery manifest, the SPSS metadata sidecar (`cohort.pulse.spss.json`), SPSS derived columns / export / convert, target-aware export predict, default-on projected buffered decode.

### Smart defaults

When a request slot names a field but omits `Type`, engine infers from schema type. Table in `internal/descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u4/u8/u16/u32/u64, f32/f64, decimal128) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `datetime` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `packed_bool` | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |

`Field.Nullable` orthogonal — never changes inferred operator. Defaults apply only when `Field` set and `Type` empty; never override explicit `Type`; never cross categories; never default tests, filter expressions, attributes, windows, features. Disable via `pulse.Options{DisableDefaults: true}` or `--no-defaults`. Predict always computes `DefaultsApplied`.

**Date-family field types.** `GROUP_DATE`, `GROUP_DATE_RANGES` and `FILTER_DATE_RANGES` accept BOTH `date` (epoch days, `int32`) and `datetime` (epoch seconds, `int64`); past the operator boundary everything speaks epoch DAYS. **All epoch-day / calendar / zone math lives in `internal/temporal`** (`TestNoZoneMathOutsideTemporal` bans `86400` / zone loading elsewhere). Zones: slot `tz` → request `time_zone` → `Options.DefaultTimeZone` → UTC; a `datetime` buckets and filters on its LOCAL day in every mode, a non-UTC zone on a derived field is `PROCESSING_CONFIG`, UTC is byte-identical to no zone. Long form: `execution-modes.md` (Date-family field types, Time zones, Labeled date ranges); skill `skills/time-zones.md`.

## Output Format Contract

### `--json` envelope

All `--json` CLI output and every descriptor operation use `descriptor.Envelope` — `{format_version, data, request, errors, warnings}`, built with `descriptor.NewEnvelope(data)`.

- `format_version` is always `"1.1"`, bumped from `"1.0"` for the Compose facade lift. **Additive-only: bump only on a backward-incompatible shape change** — new `data` fields do not bump, renames and removals do. Any bump MUST update this section.
- `errors` / `warnings` are `{"code", "message", "details"}` entries, an empty array (never null) when absent. **A FATAL `*errors.CodedError` carries its own code** — every CLI leaf (`pulse api *` included) routes through `writeCodedErrorEnvelope`, which unwraps with `errors.As` and falls back to the placeholder (`PROCESS_ERROR`, …) only for an UNCODED error. Stringifying a coded error into the placeholder makes `errors[0].code` unusable with `pulse errors lookup`. **The overlay family follows the same rule**: every `PULSE_OVERLAY_*` fault is raised with that code as its own `Code`, never `PROCESSING_INTERNAL` with a `details["code"]` echo; `PROCESSING_INTERNAL` is reserved for a caller-side invariant violation with no user-facing code.
- **Undefined figures are `null`.** A non-finite float (NaN / ±Inf — a 0/0 `AGG_RATIO`, an unfilled rolling window) is JSON `null` in place, key kept (`types.MarshalFinite`; result types marshal through it); Go results keep NaN. Not a shape change: such output never serialised before. Long form: `.claude/reference/response-components.md` (Undefined figures on the wire).
- `request` is an opt-in echo of the *normalized* request, omitted unless `Options.EchoRequest` / `--echo-request`; its shape follows the operation (one of the five request roots). Streaming skips the echo. Additive `omitempty`; no `format_version` bump.

**Compose envelope (`pulse api compose --json`).** Since the v1.1 lift, `data` is a `ComposedResponse` OBJECT — not the legacy `[]*Response` array — carrying `responses` (one `Response` per `ComposedRequest.Requests` slot, in input order) and `overlays` (one `OverlayLayer` per `ComposedRequest.Overlays` spec, omitted when there are none). Streaming (`--stream`) bypasses the envelope entirely and emits per-row `{"index", "row"}` NDJSON; Compose overlays surface only at terminal flush in non-streaming mode (`skills/streaming-and-watching.md`).

**Multiplicity outputs.** Opt-in `multiplicity {method, family, alpha}` (absent ⇒ byte-identical; `format_version` stays `"1.1"`) adds BESIDE the raw p, which never moves: `TestResult` / `OverlaySummary` `p_adjusted` + `significant_adjusted`, matching `OverlayPayload` matrices, `multiplicity {…, m}` echoes. NaN p ⇒ null. Contract: `execution-modes.md` (Multiplicity).

### Response.Components

Every `Response` carries an optional `Components *ResponseComponents` (additive `omitempty`; `format_version` stays `"1.1"`). Mirrors the request shape:

- `Aggregations []AggregationComponents` — one entry per aggregator slot; universal floor `{n, n_null}` + operator-specific `Operator map[string]any` keyed by the manifest schema.
- `Groupers []GrouperComponents` — universal floor `{total_n, n_null}` + operator-specific bucket layout.
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, row/column/grand-total margin counterparts, axis-key components. Mirrors `MatrixPayload` coordinate-for-coordinate. Auxiliary `crosstab.margin_aggregations` margin figures extend this bullet — reference below.
- `Filterers []FiltererComponents` — uniform `{n_in, n_out, n_null_input}` across all 11 filterers.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. Coexists with `Response.Metadata`: `Metadata` keeps non-numerical run facts (cohort filename); `Run` carries the typed counters.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers}`, each carrying a mergeability class — `Mergeable` / `Partial` / `None` (`types.ComponentsMergeability`). Streaming chunks emit running state for mergeable operators; non-mergeable ones surface only at terminal flush.

**Opt-out.** `Options.DisableComponents bool` (engine default) + `types.Request.DisableComponents *bool` (per-request, `nil` inherits engine); CLI `--no-components` on `pulse api process` / `process-chain` / `compose`. Disabled leaves `Components` `nil` and the wire form byte-identical to the pre-Components baseline — `format_version` is NOT bumped.

**Weighted slots** add `omitempty` aggregator-floor keys `sum_weights`, `n_eff` (probability only), `n_weight_invalid` — absent means unweighted; `n` / `n_null` and every count stay raw ints. Long form: `.claude/reference/weighting.md`.

**Long form: `.claude/reference/response-components.md`** — the auxiliary `crosstab.margin_aggregations` figures (ADMISSION rule, `present` semantics, display-flag gate, allocation/emission gap), the full opt-out contract and the Compose per-slot / per-layer surface. Skill: `skills/response-components.md`.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** Use `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf`.
- **No hand-built XML/CDATA.** Use `encoding/xml`.
- Use `descriptor.NewEnvelope(data)` for the standard envelope.

### Payload JSON Schema

`internal/descriptor.BuildPayloadSchema()` returns the deterministic JSON Schema (draft 2020-12) for every public payload — the request roots, result shapes and the `Envelope` (open `data` slot). Generated by reflection over `types`, registry-injected enums (`types.All*Types()` / `AllOverlayKinds()` / `AllRegressionTypes()`) and strict unions (`OverlayRef`, `OverlayPayload`); operator `params` stay open. Golden `descriptor/testdata/payload-schema.json`; `$id` version equals `format_version`; root `$comment` carries `feature_set_digest`. **`Pulse.PayloadSchema()` is the instance's view** (enums, slots and roots it hides are absent) and backs `pulse schema` (raw); `pulse://schema` stays full. Detail: `.claude/reference/feature-profiles.md` (Payload schema hiding). Full prose: `docs/src/contract/payload-schema.md`.

### Manifest payload

`internal/descriptor.BuildManifest()` returns the deterministic LLM-bootstrap blob — one fetch per session, client-cached, reachable as `pulse manifest --json` and `pulse_manifest`. Top level: `format_version`, `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim), `mcp_tools`, `cohort_types`, `skills`, `extensions`, `intents`, `feature_set_digest`, plus the capability blocks `Facet`, `Join`, `ProcessChain`, `Crosstab`, `Export`, `Import` (pointers, omitted when hidden) and `Overlays`. Sort-stable; golden at `descriptor/testdata/manifest.json`. Declarations live in `internal/descriptor/capabilities_*.go`; MCP tool metadata in `internal/mcp/toolmeta/meta.go`.

### Predict / Inspect contracts

**Contract: `.claude/reference/predict-inspect.md` — load it before changing predict, inspect, `pulse_inspect` or `CountRecords`.** The always-load half:

- **Predict structural ban:** `internal/descriptor/predict.go` MUST NOT import `internal/service/` or `internal/processing/`. Enforced by `TestPredictNoExecutionImports`. Reads only header + schema, never records.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via the engine's internal streamability gate (`TestPredict_Streamable_MatchesRuntime`). Beside it, `CrosstabFusable` (nil ⇔ no crosstab) + `CrosstabFusionReasons`: the fused-crosstab dispatch answer from the shared `internal/crosstabfuse` rule, instance-aware (`DisableCrosstabFusion`) — `TestPredict_CrosstabFusableMatchesRuntime`.
- **Inspect reads no record:** header + schema + sidecar metadata (`suggested_weight`); dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`; `RecordCount` derives from the file LENGTH. **`Pulse.Inspect` drops `env.Warnings`** — CLI and `pulse_inspect` read `Pulse.InspectEnvelope`.
- **CountRecords header-fast:** no payload decode; the single-file floor division exists ONCE (`encoding.Schema.RecordCountForPayload`, shared with `Inspect`). `Inspect` warns on a truncated tail, `CountRecords` floors SILENTLY — deliberately; never make it error.

### Execution modes (pointers)

**Contract: `.claude/reference/execution-modes.md` — load it before engine work.** CLAUDE.md keeps identity + the named skill only.

- **Streaming Process** (`pulse.ProcessStream`, `--stream`) — four orchestrator modes; the forced-buffered list is `skills/streaming-and-watching.md`.
- **Projected buffered decode** — default-on, output-transparent; `Options{DisableProjection}` / `--no-project`.
- **Parallel Compose** (`pulse.ComposeParallel`, `--parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}`; post-slot overlay fold at `internal/service/compose_overlay.go`. `skills/compose-requests.md`.
- **Parallel shards** (`Options.ShardWorkers`) / **parallel buffered Process** (`Options.DecodeWorkers`) — mergeable-only via the engine's internal `CanMergeRequest`, orthogonal to each other. `skills/cohort-schema-design.md`.
- **ProcessChain** (`pulse.ProcessChain`) — source-rooted linear chain, mergeable-only at v1, dual-slot overlays. `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins`) — v1 is exactly one inner join per Request. `skills/join-design.md`.
- **Crosstab / fused crosstab** (`Request.Crosstab`; fusion rule `crosstabfuse.Decide` in `internal/crosstabfuse`, shared by `CanFuseCrosstab` and predict) — composed row×column grid whose margins recompute from raw rows, plus an in-decode streaming arm. `skills/crosstab-guide.md`.
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); four FACET-host overlay kinds ride `FacetRequest.Overlays`. `skills/facet-design.md`.
- **Filter precompute** (grouped cohorts) — a filter over ONE group's members is evaluated once per dictionary entry, per-row otherwise. `execution-modes.md`.
- **Point lookup** (`pulse.Lookup`) — O(1) key-exact rows via a prebuilt sidecar index; single-file cohorts, equality-only, full-key. `skills/tool-lookup.md`.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates that **never mutate the base payload**. `skills/overlay-system.md`.

## Non-Skippable CI Gates

**The list is self-expanding.** `TestClaudeMdMentionsAllNonSkippableGates` scans every `*_test.go` for eight prefixes — `TestSkillsCover`, `TestClaudeMd`, `TestUpdateDemand`, `TestNoOrbit`, `TestGoldensNot`, `TestPredictNo`, `TestDescriptorNo`, `TestPerPackageCoverage` — and requires each match to be listed BY NAME below, including one added in a different package. Write the gate and its list entry together or the suite fails.

CLAUDE.md hygiene — `TestClaudeMdMentionsFormatVersion` (the literal `"1.1"` appears), `TestClaudeMdMentionsAllEnvVars` (every `PULSE_*` in Go source appears), `TestClaudeMdMentionsAllNonSkippableGates` (the prefix rule above), `TestClaudeMdMentionsComponentsContract` (`Response.Components` shape + universal floor + the `Response.Metadata` collision note), `TestClaudeMdSizeBudget` (**CLAUDE.md ≤ 50,000 bytes — displace prose into `.claude/reference/`, never raise the ceiling**), `TestUpdateDemandTableCovers` (every category word inside the Update Demand SECTION, not the whole file), `TestUpdateDemandTableCoversComponents` (the `Response.Components` / per-operator `ComponentSchema` / extension `ComponentSchema` rows).

Predecessor-reference hygiene — `TestNoOrbitPrefix` (no type constant), `TestNoOrbitPrefixes` (no error code) contains a predecessor reference.

Descriptor contracts — `TestPredictNoExecutionImports` (the Predict structural ban), `TestDescriptorNoFmtSprintf` (no `fmt.Sprintf` in `envelope.go`/`manifest.go`/`predict.go`/`inspect.go`), `TestGoldensNotHandEdited` (every golden ends with a valid `// golden-hash:` line), `TestPerPackageCoverageFloors` (package dirs exist; documents the coverage floors).

Skill coverage — each asserts an atomic skill file exists at the conventional stem: `TestSkillsCoverAllComponents` (aggregators / attributes / filterers / groupers / features → `op-<category>-<kebab>.md`), `TestSkillsCoverAllFieldTypes` (`type-*`), `TestSkillsCoverAllWindowTypes` (`op-win-*`), `TestSkillsCoverAllMCPTools` (`tool-*`, strip `pulse_`), `TestSkillsCoverAllSynthDistributions` (`op-synth-*`), `TestSkillsCoverAllRegressions` (`op-reg-*`), `TestSkillsCoverAllOverlayKinds` (`op-overlay-*`). Plus seven that check content rather than existence:
- `TestSkillsCoverAllPurposes` — built-in Purposes valid; a missing one fails unless exempted (`guided-analysis.md`).
- `TestSkillsCoverShardingTopics` — `skills/cohort-schema-design.md` carries a `Sharded` section.
- `TestSkillsCoverAllCliLeaves` — two-way: every runnable `buildApp()` leaf is named under `skills/` or `docs/src/` with a `docs/src/cli/flags.md` row, and every row is a mounted leaf (detail: `update-demand.md`).
- `TestSkillsCoverAllOperatorComponents` — each aggregator/grouper/filterer's `ComponentSchema` keys appear under a `## Components` section in its atomic skill.
- `TestSkillsCoverProfileGet` — per shipped feature profile, a pruned skill or example lists, searches and reads (facade + MCP) exactly like a never-existing name.
- `TestSkillsCoverFeatureFences` — an operator, feature-owned tool or `<kind>:<name>` mention in a skill body sits in a fence naming it, never in a `description`; malformed fences fail too (`skill-pack.md`).
- `TestSkillsCoverAllCrossReferences` — every skill stem named in CLAUDE.md, `.claude/reference/*.md` or the pack resolves, and a section-qualified `` `skills/<stem>.md` (Section) `` pointer names a heading the target really carries. Deliberate non-skill kebab tokens ride the allowlist in `skill_xref_test.go`.

Atomic-skill structure / budget / example-tag — `TestAtomicSkillHasRequiredSections` (the required `##` set per family), `TestSkillTokenBudget` (per-family body size; hard for `kind: design`, soft for atomic), `TestOperatorHasAtomicSkill` (every operator, MCP tool and field type has a file at its stem), `TestEveryOperatorHasAnExampleTag` (every operator name is tagged on at least one `internal/examples/<dir>/*.json`).

Other load-bearing contract gates are **not** prefix-matched (they are enforced by their own packages) and are listed in `.claude/reference/update-demand.md` (Other load-bearing contract gates) — the `TestManifest*Complete` family, `TestStreamability_*`, `TestExtensions_*`, `TestExamples_*`, `TestShardArchive*` and the per-mode suites.

## Build / Env

`make build` (default; injects `VERSION` from `git describe` via ldflags into `internal/buildinfo`, read by `pulse.Version()`), `test`, `fmt`, `vet`, `lint`, `cover`, `clean`, `dist` (6-platform archives + `checksums.txt` into `dist/`), `docs`, `docs-serve`, `docs-clean`. A `v*.*.*` tag push runs `release.yml`: `ci.yml` via `workflow_call`, then `make dist`, assets attached to the GitHub Release (`-` tags pre-release). A `.env` at repo root is auto-loaded. `make lint` = `go vet` + `staticcheck`, and must pass before any push.

**Environment variables** — one line each; `pulse.Options` always overrides:

- `PULSE_DATA_DIR` — base directory for `.pulse` cohorts, used by `fs.Default()`. The only required env var; bypass with `Options{DataDir}` or `Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdir under the fs root (default `imports`), honoured by `imports.Manager`; sidecar read settings (`source_tz`, `dst_policy`, …): `skills/session-bootstrap.md`.
- `PULSE_IMPORT_TTL` — default TTL for managed imports: Go duration (`24h`), day form (`7d`), or `pin`. Default `7d`.
- `PULSE_LABEL_TABLES_DIR` — directory whose `*.json` files auto-load as `LabelTables` at `pulse.New` time, keyed by filename. Detail: `skills/label-display.md`.
- `PULSE_RANGE_TABLES_DIR` — same shape for `RangeTables` (bare `{label,start,end}` array or a `{"description","ranges"}` wrapper; filename minus `.json` is the table name), validated through the shared range-compilation pass. A name declared both programmatically and on disk is a hard error.
- `PULSE_MCP_NO_COHORT_SCAN` — `pulse mcp` only (flag `--no-cohort-scan`): skip enumerating `.pulse` files as `pulse://` resources; the template stays, so cohorts stay readable. Library: `gosdk.Config.DisableCohortScan` / `mcpserve.Options.DisableCohortScan`.
- `PULSE_FEATURE_PROFILE` — `pulse mcp` / `mcpserve.NewPulse` only (flag `--feature-profile` wins): OS path to a feature profile.
- `PULSE_TEMPLATES_DIR` — request-template roots, `os.PathListSeparator`-separated in PATH-style precedence (first root wins; a same-named template under a later root is shadowed, not rejected). Unset with no `TemplateDirs` builds no store and lookups return `PULSE_TEMPLATE_NOT_FOUND`. **The hot-reload phase table — which malformed-file state hard-fails startup, which serves its last-good parse, which lists as broken — is the contract and lives in `.claude/reference/request-templating.md` (Hot-reload lifecycle).**

Both table directories skip Pulse's own sidecars by suffix yet hard-fail any OTHER unparseable `*.json`; `PULSE_TEMPLATES_DIR` does not skip: `.claude/reference/byte-layout.md` (Table-directory sidecar exclusion).

**Knobs.** Concurrency (`pulse.Options`, both default `0` ⇒ `NumCPU`, negatives rejected at `pulse.New()`, orthogonal to each other): `ShardWorkers` — per-shard pool for archives, explicit `1` forces serial; `DecodeWorkers` — per-segment pool for single-file cohorts above `parallelDecodeRecordThreshold` (100K records). Overlay knobs `DictPrefixFast` / `MaxPanelTargets`: `.claude/reference/execution-modes.md` (Overlays).

Hermetic testing: `internal/fs` `fs.NewMemMap()` returns a `Config` backed by `afero.NewMemMapFs()`. No disk I/O.

## Extension Points

`pulse.Options.Extensions` is the public surface for embedders injecting domain operators or expression-runtime extensions — eight operator categories plus expr functions and three named-table kinds, all registered at `pulse.New()` time and treated identically to built-ins by predict, manifest, MCP and runtime. **Operators are authored against the public `extend` package** (`extend.Record` / `extend.Rows`, one factory type per category plus optional streaming siblings); the engine under `internal/processing` is unreachable to embedders (`TestExtendImportBoundary`, `TestRootSurfaceNamesNoProcessing`). **Full recipe: `docs/src/internals/extension-points.md`; engine-side wiring: `.claude/reference/architecture.md` (Extension surface).**

- **Naming policy:** `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$`. Reserved namespaces `BUILTIN` / `STANDARD` / `CORE` / `PULSE`; a collision with a built-in is rejected.
- **Probe-validation (`PULSE_EXTENSION_*`), expr env + `LookupTables` / `LabelTables` / `RangeTables`, snapshot, `FieldInputs`, DECLARED `Streamable` / `Mergeable`, `DependsOn`, embedder skills / examples, `Purpose`:** `.claude/reference/architecture.md` (Extension surface + the relocated bullets at its end).

Surface: root `extensions*.go`; runtime overlay `internal/processing/extensions.go`.

## Feature profiles

A **feature profile** (never bare "profile" — synth owns it) is an instance's closed feature allowlist: operators bare, other kinds `<kind>:<name>`. `pulse.New` validates it (`PULSE_FEATURE_PROFILE_INVALID` → `_UNKNOWN` → `_DEPENDENCY`) and enforces it (U05): a hidden feature acts never-registered (operators, overlay kinds, tables, request slots), and the manifest, payload schema (`p.PayloadSchema()`, also served as MCP `pulse://schema`), error lists and runtime refusal prose show only the instance. Every payload carries `feature_set_digest` (`p.FeatureSetDigest()`; additive, `format_version` stays `"1.1"`); facade METHODS stay ungated. MCP mounts only the instance too; tooling is `pulse features {init,check,diff,show}`. `behaviour` switches OR into Options. `pulse.New` never reads `PULSE_FEATURE_PROFILE`, and no runtime skill mentions profiles. **Contract: `.claude/reference/feature-profiles.md`.**

## Guided analysis

Declared, never executed: a closed intent taxonomy (manifest `intents[]` = IDs only, plus per-entry `intents`), per-operator `Purpose`, per-output `Interpretation` (bands always name a `Convention`), a glossary (virtual `glossary` / `intents` skills, `pulse.Glossary()` / `pulse.Intents()`), and `details.effect_size.*` keys (omitted when undefined). **Prose is pulled, never pushed** — `TestManifestGuidanceBudget` bans it from default payloads and caps guidance per manifest entry. Coverage gates bind; gaps need owner-tagged exemptions. **Contract: `.claude/reference/guided-analysis.md`.**

## Request templating

Stored parameterised JSON that renders into a **validated typed request**. `internal/template/` is the whole implementation (import ceiling: stdlib + `types` + `errors` only — never `descriptor/`, `internal/processing/`, `internal/service/`; gated by `TestTemplatePackage_ImportBoundary`). Facade: `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`. **No CLI leaf, no MCP tool** — library/embedding surface only. **It is NOT expr-lang:** `$var` / `{{}}` / `$when` are request-authoring parameters substituted BEFORE decode, while `ATTR_FORMULA` / `FILTER_EXPRESSION` are expr-lang over row fields at execution time, and there is no interop by design.

**Contract: `.claude/reference/request-templating.md` — load it before changing the document model, the variable or target sets, or the substitution syntax.** It carries the file wrapper, the substitution forms, the variable types, directory precedence, the hot-reload phase table, the `PULSE_TEMPLATE_*` codes and why render never opens a cohort. Env var `PULSE_TEMPLATES_DIR`; skill `skills/request-templating.md`; docs `docs/src/library/request-templating.md`.

## Synthetic data

**Contract: `.claude/reference/synthetic-data.md` — you MUST load it before changing any `synth/` or `internal/synth/` code, configuration or public surface**: profile capture, `SpecFromProfile` translation, generation, structural rules, the `--suggest-rules` detectors, `--run-continuation` (the run-skip hit rate, measured on the profile scan — never in header-only `inspect`), the fidelity report. **Every failure it records was SILENT** — the run succeeded and a number was quietly wrong. It covers conditional pair capture; per-numeric linear models (selection, shrinkage, the composed draw, shape-fitted conditioning); float-fusion determinism; `bernoulli` / `discrete` marginals; correlation and residual-correlation capture, draw and recovery; the warning summary; and `Spec.Rules`.

`format_version` does NOT move for a synth change — `synth.Profile` / `synth.Spec` live in `synth/`, not `types/`, so they are unreachable from `internal/descriptor.BuildPayloadSchema`. Skills: `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md`.

## Skill Pack

The pack under `internal/skills/` (addressed pack-relative as `skills/<stem>.md`) is the LLM surface, embedded via `//go:embed *.md`. Two shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic, `kind: design`).

**Contract: `.claude/reference/skill-pack.md` — load it before adding or restructuring a skill file, changing frontmatter keys, changing a family's required `##` section set, or moving a budget.** It carries the frontmatter blocks, the required-section table per family, the budget table, the per-trigger stem table and the add-a-skill procedure. The always-load half:

- **The stem encodes the surface**, and frontmatter `name:` MUST equal the file stem: `op-<category>-<kebab>.md` per registered operator constant, `tool-<kebab>.md` per MCP tool (strip `pulse_`), `type-<kebab>.md` per `FieldType`.
- **Each family has a required `##` section set** (`TestAtomicSkillHasRequiredSections`, keyed off the `category:` frontmatter field) and a body budget (`TestSkillTokenBudget`: `op-*` ≤1200 chars, `tool-*`/`type-*` ≤2000, `kind: design` ≤6000 (hard), at `chars / 4 ≈ tokens`). Both tables are in the reference file.
- **There is no `skills/index.json`.** `skills.List()` walks the embedded `embed.FS` and parses frontmatter — the filesystem IS the manifest (plus the virtual `glossary` / `intents` skills rendered from Go registries), so a new file with valid frontmatter is picked up automatically. Never create or bump an index.
- **Never hardcode a registered count** in docs. Counts come from `pulse_manifest`; the coverage gates reject drift.
- Cross-cutting topics that are not operator-keyed route to a topical skill: `Response.Components` → `skills/response-components.md`; request slot map / smart defaults → `request-envelope`; streaming / `Watch` / request hashing → `streaming-and-watching`; error-code prose → `errors/fixup_metadata.go` via `pulse_errors_lookup`; extensions → `docs/src/internals/extension-points.md`.

## What NOT to Do

- **Do not import `internal/service/` or `internal/processing/` from `internal/descriptor/`.** Predict/inspect/manifest are no-execute; `TestPredictNoExecutionImports` fails.
- **Do not hand-edit golden files.** Regenerate: `go test ./descriptor/ -run 'Test.*Golden' -update`.
- **Do not add implementation without tests in the same PR.** TDD is a hard rule here.
- **Do not use `fmt.Sprintf` for JSON/XML.** Use `encoding/json` + `descriptor.NewEnvelope(data)`.
- **Do not defer a skill or CLAUDE.md update.** The follow-up PR will not happen and the next session reads stale guidance.
- **Do not raise `claudeMdSizeCeiling` to make CLAUDE.md fit.** Move the long form into `.claude/reference/` and leave the always-load half plus a pointer — that is the whole point of the gate.
- **Do not add a component without updating the registry** (`internal/processing/registry.go`) + `types.All*Types()`.
- **Do not bypass `afero.Fs`** — it defeats `fs.NewMemMap()` and the custom-storage extension hook.
- **Do not put business logic in `cmd/pulse/`.** The CLI parses flags, constructs library objects, calls methods, formats output.
- **Do not bypass overlay typing via direct payload mutation.** Overlays are read-only siblings keyed to host coordinates — never mutate `Response.Data` / `Response.Crosstab.Matrix` in a handler; the fold writes `Response.Overlays[i]` only.

## Reference Docs

`.claude/reference/*.md` is where CLAUDE.md's long form lives. **This is the complete index.** CLAUDE.md carries only the always-load half of each contract, so a change to one of these surfaces is not adequately informed by CLAUDE.md alone — load the named file BEFORE the work, not after. Most are also named as required companions by the Update Demand table, which makes loading them binding rather than advisory.

| File | Holds | Load before... |
|---|---|---|
| `architecture.md` | the full package tree (public vs `internal/`), split-in-place vs alias-facade narrowing, root aliases, the `io` import boundary, the surface guards, the MCP layer split, the extension surface (`extend` adapters, snapshot, streamability rule, limits) | moving a package, adding a public package or exported symbol, or touching an import-boundary gate |
| `update-demand.md` | the exhaustive per-slot trigger table + the non-prefix-matched gate list | touching ANY contract; add a row here when introducing a new Request slot, Response slot, capability block or execution-mode wiring |
| `byte-layout.md` | the sidecar index and its manifest, the SPSS metadata sidecar, derived columns, SPSS export, target-aware export predict, projected decode | changing the `.pulse` format, a field type, shard-archive layout, any sidecar file, an SPSS surface, or the projection contract |
| `execution-modes.md` | full wiring prose per mode, including every overlay host | engine work (`internal/service/`, `internal/processing/`) or adding an execution mode |
| `response-components.md` | the `crosstab.margin_aggregations` auxiliary figures (ADMISSION rule, `present` semantics, display-flag gate, allocation/emission gap), the opt-out, the Compose surface | changing `Response.Components`, a per-operator `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either arm |
| `predict-inspect.md` | inspect's envelope-vs-result split, the truncated-tail warning, `CountRecords`' deliberate silence, the predict import ban | changing `internal/descriptor/predict.go` or `inspect.go`, `Pulse.Inspect` / `InspectEnvelope`, `pulse_inspect`, or `CountRecords` |
| `request-templating.md` | the file wrapper, the three substitution forms, the nine variable types, directory precedence, the hot-reload phase table, the nine `PULSE_TEMPLATE_*` codes | changing the request-template document model, the variable or target sets, or the substitution syntax |
| `synthetic-data.md` | the whole `synth/` contract — capture, models, marginals, correlation and residual recovery, structural rules, the detectors, the fidelity report | ANY `synth/` change. Every failure it records was SILENT: the run succeeded and a number was quietly wrong |
| `feature-profiles.md` | feature kinds + spelling, the feature table, `Since` + version compare, dependencies, the profile model, codes + order, the env-var reader, U05/U06 notes | adding any feature, or touching feature-profile code |
| `skill-pack.md` | frontmatter blocks, the required `##` set per family, the budget table, the per-trigger stem table, the add-a-skill procedure | adding or restructuring a skill file, changing frontmatter keys, changing a family's required sections, or moving a budget |
| `weighting.md` | the weight surface (Request / per-slot / Options; null ≠ absent), resolution order, validation, the aggregator weight classes, refusals, floor keys, the op-order exactness rule, the extension and SPSS hooks, the feature gate | touching ANY weight surface |
| `guided-analysis.md` | intents, `Purpose`, `Interpretation` paths + probes, glossary + jargon rule, virtual skills, effect-size keys, the two-tier + budget gates, the extension hook | touching any guidance registry, an effect-size key, an overlay `Inferential` flag or the extension `Purpose` hook |

`TestClaudeMdSizeBudget` caps CLAUDE.md at 50,000 bytes so a new contract DISPLACES long form into that directory; never raise the ceiling. `.claude/reference/*.md` is inside `TestSkillsCoverAllCrossReferences`' corpus — a heading renamed in moved text still breaks the pointer that names it.
