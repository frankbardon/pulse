# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as Go library (`github.com/frankbardon/pulse`) and CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` facade (`New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `InspectEnvelope`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`, `ProcessStream`, `ProcessChain`, `CountRecords`, `ComposeParallel`, `Lookup`, `BuildIndex`, `VerifyIndex`, `ListIndexes`, `DropIndex`, `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`) is the public API. CLI never contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header. `descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `skills/` embeds an atomic-per-surface skill pack (`op-*` / `tool-*` / `type-*`) plus ~20 topical design skills via `//go:embed *.md`. LLM agents call `skills.List()` / `skills.Get(name)` for domain guidance; the filesystem walk + frontmatter parse is the source of truth.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators (AGG/ATTR/FILTER/GROUP/WIN/FEAT/TEST/SYNTH) + expr functions + lookup tables. First-class — predict, manifest, MCP, runtime treat identically to built-ins. See `docs/src/internals/extension-points.md`.
- **Nexus relationship.** Pulse standalone. Nexus discovers via `pulse manifest --json` + loads embedded skills. No reverse dependency.

For recipes (adding operators, I/O formats, MCP tools, error codes, field types; porting; debugging predict; regenerating goldens; wiring MCP client) read the mdBook Internals chapter under `docs/src/internals/` (adding-aggregator.md, adding-attribute.md, adding-filterer.md, adding-grouper.md, adding-window.md, adding-feature.md, adding-test.md, adding-synth-distribution.md, adding-mcp-tool.md, adding-io-format.md, adding-field-type.md, adding-error-code.md, adding-chain-predicate.md, adding-facet-capability.md, regenerating-goldens.md, debugging-predict.md, wiring-mcp-client.md, extension-points.md). Long-form reference docs live under `.claude/reference/` — see "Reference Docs" at the bottom.

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

One row per category: trigger → companions → gates. **The exhaustive table is `.claude/reference/update-demand.md` — load it before touching any contract below.** ATOMIC = `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`.

| If you change... (category) | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator, attribute, filterer, grouper, feature operator, window operator, statistical test (`TEST_*`), regression (`REG_*`), synth distribution or overlay kind (`OVERLAY_*`) | `skills/op-<category>-<kebab>.md` + the matching `descriptor/capabilities_*.go` + an `examples/` `_meta.operators` tag | ATOMIC, the category's `TestSkillsCoverAll*` + `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag` |
| A registered MCP tool (add/remove) | `skills/tool-<kebab>.md` (strip `pulse_`) + `mcp/toolmeta/meta.go` | ATOMIC, `TestSkillsCoverAllMCPTools` |
| A registered field type, a `.pulse` format or shard-archive change, the sidecar index, or projected decode | **`.claude/reference/byte-layout.md` — load it BEFORE the change** + `skills/type-<kebab>.md` + `skills/cohort-schema-design.md` + CLAUDE.md "Byte-layout invariants" | ATOMIC, `TestSkillsCoverAllFieldTypes`, `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + ≥1 Fixup | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A `--json` envelope, `format_version` (currently `"1.1"`), or any payload-reachable `types` slot | CLAUDE.md "Output Format Contract" + `docs/src/contract/payload-schema.md` + regenerate `descriptor/testdata/payload-schema.json` | `TestPayloadSchemaGolden`, `TestClaudeMdMentionsFormatVersion` |
| `ComposedResponse` shape, the Compose facade return type, the `compose --json` `data` wrapping, or `OverlayLayer.Warnings` | CLAUDE.md "Output Format Contract" (Compose envelope block) + `skills/overlay-system.md` (Per-layer warnings section) + the reference rows' sites | `TestComposedResponse_OverlayFreeByteIdentical` |
| A registered I/O format (`io/<fmt>/` adapter) | the eleven registry / CLI / capability sites in the reference row + `skills/tool-import.md` + `docs/src/internals/adding-io-format.md` | `TestFromExt_Matrix`, `TestManifestImportCapability` |
| A CLI leaf (add/remove) | `docs/src/cli/flags.md` command index + `skills/session-bootstrap.md` for an agent-relevant flag | `TestSkillsCoverAllCliLeaves` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| An environment variable | CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| `Response.Components` shape, a per-operator `ComponentSchema`, or an Extension registration's `ComponentSchema` | **`.claude/reference/response-components.md` — load it BEFORE the change** + CLAUDE.md "Output Format Contract" + `skills/response-components.md` + the operator's atomic skill + `descriptor/capabilities_*.go` + `docs/src/internals/extension-points.md` | `TestClaudeMdMentionsComponentsContract`, `TestExtensions_ComponentSchemaParity` |
| `CrosstabSpec.MarginAggregations` — the auxiliary margin-only surface, either arm's accumulation, or its `Response.Components.Crosstab` figures | **`.claude/reference/response-components.md` — load it BEFORE the change** + the fifteen sites in the reference row + `skills/crosstab-guide.md`. **A record reaches an auxiliary only if it reached a CELL — state that rule wherever the figures appear** | `TestCrosstab_BufferedAuxMarginMatchesFused` |
| The request-template document model, variable/target sets, or `$var` / `{{}}` / `$when` | **`.claude/reference/request-templating.md` — load it BEFORE the change** + `skills/request-templating.md` + `docs/src/library/request-templating.md` + CLAUDE.md "Request templating" | `TestTemplatePackage_ImportBoundary` |
| Any `synth/` surface — capture, `SpecFromProfile`, `Spec`, generation, structural rules, `--suggest-rules`, fidelity report | **`.claude/reference/synthetic-data.md` — load it BEFORE the change** + `skills/synthetic-data.md` + `skills/synth-models.md` + `skills/synth-structural-rules.md` | `TestSkillsCoverAllSynthDistributions` |
| Any Request slot, Response slot, capability block, or Execution-mode wiring | the per-slot row in `.claude/reference/update-demand.md` | per-slot suites cited there |

Between them the rows carry every word `TestUpdateDemandTableCovers` checks; keep it that way when editing one.

Defer the doc/skill update to "a follow-up PR" and the follow-up will not happen. Update in the same PR or do not merge.

## Architecture

```
pulse/
├── cmd/pulse/              # CLI binary — only binary
├── pulse.go                # Public facade
├── service/                # Orchestration: wires processing to encoding
├── processing/             # Aggregators, attributes, filterers, groupers
│   ├── window/             # WIN_* operators
│   └── feature/            # FEAT_* pre-filter feature engineers
├── encoding/               # .pulse binary codec (incl. archive + cohesion)
├── io/                     # Tabular ↔ .pulse adapters (csv|tsv|ndjson|jsonarray|jsonshared|arrow|parquet|excel|spss)
├── fs/                     # afero-based filesystem abstraction
├── errors/                 # Typed error codes (CodedError system)
├── types/                  # Request/response structs + streamability table
├── descriptor/             # manifest, predict, inspect, envelope (no-execute)
├── skills/                 # //go:embed markdown skill pack
├── examples/               # //go:embed runnable request examples
├── synth/                  # Synthetic data generator
├── mcp/                    # SDK-free MCP core (typed In/Out, reflected schemas, handlers, bind)
│   ├── gosdk/              # go-sdk adapter — the ONLY package importing the MCP SDK
│   └── toolmeta/           # Leaf tool name+description metadata (descriptor + core import it)
├── docs/                   # mdBook source (GitHub Pages)
└── internal/cli/           # CLI internals
```

`pulse.go` re-exports `types.Request` → `pulse.Request`, `types.Response` → `pulse.Response`, `types.ComposedRequest` → `pulse.ComposedRequest`, plus `synth.Spec`/`Result`/`Options`/`Profile`/`ProfileOptions`.

CLI commands map 1:1 to manifest commands: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `schema`, `mcp`, plus `synth from-schema`, `synth from-profile`, `profile create`, `shard {create,add,remove,list,compact,verify,extract}`, `index {build,list,verify,drop}`, `api {process,compose,facet,process-chain,lookup}`. `pulse schema` prints the payload JSON Schema raw (built by `descriptor.BuildPayloadSchema`); not envelope-wrapped.

The MCP layer is split: `mcp/` is the SDK-free core (typed In/Out structs, reflected JSON schemas, typed handlers over `*pulse.Pulse`, `ToolDescriptor`, `Tools(cfg)`, strict-decode, bind classification — imports no MCP SDK, gated by `TestMCPCore_NoSDKImport`); `mcp/gosdk/` is the only package importing `github.com/modelcontextprotocol/go-sdk`, and its `Register(server, p, cfg)` mounts the core catalog onto a caller-supplied server; `mcp/toolmeta/` is the leaf name+description metadata imported by both `descriptor` and the core. The former internal MCP server tree has been removed — `pulse mcp` builds a bare go-sdk server and calls `gosdk.Register`. The registry registers one tool per facade method plus skills/examples/errors/import/label tools (the manifest is the source-of-truth count; do not hardcode it) and two resource schemes (`pulse://`, `pulse-skill://`). The reserved static resource `pulse://schema` serves the payload JSON Schema (`descriptor.BuildPayloadSchema`) — a resource, not a tool, so the tool surface is unchanged. MCP tool I/O is the structured typed shape: payload tools take the structured request at top level, outputs are typed-wrapped, coded errors surface as `{code, message, details}`.

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

1. **9-byte header:** 8-byte magic `PULSE\x00\x00\x00` + 1-byte format version `0x01`. `encoding.MagicBytes`, `encoding.FormatVersion`, `encoding.HeaderSize = 9`.
2. **Schema block:** field descriptors (name, type byte, **nullable flag byte**, byte offset, bit position, optional description). Nullable flag immediately after type byte; `1` = participates in null bitmap.
3. **Dictionary blocks:** inline after schema for `categorical_u8/u16/u32`.
4. **Record data:** fixed-width rows; size derived from schema.
5. **Per-record null bitmap (optional).** When schema has any nullable field (`Schema.HasBitmap()`), every record carries trailing bitmap of `ceil(field_count / 8)` bytes. Field index `i` → byte `i/8`, bit `i%8` (LSB-first); `1` = null. Absent when no nullable fields. Helpers: `encoding.ReadBitmap`, `encoding.WriteBitmap`, `encoding.BitmapIsNull`, `encoding.BitmapSetNull`, `Schema.BitmapByteSize()`.

18 field types (full table in `skills/cohort-schema-design.md`): `u4`, `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `set_u8`/`u16`/`u32`/`u64`. Bit-packed (`u4`, `packed_bool`) return `ByteSize() == 0` (share bytes with neighbours via `FieldType.IsBitPacked()`). `categorical_*` and `set_*` both carry an inline dictionary block (`FieldType.HasDictionary()`); for `set_*` the on-wire value is a fixed-width bitmask where bit `i` ↔ dictionary entry `i` (up to 8/16/32/64 entries) and an empty mask is a valid "no selection" — distinct from null. `date` is epoch DAYS as a 4-byte `uint32`; `datetime` (type byte `17`) is epoch SECONDS as an 8-byte `uint64` — the two are never interchangeable, and swapping them rescales every value by 86,400. `datetime` is naive UTC (an offset-bearing literal normalises to the same instant, the offset itself is discarded), second-resolution (sub-second input truncates toward the epoch), carries no dictionary, and is not bit-packed; text form round-trips through `encoding.ParseDateTime` / `FormatDateTime` / `CanonicalDateTimeLayout` (`encoding/datetime.go`). Nullability orthogonal to type. Unknown type byte → `ENCODING_INVALID`. Decimal128 and set_* nulls via bitmap only — no in-band sentinel.

**Shard archive variant.** `.pulse` path resolves to either single-file layout above or **shard archive** — uncompressed Zip64 (Method 0, store-only) whose first four bytes are zip magic `PK\x03\x04` instead of `PULSE` magic. Single-file byte format **unchanged**; magic-byte dispatch at `pulse.Open` selects shape. Shard archive carries reserved `_schema.pulse` entry (header-only canonical schema + SHRD trailer with `aggregate_record_count` + `shard_count`) plus N standalone shard payloads. Per-shard cohesion: structural strict (byte-equal at insert), descriptions tolerant. Categorical dictionaries grow under union-merge semantics; divergent incoming shards byte-rewritten with remapped indices. Width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. Stricter prefix-only validator (`PULSE_SHARD_DICT_DIVERGENCE`) retained for `pulse shard verify`. Anchor syntax `archive.pulse#shard.pulse` opens one shard as one-shard cohort. Caller-owned concurrency. Full detail in `skills/cohort-schema-design.md` (Sharded cohorts).

**Contract: `.claude/reference/byte-layout.md` (relocated verbatim) — load it before touching any of these.** One pointer each:

- **Sidecar point-lookup index** — `cohort.pulse.<keyhash>.idx`, v3, three-outcome staleness → `.claude/reference/byte-layout.md`.
- **Sidecar index MANIFEST** — `cohort.pulse.indexes.json`, discoverability → `.claude/reference/byte-layout.md`.
- **SPSS metadata sidecar** — `cohort.pulse.spss.json`, the `code ↔ label ↔ ID` triple → `.claude/reference/byte-layout.md`.
- **SPSS derived columns** — an import can widen the cohort → `.claude/reference/byte-layout.md`.
- **SPSS export** — `pulse export spss`, a `pio.CohortWriter` → `.claude/reference/byte-layout.md`.
- **Export predict is target-aware** — `io.CohortValidator` → `.claude/reference/byte-layout.md`.
- **Projected buffered decode** — default-on; `--no-project` → `.claude/reference/byte-layout.md`.

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

**Date-family field types.** `GROUP_DATE`, `GROUP_DATE_RANGES` and `FILTER_DATE_RANGES` accept BOTH temporal field types — `date` (epoch days, `uint32`) and `datetime` (epoch seconds, `uint64`). Everything downstream of the operator boundary (labeled-range matching, calendar-component bucketing, ISO period boundaries) speaks epoch DAYS only, so a `datetime` column is day-truncated exactly once at that boundary by `encoding.DateTimeToDay` — truncation toward the past, never rounding, naive UTC (`2024-03-04T23:59:59Z` buckets as `2024-03-04`; `1969-12-31T23:59:59Z` is day −1, not day 0). The single adapter is `processing/date_field.go` (`resolveDateFieldSeconds` classifies the column once at construction, `epochDayFromValue` applies it per record) — no call site open-codes a `/ 86400`. `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` reject any other field type with `PROCESSING_CONFIG`; `GROUP_DATE` keeps its historical non-validating posture and reads a non-temporal `Field` as an epoch-day count.

`GROUP_DATE_RANGES` is an explicit-only date-family grouper (never a smart default): it buckets each row by a labeled `{label, start, end}` date-range set (E1-S1 shared model, `processing.CompileDateRanges`) and emits the matching range's label as the bucket key; out-of-range rows land in a configurable unmatched bucket (default label `"unmatched"`). Streamable + mergeable. Range source is exactly one of inline `ranges` (author-order array) XOR a named `table:` referencing a registered `RangeTable` (`Options.Extensions.RangeTables` / `PULSE_RANGE_TABLES_DIR`); both present or neither → `PULSE_RANGE_SOURCE_AMBIGUOUS`, unknown table name → `PULSE_RANGE_TABLE_UNKNOWN`, a field that is neither `date` nor `datetime` → `PROCESSING_CONFIG`. Because a grouper factory cannot reach the `ExtensionRegistry` at construction (signature `(grp, schema)`), a named `table:` is resolved lazily via the `processing.ExtensionAware` `SetExtensions` hook (threaded through every grouper construction site by `processing.ApplyGrouperExtensions`); the inline source resolves eagerly at construction.

`FILTER_DATE_RANGES` keeps rows whose date-family `Field` day-integer falls inside any range of the same E1-S1 labeled-range set (`processing.CompileDateRanges`); every out-of-range row and every null/missing date is dropped. The range `label` is irrelevant to keep/drop but the range set is still fully validated (`PULSE_RANGE_OVERLAP` / `_DUPLICATE_LABEL` / `_INVALID`); a field that is neither `date` nor `datetime` → `PROCESSING_CONFIG`. Range source is exactly one of inline `ranges` XOR a named `table:` (registered `RangeTable`, resolved via the filterer's `ExtensionAware` hook — filterers already receive `SetExtensions`); both present or neither → `PULSE_RANGE_SOURCE_AMBIGUOUS`, unknown table → `PULSE_RANGE_TABLE_UNKNOWN`. Row-local streamable, so it is auto-available to `process`, `sample`, and `facet` (via `FacetRequest.Filterers`) single-pass. Structured ranges cannot ride `Values []string`, so `types.Filterer` carries an additive `Params json.RawMessage` slot (`{"ranges":[{label,start,end}]}` or `{"table":"<name>"}`) — payload-reachable, additive `omitempty`, `format_version` stays `"1.1"`.

## Output Format Contract

### `--json` envelope

All `--json` CLI output + descriptor operations use `descriptor.Envelope`:

```json
{
  "format_version": "1.1",
  "data": { ... },
  "request": { ... },
  "errors": [],
  "warnings": []
}
```

- `format_version` always `"1.1"`. Bumped from `"1.0"` for the Compose facade lift: `pulse.Compose` / `pulse.ComposeParallel` now return `*ComposedResponse` and `pulse api compose --json` wraps that object on `data` (see Compose-specific envelope below). Future backward-incompatible shape changes MUST update this section.
- `errors` / `warnings` use `{"code", "message", "details"}`. Empty array (never null) when absent. A FATAL error that is a `*errors.CodedError` carries its own code and details — the `import` / `convert` / `export` leaves route through `writeCodedErrorEnvelope`, which unwraps with `errors.As` and falls back to the leaf's placeholder (`IMPORT_ERROR`, `CONVERT_ERROR`, …) only for an uncoded error. Stringifying a coded error into the placeholder makes `errors[0].code` unusable with `pulse errors lookup`.
- `request` is opt-in echo of the *normalized* request. Omitted unless `pulse.Options.EchoRequest` is true or CLI flag `--echo-request`. Shape varies: `Request` for process/predict, `ComposedRequest` for compose, `ChainRequest` for process-chain, `FacetRequest` for facet, `SampleRequest` for sample. Streaming output skips the echo. Use `descriptor.NewEnvelopeWithRequest(data, req)` or `env.WithRequest(req)` to populate.

Additive-only: bump `format_version` only on backward-incompatible shape changes. New `data` fields don't bump; renames/removals do. The `request` field is additive (omitempty) and does NOT bump `format_version`.

**Compose envelope (`pulse api compose --json`).** Since the v1.1 lift `data` is a `ComposedResponse` object — not the legacy `[]*Response` array:

```json
{
  "format_version": "1.1",
  "data": {
    "responses": [ /* one Response per ComposedRequest.Requests slot, in input order */ ],
    "overlays":  [ /* one OverlayLayer per ComposedRequest.Overlays spec; omitted when no Compose overlays */ ]
  },
  "errors": [],
  "warnings": []
}
```

Streaming (`--stream`) bypasses the envelope and emits per-row `{"index", "row"}` NDJSON; Compose overlays surface only at terminal flush in non-streaming mode (see `skills/streaming-and-watching.md`).

### Response.Components

Every `Response` carries an optional `Components *ResponseComponents` (additive `omitempty`; `format_version` stays `"1.1"`). Mirrors the request shape:

- `Aggregations []AggregationComponents` — one entry per aggregator slot; universal floor `{n, n_null}` + operator-specific `Operator map[string]any` keyed by the manifest schema.
- `Groupers []GrouperComponents` — universal floor `{total_n, n_null}` + operator-specific bucket layout.
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, row/column/grand-total margin counterparts, axis-key components. Mirrors `MatrixPayload` coordinate-for-coordinate. Auxiliary `crosstab.margin_aggregations` margin figures extend this bullet — reference below.
- `Filterers []FiltererComponents` — uniform `{n_in, n_out, n_null_input}` across all 11 filterers.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. Coexists with `Response.Metadata`: `Metadata` keeps non-numerical run facts (cohort filename); `Run` carries the typed counters.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers}`. Mergeability axis per operator: `Mergeable` / `Partial` / `None` (`types.ComponentsMergeability`). Streaming chunks emit running state for mergeable; non-mergeable surface only on terminal flush.

**Opt-out.** `pulse.Options.DisableComponents bool` (engine-level default) and `types.Request.DisableComponents *bool` (per-request override, `nil` inherits engine); CLI `--no-components` on `pulse api process` / `process-chain` / `compose`. When disabled, `Response.Components` stays `nil` and the wire form is byte-identical to the pre-Components baseline — `format_version` is NOT bumped.

**Long form: `.claude/reference/response-components.md`** — the auxiliary margin figures (ADMISSION rule, `present` semantics, display-flag gate, allocation/emission gap), the full opt-out contract, and the Compose per-slot / per-layer surface.

Full contract: `skills/response-components.md`.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** Use `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf`.
- **No hand-built XML/CDATA.** Use `encoding/xml`.
- Use `descriptor.NewEnvelope(data)` for the standard envelope.

### Payload JSON Schema

`descriptor.BuildPayloadSchema()` returns the formal, deterministic JSON Schema (draft 2020-12) for every public payload — the request envelopes (`#/$defs/Request`, `ComposedRequest`, `ChainRequest`, `FacetRequest`, `SampleRequest`), the result shapes (`Response`, `ComposedResponse`, `ChainResponse`, `FacetResult`), and the universal output `Envelope` (its `data` slot is intentionally open — it wraps any operation). Generated three ways so it cannot drift: reflection over the `types` structs, registry-injected enums (the operator/overlay-kind/regression discriminants pull from `types.All*Types()` / `AllOverlayKinds()` / `AllRegressionTypes()`), and hand-tuned strict unions (`OverlayRef` at-most-one-arm via `maxProperties`, `OverlayPayload` shape-discriminated via `allOf`/`if`-`then`). v1 boundaries: operator `params` (`json.RawMessage`) stay an open object (no central per-operator input-param source) and the small closed mode enums (`OverlayScope`/`OverlayShape`/`CrosstabNormalize`/…) stay plain strings (no registry helper). Golden at `descriptor/testdata/payload-schema.json`; `$id` version held equal to the envelope `format_version`. Reachable three ways: `pulse schema` (raw, not envelope-wrapped), the `pulse://schema` MCP resource, and the published file at `https://frankbardon.github.io/pulse/payload-schema.json` (copied into `docs/book/` by the docs workflow, hash line stripped). Full prose: `docs/src/contract/payload-schema.md`.

### Manifest payload

`descriptor.BuildManifest()` returns deterministic LLM-bootstrap blob — one fetch per session, client-cached. Reachable via `pulse manifest --json` and `pulse_manifest`. Top-level: `format_version`, `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim), `mcp_tools`, `cohort_types`, `skills`, `extensions`, plus capability blocks `Facet`, `Join`, `ProcessChain`, `Crosstab`, `Export`, `Import`, `Overlays`. Sort-stable; golden-checked at `descriptor/testdata/manifest.json`. Capability declarations: `descriptor/capabilities_*.go`. MCP tool metadata: `mcp/toolmeta/meta.go`.

### Predict / Inspect contracts

- **Predict structural ban:** `descriptor/predict.go` MUST NOT import `service/` or `processing/`. Enforced by `TestPredictNoExecutionImports`. Reads only header + schema, never records.
- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`. Dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`. `InspectResult.RecordCount` is derived from the file LENGTH, never by reading a record — the cumulative per-shard sum for an archive, that shard's own count for an `archive.pulse#shard.pulse` anchor, and `payload_bytes / record_stride` for a single file. `Pulse.Inspect` returns the result only; `Pulse.InspectEnvelope(ctx, path, *descriptor.InspectOptions)` returns the ENVELOPE, and is the only way to reach the `FullDict` knob or the truncated-tail warning — `Inspect` drops `env.Warnings` on the floor. **Every `pulse cohort inspect` mode reads through the facade**: the `--json` / `--full-dict` branch used to `os.ReadFile` the raw argument itself, which bypassed the injected `afero.Fs` and silently lost anchor resolution (an anchor that inspected fine as text returned `data:null`). A leaf needing options or warnings takes the envelope sibling, never its own read. **`pulse_inspect` carries the warnings too.** `mcp.InspectOut` EMBEDS `descriptor.InspectResult` and adds an `omitempty` `warnings []*descriptor.EnvelopeEntry` slot; `HandleInspect` reads `InspectEnvelope`, never `Inspect`, for the same reason the CLI does — a floored `record_count` is indistinguishable on the wire from an honest one, so a tool routed through the result-only wrapper left an agent unable to see a truncated tail at all. Embedding keeps every pre-existing key at the top level and a clean read emits no `warnings` key, so the tool's wire form is byte-identical for the ordinary case; the entries are coded `{code, message, details}` (not strings) so the code is usable with `pulse_errors_lookup` and `details` carries `record_stride` + `trailing_bytes`. `format_version` does NOT move — `InspectResult` is not payload-reachable and `payload-schema.json` is untouched; only the manifest golden moves, and only for `DescInspect`.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via `processing.CanStreamRequest(req, schema)`.
- **CountRecords header-fast:** `pulse.CountRecords(ctx, path) (uint64, error)` returns record total without decoding payload. Single-file: `(size − header − schema) / record_stride`. Shard archive: zip central directory + `_schema.pulse` SHRD trailer `AggregateRecordCount`. Anchor: named shard's count. The single-file floor division exists ONCE, at `encoding.Schema.RecordCountForPayload`, which `descriptor.Inspect` calls too — identical arithmetic written twice is one edit away from two different counts over the same bytes, and nothing on either wire says which arm produced the number. **The arms differ in OBSERVABILITY and only there, deliberately:** `Inspect` has an envelope and raises the `ENCODING_INVALID` truncated-tail warning naming the leftover bytes; `CountRecords` has no warning channel and floors SILENTLY, because it feeds the parallel-decode eligibility gate as well as the facade and a half-written trailing record must not stop a cohort that still processes from reporting its whole-record count. Do not close that gap by making `CountRecords` error.

### Execution modes (pointers)

Full prose per mode: `.claude/reference/execution-modes.md` — load it before engine work. CLAUDE.md keeps gate-relevant pointers only.

- **Streaming Process** (`pulse.ProcessStream`, `pulse api process --stream`) — four orchestrator modes; forced-buffered list at `skills/streaming-and-watching.md`.
- **Projected buffered decode** — default-on, output-transparent; opt out via `pulse.Options{DisableProjection: true}` or `--no-project`. See `.claude/reference/byte-layout.md`.
- **Parallel Compose** (`pulse.ComposeParallel`, `--parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}`; post-slot fold at `service/compose_overlay.go`. See `skills/compose-requests.md`.
- **Parallel shards** (`pulse.Options.ShardWorkers`) — per-shard pool inside `Process`; mergeable-only via `processing.CanMergeRequest`. See `skills/cohort-schema-design.md`.
- **Parallel buffered Process** (`pulse.Options.DecodeWorkers`) — per-segment pool over single-file cohorts above `parallelDecodeRecordThreshold = 100_000`.
- **ProcessChain** (`pulse.ProcessChain`, `pulse api process-chain`) — source-rooted linear chain; mergeable-only at v1; dual-slot overlays. See `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins []*JoinSpec`) — v1 = exactly one inner join per Request. See `skills/join-design.md`.
- **Crosstab** (`Request.Crosstab`, `Response.Crosstab`) — composed row×column grid; margins recompute from raw rows. `MarginReducibility` classes + the `margin_aggregations` cell-scoped admission rule both arms must agree on: reference (Crosstab). See `skills/crosstab-guide.md`.
- **Fused crosstab** (`processing.CanFuseCrosstab`) — in-decode streaming alternative; **quote peak heap, never `B/op`**. Gate composition, fan-out margin non-additivity, post-`Finalize` overlay fold: reference (Fused crosstab). See `skills/crosstab-guide.md` (Fused mergeable path).
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); four FACET-host overlay kinds ride `FacetRequest.Overlays`. See `skills/facet-design.md`.
- **Point lookup** (`pulse.Lookup`, `pulse api lookup`) — O(1) key-exact rows via a prebuilt sidecar index; single-file cohorts, equality-only, full-key. See `skills/tool-lookup.md`.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates; never mutate the base payload. Level / Within, SERIES / FACET / CHAIN hosts, FORMULA, stat-test parity: reference (Overlays). See `skills/overlay-system.md`.

## Non-Skippable CI Gates

CLAUDE.md hygiene:
- `TestClaudeMdMentionsFormatVersion` — CLAUDE.md must mention current `format_version` `"1.1"`.
- `TestClaudeMdMentionsAllEnvVars` — every `PULSE_*` env var in Go source must appear in CLAUDE.md.
- `TestClaudeMdMentionsAllNonSkippableGates` — every test name with these prefixes (`TestSkillsCover`, `TestClaudeMd`, `TestUpdateDemand`, `TestNoOrbit`, `TestGoldensNot`, `TestPredictNo`, `TestDescriptorNo`, `TestPerPackageCoverage`) must be listed in CLAUDE.md.
- `TestClaudeMdMentionsComponentsContract` — CLAUDE.md surfaces `Response.Components` shape + universal floor + naming-collision note.
- `TestUpdateDemandTableCovers` — Update Demand table must cover every component category and contract type.
- `TestUpdateDemandTableCoversComponents` — the new trigger rows for `Response.Components` shape, per-operator `ComponentSchema`, and extension `ComponentSchema` are present.

Predecessor-reference hygiene:
- `TestNoOrbitPrefix` — no type-constant string contains predecessor references.
- `TestNoOrbitPrefixes` — no error-code string contains predecessor references.

Descriptor contracts:
- `TestPredictNoExecutionImports` — `descriptor/predict.go` must not import `service/` or `processing/`.
- `TestDescriptorNoFmtSprintf` — no `fmt.Sprintf` in `descriptor/envelope.go`/`manifest.go`/`predict.go`/`inspect.go`.
- `TestGoldensNotHandEdited` — golden files end with valid `// golden-hash: <sha256>` line.
- `TestPerPackageCoverageFloors` — package directories exist; documents target coverage floors per package.

Skill-coverage (atomic-skill convention):
- `TestSkillsCoverAllComponents` — every registered aggregator/attribute/filterer/grouper/feature has a matching `skills/op-<category>-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllFieldTypes` — every `FieldType` has a matching `skills/type-<kebab>.md` atomic skill file (in addition to listing in `skills/cohort-schema-design.md`).
- `TestSkillsCoverAllWindowTypes` — every `WIN_*` operator has a matching `skills/op-win-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllMCPTools` — every tool registered via `toolmeta.Meta()` has a matching `skills/tool-<kebab-name-minus-pulse-prefix>.md` atomic skill file.
- `TestSkillsCoverAllSynthDistributions` — every distribution kind in `synth.AllDistributions()` has a matching `skills/op-synth-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllRegressions` — every constant in `types.AllRegressionTypes()` has a matching `skills/op-reg-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllOverlayKinds` — every constant in `types.AllOverlayKinds()` has a matching `skills/op-overlay-<kebab>.md` atomic skill file.
- `TestSkillsCoverShardingTopics` — `skills/cohort-schema-design.md` carries a `Sharded` section.
- `TestSkillsCoverAllCliLeaves` — every runnable CLI leaf (an actionable command in the real `buildApp()` tree, `pulse convert` included) is named verbatim somewhere under `skills/` or `docs/src/`. The command index in `docs/src/cli/flags.md` is the minimum home for a leaf with no dedicated page. Naming only — the gate cannot judge whether the prose is adequate.
- `TestSkillsCoverAllOperatorComponents` — every aggregator/grouper/filterer's per-operator `ComponentSchema` keys appear in the body of its matching `skills/op-<category>-<kebab>.md` atomic skill, under a `## Components` section.
- `TestSkillsCoverAllCrossReferences` — every cross-reference that names a skill resolves: a backticked skill stem (or `skills/<stem>.md` path) in the pack must be a stem `skills.Get` answers, and a section-qualified `` `skills/<stem>.md` (Section) `` pointer in CLAUDE.md, `.claude/reference/*.md` or the pack must name a heading the target file actually carries. Kebab-shaped tokens that are deliberately not skill names ride an explicit allowlist in `skill_xref_test.go`.

Atomic-skill structure / budget / example-tag:
- `TestAtomicSkillHasRequiredSections` — every `op-*` / `op-overlay-*` / `tool-*` / `type-*` skill file carries its required `##` section set (e.g. op-*: `## Params`, `## Inputs`, `## Output`, `## Gotchas`, `## See`; AGG/GROUP/FILTER additionally require `## Components`).
- `TestSkillTokenBudget` — per-family body-size budget enforced on atomic skills (op-*: 1200 chars, tool-*: 2000, type-*: 2000, `kind:design` frontmatter: 6000). Transitional soft-only regime today; tightens in a follow-up.
- `TestOperatorHasAtomicSkill` — every registered operator, MCP tool, and field type has a matching atomic skill file at the conventional stem (`op-<category>-<kebab>`, `tool-<kebab>`, `type-<kebab>`).
- `TestEveryOperatorHasAnExampleTag` — every registered operator name appears as a tag on at least one `examples/<dir>/*.json` example (gap-closure gate).

Other load-bearing contract gates (not prefix-matched, enforced by their own packages): `TestManifestOperatorsComplete`, `TestManifestStreamableMatchesTypes`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete`, `TestManifestDistributionsComplete`, `TestManifestRegressionsComplete`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`, `TestManifestMCPToolsComplete`, `TestManifestExamplesPopulated`, `TestManifest_SkillsNotEmpty`, `TestManifestFacetCapability`, `TestManifestComponentSchemasComplete`, `TestCodesHaveFixups`, `TestRegistryStreamabilityMatchesTypes`, `TestPredict_Streamable_MatchesRuntime`, `TestStreamability_*Known`, `TestStreamability_ComponentsMergeabilityKnown`, `TestCanStreamRequest_RegressionMatrix`, `TestCohortTypeCrossRefsDeterministic`, `TestDefaults_Applied`, `TestComponentsUniversalFloor`, `TestExamples_*`, `TestMCPSchemaBinding_*`, `TestErrorsLookup_*`, `TestExtensions_*`, `TestExtensions_ComponentSchemaParity`, `TestExtensions_MissingComponentSchema`, `TestShardArchive*`, `TestProcessChain_*`, `TestValidateChain_*`, `TestJoin_*`, `TestValidateJoin_*`, `TestFacetSchema_*`, `TestValidateFacet_*`, `TestCountRecords_*`, `TestNeededFields_*`, `TestProjection_*`, `TestReadRecordProjected_*`.

## Build / Env

`make build` (default), `make test`, `make fmt`, `make vet`, `make lint`, `make cover`, `make clean`, `make docs`, `make docs-serve`, `make docs-clean`. A `.env` at repo root auto-loaded.

**Environment variables:**

- `PULSE_DATA_DIR` — base directory for `.pulse` cohort files. Used by `fs.Default()` when no explicit `DataDir` or `afero.Fs` provided. Only required env var. Bypass via `pulse.Options{DataDir}` or `pulse.Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdir under fs root. Defaults to `imports`. Honoured by `imports.Manager`. `pulse.Options{ImportsDir}` overrides.
- `PULSE_IMPORT_TTL` — default TTL for managed imports. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d`. `pulse.Options{ImportTTL}` overrides.
- `PULSE_LABEL_TABLES_DIR` — directory of JSON files auto-loaded as `LabelTables` at `pulse.New` time. Each `*.json` becomes a registered label table keyed by its filename. Honoured when `pulse.Options{LabelTablesDir}` is empty. **Pulse's own sidecars are excluded by suffix before any parse attempt** — `spss.SidecarSuffix` (`.spss.json`), `imports.SidecarSuffix` (`.meta.json`) and `encoding.IndexManifestSuffix` (`.indexes.json`), the list at `isPulseSidecarName` in `sidecar_names.go` — so pointing this at a directory that also holds cohorts no longer fails `pulse.New` on a file Pulse itself wrote, and a skipped sidecar registers no table under any name. That is an explicit exclusion of known Pulse artefacts, **not** tolerance of unparseable JSON: any OTHER `*.json` that fails to parse is still a hard `pulse.New` error naming the offending path, because a typo'd label table must not silently become a table that is not there. The `.idx` sidecar index itself needs no entry — it is not `*.json`; its MANIFEST is, and has one. `PULSE_RANGE_TABLES_DIR` carries the identical exclusion via the same helper; `PULSE_TEMPLATES_DIR` walks the same shape and does **not** yet carry it (its recursive walk and `Summary.Broken` visibility lifecycle make the right treatment a separate design call).
- `PULSE_RANGE_TABLES_DIR` — directory of JSON files auto-loaded as `RangeTables` (named labeled-date-range sets) at `pulse.New` time. Each `*.json` is a bare array of `{label,start,end}` objects (or a wrapped `{"description":...,"ranges":[...]}`); the filename minus `.json` becomes the registered table name. Registered ranges are validated via the shared range-compilation pass (`PULSE_RANGE_*` codes). Honoured when `pulse.Options{RangeTablesDir}` is empty; a name declared both programmatically and on disk is a hard error. **Pulse's own sidecars are excluded by suffix before any parse attempt** — the same `isPulseSidecarName` list `PULSE_LABEL_TABLES_DIR` uses, with the same boundary: an exclusion of known Pulse artefacts, not tolerance of unparseable JSON, so any other malformed `*.json` still hard-fails `pulse.New` naming its path.
- `PULSE_TEMPLATES_DIR` — one or more directory roots scanned for request templates at `pulse.New` time, separated by `os.PathListSeparator` (`:` on Unix, `;` on Windows) in precedence order, PATH-style. Every `*.json` file beneath a root is a template; its name is its path relative to its own root minus `.json`, forward-slash separated (`<root>/finance/revenue.json` → `finance/revenue`). The first root wins — the same name under a later root is shadowed, not rejected. A root that does not exist is skipped; a root that exists but is not a directory is a `DATA_FILE` error. The store is built eagerly, so a malformed template fails `pulse.New` with the offending path named. The roots stay live afterwards: a lookup whose cached snapshot has aged past the store's 1s rescan interval re-walks them, so added / changed / deleted files are picked up without a restart, and a file is re-parsed only when its size or mtime moved. `Pulse.ReloadTemplates()` forces that walk immediately (no-op returning nil when no directories are configured) and is the deterministic path. **Post-startup breakage degrades per file, not globally:** a template that parsed once and whose file later becomes malformed keeps serving its last-good parse, and `ReloadTemplates()` returns `nil` for it — a per-file document fault (or an unreadable file) must not mask an otherwise-healthy catalog. Brokenness is visible instead on `template.Summary.Broken` + `.Error` from `Pulse.ListTemplates()` (both `omitempty`, so a healthy listing is byte-identical to the pre-E3-S2 shape). A file malformed on its FIRST appearance has no last-good parse: `GetTemplate` / `RenderTemplate` return `PULSE_TEMPLATE_INVALID` naming the path, and it lists as broken with an empty `Target` so it never looks fetchable. Repairing the file clears the state on the next rescan. Whole-walk faults (a root that exists but is not a directory) are still returned by `ReloadTemplates()` and leave the previous index entirely in place. Honoured when `pulse.Options{TemplateDirs}` is empty; unset with no `TemplateDirs` builds no store at all and template lookups return `PULSE_TEMPLATE_NOT_FOUND`.

**Concurrency knobs (`pulse.Options`):**

- `ShardWorkers` (default `0` ⇒ `NumCPU`): bounded per-shard worker pool for shard archives. Explicit `1` forces serial.
- `DecodeWorkers` (default `0` ⇒ `NumCPU`): bounded per-segment pool for single-file cohorts above `parallelDecodeRecordThreshold` (100K records). Negative values rejected at `pulse.New()`. Orthogonal to `ShardWorkers`.

**Overlay knobs (`OverlaySpec.Options`):**

- `DictPrefixFast bool` — Compose multi-slot overlay schema-match via byte-equal dictionary prefix probe. Requires embedder to verify prefix-equal dicts. Default `false`.
- `MaxPanelTargets int` — caps Targets on multi-reference Compose overlays (`OVERLAY_PROP_Z_PANEL`, `OVERLAY_PANEL_INDEX_VS_REF`); overflow → `PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP`. Default `16`.

Hermetic testing: `fs.NewMemMap()` returns a `Config` backed by `afero.NewMemMapFs()`. No disk I/O.

## Extension Points

`pulse.Options.Extensions` is the public surface for embedders injecting domain operators or expression-runtime extensions. Eight operator categories plus expr functions and lookup tables. Registration at `pulse.New()` time.

- **Naming policy:** `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$`. Reserved namespaces: `BUILTIN`, `STANDARD`, `CORE`, `PULSE`. Collision with built-in rejected.
- **Probe-validation:** engine constructs each factory once against minimal synthetic schema. Streamable-flagged registrations must return streaming interface; mismatch → `PULSE_EXTENSION_STREAMABLE_MISMATCH`. Factory panics → `PULSE_EXTENSION_FACTORY_PANIC`.
- **Expression environment:** `ExprFunctions` merged into expr-lang env (`ATTR_FORMULA`, `FILTER_EXPRESSION`). `LookupTables` reachable via `lookup(table, keys...)`. Unknown → `PULSE_LOOKUP_TABLE_UNKNOWN`. Missing key → `PULSE_LOOKUP_MISS`.
- **Auxiliary tables:** three named-table kinds ride `Extensions` alongside operators — `LookupTables` (numeric `key→float64` for the expr env), `LabelTables` (output-time `key→label`, referenced from a per-request `LabelBinding.Table`; dir-loadable via `PULSE_LABEL_TABLES_DIR`), and `RangeTables` (ordered `{label,start,end}` sets a `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` references by name; dir-loadable via `PULSE_RANGE_TABLES_DIR`; validated at `pulse.New` via the shared range-compilation pass). All three project into the manifest `extensions` block (`lookup_tables` / `label_tables` / `range_tables`).
- **Manifest visibility:** root manifest carries `extensions` block; schema-bound MCP tools include custom names in per-category enums.
- **Snapshot pattern:** `descriptor.ExtensionsSnapshot` — read-only projection passed into `descriptor.PredictOptions.Extensions` and `mcp.BindSessionToolsWithExtensions`. Built by `pulse.New` via `buildExtensionsSnapshot`. Descriptor stays free of `service/` and `processing/` imports.
- **FieldInputs hook:** every operator registration accepts optional `FieldInputs FieldInputsFunc`. Used by `processing.NeededFields` for projection. Absent → retained set widens to `*` (full-decode fallback).

Surface: `extensions.go`, `extensions_validate.go`, `extensions_probe.go`, `extensions_runtime.go`, `extensions_snapshot.go`. Runtime overlay: `processing/extensions.go`. Full recipe: `docs/src/internals/extension-points.md`.

## Request templating

Stored parameterised JSON that renders into a **validated typed request**. `template/` is the whole implementation (import ceiling: stdlib + `types` + `errors` only — never `descriptor/`, `processing/`, `service/`; gated by `TestTemplatePackage_ImportBoundary`). Facade: `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`. **No CLI leaf, no MCP tool** — library/embedding surface only.

**Contract: `.claude/reference/request-templating.md` (relocated verbatim). Load it before changing the request-template document model, the variable or target sets, or the substitution syntax.** Covers: the file wrapper and its rejected-unknown-key rule; the three substitution forms (`$var` slot marker, `{{}}` string sugar, `$when` presence guard); the nine variable types; directory precedence and shadowing; the hot-reload lifecycle phase table; the nine `PULSE_TEMPLATE_*` codes chosen by provenance; and why render never opens a cohort.

Env var: `PULSE_TEMPLATES_DIR` — see "Build / Env". Detail: `skills/request-templating.md` + `docs/src/library/request-templating.md`.

## Synthetic data

**Contract: `.claude/reference/synthetic-data.md` (141,840 bytes, relocated verbatim). You MUST load it before changing any `synth/` code, configuration or public surface** — profile capture, `SpecFromProfile` translation, generation, structural rules, fidelity report. Every failure it records was SILENT: the run succeeded and a number was quietly wrong.

Covers: conditional pair capture; per-numeric linear models (selection, shrinkage, the composed draw, shape-fitted conditioning); float-fusion determinism; `bernoulli` / `discrete` marginals; correlation and residual-correlation capture, draw and recovery; the warning summary; structural rules (`Spec.Rules`) and the three `--suggest-rules` detectors.

`format_version` does NOT move for a synth change — `synth.Profile` / `synth.Spec` live in `synth/`, not `types/`, unreachable from `descriptor.BuildPayloadSchema`. Skills: `skills/synthetic-data.md`, `skills/synth-models.md`, `skills/synth-structural-rules.md`.

## Skill Pack

The pack under `skills/` is the LLM surface, embedded via `//go:embed *.md`. Two skill shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic).

### Convention

- **Atomic skill = one operator / tool / type per file.** Stem encodes the surface:
  - `op-agg-<kebab>.md`, `op-attr-<kebab>.md`, `op-filter-<kebab>.md`, `op-group-<kebab>.md`, `op-win-<kebab>.md`, `op-feat-<kebab>.md`, `op-test-<kebab>.md`, `op-reg-<kebab>.md` / `op-reg-mod-<kebab>.md`, `op-synth-<kebab>.md`, `op-overlay-<kebab>.md` — one per registered operator constant.
  - `tool-<kebab>.md` — one per registered MCP tool (drop the `pulse_` prefix).
  - `type-<kebab>.md` — one per `FieldType`.
- **Topical skill = one cross-cutting design topic per file.** ~17 files (`aggregation-design`, `attribute-composition`, `cohort-schema-design`, `compose-requests`, `crosstab-guide`, `facet-design`, `feature-engineering`, `grouper-design`, `join-design`, `label-display`, `overlay-system`, `process-chain`, `regression-modeling`, `request-envelope`, `response-components`, `session-bootstrap`, `spss-cohorts`, `statistical-testing`, `streaming-and-watching`, `synthetic-data`, `synth-models`, `window-design`, plus the optional `financial-cohorts` example pack). Atomic skills cross-link into these for the why/how-it-composes prose; the topical files keep no per-operator detail.

### Frontmatter

Atomic:

```yaml
---
name: op-agg-count            # must match file stem
description: <one-line — what this operator does>
kind: operator                # operator | tool | type
category: AGG                 # AGG | ATTR | FILTER | GROUP | WIN | FEAT | TEST | REG | OVERLAY | SYNTH (empty for tool / type)
operator: AGG_COUNT           # full SCREAMING_SNAKE constant (empty for tool / type)
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, cohort-analysis]
---
```

Topical:

```yaml
---
name: aggregation-design
description: <what the topic teaches>
kind: design
type: guide
applies_to: process, compose, predict
covers: [AGG, FILTER, aggregations, filterers]
---
```

`applies_to` entries must be valid CLI leaves (`process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`) — or `mcp` on `tool-*` skills.

### Required body sections (atomic skills)

| Family | Required `##` sections |
|---|---|
| `op-*` (default) | `## Params`, `## Inputs`, `## Output`, `## Gotchas`, `## See` |
| `op-agg-*`, `op-group-*`, `op-filter-*` | the above **plus** `## Components` (v0.20.0 `Response.Components` contract — universal floor + per-operator schema must appear here) |
| `op-overlay-*` | `## Params`, `## Host shape` (replaces `## Inputs` — overlays decorate a host result), `## Output`, `## Gotchas`, `## See` |
| `type-*` | `## Bytes`, `## Range`, `## Null`, `## Dictionary`, `## See` |
| `tool-*` | `## When to use`, `## Input`, `## Output`, `## Gotchas`, `## See` |

`TestAtomicSkillHasRequiredSections` keys off the `category:` frontmatter field; stem prefix is the fallback.

### Token budget

Heuristic: `chars / 4 ≈ tokens`. Budgets are byte counts of the post-frontmatter body.

| Family | Budget (chars) | Token target |
|---|---|---|
| `op-*` | ≤1200 | ≤300 |
| `tool-*` | ≤2000 | ≤500 |
| `type-*` | ≤2000 | ≤500 |
| `kind: design` (topical) | ≤6000 | ≤1500 |

`TestSkillTokenBudget` enforces these. The current regime is transitional — the soft cap allows up to 1000% over budget so reviewers see the live state of legacy bodies without a red gate; a follow-up tightens to 30% over and flips `t.Logf` → `t.Errorf` once the offending `op-reg-*` / `op-reg-mod-*` / `op-feat-*` / `op-synth-regex` bodies have been trimmed.

### List source of truth

`skills.List()` walks the embedded `embed.FS` for `*.md` files and parses each frontmatter block. There is no `skills/index.json` — the filesystem is the manifest, and any new file with valid frontmatter is picked up automatically. Manifest visibility lands via the `skills` block emitted by `BuildManifest()`.

### Per-trigger target convention

| Trigger | Atomic skill convention |
|---|---|
| Aggregator (`AGG_*`) | `op-agg-<name>.md` |
| Attribute (`ATTR_*`) | `op-attr-<name>.md` |
| Filterer (`FILTER_*`) | `op-filter-<name>.md` |
| Grouper (`GROUP_*`) | `op-group-<name>.md` |
| Window (`WIN_*`) | `op-win-<name>.md` |
| Feature (`FEAT_*`) | `op-feat-<name>.md` |
| Statistical test (`TEST_*`) | `op-test-<name>.md` |
| Regression (`REG_*`) | `op-reg-<name>.md` / `op-reg-mod-<name>.md` |
| Synth distribution | `op-synth-<name>.md` |
| Overlay (`OVERLAY_*`) | `op-overlay-<name>.md` |
| Field type | `type-<name>.md` |
| MCP tool | `tool-<name>.md` (strip the `pulse_` prefix) |

Cross-cutting topics that are not operator-keyed route to the matching topical skill: `Response.Components` shape → `skills/response-components.md` (the canonical Components contract topical, paired with the v0.20.0 per-operator `## Components` requirement above); request slot map / smart defaults → `request-envelope`; streaming / `StreamResult` / `Watch` / `FilterToFileWithRequest` / request hashing → `streaming-and-watching`; error codes → `errors/fixup_metadata.go` via `pulse_errors_lookup`; extension surface → `docs/src/internals/extension-points.md`.

### Registered counts

Counts surfaced at runtime via `pulse_manifest` (`commands`, `components.{aggregators,attributes,filterers,groupers,windows,features}`, `tests`, `post_tests`, `synth_distributions`, `regressions`, `mcp_tools`). Never hardcode these in docs — the manifest is the single source of truth and the per-category coverage gates (`TestSkillsCoverAll*`, `TestOperatorHasAtomicSkill`) reject drift.

### Adding a skill

1. Create the file at the conventional stem (`op-<category>-<kebab>.md`, `tool-<kebab>.md`, `type-<kebab>.md`, or a new topical name).
2. Write the required frontmatter for the matching shape (atomic or topical) and the required `##` section set for that family.
3. Stay under budget — atomic op ≤1200 chars body, tool/type ≤2000, topical ≤6000.
4. Run `go test ./skills/... -count=1`. The filesystem walk picks the new file up; no count bump or index entry is needed.

## What NOT to Do

- **Do not import `service/` or `processing/` from `descriptor/`.** Predict/inspect/manifest are no-execute. `TestPredictNoExecutionImports` fails.
- **Do not hand-edit golden files.** Regenerate via `go test ./descriptor/ -run 'Test.*Golden' -update`. `TestGoldensNotHandEdited` verifies hashes.
- **Do not add implementation without tests in the same PR.** TDD.
- **Do not use `fmt.Sprintf` for JSON/XML.** Use `encoding/json` + `descriptor.NewEnvelope(data)`.
- **Do not defer skill or CLAUDE.md updates.** Follow-up PR won't happen. Next session reads stale guidance.
- **Do not add a component without updating the registry** (`processing/registry.go`) + `types.All*Types()`.
- **Do not bypass `afero.Fs`** — defeats `fs.NewMemMap()` + custom-storage extension hook.
- **Do not put business logic in `cmd/pulse/`.** CLI parses flags, constructs library objects, calls methods, formats output.
- **Do not bypass overlay typing via direct payload mutation.** Overlays are read-only siblings keyed to host coordinates — never mutate `Response.Data` / `Response.Crosstab.Matrix` in an overlay handler. The fold writes `Response.Overlays[i]` only.

## Reference Docs

Long-form contract detail lives under `.claude/reference/` — consult before non-trivial work:

- `.claude/reference/update-demand.md` — the exhaustive per-contract trigger table, and the full pre-compression form of CLAUDE.md's own table. **CLAUDE.md keeps one row per category; the per-slot rows and the complete companion file lists live only here — load it before touching any contract.** Add new rows here when introducing a new Request slot, Response slot, capability block, or execution-mode wiring.
- `.claude/reference/synthetic-data.md` — the whole synthetic-data contract (conditional pair capture, per-numeric linear models, the composed model draw, boolean / small-integer marginals, correlation and residual-correlation capture and recovery, structural rules, the `--suggest-rules` detectors, the fidelity report). **Load it before any `synth/` change** — profile capture, `SpecFromProfile` translation, generation, structural rules or fidelity reporting. Named as a required companion by the Update Demand row above.
- `.claude/reference/byte-layout.md` — the long form of `### Byte-layout invariants`: the sidecar point-lookup index and its manifest, the SPSS metadata sidecar, SPSS derived columns, SPSS export, target-aware export predict, and projected buffered decode. **Load it before changing the `.pulse` format, a field type, a sidecar file, an SPSS surface or the projection contract.** Named as a required companion by the Update Demand rows above.
- `.claude/reference/response-components.md` — the long form of `### Response.Components`: the `crosstab.margin_aggregations` auxiliary margin figures (the ADMISSION rule, `present` semantics, the display-flag gate, the allocation/emission gap), the Components opt-out contract, and the Compose per-slot / per-layer surface. **Load it before changing `Response.Components`, a per-operator `ComponentSchema`, or `CrosstabSpec.MarginAggregations` on either execution arm.** Named as a required companion by the Update Demand rows above.
- `.claude/reference/request-templating.md` — the long form of `## Request templating`: the file wrapper, the three substitution forms, the nine variable types, directory precedence, the hot-reload lifecycle phase table and the nine `PULSE_TEMPLATE_*` codes. **Load it before changing the `template/` document model, the variable or target sets, or the substitution syntax.** Library/embedding surface only — no CLI leaf, no MCP tool. Named as a required companion by the Update Demand row above.
- `.claude/reference/execution-modes.md` — full prose for every execution mode (Streaming Process, Compose, Parallel shards, Parallel buffered Process, ProcessChain, Join, Crosstab, Fused crosstab, Facet, Overlays — Level/Within, SERIES, FACET, CHAIN, FORMULA).
