# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as Go library (`github.com/frankbardon/pulse`) and CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` facade (`New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`, `ProcessStream`, `ProcessChain`, `CountRecords`, `ComposeParallel`) is the public API. CLI never contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header. `descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `skills/` embeds an atomic-per-surface skill pack (`op-*` / `tool-*` / `type-*`) plus ~20 topical design skills via `//go:embed *.md`. LLM agents call `skills.List()` / `skills.Get(name)` for domain guidance; the filesystem walk + frontmatter parse is the source of truth.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators (AGG/ATTR/FILTER/GROUP/WIN/FEAT/TEST/SYNTH) + expr functions + lookup tables. First-class — predict, manifest, MCP, runtime treat identically to built-ins. See `docs/src/internals/extension-points.md`.
- **Nexus relationship.** Pulse standalone. Nexus discovers via `pulse manifest --json` + loads embedded skills. No reverse dependency.

For recipes (adding operators, I/O formats, MCP tools, error codes, field types; porting; debugging predict; regenerating goldens; wiring MCP client) read the mdBook Internals chapter under `docs/src/internals/` (adding-aggregator.md, adding-attribute.md, adding-filterer.md, adding-grouper.md, adding-window.md, adding-feature.md, adding-test.md, adding-synth-distribution.md, adding-mcp-tool.md, adding-io-format.md, adding-field-type.md, adding-error-code.md, adding-chain-predicate.md, adding-facet-capability.md, regenerating-goldens.md, debugging-predict.md, wiring-mcp-client.md, extension-points.md). Long-form reference docs live under `.claude/reference/` — see "Reference Docs" at the bottom.

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

This is the compressed surface — the full per-contract trigger table lives at `.claude/reference/update-demand.md`.

| If you change... (category) | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator | `skills/op-agg-<kebab>.md` (atomic skill) + `descriptor/capabilities_aggregators.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered attribute | `skills/op-attr-<kebab>.md` (atomic skill) + `descriptor/capabilities_attributes.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered filterer | `skills/op-filter-<kebab>.md` (atomic skill) + `descriptor/capabilities_filterers.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered grouper, or the `Group.Include` inclusion-list slot | `skills/op-group-<kebab>.md` (atomic skill) + `descriptor/capabilities_groupers.go` + (Include: `processing/grouper.go` + `processing/grouper_set.go`) | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete`, `TestGroupCategory_IncludeFiltersLabels`, `TestGroupSetValue_IncludeFiltersCompositeKey`, `TestGroupSetPerElement_IncludeFilters` |
| A registered window operator | `skills/op-win-<kebab>.md` (atomic skill) + `descriptor/capabilities_windows.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllWindowTypes`, `TestManifestOperatorsComplete` |
| A registered feature operator | `skills/op-feat-<kebab>.md` (atomic skill) + `descriptor/capabilities_features.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered statistical test (`TEST_*`) or tier-2 variant | `skills/op-test-<kebab>.md` (atomic skill) + `types/streamability.go` + `descriptor/capabilities_tests.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestStreamability_TestsKnown`, `TestManifestTestsComplete` |
| A registered regression (`REG_*`) or modifier | `skills/op-reg-<kebab>.md` (atomic skill; modifiers ship as `skills/op-reg-mod-<kebab>.md`) + `descriptor/capabilities_regressions.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllRegressions`, `TestManifestRegressionsComplete` |
| A registered synth distribution | `skills/op-synth-<kebab>.md` (atomic skill) + `descriptor/capabilities_distributions.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllSynthDistributions`, `TestManifestDistributionsComplete` |
| A registered overlay kind (`OVERLAY_*`) | `skills/op-overlay-<kebab>.md` (atomic skill) + `types/overlay.go` (`AllOverlayKinds`) | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllOverlayKinds` |
| A registered MCP tool (add/remove) | `skills/tool-<kebab>.md` (atomic skill; strip `pulse_` prefix) + `internal/mcp/mcptools/meta.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllMCPTools`, `TestManifestMCPToolsComplete` |
| A registered field type | `skills/type-<kebab>.md` (atomic skill) + CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllFieldTypes`, `TestClaudeMdMentionsFormatVersion` |
| An example tag for a registered operator | `examples/<category>/*.json` `_meta.operators` (tag the operator string in at least one example body; overlay kinds tag via `overlays[].kind`) | `TestEveryOperatorHasAnExampleTag`, `TestExamples_OperatorsMatchBody` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + Fixups | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A `--json` envelope or `format_version` value (currently `"1.1"`) | CLAUDE.md "Output Format Contract" | `TestClaudeMdMentionsFormatVersion` |
| A `.pulse` file format change (header, field type) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` | `TestSkillsCoverAllFieldTypes`, `TestClaudeMdMentionsFormatVersion` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| An environment variable | CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| `Response.Components` shape change | CLAUDE.md "Output Format Contract" + `skills/response-components.md` | `TestClaudeMdMentionsComponentsContract` |
| Per-operator `ComponentSchema` change | the operator's atomic skill (per category above) + `descriptor/capabilities_*.go` + `internal/mcp/mcptools/meta.go` | `TestManifestComponentSchemasComplete`, `TestSkillsCoverAllOperatorComponents`, `TestComponentsUniversalFloor` |
| Extension registration `ComponentSchema` | `docs/src/internals/extension-points.md` + Update Demand table | `TestExtensions_ComponentSchemaParity` |
| Shard archive layout (entry names, `_schema.pulse` block, magic dispatch, dict prefix rule) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` (Sharded) | `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| Any Request slot, Response slot, capability block, or Execution-mode wiring | See `.claude/reference/update-demand.md` for the per-slot trigger row | per-slot test suites cited there |

Table covers every component category and contract type required by `TestUpdateDemandTableCovers` (case-insensitive checks for: aggregator, attribute, filterer, grouper, error code, format_version, field type, CI gate, environment variable, MCP tool, feature operator). New trigger rows for slot-level contracts go into `.claude/reference/update-demand.md`.

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
├── io/                     # Tabular ↔ .pulse adapters (csv|tsv|ndjson|jsonarray|jsonshared|arrow|parquet|excel)
├── fs/                     # afero-based filesystem abstraction
├── errors/                 # Typed error codes (CodedError system)
├── types/                  # Request/response structs + streamability table
├── descriptor/             # manifest, predict, inspect, envelope (no-execute)
├── skills/                 # //go:embed markdown skill pack
├── examples/               # //go:embed runnable request examples
├── synth/                  # Synthetic data generator
├── docs/                   # mdBook source (GitHub Pages)
└── internal/{cli,mcp}/     # CLI internals + MCP server
```

`pulse.go` re-exports `types.Request` → `pulse.Request`, `types.Response` → `pulse.Response`, `types.ComposedRequest` → `pulse.ComposedRequest`, plus `synth.Spec`/`Result`/`Options`/`Profile`/`ProfileOptions`.

CLI commands map 1:1 to manifest commands: `process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `mcp`, plus `synth from-schema`, `synth from-profile`, `profile create`, `shard {create,add,remove,list,compact,verify,extract}`, `api {process,compose,facet,process-chain}`.

`internal/mcp/` registers ten tools (one per facade method plus `pulse_facet_schema`) and two resource schemes (`pulse://`, `pulse-skill://`).

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

17 field types (full table in `skills/cohort-schema-design.md`): `u4`, `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `categorical_u8`/`u16`/`u32`, `decimal128`, `set_u8`/`u16`/`u32`/`u64`. Bit-packed (`u4`, `packed_bool`) return `ByteSize() == 0` (share bytes with neighbours via `FieldType.IsBitPacked()`). `categorical_*` and `set_*` both carry an inline dictionary block (`FieldType.HasDictionary()`); for `set_*` the on-wire value is a fixed-width bitmask where bit `i` ↔ dictionary entry `i` (up to 8/16/32/64 entries) and an empty mask is a valid "no selection" — distinct from null. Nullability orthogonal to type. Unknown type byte → `ENCODING_INVALID`. Decimal128 and set_* nulls via bitmap only — no in-band sentinel.

**Shard archive variant.** `.pulse` path resolves to either single-file layout above or **shard archive** — uncompressed Zip64 (Method 0, store-only) whose first four bytes are zip magic `PK\x03\x04` instead of `PULSE` magic. Single-file byte format **unchanged**; magic-byte dispatch at `pulse.Open` selects shape. Shard archive carries reserved `_schema.pulse` entry (header-only canonical schema + SHRD trailer with `aggregate_record_count` + `shard_count`) plus N standalone shard payloads. Per-shard cohesion: structural strict (byte-equal at insert), descriptions tolerant. Categorical dictionaries grow under union-merge semantics; divergent incoming shards byte-rewritten with remapped indices. Width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. Stricter prefix-only validator (`PULSE_SHARD_DICT_DIVERGENCE`) retained for `pulse shard verify`. Anchor syntax `archive.pulse#shard.pulse` opens one shard as one-shard cohort. Caller-owned concurrency. Full detail in `skills/cohort-schema-design.md` (Sharded cohorts).

**Projected buffered decode.** `pulse.Options.ProjectBufferedFields` enables per-request field projection on the streaming iterator. `processing.NeededFields(req, schema, ext)` walks every request slot and returns a `FieldSet`; the iterator turns retained set into a cached `encoding.DecodePlan` via `Schema.BuildDecodePlan(retained)`. Plan segments: `SkipBytes{N}` (one discard over N bytes) and `DecodeFields{Fields}`. Bit-packed runs stay grouped; null-bitmap whole-or-skip. Extension operators without `FieldInputs` widen retained set to `*`. Bench: ~7× speedup, ~14× fewer allocs on a 4-field projection of a 200-field cohort. Detail: `skills/cohort-schema-design.md` + `docs/src/internals/extension-points.md` + `.claude/reference/execution-modes.md`.

### Smart defaults

When a request slot names a field but omits `Type`, engine infers from schema type. Table in `descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u4/u8/u16/u32/u64, f32/f64, decimal128) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

`Field.Nullable` orthogonal — never changes inferred operator. Defaults apply only when `Field` set and `Type` empty; never override explicit `Type`; never cross categories; never default tests, filter expressions, attributes, windows, features. Disable via `pulse.Options{DisableDefaults: true}` or `--no-defaults`. Predict always computes `DefaultsApplied`.

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

- `format_version` always `"1.1"`. Changes MUST update this section.
- `errors` / `warnings` use `{"code", "message", "details"}`. Empty array (never null) when absent.
- `request` is opt-in echo of the *normalized* request. Omitted unless `pulse.Options.EchoRequest` is true or CLI flag `--echo-request`. Shape varies: `Request` for process/predict, `ComposedRequest` for compose, `ChainRequest` for process-chain, `FacetRequest` for facet, `SampleRequest` for sample. Streaming output skips the echo. Use `descriptor.NewEnvelopeWithRequest(data, req)` or `env.WithRequest(req)` to populate.

Additive-only: bump `format_version` only on backward-incompatible shape changes. New `data` fields don't bump; renames/removals do. The `request` field is additive (omitempty) and does NOT bump `format_version`.

### Response.Components

Every `Response` carries an optional `Components *ResponseComponents` (additive `omitempty`; `format_version` stays `"1.1"`). Mirrors the request shape:

- `Aggregations []AggregationComponents` — one entry per aggregator slot; universal floor `{n, n_null}` + operator-specific `Operator map[string]any` keyed by the manifest schema.
- `Groupers []GrouperComponents` — universal floor `{total_n, n_null}` + operator-specific bucket layout.
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, row/column/grand-total margin counterparts, axis-key components. Mirrors `MatrixPayload` coordinate-for-coordinate.
- `Filterers []FiltererComponents` — uniform `{n_in, n_out, n_null_input}` across all 11 filterers.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. Coexists with `Response.Metadata`: `Metadata` keeps non-numerical run facts (cohort filename); `Run` carries the typed counters.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers}`. Mergeability axis per operator: `Mergeable` / `Partial` / `None` (`types.ComponentsMergeability`). Streaming chunks emit running state for mergeable; non-mergeable surface only on terminal flush.

Full contract: `skills/response-components.md`.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** Use `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf`.
- **No hand-built XML/CDATA.** Use `encoding/xml`.
- Use `descriptor.NewEnvelope(data)` for the standard envelope.

### Manifest payload

`descriptor.BuildManifest()` returns deterministic LLM-bootstrap blob — one fetch per session, client-cached. Reachable via `pulse manifest --json` and `pulse_manifest`. Top-level: `format_version`, `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim), `mcp_tools`, `cohort_types`, `skills`, `extensions`, plus capability blocks `Facet`, `Join`, `ProcessChain`, `Crosstab`, `Overlays`. Sort-stable; golden-checked at `descriptor/testdata/manifest.json`. Capability declarations: `descriptor/capabilities_*.go`. MCP tool metadata: `internal/mcp/mcptools/meta.go`.

### Predict / Inspect contracts

- **Predict structural ban:** `descriptor/predict.go` MUST NOT import `service/` or `processing/`. Enforced by `TestPredictNoExecutionImports`. Reads only header + schema, never records.
- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`. Dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via `processing.CanStreamRequest(req, schema)`.
- **CountRecords header-fast:** `pulse.CountRecords(ctx, path) (uint64, error)` returns record total without decoding payload. Single-file: `(size − header − schema) / record_stride`. Shard archive: zip central directory + `_schema.pulse` SHRD trailer `AggregateRecordCount`. Anchor: named shard's count.

### Execution modes (pointers)

Heavy detail lives in `.claude/reference/execution-modes.md` and the named skill. CLAUDE.md keeps gate-relevant pointers only.

- **Streaming Process** (`pulse.ProcessStream`, `pulse api process --stream`) — four orchestrator modes; forced-buffered list at `skills/streaming-and-watching.md` and `.claude/reference/execution-modes.md`.
- **Projected buffered decode** — see "Byte-layout invariants" above + `docs/src/internals/extension-points.md`.
- **Parallel Compose** (`pulse.ComposeParallel`, `pulse api compose --parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}`; post-slot Compose-overlay fold at `service/compose_overlay.go`. See `skills/compose-requests.md`.
- **Parallel shards** (`pulse.Options.ShardWorkers`) — bounded per-shard pool inside `Process`; mergeable-only via `processing.CanMergeRequest`. See `skills/cohort-schema-design.md`.
- **Parallel buffered Process** (`pulse.Options.DecodeWorkers`) — bounded per-segment pool over single-file mmap'd cohorts; threshold `parallelDecodeRecordThreshold = 100_000`. See `skills/cohort-schema-design.md`.
- **ProcessChain** (`pulse.ProcessChain`, `pulse_process_chain`, `pulse api process-chain`) — source-rooted linear chain; mergeable-only at v1; dual-slot overlay design (per-stage + whole-chain). See `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins []*JoinSpec`) — v1 = exactly one inner join per Request. See `skills/join-design.md`.
- **Crosstab** (`Request.Crosstab`, `Response.Crosstab`) — composed row×column grid; margins recompute from raw rows; `normalize_level` / `normalize_within` compose. See `skills/crosstab-guide.md`.
- **Fused crosstab** (`processing.CanFuseCrosstab`, `processing.StreamableGrouper.KeyFor`) — in-decode streaming alternative; ~30–47% faster on benches. See `skills/crosstab-guide.md` (Fused mergeable path) + `skills/grouper-design.md`.
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); four FACET-host overlay kinds (`OVERLAY_INDEX_VS_POP` / `OVERLAY_ZSCORE_VS_POP` / `OVERLAY_CHISQ_VS_POP` / `OVERLAY_KS_VS_POP`) ride `FacetRequest.Overlays`. FACET-host wiring is the FacetSchema-buffered-exit hook at `service.applyFacetOverlays`. See `skills/facet-design.md`.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates; never mutate base payload. Level / Within prefix composition, SERIES-host fold (E3-S6), FACET-host wiring (E5-S6), CHAIN-host barrier (E6-S3), FORMULA kind (E8-S2). Stat-test parity family (`OVERLAY_T_CELL` / `OVERLAY_T_VS_REF` Welch upgrade + `OVERLAY_Z_CELL` / `OVERLAY_Z_VS_REF`) reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via the `MetaAggregator` path) and computes p-values byte-equal to the standalone `TEST_WELCH` / `TEST_Z_TWO_SAMPLE` row tests over the same inputs. The legacy `processing.WelfordTriple` smuggle through `MatrixCell.Value` is removed (E3-S7/S8); `MatrixCell.Value` for `AGG_WELFORD` cells now carries the scalar mean per `Aggregate()`. Additive contract preserved — when no `CellComponents` triple is present, the handler falls back to `Params`-supplied mean/variance/N. See `skills/overlay-system.md` for the migration.

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

Skill-coverage (atomic-skill convention, post-E4):
- `TestSkillsCoverAllComponents` — every registered aggregator/attribute/filterer/grouper/feature has a matching `skills/op-<category>-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllFieldTypes` — every `FieldType` has a matching `skills/type-<kebab>.md` atomic skill file (in addition to listing in `skills/cohort-schema-design.md`).
- `TestSkillsCoverAllWindowTypes` — every `WIN_*` operator has a matching `skills/op-win-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllMCPTools` — every tool registered via `mcptools.Meta()` has a matching `skills/tool-<kebab-name-minus-pulse-prefix>.md` atomic skill file.
- `TestSkillsCoverAllSynthDistributions` — every distribution kind in `synth.AllDistributions()` has a matching `skills/op-synth-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllRegressions` — every constant in `types.AllRegressionTypes()` has a matching `skills/op-reg-<kebab>.md` atomic skill file.
- `TestSkillsCoverAllOverlayKinds` — every constant in `types.AllOverlayKinds()` has a matching `skills/op-overlay-<kebab>.md` atomic skill file.
- `TestSkillsCoverShardingTopics` — `skills/cohort-schema-design.md` carries a `Sharded` section.
- `TestSkillsCoverAllOperatorComponents` — every aggregator/grouper/filterer's per-operator `ComponentSchema` keys appear in the body of its matching `skills/op-<category>-<kebab>.md` atomic skill, under a `## Components` section.

Atomic-skill structure / budget / example-tag (post-E4):
- `TestAtomicSkillHasRequiredSections` — every `op-*` / `op-overlay-*` / `tool-*` / `type-*` skill file carries its required `##` section set (e.g. op-*: `## Params`, `## Inputs`, `## Output`, `## Gotchas`, `## See`; AGG/GROUP/FILTER additionally require `## Components`).
- `TestSkillTokenBudget` — per-family body-size budget enforced on atomic skills (op-*: 1200 chars, tool-*: 2000, type-*: 2000, `kind:design` frontmatter: 6000). Transitional soft-only regime today; tightens in E4-S15.
- `TestOperatorHasAtomicSkill` — every registered operator, MCP tool, and field type has a matching atomic skill file at the conventional stem (`op-<category>-<kebab>`, `tool-<kebab>`, `type-<kebab>`).
- `TestEveryOperatorHasAnExampleTag` — every registered operator name appears as a tag on at least one `examples/<dir>/*.json` example (gap-closure gate).

Other load-bearing contract gates (not prefix-matched, enforced by their own packages): `TestManifestOperatorsComplete`, `TestManifestStreamableMatchesTypes`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete`, `TestManifestDistributionsComplete`, `TestManifestRegressionsComplete`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`, `TestManifestMCPToolsComplete`, `TestManifestExamplesPopulated`, `TestManifest_SkillsNotEmpty`, `TestManifestFacetCapability`, `TestManifestComponentSchemasComplete`, `TestCodesHaveFixups`, `TestRegistryStreamabilityMatchesTypes`, `TestPredict_Streamable_MatchesRuntime`, `TestStreamability_*Known`, `TestStreamability_ComponentsMergeabilityKnown`, `TestCanStreamRequest_RegressionMatrix`, `TestCohortTypeCrossRefsDeterministic`, `TestDefaults_Applied`, `TestComponentsUniversalFloor`, `TestExamples_*`, `TestMCPSchemaBinding_*`, `TestErrorsLookup_*`, `TestExtensions_*`, `TestExtensions_ComponentSchemaParity`, `TestExtensions_MissingComponentSchema`, `TestShardArchive*`, `TestProcessChain_*`, `TestValidateChain_*`, `TestJoin_*`, `TestValidateJoin_*`, `TestFacetSchema_*`, `TestValidateFacet_*`, `TestCountRecords_*`, `TestNeededFields_*`, `TestProjection_*`, `TestReadRecordProjected_*`.

## Build / Env

`make build` (default), `make test`, `make fmt`, `make vet`, `make lint`, `make cover`, `make clean`, `make docs`, `make docs-serve`, `make docs-clean`. A `.env` at repo root auto-loaded.

**Environment variables:**

- `PULSE_DATA_DIR` — base directory for `.pulse` cohort files. Used by `fs.Default()` when no explicit `DataDir` or `afero.Fs` provided. Only required env var. Bypass via `pulse.Options{DataDir}` or `pulse.Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdir under fs root. Defaults to `imports`. Honoured by `imports.Manager`. `pulse.Options{ImportsDir}` overrides.
- `PULSE_IMPORT_TTL` — default TTL for managed imports. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d`. `pulse.Options{ImportTTL}` overrides.
- `PULSE_LABEL_TABLES_DIR` — directory of JSON files auto-loaded as `LabelTables` at `pulse.New` time. Each `*.json` becomes a registered label table keyed by its filename. Honoured when `pulse.Options{LabelTablesDir}` is empty.

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
- **Manifest visibility:** root manifest carries `extensions` block; schema-bound MCP tools include custom names in per-category enums.
- **Snapshot pattern:** `descriptor.ExtensionsSnapshot` — read-only projection passed into `descriptor.PredictOptions.Extensions` and `mcp.BindSessionToolsWithExtensions`. Built by `pulse.New` via `buildExtensionsSnapshot`. Descriptor stays free of `service/` and `processing/` imports.
- **FieldInputs hook:** every operator registration accepts optional `FieldInputs FieldInputsFunc`. Used by `processing.NeededFields` for projection. Absent → retained set widens to `*` (full-decode fallback).

Surface: `extensions.go`, `extensions_validate.go`, `extensions_probe.go`, `extensions_runtime.go`, `extensions_snapshot.go`. Runtime overlay: `processing/extensions.go`. Full recipe: `docs/src/internals/extension-points.md`.

## Skill Pack

The pack under `skills/` is the LLM surface, embedded via `//go:embed *.md`. Two skill shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic).

### Convention

- **Atomic skill = one operator / tool / type per file.** Stem encodes the surface:
  - `op-agg-<kebab>.md`, `op-attr-<kebab>.md`, `op-filter-<kebab>.md`, `op-group-<kebab>.md`, `op-win-<kebab>.md`, `op-feat-<kebab>.md`, `op-test-<kebab>.md`, `op-reg-<kebab>.md` / `op-reg-mod-<kebab>.md`, `op-synth-<kebab>.md`, `op-overlay-<kebab>.md` — one per registered operator constant.
  - `tool-<kebab>.md` — one per registered MCP tool (drop the `pulse_` prefix).
  - `type-<kebab>.md` — one per `FieldType`.
- **Topical skill = one cross-cutting design topic per file.** ~17 files (`aggregation-design`, `attribute-composition`, `cohort-schema-design`, `compose-requests`, `crosstab-guide`, `facet-design`, `feature-engineering`, `grouper-design`, `join-design`, `label-display`, `overlay-system`, `process-chain`, `regression-modeling`, `request-envelope`, `response-components`, `session-bootstrap`, `statistical-testing`, `streaming-and-watching`, `synthetic-data`, `window-design`, plus the optional `financial-cohorts` example pack). Atomic skills cross-link into these for the why/how-it-composes prose; the topical files keep no per-operator detail.

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

`TestSkillTokenBudget` enforces these. The current regime is transitional — the soft cap allows up to 1000% over budget so reviewers see the live state of legacy bodies without a red gate; E4-S15 tightens to 30% over and flips `t.Logf` → `t.Errorf` once the offending `op-reg-*` / `op-reg-mod-*` / `op-feat-*` / `op-synth-regex` bodies have been trimmed.

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

- `.claude/reference/update-demand.md` — the exhaustive per-slot trigger table. Add new rows here when introducing a new Request slot, Response slot, capability block, or execution-mode wiring.
- `.claude/reference/execution-modes.md` — full prose for every execution mode (Streaming Process, Compose, Parallel shards, Parallel buffered Process, ProcessChain, Join, Crosstab, Fused crosstab, Facet, Overlays — Level/Within, SERIES, FACET, CHAIN, FORMULA).
