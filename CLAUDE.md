# CLAUDE.md

Pulse is a self-describing tabular data processing engine. Ships as Go library (`github.com/frankbardon/pulse`) and CLI (`cmd/pulse/`). Library primary; CLI thin adapter.

**Design principles**

- **Library-first.** `pulse.go` facade (`New`, `Open`, `Process`, `Compose`, `Import`, `Export`, `Convert`, `Inspect`, `Predict`, `Sample`, `Facet`, `Synth`, `Profile`, `ProcessStream`, `ProcessChain`, `CountRecords`, `ComposeParallel`) is the public API. CLI never contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header. `descriptor/` provides `manifest`, `predict`, `inspect` — no-execute operations.
- **Skill-augmented.** `skills/` embeds 23 markdown skills via `//go:embed`. LLM agents call `skills.List()` / `skills.Get(name)` for domain guidance.
- **Embedder-extensible.** `pulse.Options.Extensions` registers custom operators (AGG/ATTR/FILTER/GROUP/WIN/FEAT/TEST/SYNTH) + expr functions + lookup tables. First-class — predict, manifest, MCP, runtime treat identically to built-ins. See `skills/extension-points.md`.
- **Nexus relationship.** Pulse standalone. Nexus discovers via `pulse manifest --json` + loads embedded skills. No reverse dependency.

For recipes (adding operators, I/O formats, MCP tools, error codes, field types; porting; debugging predict; regenerating goldens; wiring MCP client) read `skills/contributor-workflow.md`.

## The Update Demand

Any change to Pulse code, configuration, file format, or public surface MUST update the corresponding skill file(s) and CLAUDE.md in the same PR. Non-skippable CI failure if trigger fires without required update.

| If you change... | You MUST also update... | Enforced by |
|---|---|---|
| A registered aggregator | `skills/aggregation-guide.md` + `descriptor/capabilities_aggregators.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered attribute | `skills/attribute-composition.md` + `descriptor/capabilities_attributes.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered filterer | `skills/aggregation-guide.md` (filtering section) + `descriptor/capabilities_filterers.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered grouper | `skills/grouper-design.md` + `descriptor/capabilities_groupers.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered window operator | `skills/window-operations.md` + `descriptor/capabilities_windows.go` | `TestSkillsCoverAllWindowTypes`, `TestManifestOperatorsComplete` |
| A registered feature operator | `skills/feature-engineering.md` + `descriptor/capabilities_features.go` | `TestSkillsCoverAllComponents`, `TestManifestOperatorsComplete` |
| A registered statistical test (`TEST_*`) or tier-2 variant | `skills/statistical-testing.md` + `types/streamability.go` + `descriptor/capabilities_tests.go` | `TestStreamability_TestsKnown`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete` |
| A registered regression (`REG_*`) or modifier | `skills/regression-modeling.md` + `descriptor/capabilities_regressions.go` | `TestSkillsCoverAllRegressions`, `TestManifestRegressionsComplete` |
| A registered synth distribution | `skills/synthetic-data.md` + `descriptor/capabilities_distributions.go` | `TestSkillsCoverAllSynthDistributions`, `TestManifestDistributionsComplete` |
| An operator's streaming capability | `types/streamability.go` + `types/streamability_test.go` | `TestRegistryStreamabilityMatchesTypes`, `TestManifestStreamableMatchesTypes` |
| An error code (add/remove/rename) | `errors/fixup_metadata.go` (`codeMetadata`) — Message + Fixups | `TestCodesHaveFixups`, `TestManifestErrorCodesComplete` |
| A CLI leaf (add/remove/flag) | `skills/getting-started.md` + `skills/contributor-workflow.md` if internal | `TestSkillsCoverAllCliLeaves` |
| A `--json` envelope or `format_version` value | CLAUDE.md "Output Format Contract" | `TestClaudeMdMentionsFormatVersion` |
| A `.pulse` file format change (header, field type) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` | `TestSkillsCoverAllFieldTypes`, `TestClaudeMdMentionsFormatVersion` |
| A new non-skippable CI gate | CLAUDE.md "Non-Skippable CI Gates" list | `TestClaudeMdMentionsAllNonSkippableGates` |
| A new architectural decision | CLAUDE.md (relevant section) + PRD if applicable | reviewer enforcement |
| An environment variable | CLAUDE.md "Build / Env" + `skills/getting-started.md` | `TestClaudeMdMentionsAllEnvVars` |
| A registered MCP tool (add/remove) | `skills/mcp-integration.md` + `internal/mcp/mcptools/meta.go` | `TestSkillsCoverAllMCPTools`, `TestManifestMCPToolsComplete` |
| Managed-import sidecar shape (`imports.Sidecar`) | `skills/mcp-integration.md` + CLAUDE.md "Build / Env" | reviewer enforcement |
| A new MCP action tool with field-name params | `internal/mcp/schema_bind.go` + `skills/mcp-integration.md` | `TestMCPSchemaBinding_*` suite |
| The public MCP-serve entry (`mcpserve.Serve` / `mcpserve.ServeStdio`) | `mcpserve/` + `skills/mcp-integration.md` (Embedding section) | reviewer enforcement |
| The default operator table | CLAUDE.md "Smart defaults" + `skills/getting-started.md` | `TestDefaults_Applied` + reviewer |
| A natural-query parsing route | `internal/query/query.go` + tests + `skills/query-router-prompt.md` + `skills/request-recipes.md` | `TestNaturalQuery_HeuristicGrammar` |
| A request example under `examples/` | `_meta` block (kebab name, category=dir, canonical tags, alphabetized operators matching body) | `TestExamples_*` suite |
| Example tag taxonomy | `CanonicalTags` in `examples/library.go` + `docs/src/examples/library.md` | `TestExamples_TagsFromTaxonomy` |
| `pulse.Options.Extensions` API or registration validation rule | `extensions_validate.go` + `skills/extension-points.md` + CLAUDE.md "Extension Points" | `TestExtensions_*` suite |
| `FacetRequest` / `FacetResult` shape or facet streamability | `types/facet.go` + `skills/facet-design.md` + `descriptor/capabilities_facet.go` + `descriptor/facet.go` | `TestFacetSchema_*`, `TestManifestFacetCapability` |
| `pulse.Options.ProjectBufferedFields` flag or `processing.NeededFields` extractor | CLAUDE.md "Byte-layout invariants" pointer + `skills/extension-points.md` (FieldInputs hook) | `TestNeededFields_*`, `TestProjection_*`, `TestReadRecordProjected_*` |
| `FieldInputsFunc` hook on a registration struct | `extensions.go` + `extensions_runtime.go` + `skills/extension-points.md` | `TestNeededFields_ExtensionWithFieldInputs`, `TestNeededFields_UnknownExtensionWidens` |
| `DecodePlan` segment shape, `Schema.BuildDecodePlan` output, the plan-driven `RecordReader.ReadRecordWithWidePlan`, or the iterator's per-iterator plan cache | `encoding/decode_plan.go` + `encoding/reader_plan.go` + `service/decode_plan_cache.go` + `skills/cohort-schema-design.md` (Decode plan and projection section) + `skills/extension-points.md` (FieldInputs interaction) + CLAUDE.md "Byte-layout invariants" projected-decode paragraph | `TestDecodePlan_Equivalence_*`, `TestCrosstab_DecodePlanEquivalence_*` |
| Whole-file slurp avoidance in `Service.Open` / streaming iterator init (single-file branch reads magic + 9-byte header + schema only, never `afero.ReadFile` on the payload) | `service/service.go` (`Open`) + `skills/contributor-workflow.md` (mmap / Open rule) | `TestCountingFs_*` (single-read assertion added by E1-S3) |
| An `afero.Fs` eligibility probe (`service.RealPather` capability interface or wrapper-fs unwrap order) that decides whether the iterator's mmap fast path engages | `service/fs_probe.go` (`RealPather`, `resolveRealPath`) + `service/stream.go` (probe call site) + `skills/cohort-schema-design.md` (mmap policy section) + `skills/contributor-workflow.md` (`RealPather` contributor rule) | `TestCountingFs_*` (mmap-engages / ReadFile=0 assertions added by E1-S3) |
| Shard archive layout (entry names, `_schema.pulse` block, magic dispatch, dict prefix rule) | CLAUDE.md "Byte-layout invariants" + `skills/cohort-schema-design.md` (Sharded) + `skills/contributor-workflow.md` | `TestShardArchiveLayoutDocumented`, `TestSkillsCoverShardingTopics` |
| `ChainRequest` / `ChainResponse` shape OR `processing.CanChainRequest` gate | `types/chain.go` + `processing/chain.go` + `service/chain.go` + `descriptor/chain.go` + `descriptor/capabilities_chain.go` + `skills/contributor-workflow.md` + `skills/getting-started.md` + `skills/mcp-integration.md` | `TestProcessChain_*`, `TestValidateChain_*`, `TestSkillsCoverAllCliLeaves`, `TestSkillsCoverAllMCPTools` |
| `pulse.CountRecords` facade or its O(1) contract | `service/count.go` + `pulse.go` | `TestCountRecords_*` |
| `Request.Joins` slot or `JoinSpec`/`OnPair` shape | `types/join.go` + `processing/join.go` + `service/join.go` + `descriptor/join.go` + `descriptor/capabilities_join.go` + `skills/join-design.md` | `TestJoin_*`, `TestValidateJoin_*` |
| Cohort-analytics aggregator catalog (`AGG_WEIGHTED_MEAN`/`RATIO`/`CI_LOWER`/`CI_UPPER`) | `processing/aggregator_cohort.go` + `types/types.go` + `types/streamability.go` + `descriptor/capabilities_aggregators.go` + `skills/aggregation-guide.md` | `TestAggregator_*`, `TestManifestOperatorsComplete`, `TestStreamability_AggregationsKnown` |
| `ExportJob.Includes` / `ConvertJob.Includes` projection slot or its CLI `--include` surface | `io/io.go` + `io/export.go` + `io/convert.go` + `internal/cli/export.go` + `errors/codes.go` + `errors/fixup_metadata.go` + `skills/export-format-selection.md` | `TestExportJob_Includes_*`, `TestConvertJob_Includes_*`, `TestCodesHaveFixups` |
| `pulse.Options.Extensions.LabelTables` registration, `types.LabelBinding` slot on `Request`/`SampleRequest`/`FacetRequest`/`ExportJob`/`ConvertJob`, or `PULSE_LABEL_TABLES_DIR` loader | `extensions.go` + `extensions_validate.go` + `extensions_runtime.go` + `extensions_snapshot.go` + `processing/extensions.go` + `processing/labels.go` + `types/labels.go` + `descriptor/labels.go` + `descriptor/extensions.go` + `io/labels.go` + `io/io.go` + `io/export.go` + `io/convert.go` + `label_adapter.go` + `label_loader.go` + `internal/cli/labels.go` + `internal/mcp/schema_bind.go` + `skills/label-display.md` + `skills/index.json` + CLAUDE.md "Build / Env" | `TestExtensions_LabelTable*`, `TestLabelResolver_*`, `TestValidateLabels_*`, `TestSampleWithRequest_*`, `TestProcess_GroupKey_*`, `TestProcess_Labels_*`, `TestFacetSchema_Labels_*`, `TestExportJob_Labels_*`, `TestLoadLabelTables_*`, `TestParseLabelBindings_*`, `TestMCPSchemaBinding_Labels*`, `TestSkillsList_ReturnsAll`, `TestSkillsNames`, `TestClaudeMdMentionsAllEnvVars` |
| `types.CanonicalHash` algorithm or `Request.Hash` / `ComposedRequest.Hash` / `FacetRequest.Hash` / `ChainRequest.Hash` / `synth.Spec.Hash` surface | `types/hash.go` + `synth/hash.go` | `TestCanonicalHash_*`, `TestSpecHash_*` |
| `StreamResult[T]` shape or `Pulse.ProcessStreamResult` / `Pulse.SynthStream` variants | `stream.go` + `descriptor/manifest.go` (Operations) | `TestProcessStreamResult_*` |
| `Pulse.Watch` / `Pulse.WatchDir` API or `WatchOptions` / `ChangeEvent` shape | `watch.go` + `descriptor/manifest.go` (Operations) | `TestWatch_*` |
| `FilterToFileRequest` / `FilterToFileResult` shape, deterministic-naming rule, or `Pulse.FilterToFileWithRequest` dedup contract | `filter_to_file_request.go` + `descriptor/manifest.go` (Operations) | `TestFilterToFileWithRequest_*` |
| Manifest `CommandAnnotations` field or `Manifest.Operations` slot | `descriptor/manifest.go` + `descriptor/testdata/manifest.json` | `TestManifest_CommandAnnotationsPopulated`, `TestManifest_OperationsPopulated`, `TestManifest_AnnotationSemantics` |
| `Request.Crosstab` slot, `CrosstabSpec` / `MatrixPayload` / `CrosstabResult` shape, or `AggregationType.MarginReducibility` classification | `types/crosstab.go` + `types/streamability.go` + `types/types.go` + `processing/crosstab.go` + `service/crosstab.go` + `descriptor/crosstab.go` + `descriptor/capabilities_crosstab.go` + `descriptor/manifest.go` + `internal/mcp/schema_bind.go` + `skills/crosstab-guide.md` + `skills/index.json` | `TestCrosstab_CountCellByteEqualToManual`, `TestCrosstab_MedianMarginRecomputesFromRaw`, `TestCrosstab_NormalizeRow_SumsToOne`, `TestCrosstab_NormalizeColumn_SumsToOne`, `TestCrosstab_NormalizeTotal_SumsToOne`, `TestCrosstab_NestedAxes`, `TestCrosstab_BinningGrouperOnAxis`, `TestCrosstab_LongShape`, `TestCrosstab_ConflictsWithGroupsRejected`, `TestCrosstab_AllAggregatorsClassified`, `TestCrosstab_PartialColumnNormalize_SumsToOneWithinParent`, `TestCrosstab_PartialRowNormalize_SumsToOneWithinParent`, `TestCrosstab_NormalizeLevelLeaf_ByteEqualToDefault`, `TestCrosstab_NormalizeLevelOutOfRangeRejected`, `TestCrosstab_NormalizeLevelWithTotalRejected`, `TestCrosstab_NormalizeLevelLongShapeTagEmission`, `TestCrosstab_PartialMarginMedianRecomputesFromRaw`, `TestCrosstab_NormalizeWithin_RowFixesColPrefix`, `TestCrosstab_NormalizeWithin_ColumnFixesRowPrefix`, `TestCrosstab_NormalizeWithin_CombinedWithLevel`, `TestCrosstab_NormalizeWithin_ColumnCombinedWithLevel`, `TestCrosstab_NormalizeWithin_LongShape`, `TestCrosstab_NormalizeWithinOutOfRangeRejected`, `TestCrosstab_NormalizeWithinOutOfRangeRejected_ColumnAxis`, `TestCrosstab_NormalizeWithinWithTotalRejected`, `TestCrosstab_NormalizeWithinWithoutNormalizeRejected`, `TestPredict_Crosstab_MatrixForcesBuffered`, `TestPredict_Crosstab_LongNoMarginsStreamable`, `TestPredict_Crosstab_NormalizeLevelGate`, `TestPredict_Crosstab_NormalizeWithinGate`, `TestManifest_CrosstabCapabilityPopulated`, `TestManifest_CrosstabCapabilityNormalizeLevel`, `TestManifest_CrosstabCapabilityNormalizeWithin`, `TestMCPSchemaBinding_CrosstabNormalizeLevel`, `TestMCPSchemaBinding_CrosstabNormalizeWithin` |
| `descriptor.Envelope.Request` field, `pulse.Options.EchoRequest`, `PredictOptions.EchoRequest`, `ChainResponse.NormalizedRequest`, or the `--echo-request` CLI surface | `descriptor/envelope.go` + `descriptor/predict.go` + `pulse.go` + `service/service.go` + `service/chain.go` + `types/chain.go` + `internal/cli/api.go` + `internal/cli/json.go` + CLAUDE.md "Output Format Contract" | `TestEnvelope_RequestOmittedByDefault`, `TestEnvelope_RequestPopulatedWhenSet`, `TestPredict_EchoRequest_Normalized`, `TestProcessChain_NormalizedRequest_PerStage` |
| `MatrixCell.Value` shape (scalar/rich union), `AggregationType.MapValued` classifier, or the RichAggregator dispatch hook | `types/crosstab.go` + `types/streamability.go` + `processing/crosstab.go` + `processing/processor.go` + `processing/interfaces.go` + `descriptor/crosstab.go` + `descriptor/capabilities_crosstab.go` + `descriptor/manifest.go` + `internal/mcp/schema_bind.go` + `errors/codes.go` + `errors/fixup_metadata.go` + `skills/crosstab-guide.md` | `TestCrosstab_SetFrequencyCellEmitsMap`, `TestCrosstab_SetFrequencyMarginEmitsMap`, `TestCrosstab_MapValuedCellRejectsNormalize`, `TestCrosstab_ScalarCellPathUnchanged`, `TestCrosstab_RichDispatchInBufferedAggregate`, `TestPredict_Crosstab_MapValuedNormalizeRejected`, `TestPredict_Crosstab_MapValuedNormalizeNoneAccepted`, `TestManifest_CrosstabMapValuedCellAggregators` |
| `pulse.Options.SetInferenceMinPct`, `imports.Sidecar.ColumnTypeOverrides`, `pio.InferOptions`, `pio.ImportJob.SetDelimiters`, the `setWidth` helper, the delimited-cell heuristic in `inferColumnTypeWithOpts`, or the Arrow `LIST<UTF8>` mapping | `pulse.go` + `imports/imports.go` + `imports/manager.go` + `io/io.go` + `io/infer.go` + `io/import.go` + `io/arrow/types.go` + `io/jsonshared/values.go` + `io/ndjson/ndjson.go` + `io/jsonarray/jsonarray.go` + `encoding/field_type.go` (`ParseFieldType`) + `internal/cli/fieldtype.go` + `skills/cohort-schema-design.md` ("Importing set columns") | `TestInfer_SetFromPipeDelimitedCSV`, `TestInfer_SetRespectsMinPct`, `TestInfer_SetOverflowFallsBackToCategorical`, `TestInfer_AvgCardinalityOneRejectsSet`, `TestInfer_ForceTypeOverridesInference`, `TestInfer_SetMinPctRespectsCustom`, `TestConvertValue_SetU8`, `TestConvertValue_SetU8_OverflowEmitsTypedError`, `TestImportJob_SetEndToEnd`, `TestImportJob_SetForceTypeOverride`, `TestImportJob_SetInferenceMinPctRespected`, `TestArrow_TypeToPulseListUTF8`, `TestArrow_TypeFromPulseSetU8`, `TestArrow_FormatValueListUTF8JoinsWithPipe`, `TestArrow_InferPulseSchemaListUTF8`, `TestManager_Open_CSV_ColumnTypeOverridesPersist`, `TestManager_Open_CSV_ColumnTypeOverridesUnknownRejected` |

Table is self-referential — new trigger rows require updating this table in the same PR. `TestUpdateDemandTableCovers` parses this section and asserts every component category and contract type has a row.

Defer the doc/skill update to "a follow-up PR" and the follow-up will not happen. Update in the same PR or do not merge.

## Architecture

```
pulse/
├── cmd/pulse/              # CLI binary — only binary
├── pulse.go                # Public facade
├── service/                # Orchestration: wires processing to encoding
│   ├── shard_iter.go       # Multi-shard row iterator (serial)
│   ├── shard_reduce.go     # Per-shard parallel reducer for mergeable ops
│   ├── shard_admin.go      # Create / Add / Remove / List / Extract
│   ├── shard_compact.go    # Orphan-byte reclamation
│   ├── shard_verify.go     # Full re-validation
│   ├── chain.go            # ProcessChain: source-rooted linear chain
│   └── anchor_overlay.go   # archive.pulse#shard.pulse anchor overlay
├── processing/             # Aggregators, attributes, filterers, groupers
│   ├── window/             # WIN_* operators
│   └── feature/            # FEAT_* pre-filter feature engineers
├── encoding/               # .pulse binary codec
│   ├── archive.go          # Zip64 read/write + EOCD parsing
│   ├── schema_doc.go       # _schema.pulse canonical schema + SHRD trailer
│   └── cohesion.go         # Structural + dict-prefix cohesion validators
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

`internal/mcp/` registers eleven tools (one per facade method plus `pulse_ask` one-shot + `pulse_facet_schema`) and two resource schemes (`pulse://`, `pulse-skill://`).

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

**Shard archive variant.** `.pulse` path resolves to either single-file layout above or **shard archive** — uncompressed Zip64 (Method 0, store-only) whose first four bytes are zip magic `PK\x03\x04` instead of `PULSE` magic. Single-file byte format **unchanged**; magic-byte dispatch at `pulse.Open` selects shape. Shard archive carries reserved `_schema.pulse` entry (header-only canonical schema + SHRD trailer with `aggregate_record_count` + `shard_count`) plus N standalone shard payloads. Per-shard cohesion: structural strict (byte-equal at insert), descriptions tolerant (divergence → warning). Categorical dictionaries grow under union-merge semantics; divergent incoming shards byte-rewritten with remapped indices so every record references canonical dict. Width overflow → `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. Stricter prefix-only validator (`PULSE_SHARD_DICT_DIVERGENCE`) retained for `pulse shard verify`. Anchor syntax `archive.pulse#shard.pulse` opens one shard as one-shard cohort. Caller-owned concurrency. Forced-buffered ops materialize across union. Full detail + dict semantics in `skills/cohort-schema-design.md` (Sharded cohorts).

**Projected buffered decode.** `pulse.Options.ProjectBufferedFields` (opt-in) enables per-request field projection on the streaming iterator. `processing.NeededFields(req, schema, ext)` walks every request slot — Aggregations, Attributes (incl. expr-AST identifiers via `expr-lang/expr/parser` + `expr-lang/expr/ast` for `ATTR_FORMULA` / `FILTER_EXPRESSION`), Filterers, Groups, Windows (Field + PartitionBy + OrderBy), Features (Field + `stratify`/`target` Params), Tests, Regressions, Sort.Field — returns `FieldSet`. The iterator turns that retained set into an `encoding.DecodePlan` via `Schema.BuildDecodePlan(retained)` (pure function of schema + retained set; cached per-iterator keyed by schema-pointer identity + sorted retained set; lifetime dies with the iterator) and the per-record hot path walks the plan via `RecordReader.ReadRecordWithWidePlan` instead of the schema. Plan segments are `SkipBytes{N}` (one `Seek` / `io.CopyN` discard advancing N bytes with no per-field iteration, coalescing every contiguous unprojected non-bit-packed run) and `DecodeFields{Fields}` (the existing per-field decode loop scoped to one group). Bit-packed grouping: any contiguous run of `u4`/`packed_bool` fields stays grouped — if any member is retained the whole group becomes one `DecodeFields` (unretained members are still decoded to keep `ReadBit`/`ReadNibble` cursor-aligned, only their map writes are suppressed); otherwise the whole group folds into a single `SkipBytes` of `K` bytes (one per bit-packed field). Null bitmap whole-or-skip: when `Schema.HasBitmap()` and any nullable field is retained, the iterator decodes the bitmap once and surfaces nulls for the retained subset; when no nullable field is retained the bitmap becomes a single `SkipBytes{N: Schema.BitmapByteSize()}` — see the bitmap paragraph above. Extension operators surface `FieldInputs FieldInputsFunc` — absent on a custom operator → retained set widens to `*` (full-coverage plan, no `SkipBytes`, behaves like the unprojected walk). Bench: synth 200-field × 100K-row Process drops from ~1.07s to ~155ms (~7×, 14× fewer allocs) on a 4-field projection. Detail: `skills/cohort-schema-design.md` (Decode plan and projection) + `skills/extension-points.md` (FieldInputs hook).

### Smart defaults

When a request slot names a field but omits `Type`, engine infers from schema type. Table in `descriptor/defaults.go` (`defaultRules`).

| Field type | Default aggregation | Default grouper |
|---|---|---|
| numeric (u4/u8/u16/u32/u64, f32/f64, decimal128) | `AGG_SUM` | `GROUP_RANGE` (Interval 10) |
| categorical_* | `AGG_FREQUENCY` | `GROUP_CATEGORY` |
| `date` | (explicit only) | `GROUP_DATE` (`"day"`) |
| `packed_bool` | `AGG_FREQUENCY` | `GROUP_CATEGORY` |

`Field.Nullable` orthogonal — never changes inferred operator, only bitmap participation. Defaults apply only when `Field` set and `Type` empty; never override explicit `Type`; never cross categories; never default tier-1/tier-2 tests, filter expressions, attributes, windows, features. Disable via `pulse.Options{DisableDefaults: true}` or `--no-defaults`. Predict always computes `DefaultsApplied`.

## Output Format Contract

### `--json` envelope

All `--json` CLI output + descriptor operations use `descriptor.Envelope`:

```json
{
  "format_version": "1.0",
  "data": { ... },
  "request": { ... },
  "errors": [],
  "warnings": []
}
```

- `format_version` always `"1.0"`. Changes MUST update this section.
- `errors` / `warnings` use `{"code", "message", "details"}`. Empty array (never null) when absent.
- `request` is opt-in echo of the *normalized* request that produced `data`. Omitted entirely (the `omitempty` rule) unless `pulse.Options.EchoRequest` is true or the CLI was invoked with `--echo-request`. Shape varies by operation: `Request` for process/predict, `ComposedRequest` for compose, `ChainRequest` for process-chain (per-stage normalized form captured during execution), `FacetRequest` for facet, `SampleRequest` for sample. Streaming output (`--stream`, `ProcessStream`) skips the echo by construction — NDJSON has no envelope. Use `descriptor.NewEnvelopeWithRequest(data, req)` or `env.WithRequest(req)` to populate it.

Additive-only: bump `format_version` only on backward-incompatible shape changes. New `data` fields don't bump; renames/removals do. The `request` field is additive (omitempty) and does NOT bump `format_version`.

### Structural defense bans

- **No `fmt.Sprintf`-built JSON.** All structured output through `encoding/json`. Grep-gated by `TestDescriptorNoFmtSprintf`.
- **No hand-built XML/CDATA.** Use `encoding/xml`.
- Use `descriptor.NewEnvelope(data)` — auto-sets `format_version`, empty `errors`, empty `warnings`. Use `descriptor.NewEnvelopeWithRequest(data, req)` (or `env.WithRequest(req)`) when echo is enabled at the call site.

### Manifest payload

`descriptor.BuildManifest()` returns deterministic LLM-bootstrap blob — one fetch per session, client-cached. Reachable via `pulse manifest --json` and `pulse_manifest`. Top-level: `format_version`, `commands`, `components` (six operator slices), `tests` + `post_tests`, `synth_distributions`, `regressions`, `error_codes_count` + `error_domains` + `error_codes` (slim, name-only), `mcp_tools`, `cohort_types`, `skills`, `extensions`, plus capability blocks `Facet`, `Join`, `ProcessChain`. Sort-stable; golden-checked at `descriptor/testdata/manifest.json`. Capability declarations: `descriptor/capabilities_*.go`. MCP tool metadata: `internal/mcp/mcptools/meta.go`.

### Predict / Inspect contracts

- **Predict structural ban:** `descriptor/predict.go` MUST NOT import `service/` or `processing/`. Enforced by `TestPredictNoExecutionImports`. Reads only header + schema, never records. Capability lookups via `types/` constants.
- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`. Dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`. Missing descriptions get synthesized fallback flagged with `description_source`.
- **Predict streamability:** `PredictResult.Streamable` mirrors per-type `Streamable()` methods plus schema gates (decimal). Runtime parity via `processing.CanStreamRequest(req, schema)`.
- **CountRecords header-fast:** `pulse.CountRecords(ctx, path) (uint64, error)` returns record total without decoding payload. Single-file: `(size − header − schema) / record_stride`. Shard archive: zip central directory + `_schema.pulse` SHRD trailer `AggregateRecordCount` (fallback to per-shard `PeekShardRecordCount`). Anchor: named shard's count.

### Execution modes (pointers)

Heavy detail lives in the named skill — CLAUDE.md keeps the gate-relevant facts only.

- **Streaming Process** (`pulse.ProcessStream`, `pulse api process --stream`): four orchestrator modes — single-pass, grouped, two-pass attributes (Welford-Pébaÿ), streaming features. Forced-buffered: median/percentile/zscore aggregators, `ATTR_PERCENTILE`, `GROUP_QUANTILE`/`GROUP_DATE`, window operators, decimal paths, tier-1 tests with groupers/features/two-pass, all tier-2 tests. NDJSON one row per line. See `skills/getting-started.md`.
- **Projected buffered decode**: see "Byte-layout invariants" subsection above and `skills/extension-points.md` for the `FieldInputs` hook.
- **Parallel Compose** (`pulse.ComposeParallel`, `pulse api compose --parallel N`): bounded worker pool over `ComposedRequest`. Order-preserving by slot index. `ComposeOptions{MaxWorkers, PerRequestTimeout, FailFast}` (FailFast defaults true). See `skills/compose-requests.md`.
- **Parallel shards** (`pulse.Options.ShardWorkers`, default `0` ⇒ `runtime.NumCPU()`): bounded per-shard worker pool inside `Process` when cohort is a shard archive. Reducer engages only when every operator is mergeable per `processing.CanMergeRequest` (built-ins only; non-decimal targets; no windows/features/regressions/tests/two-pass). Non-mergeable falls through to serial `shardIter`. Associative+commutative byte-equal vs serial; Welford within ULP via Chan-Welford. Surface: `service/shard_reduce.go`, `processing.MergeableAggregator`, `types.AggregationType.Mergeable()`. See `skills/cohort-schema-design.md` (Sharded).
- **ProcessChain** (`pulse.ProcessChain`, `pulse_process_chain`, `pulse api process-chain`): source-rooted linear chain. Stage 0 against `ChainRequest.Cohort`; subsequent stages receive prior `Response.Data` as synthesised in-memory cohort (grouper keys → categorical_u32, aggregator outputs → f64). Mergeable-only at v1 (`CanChainRequest` requires `CanMergeRequest` AND scalar aggregator emission — rejects `AGG_FREQUENCY`/`AGG_MODE`). Failures: `PULSE_CHAIN_NOT_MERGEABLE` (with `stage_index`/`stage_name`), `PULSE_CHAIN_EMPTY`. Predict equivalent: `descriptor.ValidateChain`. Surface: `types/chain.go`, `processing/chain.go`, `service/chain.go`, `descriptor/chain.go`, `descriptor/capabilities_chain.go`. See `skills/contributor-workflow.md`.
- **Pushdown hash join** (`Request.Joins []*JoinSpec`): v1 = exactly one inner join per Request (`PULSE_JOIN_TOO_MANY`, `PULSE_JOIN_KIND_NOT_IMPLEMENTED`); in-memory build (no spill; `PULSE_JOIN_SPILL_DIR` + `PULSE_JOIN_MAX_MEMORY_BYTES` reserved); no smarter-side detection. `JoinedSchema(left,right,spec)` produces `left.Fields + right.Fields` with optional `spec.As` prefix; non-prefixed collisions → `PULSE_JOIN_FIELD_COLLISION`. Key compatibility: identical types match; categorical of any width match; unsigned-int/float/date numeric family interchangeable; decimal128 rejects cross-type. Surface: `types/join.go`, `processing/join.go`, `service/join.go`, `descriptor/join.go`, `descriptor/capabilities_join.go`. See `skills/join-design.md`.
- **Crosstab** (`Request.Crosstab`, `Response.Crosstab`): composes existing groupers + aggregators across a row × column axis grid; reshape, margin recompute, and normalization layered on top. Multi-grouper axes are recursively partitioned inside the orchestrator (the standard `processing.processGrouped` path still only consults the first grouper). Margins are always recomputed from raw rows for correctness — `AGG_MEDIAN` row margin equals the standalone single-axis median, NOT the median of cell medians. Per-aggregator classification at `AggregationType.MarginReducibility` (`summable` / `mean_reducible` / `recompute`). `shape: matrix` (default) populates `Response.Crosstab.Matrix`; `shape: long` emits tuple rows on `Response.Data` with margin rows tagged `_margin=row|column|grand`. Inherently buffered except `shape=long` + no margins + `normalize=none`. Normalization layering: `normalize_level` truncates the same axis as `normalize` (parent-grouper denominator); `normalize_within` fixes a prefix of the OPPOSITE axis (cross-axis partitioned denominator — e.g. `normalize=row normalize_within=0` ⇒ cells in each `(rowKey, outerColPrefix)` slab sum to 1.0). Both gates compose. The buffered record set is automatically projected to only request-referenced fields via `service.applyCrosstabProjection` (extends `processing.NeededFields` to walk `Crosstab.Rows/Columns/Cell` + `req.Labels[].Field` + `AGG_WEIGHTED_MEAN/RATIO` Cell.Params) so wide-cohort crosstab cost is `O(|referenced|)` not `O(|schema|)`; forced on regardless of `opts.ProjectBufferedFields` because the crosstab path is unconditionally buffered. Capability surface: `Manifest.Crosstab`. Predict gate: `descriptor.validateCrosstab`. See `skills/crosstab-guide.md`.
- **Facet endpoints**: `pulse.Facet(ctx, path, field)` simple distinct-values (categorical fast path through dict, numeric streams file). `pulse.FacetSchema(ctx, *FacetRequest)` rich multi-field — counts (sorted desc by count, ties asc by value), null tallies, Welford online numeric stats, optional `NumericPercentiles` (forces buffered per-field sort), optional `IncludeHistogram` with caller-supplied `HistogramRange` (single-pass), optional `DiscreteTopK` (`TruncatedAt` warning), optional `AdditiveFields` contribution counts. Capability: `Manifest.Facet`. Predict: `descriptor.ValidateFacet`. CLI `pulse api facet` falls back to simple shape unless rich flag set. See `skills/facet-design.md`.

## Non-Skippable CI Gates

CLAUDE.md hygiene:
- `TestClaudeMdMentionsFormatVersion` — CLAUDE.md must mention current `format_version` `"1.0"`.
- `TestClaudeMdMentionsAllEnvVars` — every `PULSE_*` env var in Go source must appear in CLAUDE.md.
- `TestClaudeMdMentionsAllNonSkippableGates` — every test name with these prefixes (`TestSkillsCover`, `TestClaudeMd`, `TestUpdateDemand`, `TestNoOrbit`, `TestGoldensNot`, `TestPredictNo`, `TestDescriptorNo`, `TestPerPackageCoverage`) must be listed in CLAUDE.md.
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
- `TestSkillsCoverAllSynthDistributions` — every distribution kind appears in `skills/synthetic-data.md`.
- `TestSkillsCoverAllRegressions` — every `REG_*` operator appears in `skills/regression-modeling.md`.
- `TestSkillsCoverShardingTopics` — `skills/cohort-schema-design.md` carries a `Sharded` section and `skills/contributor-workflow.md` mentions sharding.

Other load-bearing contract gates (not prefix-matched, enforced by their own packages): `TestManifestOperatorsComplete`, `TestManifestStreamableMatchesTypes`, `TestManifestTestsComplete`, `TestManifestPostTestsComplete`, `TestManifestDistributionsComplete`, `TestManifestRegressionsComplete`, `TestManifestErrorCodesComplete`, `TestManifest_ErrorCodesSlim`, `TestManifestMCPToolsComplete`, `TestManifestExamplesPopulated`, `TestManifest_SkillsNotEmpty`, `TestManifestFacetCapability`, `TestCodesHaveFixups`, `TestRegistryStreamabilityMatchesTypes`, `TestPredict_Streamable_MatchesRuntime`, `TestStreamability_*Known`, `TestCanStreamRequest_RegressionMatrix`, `TestCohortTypeCrossRefsDeterministic`, `TestDefaults_Applied`, `TestNaturalQuery_HeuristicGrammar`, `TestExamples_*`, `TestMCPSchemaBinding_*`, `TestErrorsLookup_*`, `TestExtensions_*`, `TestShardArchive*`, `TestProcessChain_*`, `TestValidateChain_*`, `TestJoin_*`, `TestValidateJoin_*`, `TestFacetSchema_*`, `TestValidateFacet_*`, `TestCountRecords_*`, `TestNeededFields_*`, `TestProjection_*`, `TestReadRecordProjected_*`.

## Build / Env

`make build` (default), `make test`, `make fmt`, `make vet`, `make lint`, `make cover`, `make clean`, `make docs`, `make docs-serve`, `make docs-clean`. A `.env` at repo root auto-loaded.

**Environment variables:**

- `PULSE_DATA_DIR` — base directory for `.pulse` cohort files. Used by `fs.Default()` when no explicit `DataDir` or `afero.Fs` is provided. Only required env var for runtime. Bypass via `pulse.Options{DataDir}` or `pulse.Options{FS}`.
- `PULSE_IMPORTS_DIR` — managed-imports subdir under fs root. Defaults to `imports`. Honoured by `imports.Manager` (and so `pulse_import` / `pulse import auto`). `pulse.Options{ImportsDir}` overrides.
- `PULSE_IMPORT_TTL` — default TTL for managed imports when caller doesn't pass one. Go duration (`24h`, `30m`), day form (`7d`, `30d`), or `pin`. Defaults to `7d`. `pulse.Options{ImportTTL}` overrides.
- `PULSE_LABEL_TABLES_DIR` — directory of JSON files auto-loaded as `LabelTables` at `pulse.New` time. Each `*.json` becomes a registered label table keyed by its filename (without the extension); file content is either a flat `{key: value}` map or `{"description": "...", "rows": {...}}`. Honoured when `pulse.Options{LabelTablesDir}` is empty. Empty / missing dir = no-op.

Hermetic testing: `fs.NewMemMap()` returns a `Config` backed by `afero.NewMemMapFs()`. No disk I/O.

## Extension Points

`pulse.Options.Extensions` is the public surface for embedders injecting domain operators or expression-runtime extensions. Eight operator categories plus expr functions and lookup tables. Registration at `pulse.New()` time; restart to change.

**Naming policy:** `^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$`. Reserved namespaces: `BUILTIN`, `STANDARD`, `CORE`, `PULSE`. Collision with built-in rejected. Validation order at `pulse.New`: regex/reserved/collision/duplicate → probe-validation (factory + interface check) → runtime registration.

**Probe-validation:** engine constructs each factory once against minimal synthetic schema. Streamable-flagged registrations must return streaming interface; mismatch → `PULSE_EXTENSION_STREAMABLE_MISMATCH`. Factory panics → `PULSE_EXTENSION_FACTORY_PANIC`.

**Expression environment:** `ExprFunctions` merged into expr-lang env used by `ATTR_FORMULA` and `FILTER_EXPRESSION`. `LookupTables` reachable via auto-injected `lookup(table, keys...)`. Rows-backed tables join keys with `|`; function-backed receive raw `[]string`. Unknown table → `PULSE_LOOKUP_TABLE_UNKNOWN`. Missing key → `PULSE_LOOKUP_MISS`.

**Manifest visibility:** root manifest carries `extensions` block listing every registered operator + expr function + lookup table (`has_rows_data` distinguishes static maps from function-driven). Schema-bound MCP tools (post-`pulse_inspect`) include custom names in per-category enums.

**Snapshot pattern:** `descriptor.ExtensionsSnapshot` — read-only projection passed into `descriptor.PredictOptions.Extensions` and `mcp.BindSessionToolsWithExtensions`. Built by `pulse.New` via `buildExtensionsSnapshot`, cached on Service. Descriptor stays free of `service/` and `processing/` imports.

**FieldInputs hook:** every operator registration accepts optional `FieldInputs FieldInputsFunc`. When set, `processing.NeededFields` calls it with operator's raw `Params` and includes returned field names in projection set. When omitted on custom operator, projection extractor widens to "every field" (full-decode fallback). Hook plumbed via `buildRuntimeExtensions` into `processing.ExtensionRegistry.FieldInputs`, keyed by `StreamabilityKey(category, name)`.

Surface: `extensions.go` (types), `extensions_validate.go` (validation), `extensions_probe.go` (probe), `extensions_runtime.go` (runtime registry), `extensions_snapshot.go` (manifest/predict snapshot). Runtime overlay: `processing/extensions.go`. Full recipe: `skills/extension-points.md`.

## Skill Pack

26 skills under `skills/`, embedded via `//go:embed`. Frontmatter:

```yaml
---
name: skill-name
description: What the skill teaches
type: guide   # or "reference"
applies_to: process, compose, predict
---
```

`applies_to` entries must be valid CLI leaves (`process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`).

| Category | Target skill |
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
| Facet endpoint | `skills/facet-design.md` |
| Join | `skills/join-design.md` |
| Label binding / display overlay | `skills/label-display.md` |
| Crosstab / cross-tabulation | `skills/crosstab-guide.md` |
| Error code | `errors/fixup_metadata.go` (via `pulse_errors_lookup`) |
| Extension API surface | `skills/extension-points.md` |
| Request hashing / StreamResult / Watch / FilterToFileWithRequest / manifest annotations | `skills/streaming-and-watching.md` |

Current registered counts: 27 aggregators, 11 attributes, 11 filterers, 7 groupers, 10 windows, 9 features, 20 tests, 12 synth distributions, 3 regressions.

Adding a skill: create `skills/<name>.md` with frontmatter, add entry to `skills/index.json`, bump count in `TestSkillsList_ReturnsAll` and `TestSkillsNames`. Run `go test ./skills/...`.

## What NOT to Do

- **Do not import `service/` or `processing/` from `descriptor/`.** Predict/inspect/manifest are no-execute. `TestPredictNoExecutionImports` fails.
- **Do not hand-edit golden files.** Regenerate via `go test ./descriptor/ -run 'Test.*Golden' -update`. `TestGoldensNotHandEdited` verifies hashes.
- **Do not add implementation without tests in the same PR.** TDD.
- **Do not use `fmt.Sprintf` for JSON/XML.** Use `encoding/json` + `descriptor.NewEnvelope(data)`.
- **Do not defer skill or CLAUDE.md updates.** Follow-up PR won't happen. Next session reads stale guidance.
- **Do not add a component without updating the registry** (`processing/registry.go`) + `types.All*Types()`.
- **Do not bypass `afero.Fs`** — defeats `fs.NewMemMap()` + custom-storage extension hook.
- **Do not put business logic in `cmd/pulse/`.** CLI parses flags, constructs library objects, calls methods, formats output.
