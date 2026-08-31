# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as Go library (`github.com/frankbardon/pulse`) and CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` facade (`New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`, `ProcessStream`, `ProcessChain`, `CountRecords`, `ComposeParallel`, `Lookup`, `BuildIndex`, `VerifyIndex`, `ListIndexes`, `DropIndex`, `ListTemplates`, `GetTemplate`, `RenderTemplate`, `RenderTemplateRequest`, `ReloadTemplates`) is the public API. CLI never contains business logic.
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
| `OverlayLayer.Warnings` slot shape or the dispatcher-stamped `Details["overlay_index"]` routing key | `skills/overlay-system.md` (Per-layer warnings section) + `types/overlay.go` + `processing/overlay_chain_dispatch.go` + `processing/overlay_compose_dispatch.go` + `service/chain.go` + `service/compose_overlay.go` | `TestOverlayLayer_WarningsFreeByteIdentical`, `TestComposedResponse_OverlayFreeByteIdentical`, `TestSkillsCoverAllOverlayKinds` |
| `ComposedResponse` shape (`Responses`/`Overlays` slots) OR the `Pulse.Compose` / `Pulse.ComposeParallel` facade return type OR the `pulse api compose --json` `data` envelope wrapping | CLAUDE.md "Output Format Contract" (`--json` envelope + Compose envelope block) + `skills/compose-requests.md` + `skills/tool-compose.md` + `skills/session-bootstrap.md` (step 7 Compose return shape) + `skills/overlay-system.md` (Compose-host fold) + `skills/streaming-and-watching.md` (Compose streaming vs. overlays) + `docs/src/cli/api-compose.md` + `types/types.go` (`ComposedResponse`) + `descriptor/envelope.go` (`format_version`) + `mcp/toolmeta/meta.go` (`DescCompose`) + `internal/cli/api.go` (Compose handler) | `TestClaudeMdMentionsFormatVersion`, `TestComposedResponse_OverlayFreeByteIdentical` |
| A registered MCP tool (add/remove) | `skills/tool-<kebab>.md` (atomic skill; strip `pulse_` prefix) + `mcp/toolmeta/meta.go` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllMCPTools`, `TestManifestMCPToolsComplete` |
| A registered field type | `skills/type-<kebab>.md` (atomic skill) + CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` | `TestOperatorHasAtomicSkill`, `TestAtomicSkillHasRequiredSections`, `TestSkillTokenBudget`, `TestSkillsCoverAllFieldTypes`, `TestClaudeMdMentionsFormatVersion` |
| An example tag for a registered operator | `examples/<category>/*.json` `_meta.operators` (tag the operator string in at least one example body; overlay kinds tag via `overlays[].kind`) | `TestEveryOperatorHasAnExampleTag`, `TestExamples_OperatorsMatchBody` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + Fixups | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A `--json` envelope or `format_version` value (currently `"1.1"`) | CLAUDE.md "Output Format Contract" + the payload schema `$id` version (`descriptor.PayloadSchemaFormatVersion`, regenerate `descriptor/testdata/payload-schema.json`) | `TestClaudeMdMentionsFormatVersion`, `TestPayloadSchema_VersionMatchesEnvelope` |
| The public payload contract: any `types` request/response struct slot reachable from a payload, a registry enum surfaced in the schema (`types.All*Types` / `AllOverlayKinds` / `AllRegressionTypes`), or a strict-union shape (`OverlayRef` / `OverlayPayload`) | regenerate `descriptor/testdata/payload-schema.json` (`go test ./descriptor/ -run TestPayloadSchemaGolden -update`) + `descriptor/schema.go` (only if enum/union wiring changes) + `docs/src/contract/payload-schema.md` | `TestPayloadSchemaGolden`, `TestPayloadSchema_EnumsMatchRegistry`, `TestPayloadSchema_VersionMatchesEnvelope`, `TestGoldensNotHandEdited` |
| A `.pulse` file format change (header, field type) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` | `TestSkillsCoverAllFieldTypes`, `TestClaudeMdMentionsFormatVersion` |
| A registered I/O format (`io/<fmt>/` adapter reachable from `io/format`) | `io/format/format.go` (const + `FromExt` + `SupportedImport` + `NewReader`) + `internal/cli/import.go` (`makeImportReader` + `Commands:`) + `internal/cli/format.go` (`newWriterForFormat`) + `descriptor/capabilities_export.go` (`importCapability` / `exportCapability`) + `mcp/contract.go` (`ImportIn.Format` enum prose) + `mcp/toolmeta/meta.go` (`DescImport`) + (a per-format `format.ReaderOptions` knob additionally: `imports/imports.go` `Spec` + `imports/manager.go` `openReader` + `internal/cli/import_auto.go` `importAutoSpec` + an `ImportIn` slot — `Sheet` and `Charset` are the two that ride all four) + `skills/cohort-schema-design.md` + `skills/tool-import.md` (+ a per-format topical skill when the format carries its own fidelity model, e.g. `skills/spss-cohorts.md`) + `docs/src/internals/adding-io-format.md` + CLAUDE.md "Architecture" `io/` line + "Manifest payload" | `TestFromExt_Matrix`, `TestSupportedImport_EveryEntryConstructs`, `TestManifestImportCapability`, `TestManifestImportCapability_MatchesFormatRegistry`, `TestMakeImportReader_SPSS`, `TestSkillsCoverAllMCPTools` |
| A CLI leaf (add/remove) | the command index in `docs/src/cli/flags.md` + `skills/session-bootstrap.md` when the leaf carries a flag an agent must know about + a `docs/src/cli/` page when `--help` is not enough | `TestSkillsCoverAllCliLeaves` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| An environment variable | CLAUDE.md "Build / Env" + `skills/session-bootstrap.md` | `TestClaudeMdMentionsAllEnvVars` |
| `Response.Components` shape change | CLAUDE.md "Output Format Contract" + `skills/response-components.md` | `TestClaudeMdMentionsComponentsContract` |
| Per-operator `ComponentSchema` change | the operator's atomic skill (per category above) + `descriptor/capabilities_*.go` + `mcp/toolmeta/meta.go` | `TestManifestComponentSchemasComplete`, `TestSkillsCoverAllOperatorComponents`, `TestComponentsUniversalFloor` |
| Extension registration `ComponentSchema` | `docs/src/internals/extension-points.md` + Update Demand table | `TestExtensions_ComponentSchemaParity` |
| Shard archive layout (entry names, `_schema.pulse` block, magic dispatch, dict prefix rule) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` (Sharded) | `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| The request-template document model (`template.Template` / `Variable` / `Summary` wrapper keys), the variable type set (`template.AllVarTypes`), the target set (`template.AllTargets`), or the substitution syntax (`$var` / `{{}}` / `$when`) | `skills/request-templating.md` + `docs/src/library/request-templating.md` + CLAUDE.md "Request templating" + `template/` | `TestTemplatePackage_ImportBoundary`, `TestValidate_Matrix`, `TestRenderJSON_Matrix`, `TestRender_TargetMatrix`, `TestAllVarTypes_SpellingsAndCopy`, `TestAllTargets_SpellingsAndCopy`, `TestSkillTokenBudget` |
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

**Sidecar point-lookup index.** `Pulse.Lookup` / `pulse index build` / `pulse api lookup` are backed by a **separate file** next to the cohort — `cohort.pulse.<keyhash>.idx` — never the `.pulse` layout above. Format **v3** (own magic + version, distinct from `encoding.MagicBytes`/`FormatVersion`): 9-byte header (magic `PULSEIDX` + version `0x03`) → 32-byte SHA-256 source fingerprint → key-spec (ordered key columns + types) → `SourceSize` (u64) + `SourceModTime` (i64 Unix ns) → `u32 bucket_count` → fixed-width `bucket_count × u64` bucket-offset table → self-delimited bucket data (FNV-1a hash buckets → `[]uint64` row-id multimap). The fixed-width offset table makes one bucket directly addressable — a lookup hashes the key, seeks to its offset entry, seeks to that bucket's data, then seeks straight to each matched record via `RecordLocator`; never a full-cohort or full-index read (O(1) flat, measured ~5µs across 10k→1M rows vs. linear unindexed scan). Read-path staleness is a cheap O(1) size+mtime stat (mismatch → `PULSE_INDEX_STALE`); `pulse index verify` recomputes the authoritative full SHA-256 fingerprint instead. **Keyable-type policy** (`processing.IsIndexKeyableFieldType`): ALLOW `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64` (raw bit-pattern equality — `-0.0`/NaN caveat), `date`, `datetime` (epoch seconds; literal resolves through `encoding.ParseDateTime`, never `ParseFloat` — inherits `u64`'s >2^53 float-echo caveat, reachable only by a pre-1970 instant), `decimal128` (exact mantissa, no float round-trip), `categorical_*` (dictionary ID), `packed_bool`. REJECT `set_*` (a multi-select bitmask has no single unambiguous equality value — use `FILTER_SET` instead). Single-file cohorts only: shard archives → `PULSE_INDEX_UNSUPPORTED_SHARDED` (the `archive.pulse#shard.pulse` anchor is a tested single-shard workaround). Equality-only, full-key required, composite-key order significant end to end. Coded errors: `PULSE_INDEX_MISSING`, `PULSE_INDEX_STALE`, `PULSE_INDEX_UNSUPPORTED_SHARDED`, `PULSE_LOOKUP_NOT_FOUND`, `PULSE_LOOKUP_AMBIGUOUS`. Detail: `skills/cohort-schema-design.md` (Sidecar index) + `skills/tool-lookup.md`.

**SPSS metadata sidecar.** An SPSS import writes a SECOND sidecar next to the cohort — `cohort.pulse.spss.json` (`spss.SidecarSuffix`, the `imports.Sidecar` "suffix appended to the cohort filename" convention; deliberately distinct from `imports.SidecarSuffix` `.meta.json`, which a managed import writes for the same cohort). JSON, not a `.pulse` layout. It holds every dictionary element the `.pulse` byte format has no slot for: measure levels, print/write format codes, records `7/17` file attributes and `7/18` variable attributes (kept distinct), record `6` documents, weight variable, compression bias, `nominal_case_size`, original short names, declared byte widths + `7/14` very-long-string segmentation, MR/MC set definitions, `7/5` variable sets, the source charset **in the file's own declared spelling**, product name, missing-value specs in all three shapes (discrete ≤3 / range / range-plus-one-discrete, raw 8-byte slots retained verbatim), and — the load-bearing payload — the `code ↔ label ↔ Pulse dictionary ID` triple per categorical column, since the cohort dictionary holds SPSS **codes** and this is the only place the **labels** live. Document shape: `{format_version, kind, fingerprint, payload}`. **`payload` is flat and self-contained** (no paths, no offsets, no external references) so it can be lifted verbatim into a future `.pulse` schema metadata block — that block is **deferred, not rejected** (it needs a `FormatVersion` `0x01` → `0x02` bump affecting every Pulse user), and this shape is what keeps the door open. `fingerprint` is the only file-bound part and a lift drops it: 32-byte SHA-256 + `SourceSize` (u64) + `SourceModTime` (i64 Unix ns) over the **`.pulse` cohort** (not the source `.sav`), mirroring the sidecar index's O(1) size+mtime staleness check. Read-path policy, implemented at `spss.LoadSidecar(fs, cohortPath, spss.WriterOptions{})` (the write side's first act, reached from `pulse export spss`): **absent → warning** `PULSE_SPSS_SIDECAR_ABSENT` and the caller synthesises a default dictionary from the `.pulse` schema (a cohort that was never SPSS-derived correctly has none), **stale → error** `PULSE_SPSS_SIDECAR_STALE` (a stale dictionary over changed data produces a file that looks authoritative and is wrong — a refusal returns NO `SidecarResolution`, so there is no shape in which a caller reaches the stale document), **present-but-unreadable → error** `PULSE_SPSS_SIDECAR_INVALID` (foreign `kind`, unrecognised `format_version`, malformed digest, or a broken `multiple_response_sets[].fields` parallel array). The staleness check is the same cheap O(1) size+mtime comparison `PULSE_INDEX_STALE` uses, with the same residual gap (a size- and mtime-preserving in-place edit); `Document.VerifyDigest` is the authoritative full SHA-256 recompute for a verify-style pass. `spss.WriterOptions{IgnoreSidecar: true}` (`--ignore-sidecar`) suppresses the sidecar READ entirely — not merely the staleness verdict — so a healthy sidecar is ignored too and an unreadable one cannot block; it downgrades both refusals to a `PULSE_SPSS_SIDECAR_IGNORED` warning on the same synthesise-a-default path, and never applies a stale dictionary. That warning deliberately does not report which refusal it silenced — skipping the read means nothing on that path knows. `MRSet.Fields` is additive under `omitempty` and `SidecarFormatVersion` did not move for it, so an ABSENT `fields` key means “written before the slot existed” and is back-filled by `Document.Normalise` from `variables[].short_name` (case-insensitive, first declaration wins), never rejected; a PRESENT one of the wrong length IS rejected. Written via `afero.Fs` + `encoding/json` through the optional `io.SidecarEmitter` interface, which `ImportJob.Run` calls after the cohort write; a source not implementing it yields a byte-identical import and no sidecar. Detail: `skills/spss-cohorts.md` (the metadata sidecar + the derived-column registry) + `docs/src/cli/import-spss.md` + `docs/src/internals/adding-io-format.md`.

**SPSS derived columns.** An SPSS import can produce a cohort WIDER than the source dictionary — count columns from the returned schema, never from the SPSS variable count. Two synthesised kinds, both **additive**: a `<var>_missing` `categorical_*` sibling immediately after every numeric variable declaring user-missing values (the null bitmap is one bit and cannot record *why*, so the reason must be materialised as data), and one `set_*` bitmask per multiple-DICHOTOMY response set immediately after its LAST constituent (never instead of the constituents — a bit cannot separate "not selected" from "not asked"). The asymmetry with the CATEGORICAL arm is deliberate and must not be "fixed": a categorical column already stores the SPSS code as a dictionary entry, so the code is preserved losslessly and a sibling would double the width of an all-categorical survey; those entries are FLAGGED instead (`variables[].categories[].missing` on the sidecar + one per-FILE `PULSE_SPSS_CATEGORICAL_USER_MISSING` diagnostic). `--spss-missing=null` / `spss.WithMissingMode` suppresses the numeric siblings only; the `set_*` column has no opt-out. Every synthesised column is registered on the sidecar's `payload.derived` (closed `kind` vocabulary — `spss.DerivedKinds()` / `spss.DerivedFoldFor`; a cohort that derived nothing writes `"derived": []`, never a missing key), so the export folds them mechanically rather than by name-matching. Detail: `skills/spss-cohorts.md`.

**SPSS export.** `pulse export spss -i cohort.pulse -o out.sav` (and `pulse convert x.csv out.sav`) writes `.sav`; `descriptor.importCapability()` carries `Export: true` for `spss` and `exportCapability()` its `warn_and_skip` overlay entry — `TestManifestImportCapability_ExportFlagMatchesExportBlock` fails if only one moves. Four per-command flags, one per `spss.WriterOptions` field: `--ignore-sidecar`, `--uncompressed`, `--charset`, `--sanitize-names`. The `.sav` writer is a **`pio.CohortWriter`**, the one optional writer interface that replaces the row loop rather than decorating it: `ExportJob.Run` calls `WriteCohort(ctx, CohortSource{FS, Path, Includes, Labelled})` after `SetPulseSchema` + `WriteHeader` and never calls `WriteRow`, because a `.sav` value is derived from a categorical's dictionary ID, a `set_*`'s mask bits and the null bitmap — all three erased by `formatFieldValue`'s rendering (a null and an empty string categorical become the same cell). Consequences: `ExportJob.Includes` / `Labels` are REFUSED with `PULSE_SPSS_EXPORT_UNSUPPORTED` rather than silently dropped, overlays are warn-and-skip, and a `convert` from a text source (no cohort exists) buffers rows and builds an intermediate in-memory cohort through the ordinary import path. Encode-side diagnostics ride the new `io.TargetWarningEmitter` optional interface onto `ExportReport.TargetWarnings` / `ConvertReport.TargetWarnings` (nil for every pre-existing adapter); the CLI re-reads them AFTER `Close`, because a writer that encodes at `Close` has raised none of them when the job builds its report. `PULSE_SPSS_EXPORT_UNSUPPORTED` was REPURPOSED, not retired — it now means "this cohort has no honest `.sav` form", matching E3-S2's treatment of `PULSE_SPSS_COMPRESSION_UNSUPPORTED`. Name policy: illegal names are `PULSE_SPSS_NAME_INVALID` / `_COLLISION` by default; `--sanitize-names` opts into a deterministic, collision-safe rewrite of the SYNTHESISED path only, reporting every rename as `PULSE_SPSS_NAME_SANITIZED`. Detail: `skills/spss-cohorts.md` (Writing `.sav`) + `skills/session-bootstrap.md` (Target-format CLI flags).

**Export predict is target-aware.** `ExportJob.Predict` consults the target through the optional **`io.CohortValidator`** interface (`ValidateCohort(ctx, CohortSource) ([]*errors.CodedError, error)`), the write-side peer of `CohortWriter`; diagnostics land on the new `io.PredictReport.TargetWarnings` slot (symmetric with `ExportReport.TargetWarnings`). A Target that is nil or does not implement it is predicted EXACTLY as before — no extra read, no new failure mode (`TestExportJob_Predict_NonValidatingTargetUnchanged`). `spss.Writer` is the only implementer: `ValidateCohort` runs the writer's own non-data pass (`planCohort` — sidecar resolution, `BuildDictionary`, name policy, charset transcode, derived fold, `NewDataEncoder` column checks) and discards it, so a predicted refusal is the export's own check code-for-code, and it mutates nothing on the Writer. **Sound but INCOMPLETE by design:** anything needing a record (value width overflow, cell-text charset unencodability, a dictionary ID with no source code) is unreachable, and predict must NEVER refuse what the real export would accept — a false refusal is worse than silence. `pulse export predict` reads `--format` (writer built via `newWriterForFormat` against a MemMapFs + throwaway path, never `Close`d, so no file is created) and mounts the four `.sav` write flags, because `--sanitize-names` flips a `PULSE_SPSS_NAME_INVALID` refusal into a warning; with no `--format` behaviour is unchanged and the text output says the target was not checked. `format_version` stays `"1.1"` — `io.PredictReport` is not payload-reachable. Detail: `docs/src/internals/adding-io-format.md` + `docs/src/cli/export-spss.md`.

**Projected buffered decode.** Per-request field projection on the buffered decode iterator is **default-on** — it is output-transparent (same result payload, only faster and lighter per-record), so callers get the win with no flags. Opt out with `pulse.Options{DisableProjection: true}` or the `--no-project` CLI flag on `pulse api process` (v1: `pulse api process` only — `process-chain` / `compose` do not expose it yet). `pulse.Options.ProjectBufferedFields` is retained but deprecated: setting `true` is a harmless no-op; use `DisableProjection` to force full-record decode. Wiring at `pulse.go`: `svc.SetProjectBufferedFields(opts.ProjectBufferedFields || !opts.DisableProjection)`. `processing.NeededFields(req, schema, ext)` walks every request slot and returns a `FieldSet`; the iterator turns retained set into a cached `encoding.DecodePlan` via `Schema.BuildDecodePlan(retained)`. Plan segments: `SkipBytes{N}` (one discard over N bytes) and `DecodeFields{Fields}`. Bit-packed runs stay grouped; null-bitmap whole-or-skip. Extension operators without `FieldInputs` widen retained set to `*`. Bench: ~7× speedup, ~14× fewer allocs on a 4-field projection of a 200-field cohort. Detail: `skills/cohort-schema-design.md` + `docs/src/internals/extension-points.md` + `.claude/reference/execution-modes.md`.

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
- `Crosstab *CrosstabComponents` — `CellCounts[r][c]`, `CellComponents[r][c]`, row/column/grand-total margin counterparts, axis-key components. Mirrors `MatrixPayload` coordinate-for-coordinate.
- `Filterers []FiltererComponents` — uniform `{n_in, n_out, n_null_input}` across all 11 filterers.
- `Run *RunComponents` — `total_records`, `filtered_records`, `null_records`, `shard_count`, `partial_cohort_reason`. Coexists with `Response.Metadata`: `Metadata` keeps non-numerical run facts (cohort filename); `Run` carries the typed counters.

Per-operator schemas live in `descriptor.Manifest.ComponentsSchemas.{Aggregators,Groupers,Filterers}`. Mergeability axis per operator: `Mergeable` / `Partial` / `None` (`types.ComponentsMergeability`). Streaming chunks emit running state for mergeable; non-mergeable surface only on terminal flush.

**Opt-out.** `pulse.Options.DisableComponents bool` (engine-level default) and `types.Request.DisableComponents *bool` (per-request override, `nil` inherits engine). When the effective decision is "disabled", `Response.Components` stays `nil` and the wire form is byte-identical to the pre-Components baseline — `format_version` is NOT bumped. The gate sits at each execution path's emission block, so the MetaAggregator.Components / MetaGrouper.Components construction work is skipped, not built then discarded. CLI surface: `--no-components` on `pulse api process` / `process-chain` / `compose`. ProcessStream's `.Components()` returns `nil` when disabled. Compose: each `ComposedRequest.Requests[i]` carries its own per-request override; engine flag applies across the batch. MCP tools do not surface the knob (matches `DisableDefaults` / `EchoRequest` precedent).

**Compose surface (v1.1).** `ComposedResponse.Responses[i].Components` carries the per-slot block exactly as it does for a buffered `Process` call — the Compose-overlay fold treats per-slot Components as read-only. Diagnostics from the Compose-host overlay fold (cohesion failures, missing host coordinates, panel-target overflow) ride a sibling per-layer slot `ComposedResponse.Overlays[i].Warnings []OverlayWarning`; the slot is `omitempty` so overlay-free Compose responses stay byte-identical to the v1.0 wire shape (`TestComposedResponse_OverlayFreeByteIdentical`). The same per-layer `Warnings` slot is shared with the CHAIN-host barrier. See `skills/overlay-system.md` (Per-layer warnings).

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
- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`. Dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via `processing.CanStreamRequest(req, schema)`.
- **CountRecords header-fast:** `pulse.CountRecords(ctx, path) (uint64, error)` returns record total without decoding payload. Single-file: `(size − header − schema) / record_stride`. Shard archive: zip central directory + `_schema.pulse` SHRD trailer `AggregateRecordCount`. Anchor: named shard's count.

### Execution modes (pointers)

Heavy detail lives in `.claude/reference/execution-modes.md` and the named skill. CLAUDE.md keeps gate-relevant pointers only.

- **Streaming Process** (`pulse.ProcessStream`, `pulse api process --stream`) — four orchestrator modes; forced-buffered list at `skills/streaming-and-watching.md` and `.claude/reference/execution-modes.md`.
- **Projected buffered decode** — default-on (output-transparent); opt out via `pulse.Options{DisableProjection: true}` or `--no-project` on `pulse api process`. See "Byte-layout invariants" above + `docs/src/internals/extension-points.md`.
- **Parallel Compose** (`pulse.ComposeParallel`, `pulse api compose --parallel N`) — `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}`; post-slot Compose-overlay fold at `service/compose_overlay.go`. See `skills/compose-requests.md`.
- **Parallel shards** (`pulse.Options.ShardWorkers`) — bounded per-shard pool inside `Process`; mergeable-only via `processing.CanMergeRequest`. See `skills/cohort-schema-design.md`.
- **Parallel buffered Process** (`pulse.Options.DecodeWorkers`) — bounded per-segment pool over single-file mmap'd cohorts; threshold `parallelDecodeRecordThreshold = 100_000`. See `skills/cohort-schema-design.md`.
- **ProcessChain** (`pulse.ProcessChain`, `pulse_process_chain`, `pulse api process-chain`) — source-rooted linear chain; mergeable-only at v1; dual-slot overlay design (per-stage + whole-chain). See `skills/process-chain.md`.
- **Pushdown hash join** (`Request.Joins []*JoinSpec`) — v1 = exactly one inner join per Request. See `skills/join-design.md`.
- **Crosstab** (`Request.Crosstab`, `Response.Crosstab`) — composed row×column grid; margins recompute from raw rows; `normalize_level` / `normalize_within` compose. `AggregationType.MarginReducibility` classifies each cell aggregator into FOUR classes — `summable` / `mean_reducible` / `independent` / `recompute` — surfaced as `crosstab.*_aggregators` in the manifest. `independent` (`AGG_DISTINCT_COUNT`, `AGG_DISTINCT_SUM`) means the operator keeps its OWN row/column/grand accumulator: the margin is the union across cells, never their sum, and is exact after one pass — so the fused gate ADMITS it. Re-labelling those `recompute` because "a distinct count is not a sum of cells" changes no number and silently costs fusion. `CrosstabSpec.MarginAggregations` (`margin_aggregations`, additive + `omitempty`) carries AUXILIARY aggregations evaluated into the row/column/grand margin accumulators ONLY, never into a cell — `Cell` is a single `*Aggregation`, so this is how a second figure (canonically an unweighted respondent base beside a weighted metric) rides one request instead of a whole second scan. Effective labels (`Label`, else `TYPE_field`) must be unique across the slot AND distinct from `CellLabel`, because margin components are keyed by label. Structural detection is SHARED — `types.CrosstabSpec.MarginAggregationFaults` is called by both `processing.validateCrosstabSpec` and `descriptor.validateCrosstab`, so predict and execution cannot drift on WHICH specs they refuse; only the coded rendering differs. An auxiliary observes the SAME record admission as the cell aggregator (a record contributes only if it contributed to a cell) — deliberately unlike the cell's own margins, and deliberately not configurable. BOTH execution arms implement it and they must AGREE: dispatch picks fused or buffered on request SHAPE and nothing in `Response` reports which ran, so an auxiliary present on one arm only moves a sample-size figure for reasons a caller cannot see or ask about — and `AGG_WELFORD` / `AGG_MEDIAN` force buffered by construction, which is the arm two BERA stat-test families land on. The buffered half is `processing.(*Processor).computeAuxMargins` (`processing/crosstab_margin_agg.go`): it resolves each axis's membership ONCE into pointer sets and narrows each margin slot's own routed bucket, deliberately NOT walking the `(rkey, ckey)` cell buckets — those are admitted by construction but would fold a record into a ROW auxiliary once per COLUMN it landed in, M times under a `GROUP_SET_PER_ELEMENT` fan-out, which is precisely the axis this slot exists to serve and the one shape where the two arms could silently disagree. A slot admitting no record carries no figure on either arm, never a fabricated 0. `TestCrosstab_BufferedAuxMarginMatchesFused` drives one request down both. See `skills/crosstab-guide.md`.
- **Fused crosstab** (`processing.CanFuseCrosstab`, `processing.StreamableGrouper.KeyFor`, `processing.MultiKeyStreamingGrouper.KeysForRow`) — in-decode streaming alternative; ~30–47% faster on benches, and `peak-heap-MB` 8.8× / 18.6× / 20.8× better than buffered at 25k / 100k / 400k rows. **Quote peak heap, never `B/op`** — `B/op` is cumulative bytes allocated, cannot see retention, and understates the win by ~20×; fused `allocs/op` is deliberately HIGHER. Axis groupers may implement EITHER per-record keying interface, so `GROUP_SET_PER_ELEMENT` fan-out axes fuse at any position, on either or both axes; `GROUP_QUANTILE` implements neither and is the only grouper forcing buffered. `Request.Overlays` does NOT force buffered: `RunCrosstabFused` folds layers after `state.Finalize()` through the same `applyOverlaysToResponse` hook the buffered exit calls, byte-identical across arms, and `types.OverlayStreamability` stays all-`false` because a post-`Finalize` fold is not an in-pass computation. The surviving reason an overlay-carrying crosstab may still buffer is the cell-aggregator arm — `AGG_WELFORD` is non-mergeable, so `OVERLAY_PAIRWISE_WELCH_T` / `OVERLAY_PAIRWISE_TWO_MEANS_Z` stay buffered while `OVERLAY_PAIRWISE_PROP_Z` over `AGG_WEIGHTED_MEAN` fuses. Fan-out margins are non-additive on BOTH arms (a 3-label record counts 3× across row margins, once in the grand margin) — deliberate. `CrosstabSpec.MarginAggregations` gets the SAME two gate checks the cell gets — mergeable (an auxiliary rides the same per-record `UpdateRow` walk, so it must be online) and non-decimal — and declining sends the request to buffered rather than DROPPING the auxiliary, which would return a margin with the requested figure silently missing. Its `MarginReducibility` is deliberately NOT consulted: that classification answers "can this aggregator's margin be derived from its CELLS", which an auxiliary does not have — both paths give it its own row/column/grand accumulator fed record by record, so every auxiliary is `independent` in role whatever its declared class, and requiring a class would decline fusion for a request the fused walk computes exactly. The fused walk accumulates auxiliaries in `rowMarginAux` / `colMarginAux` / `grandMarginAux` beside the cell's own margin slices, under the admission rule above — a record reaches an auxiliary ONLY if it reached a cell, so a null cell field and an `Include`-excluded axis key are both absent from it while the cell's own margins still count them. Getting that wrong is SILENT: every number still renders, the base is merely cohort-wide instead of metric-scoped. Slots allocate only for the margins the spec asks for, and nothing at all when the slot is absent. See `skills/crosstab-guide.md` (Fused mergeable path) + `skills/grouper-design.md` + `.claude/reference/execution-modes.md`.
- **Facet endpoints** — simple (`pulse.Facet`) + rich (`pulse.FacetSchema`); four FACET-host overlay kinds (`OVERLAY_INDEX_VS_POP` / `OVERLAY_ZSCORE_VS_POP` / `OVERLAY_CHISQ_VS_POP` / `OVERLAY_KS_VS_POP`) ride `FacetRequest.Overlays`. FACET-host wiring is the FacetSchema-buffered-exit hook at `service.applyFacetOverlays`. See `skills/facet-design.md`.
- **Point lookup** (`pulse.Lookup`, `pulse api lookup`) — O(1) key-exact row addressing against a prebuilt sidecar index (`pulse.BuildIndex` / `pulse index build`; `pulse.VerifyIndex` / `ListIndexes` / `DropIndex` manage it). Single-file cohorts only; equality-only, full-key required. See "Byte-layout invariants" above (Sidecar point-lookup index) + `skills/cohort-schema-design.md` + `skills/tool-lookup.md`.
- **Overlays** (`Request.Overlays`, `Response.Overlays`) — additive post-result decorations keyed to host coordinates; never mutate base payload. Level / Within prefix composition, SERIES-host fold, FACET-host wiring, CHAIN-host barrier, FORMULA kind. Stat-test parity family (`OVERLAY_T_CELL` / `OVERLAY_T_VS_REF` Welch upgrade + `OVERLAY_Z_CELL` / `OVERLAY_Z_VS_REF`) reads `{n, mean, variance}` from `Response.Components.Crosstab.CellComponents[r][c]` (populated by `AGG_WELFORD` via the `MetaAggregator` path) and computes p-values byte-equal to the standalone `TEST_WELCH` / `TEST_Z_TWO_SAMPLE` row tests over the same inputs. The legacy `processing.WelfordTriple` smuggle through `MatrixCell.Value` is removed; `MatrixCell.Value` for `AGG_WELFORD` cells now carries the scalar mean per `Aggregate()`. Additive contract preserved — when no `CellComponents` triple is present, the handler falls back to `Params`-supplied mean/variance/N. See `skills/overlay-system.md` for the migration.

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
- `PULSE_LABEL_TABLES_DIR` — directory of JSON files auto-loaded as `LabelTables` at `pulse.New` time. Each `*.json` becomes a registered label table keyed by its filename. Honoured when `pulse.Options{LabelTablesDir}` is empty. **Pulse's own sidecars are excluded by suffix before any parse attempt** — `spss.SidecarSuffix` (`.spss.json`) and `imports.SidecarSuffix` (`.meta.json`), the list at `isPulseSidecarName` in `sidecar_names.go` — so pointing this at a directory that also holds cohorts no longer fails `pulse.New` on a file Pulse itself wrote, and a skipped sidecar registers no table under any name. That is an explicit exclusion of known Pulse artefacts, **not** tolerance of unparseable JSON: any OTHER `*.json` that fails to parse is still a hard `pulse.New` error naming the offending path, because a typo'd label table must not silently become a table that is not there. The `.idx` sidecar index needs no entry — it is not `*.json`. `PULSE_RANGE_TABLES_DIR` carries the identical exclusion via the same helper; `PULSE_TEMPLATES_DIR` walks the same shape and does **not** yet carry it (its recursive walk and `Summary.Broken` visibility lifecycle make the right treatment a separate design call).
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

**Not expr-lang.** `$var` / `{{}}` / `$when` are *request-authoring* parameters substituted **before** decode; `ATTR_FORMULA` / `FILTER_EXPRESSION` are expr-lang over *row fields* at execution time. No interop by design — a formula cannot see a template variable, and a formula string in a body is inert text to the renderer.

**File wrapper.** `{"name"?, "description"?, "target", "variables"[], "body"}`; **unknown top-level keys rejected** (a typo'd `"varaibles"` yielding zero variables is the silent failure this feature exists to kill). `target` ∈ `request | composed | chain | facet | sample` (lowercase) selects the strict-decode root — one of the five `types` request roots — required, never inferred. `name` derives from the file path (path relative to its own root, minus `.json`, forward-slash separated); a `name` key disagreeing with the path is rejected. `body` is a non-empty object, deliberately **not runnable as-is**.

**Substitution — three forms, no expression language.** (1) Slot marker `{"$var":"bucket"}` — a marker **iff** `$var` is the only key and its value a non-empty string; replaced whole and **type-preserving** (`{"interval":{"$var":"bucket"}}` → `{"interval":10}`, never `"10"`). `{"$var":"x","other":1}` is literal data; substituted values are spliced as data and never re-walked. (2) String sugar `"{{name}}"` — inside a **string value** only, `{{ name }}` tolerated, `{{{{` escapes a literal `{{`; `list`/`period` have no text form → `PULSE_TEMPLATE_VAR_TYPE`; `{{` in an object **key** is rejected at validation. (3) Guard `{"$when":"segs",…}` — block survives iff the variable resolved; key stripped either way. **Presence, not truthiness**: supplied `""`/`[]`/`0`/`false` KEEPS the block; it drops only when unsupplied AND undefaulted. Guards evaluate **before** descent, so markers inside a dropped block never raise. Array drops compact the slice (a `null` hole would decode to a nil operator slot); root-level `$when` errors.

**Nine variable types** (`template.AllVarTypes()`): `string`, `number`, `integer` (`1.0` yes, `1.5` no), `boolean`, `field` (a string today; cohort binding can layer on later without a wire change), `enum` (+`values`, exact + case-sensitive), `list` (+`items`, scalar element types only — lists do not nest), `date` (only `YYYY-MM-DD`, a strict subset of `encoding.DateFormats`; `03/04/2024` is ambiguous), `period` (exactly one of `ranges` XOR `table`, mirroring the `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` `Params` shape; unknown keys in it or in a `{label,start,end}` range rejected). The six scalars (`string`/`number`/`integer`/`boolean`/`field`/`date`) are the legal `items` values. Per-declaration slots: `required` (legal together with `default` — the default resolves it, so it can never go missing), `default` (raw JSON, so integer fidelity survives; explicit `null` = no default), `description`.

**Directory precedence.** `Options.TemplateDirs []string`, else `PULSE_TEMPLATES_DIR` split on `os.PathListSeparator` — the programmatic option wins outright and suppresses the env var entirely. Roots are an **ordered precedence list; first root wins**. A same-named template under a later root is **shadowed, not rejected**, and the losers land on the winner's `Summary.Shadows` rather than being discarded (shadowed entries get no summary of their own — a listing whose entries cannot all be fetched would be a trap). Blank root → skipped; missing root → skipped; root that exists but is a **regular file** → error naming the path. **Filesystem faults are `DATA_FILE`**, deliberately outside the `PULSE_TEMPLATE_*` family. Config-dir loading goes through `os`, not afero — same sanctioned exception as `label_loader.go` / `range_loader.go`.

**Hot-reload lifecycle — phase split is the contract.** A lookup whose snapshot has aged past the store's 1s rescan interval (package constant, deliberately not an `Option`) re-walks the roots; a file is re-parsed only when size or mtime moved. `ReloadTemplates()` forces the walk now — the deterministic path for a deploy step that writes then renders.

| Phase | Malformed file does | Why |
|---|---|---|
| At `pulse.New()` | **hard-fails startup**, path named | a broken document at boot is a deploy error the operator must see immediately |
| After startup, parsed before | **serves its last-good parse**; `ListTemplates` marks `Summary.Broken` + `.Error` | a half-written file is the normal transient state of in-place editing; killing the catalog is worse than slightly stale content |
| After startup, never parsed | listed to be SEEN (empty `Target`), not fetchable; `GetTemplate`/`RenderTemplate` → `PULSE_TEMPLATE_INVALID` naming the path | no last-good to fall back to |

`ReloadTemplates()` returns **nil** for a broken file — an error there would mask every otherwise-healthy template. A root that is not a directory **is** still a hard error (misconfiguration, not a transient edit) and a failed walk leaves the previous index entirely in place. Repair clears the state on the next rescan. `Summary.Broken`/`.Error` are both `omitempty`, so healthy listings stay byte-identical to the pre-E3 wire shape.

**Errors — nine `PULSE_TEMPLATE_*` codes, chosen by provenance not detection time.** A bad **declared default** is an author error → `_INVALID` (checked semantically at declaration: enum membership, date parse, period XOR — that is what keeps the fail-fast-at-`pulse.New()` promise real); a bad **caller value** → `_VAR_TYPE` / `_VAR_ENUM`. Same split on names: `$var`/`{{}}`/`$when` naming an **undeclared** variable is an author error caught at validation (`_INVALID`); a **declared but unresolved** reference is render-time `_UNRESOLVED`. Full family: `_NOT_FOUND`, `_INVALID`, `_TARGET_UNKNOWN`, `_VAR_MISSING`, `_VAR_UNKNOWN`, `_VAR_TYPE`, `_VAR_ENUM`, `_UNRESOLVED`, `_RENDER_INVALID`. **`_TARGET_UNKNOWN` currently carries two meanings** — an absent/unrecognised `target`, AND `RenderTemplateRequest` called on a template declaring a different (valid) target; the second case also carries `expected_target` in details. Details keys: `errors.DetailTemplate` (`"template"`) + `errors.DetailVariable` (`"variable"`); `pulse errors lookup CODE` is authoritative.

**Render never opens a cohort.** A rendering template is well-formed against the request *shape* only; field existence, type compatibility, operator applicability and streamability stay `Predict`'s job. Strict decode is harsher than the rest of Pulse — a body pasted from an `examples/` file with its `_meta` block attached fails `_RENDER_INVALID`. `Rendered.JSON` is retained alongside the typed value on purpose: re-marshaling the typed request would NOT reproduce it, because the request structs are dense with `omitempty` and any slot that rendered to an explicit zero would vanish on a round trip — echo `Rendered.JSON`, never a re-marshal.

**`format_version` stays `"1.1"`.** Nothing payload-reachable changed: `Options.TemplateDirs` is not a payload type, and `template.Template` / `Variable` / `Summary` / `Rendered` live in `template/`, not `types/`, so they are not reachable from `descriptor.BuildPayloadSchema`. `descriptor/testdata/payload-schema.json` is untouched by templating; only the manifest golden moved, and only for the nine new error codes.

Env var: `PULSE_TEMPLATES_DIR` — see "Build / Env". Detail: `skills/request-templating.md` + `docs/src/library/request-templating.md`.

## Skill Pack

The pack under `skills/` is the LLM surface, embedded via `//go:embed *.md`. Two skill shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic).

### Convention

- **Atomic skill = one operator / tool / type per file.** Stem encodes the surface:
  - `op-agg-<kebab>.md`, `op-attr-<kebab>.md`, `op-filter-<kebab>.md`, `op-group-<kebab>.md`, `op-win-<kebab>.md`, `op-feat-<kebab>.md`, `op-test-<kebab>.md`, `op-reg-<kebab>.md` / `op-reg-mod-<kebab>.md`, `op-synth-<kebab>.md`, `op-overlay-<kebab>.md` — one per registered operator constant.
  - `tool-<kebab>.md` — one per registered MCP tool (drop the `pulse_` prefix).
  - `type-<kebab>.md` — one per `FieldType`.
- **Topical skill = one cross-cutting design topic per file.** ~17 files (`aggregation-design`, `attribute-composition`, `cohort-schema-design`, `compose-requests`, `crosstab-guide`, `facet-design`, `feature-engineering`, `grouper-design`, `join-design`, `label-display`, `overlay-system`, `process-chain`, `regression-modeling`, `request-envelope`, `response-components`, `session-bootstrap`, `spss-cohorts`, `statistical-testing`, `streaming-and-watching`, `synthetic-data`, `window-design`, plus the optional `financial-cohorts` example pack). Atomic skills cross-link into these for the why/how-it-composes prose; the topical files keep no per-operator detail.

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

- `.claude/reference/update-demand.md` — the exhaustive per-slot trigger table. Add new rows here when introducing a new Request slot, Response slot, capability block, or execution-mode wiring.
- `.claude/reference/execution-modes.md` — full prose for every execution mode (Streaming Process, Compose, Parallel shards, Parallel buffered Process, ProcessChain, Join, Crosstab, Fused crosstab, Facet, Overlays — Level/Within, SERIES, FACET, CHAIN, FORMULA).
