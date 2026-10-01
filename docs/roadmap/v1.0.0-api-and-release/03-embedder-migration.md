# 03 — Embedder migration guide

**Status:** decided · U01 and U02 rows landed · **Target:** v1.0.0 · **Applies:** as U01, U02, U02b and U02c land

## Purpose

[00](00-public-surface.md) decides which packages stay public at v1.0.0 and which move under `internal/`. This guide lists every resulting contract difference an embedder can hit — old spelling, new spelling, and the mechanical adaptation — so a library that imports Pulse today can upgrade without reading the unit diffs.

It applies in stages: `pulse.Version()` and the MCP version defaults land with **U01**; the root, `encoding`, `io`, `descriptor` and other-package rows land with **U02**; the extension-authoring rows land with **U02b**; `CohortReader` / `CohortWriter` and `PredictResult.CrosstabFusable` land with **U02c**. Each row names its unit. Anything not listed keeps its spelling and behaviour. Every U01 / U02 row is compiled against the public spellings by the embedder smoke module (`internal/embeddersmoke`, built by `make smoke` in CI), and `internal/apigolden/testdata/public_api.txt` is the exhaustive list of what stayed exported.

## Legend

| Kind | Meaning |
|---|---|
| **kept** | unchanged; no action |
| **alias-kept** | same `pulse.X` spelling, now an alias or root-native type; no action |
| **narrowed** | package stays public but only the listed subset stays exported |
| **moved** | import path or constructor changes; behaviour unchanged |
| **replaced** | a new API takes over; the old one is gone |
| **removed** | gone; the replacement (if any) is named |
| **added** | new API; adopt when convenient |
| **behaviour change** | same spelling, different default or result |

## Root `pulse`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `pulse.DateRangeSpec` (alias of `processing.DateRangeSpec`) | `pulse.DateRangeSpec` (root-native type) | alias-kept | none | U02 |
| `pulse.MemberSet`, `pulse.LoadMemberSetResult` | root-native types | alias-kept | none | U02 |
| `pulse.ComposeOptions`, `BuildIndexResult`, `SetWidening`, `CreateShardArchiveResult`, `AddShardResult`, `GroupReconciliation`, `VerifyResult`, `ShardEntry`, `VerifyIndexResult`, `IndexFreshnessReason`, `IndexInfo`, `Row`, `RowIter` | same names, now aliases into `internal/service` | alias-kept | none | U02 |
| `service.Cohort` and the other `service` types reached only through `(*Pulse).Service()` | gone with `Service()` | removed | use the facade methods | U02 |
| `encoding.CohesionWarning`, `encoding.GroupIndexHeadroom`, `encoding.Index` (the element types inside `VerifyResult` / `SetWidthHeadroom` / `BuildIndexResult.Index`) | `pulse.CohesionWarning`, `pulse.GroupIndexHeadroom`, `pulse.SidecarIndex` | moved | spell the root alias; the fields are unchanged. `SidecarIndex`'s field closure is aliased too — `encoding.IndexKeySpec` → `pulse.SidecarIndexKeySpec`, `encoding.IndexBucket` → `pulse.SidecarIndexBucket`, `encoding.IndexEntry` → `pulse.SidecarIndexEntry`, `encoding.Fingerprint` → `pulse.CohortFingerprint` — so the whole index is nameable and walkable | U02 |
| `imports.Spec`, `imports.Result`, `imports.Entry`, `imports.Sidecar` | `pulse.ImportSpec`, `pulse.ImportResult`, `pulse.ImportEntry`, `pulse.ImportSidecar` | moved | spell the root alias | U02 |
| `examples.Example`, `examples.ExampleSummary` | `pulse.Example`, `pulse.ExampleSummary` | moved | spell the root alias; reach the library through `ExampleGet` / `ExamplesSearch` | U02 |
| `template.Template`, `Summary`, `Rendered`, `Target`, `VarType`, `Variable` | `pulse.Template`, `pulse.TemplateSummary`, `pulse.RenderedTemplate`, `pulse.TemplateTarget`, `pulse.TemplateVarType`, `pulse.TemplateVariable` | moved | spell the root alias. the `template.Target*` / `Var*` constants are re-declared at the root as `pulse.TemplateTarget*` (`TemplateTargetRequest`, `TemplateTargetComposed`, `TemplateTargetChain`, `TemplateTargetFacet`, `TemplateTargetSample`) and `pulse.TemplateVar*` (`TemplateVarString`, `TemplateVarNumber`, `TemplateVarInteger`, `TemplateVarBoolean`, `TemplateVarField`, `TemplateVarEnum`, `TemplateVarList`, `TemplateVarDate`, `TemplateVarPeriod`) — compare against those, not `.String()` | U02 |
| `(*Pulse).Service()` + `service.(*Service).SetDisableCrosstabFusion` | `pulse.Options{DisableCrosstabFusion: true}` | removed / replaced | set the option at `pulse.New` instead of mutating the service | U02 |
| `descriptor.InspectFromBytes(data, opts)` | `(*Pulse).InspectBytes(ctx, data, *descriptor.InspectOptions) (*descriptor.Envelope, error)` | replaced | call on the instance; the envelope is returned whole (warnings included). Inspect has no `Strict` / `EchoRequest` behaviour, so none is filled | U02 |
| `descriptor.PredictFromBytes(data, req, opts)` | `(*Pulse).PredictBytes(ctx, data, *pulse.Request) (*descriptor.Envelope, error)` | replaced | call on the instance; the extension snapshot, `Options.Strict` and `Options.EchoRequest` now come from the instance (previously an unset snapshot treated extension operators as unknown) | U02 |
| `encoding.SidecarIndexPath`, `IndexManifestPath`, `ReadIndexManifest`, `WriteIndexManifest`, `NewIndexManifest` | `(*Pulse).CohortArtifacts(ctx, cohort) ([]string, error)` | replaced | manage indexes with `BuildIndex` / `ListIndexes` / `VerifyIndex` / `DropIndex`. To move a cohort, move every path `CohortArtifacts` returns (each existing `.<keyhash>.idx`, `.indexes.json`, `.spss.json`, `.meta.json`, sorted) **and keep the cohort's modification time** — the index and the SPSS sidecar both fingerprint it, so a touched cohort reads as stale | U02 |
| `(*Pulse).Lookup` | unchanged | kept | none | — |

## Engine packages → internal

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `processing.CanFuseCrosstab(req, schema)` | `PredictResult.CrosstabFusable` (from `Predict` / `PredictBytes`) | replaced | run predict and read the field; a parity test keeps it equal to the runtime decision | U02c |
| `processing.*` extension-authoring interfaces and factories (`Aggregator`, `Grouper`, `FiltererBuilder`, `Record`, `*Factory`, …), `processing/feature`, `processing/window` | `github.com/frankbardon/pulse/extend` | moved + reshaped | port custom operators to `extend`; `Record` becomes a small read-only interface (values, nulls, wide and set accessors). Registration through `pulse.Options.Extensions` is unchanged. **Until U02b lands, `processing`, `processing/feature` and `processing/window` stay public at their current paths** | U02b |
| `processing.ApplyOverlays`, `CompileDateRanges`, `DateRangeSet`, `NewCrosstabHostViewWithComponents` | internal, no replacement | removed | drive overlays through `Request.Overlays` / `(*Pulse).ApplySeriesOverlays`; range tables are validated at `pulse.New` | U02 |
| `processing/regression`, `processing/arena` | `internal/processing/regression`, `internal/processing/arena` | moved | none; reach regressions through `REG_*` requests | U02 |
| `service` package | `internal/service` | moved | use the root aliases above | U02 |
| `fs` package (`fs.New`, `fs.Default`, `fs.NewMemMap`, …) | `internal/fs` | moved | pass an `afero.Fs` through `Options.FS` (unchanged) — `afero.NewMemMapFs()` for hermetic tests; `(*Pulse).Fs()` returns the resolved filesystem | U02 |

## `encoding`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `Schema`, `Field`, `FieldType`, `FieldType*` constants, `ParseFieldType`, `ParseDate`, `Dictionary`, `WidenReport`, `SetWidthHeadroom` | unchanged | kept | none | — |
| `ReadHeader`, `ReadSchema`, `WriteSchema`, schema geometry, `ReadFieldValue` / `WriteFieldValue`, bitmap helpers (`ReadBitmap`, `WriteBitmap`, `BitmapIsNull`, `BitmapSetNull`) | kept as the public raw-byte primitive set — **ungrouped (`0x01`) cohorts only** | kept / narrowed | none for ungrouped writes; write grouped (`0x02`) cohorts through `CohortWriter` | U02 |
| `WriteHeader`, `MagicBytes`, `HeaderSize`, `FormatVersion`, `FormatVersionV1` / `V2`, `IsSupportedFormatVersion`, `SupportedFormatVersions` | unchanged | kept | none. `WriteHeader` always writes `FormatVersionV1` (`0x01`): the raw primitives are ungrouped-only | U02 |
| `ReadBit` / `WriteBit`, `ReadNibble` / `WriteNibble`, `Decimal128` + `Read/WriteDecimal128`, `SetMask` + `Read/WriteSetMask`, `ParseDateTime` / `FormatDateTime` / `DateTimeToDay`, `Group` / `GroupMember` / `GroupKind` (read-only nouns) | unchanged | kept | none — together these cover a raw ungrouped write of every field type | U02 |
| `NewRecordLocator` / `RecordLocator` | `RecordLocator` keeps the fields `Schema`, `RecordRegionStart`, `Stride`, `TotalRecords` and the method `Offset(i)`; the `ReadRecordAt` method (with `DecodePlan` / `FieldFilter` maps) is internal | narrowed | read records through `CohortReader.RecordAt(i)`, or compute byte offsets from the geometry and decode with `ReadFieldValue` | U02 (narrowing), U02c (`CohortReader`) |
| (new) | facade `CohortWriter` (schema + append rows) and `CohortReader` (`Schema()`, `Len()`, `RecordAt(i)`) | added | the recommended path; the raw primitives remain the expert escape hatch | U02c |
| `ReadPreamble` / `WritePreamble`, shard-archive (`Archive`, `OpenArchive`, `IsArchive`, `SchemaDoc`, …), cohesion and widen planners, group encoders / decoders and viability, dedup, decode plans and record readers, the sidecar index (`Index`, `ReadIndex`, `WriteIndex`, `HashKey`, …) and index-manifest helpers, `RefuseGroups` | `internal/encoding` | removed | use the facade verbs (`CreateShardArchive`, `AddShard`, `VerifyShardArchive`, `WidenSetField`, `Dedup`, `BuildIndex`, `CohortArtifacts`, …); `Index` survives as `pulse.SidecarIndex` | U02 |

## `io`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `io.ImportJob`, `ExportJob`, `ConvertJob`, `DedupJob`, `Transfer*Job`, their reports, `RowError`, `Reader`, `Writer` and the optional writer / reader interfaces | unchanged spelling, now aliases into `internal/io` / `internal/iocore` | alias-kept | none | U02 |
| `io.NewImportJob(source, target)` | unchanged | kept | none | — |
| `io.NewExportJob(source, writer)` | unchanged | kept | none. The caller constructs the writer (now with `io.NewWriter`) and **owns it**: call `writer.Close()` after `(*Pulse).Export` to flush it to the path | — |
| `io.NewConvertJob(…)` | `&io.ConvertJob{…}` | removed | build the struct literal directly | U02 |
| `io/csv.NewReader(fs, p)` (and the other `io/<fmt>.NewReader`) | `io.NewReader(io.FormatCSV, fs, p, io.ReaderOptions{})` | moved | swap the constructor; pick the format with an `io.Format` constant (`FormatCSV`, `FormatTSV`, `FormatNDJSON`, `FormatJSONArray`, `FormatArrow`, `FormatParquet`, `FormatExcel`, `FormatSPSS`) or `io.FormatFromPath(path)` | U02 |
| `io/{csv,tsv,ndjson,jsonarray,excel,arrow,parquet,spss}.NewWriter(fs, p[, opts])` | `io.NewWriter(io.Format<X>, fs, p, io.WriterOptions{…})` | moved | swap the constructor. The only writer sub-struct is `WriterOptions.SPSS` (`Charset`, `Uncompressed`, `SanitizeNames`, `IgnoreSidecar`); reader knobs ride `ReaderOptions.Excel{Sheet}` and `ReaderOptions.SPSS{Charset, MissingMode}` (typed `io.SPSSMissingAuto` / `SPSSMissingNull`). A sub-struct for another format is ignored | U02 |
| `*FromBytes` / `*ToBuffer` constructor variants | `io.NewReaderFromBytes(format, data, opts)` / `io.NewWriterToBuffer(format, opts)` (returns `io.BufferWriter`: a `Writer` with `Bytes()`) | moved | swap the constructor | U02 |
| `io/format.FromExt`, the `format.CSV` … constants, `format.SupportedImport` | `io.FormatFromPath`, `io.Format*`, `io.Formats()` + `Format.CanRead()` / `CanWrite()` | replaced | swap the spelling; `.xls` still maps to Excel. `io.FormatPulse` is recognised by `FormatFromPath` and refused by every factory | U02 |
| (new) | `PULSE_IO_FORMAT_UNSUPPORTED` coded error from the factory for an unknown format or an unsupported direction | added | branch on the code; an import-only format refuses a writer with its own coded error | U02 |
| every `io/<fmt>` subpackage, `io/format`, `io/jsonshared`, `io/exportoverlay`, `io/nullcell`, `io/settristate`, `io/setwide` | internal | moved | use the `io` factory | U02 |
| `io.InferSchema`, `InferSchemaWithOptions`, `InferOptions`, `InferenceResult`, `ParseGroupDecl`, `DefaultSetDelimiter`, `EmptySetCell`, `IsNullCell`, `DiscardWriter`, `MaxSetElements`, `SetTypeFor`, `WidestSetType`, `ErrStopIteration`, the `Compare*Overlay*` helpers, the transfer codec / level constants | internal | removed | inference runs inside `ImportJob`; drive it through the job and `pulse.Options` (`SetInferenceMinPct`, …) | U02 |

## `descriptor`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `Envelope`, `EnvelopeEntry`, `NewEnvelope`, `NewEnvelopeWithRequest`, `InspectResult`, `InspectOptions`, `PredictResult`, `Manifest` (+ sub-types), `ComponentSchema`, `ComponentKey`, `ShardInfo`, the `Mergeable` / `Partial` / `None` constants | unchanged | kept | none | — |
| `Predict`, `Inspect`, `InspectFromBytes`, `PredictFromBytes`, `PredictOptions`, `ExtensionsSnapshot`, `BuildManifest`, `BuildManifestWithExtensions`, `BuildPayloadSchema`, `SlimManifest`, `NormalizeRequest`, `ResolveDefaults`, `Validate*` and their `*ValidationResult` types, `OverlayCapabilities`, the mergeability helpers, `DefaultDictionaryLimit`, `PayloadSchemaFormatVersion` | `internal/descriptor` | moved | use the facade (`Manifest`, `Predict`, `PredictBytes`, `Inspect`, `InspectEnvelope`, `InspectBytes`); the payload schema stays reachable as `pulse schema` and the `pulse://schema` MCP resource | U02 |

## Other packages

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `synth` | public, narrowed (alias facade over `internal/synth`): `Spec`, `FieldSpec`, `Options`, `Result`, `Profile`, `ProfileOptions`, the profile / spec / fidelity sub-types (`FidelityReport` family), `Dist*` constants, `Synth`, `SynthBytes`, `ProfileFile`, `ProfileBytes`, `SpecFromProfile`, `ParseSpec`, `WriteSpec` | narrowed | none for fixture building — `synth.Synth(fs, spec, out, opts)` and `synth.SynthBytes` stay public | U02 |
| `synth.AllDistributions`, `ApplyRulesFile`, `AugmentFromProfile`, `ParseRules`, `ResolveConflicts`, `WriteRuleCandidates`, `SyntheticAsCategoricalSchema`, `SyntheticFieldName`, the `Build*` fidelity builders (`BuildFidelityReport`, `BuildModelFidelity`, `BuildPairwise`, …), `GroupWarnings`, `CountWarningsNeedingAttention`, `WarningGroup`, `TestRunner`, the tuning constants (`ContingencyCellCap`, `MinPairObservations`, `MinShapeFitObservations`, `ModelRecoveryTolerance`, `ResidualRecoveryTolerance`, `RunContinuationHighThreshold`) | internal | removed | generate through `(*Pulse).Synth` / `synth.Synth` — with `SynthOptions{SourceCohort, FidelityReportPath}` the instance augments from the source profile and writes the fidelity report; apply rules through `Spec.Rules` (`synth.ParseSpec`); read distributions from the manifest's `synth_distributions` | U02 |
| `errors` | public **in full** (left whole, not split): `CodedError`, `Code` + every code constant, constructors, `HasCode`, `WrapCodedError`, `Lookup`, `LookupResult`, `Metadata`, `MetadataFor`, `Fixup`, `ByDomain`, `Search`, `AllCodes`, `AllDomains`, `Domain`, `ParseCode`, `SortedCodeNames`, the `Detail*` keys | kept | none | U02 |
| `types` | public in full; Go names **and** JSON field names frozen | kept | none | — |
| `imports`, `template`, `examples` | internal; root aliases for facade-returned types | moved | use the root spellings above | U02 |
| — | `pulse.Version()` | added | read the real Pulse build version (the release tag, the `go install` module version, or `devel`) | U01 |
| `mcpserve.Options.Version` empty → `"1.0.0"` | empty → `pulse.Version()` | behaviour change | none; set `Options.Version` to keep a custom identity | U01 |
| `gosdk.Config.Version` empty → `""` | empty → `pulse.Version()` | behaviour change | none; set `Config.Version` to keep a custom identity | U01 |
| `mcp/gosdk` | public, narrowed: `Register`, `Config`, `RegisteredTools`, `RegisteredPrompts`, the URI and prompt-name constants; `Config.Core()` removed | narrowed | none unless you called `Config.Core()` (no replacement) | U02 |
| `mcpserve` | public, unchanged: `Options`, `Serve`, `ServeStdio` | kept | none | — |
| `mcp` core, `mcp/toolmeta`, `skills` | internal | moved | use the manifest (`(*Pulse).Manifest`, `pulse manifest --json`), the MCP tools, or `pulse skills list` / `show` | U02 |

## Third-party dependency

- `afero.Fs` is a frozen third-party type in the v1 API (`Options.FS`, the `io` factory). No change; no Pulse-owned filesystem interface replaces it.
