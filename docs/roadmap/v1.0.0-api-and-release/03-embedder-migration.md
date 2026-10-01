# 03 — Embedder migration guide

**Status:** decided · **Target:** v1.0.0 · **Applies:** as U01, U02, U02b and U02c land

## Purpose

[00](00-public-surface.md) decides which packages stay public at v1.0.0 and which move under `internal/`. This guide lists every resulting contract difference an embedder can hit — old spelling, new spelling, and the mechanical adaptation — so a library that imports Pulse today can upgrade without reading the unit diffs.

It applies in stages: `pulse.Version()` and the MCP version defaults land with **U01**; the root, `encoding`, `io`, `descriptor` and other-package rows land with **U02**; the extension-authoring rows land with **U02b**; `CohortReader` / `CohortWriter` and `PredictResult.CrosstabFusable` land with **U02c**. Each row names its unit. Anything not listed keeps its spelling and behaviour.

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
| `(*Pulse).Service()` + `service.(*Service).SetDisableCrosstabFusion` | `pulse.Options{DisableCrosstabFusion: true}` | removed / replaced | set the option at `pulse.New` instead of mutating the service | U02 |
| `descriptor.InspectFromBytes(data, opts)` | `(*Pulse).InspectBytes(data, opts)` | replaced | call on the instance | U02 |
| `descriptor.PredictFromBytes(data, req, opts)` | `(*Pulse).PredictBytes(data, req)` | replaced | call on the instance; the extension snapshot is now filled automatically (previously an unset snapshot treated extension operators as unknown) | U02 |
| `(*Pulse).Lookup` | unchanged | kept | none | — |

## Engine packages → internal

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `processing.CanFuseCrosstab(req, schema)` | `PredictResult.CrosstabFusable` (from `Predict` / `PredictBytes`) | replaced | run predict and read the field; a parity test keeps it equal to the runtime decision | U02c |
| `processing.*` extension-authoring interfaces and factories (`Aggregator`, `Grouper`, `FiltererBuilder`, `Record`, `*Factory`, …), `processing/feature`, `processing/window` | `github.com/frankbardon/pulse/extend` | moved + reshaped | port custom operators to `extend`; `Record` becomes a small read-only interface (values, nulls, wide and set accessors). Registration through `pulse.Options.Extensions` is unchanged | U02b |
| `processing.ApplyOverlays`, `CompileDateRanges`, `NewCrosstabHostViewWithComponents` | internal, no replacement | removed | drive overlays through `Request.Overlays` / `(*Pulse).ApplySeriesOverlays`; range tables are validated at `pulse.New` | U02 |
| `service` package | `internal/service` | moved | use the root aliases above | U02 |
| `fs` package | internal | moved | pass an `afero.Fs` through `Options.FS` (unchanged) | U02 |

## `encoding`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `Schema`, `Field`, `FieldType`, `FieldType*` constants, `ParseFieldType`, `ParseDate` | unchanged | kept | none | — |
| `ReadHeader`, `ReadSchema`, `WriteSchema`, schema geometry, `ReadFieldValue` / `WriteFieldValue`, bitmap helpers | kept as the public raw-byte primitive set — **ungrouped (`0x01`) cohorts only** | kept / narrowed | none for ungrouped writes; write grouped (`0x02`) cohorts through `CohortWriter` | U02 |
| `NewRecordLocator` / `RecordLocator` | geometry only (offset, stride, count); `ReadRecordAt` with `DecodePlan` / `FieldFilter` maps → internal | narrowed | read records through `CohortReader.RecordAt(i)`, or compute byte offsets from the geometry | U02 (narrowing), U02c (`CohortReader`) |
| (new) | facade `CohortWriter` (schema + append rows) and `CohortReader` (`Schema()`, `Len()`, `RecordAt(i)`) | added | the recommended path; the raw primitives remain the expert escape hatch | U02c |
| index-manifest helpers (`SidecarIndexPath`, `IndexManifestPath`, `ReadIndexManifest`, `WriteIndexManifest`, `NewIndexManifest`) | internal; new `(*Pulse).IndexArtifacts(cohort) []string` | replaced | manage indexes with `BuildIndex` / `ListIndexes` / `VerifyIndex` / `DropIndex`; to move a cohort with its sidecars, copy every path `IndexArtifacts` returns | U02 |

## `io`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `io.NewImportJob`, `NewExportJob`, `ImportReport`, `ExportReport`, `RowError`, `Writer`, `Reader` | unchanged | kept | none | — |
| `io/csv.NewReader(fs, p)` | `io.NewReader(io.FormatCSV, fs, p, io.ReaderOptions{})` | moved | swap the constructor | U02 |
| `io/{csv,tsv,ndjson,jsonarray,excel,arrow}.NewWriter(fs, p[, opts])` | `io.NewWriter(io.Format<X>, fs, p, io.WriterOptions{...})`; format knobs on typed sub-structs (`WriterOptions.Excel`, `.SPSS`, …) | moved | swap the constructor; move format options onto the sub-struct | U02 |
| `*FromBytes` / `*ToBuffer` constructor variants | `io.NewReaderFromBytes(format, data, opts)` / `io.NewWriterToBuffer(format, opts)` | moved | swap the constructor | U02 |
| every `io/<fmt>` subpackage, `io/format` | internal | moved | use the `io` factory | U02 |

## `descriptor`

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `Envelope`, `EnvelopeEntry`, `NewEnvelope`, `NewEnvelopeWithRequest`, `InspectResult`, `InspectOptions`, `PredictResult`, `Manifest` (+ sub-types), `ComponentSchema`, `ComponentKey`, `ShardInfo` | unchanged | kept | none | — |
| `PredictOptions`, `ExtensionsSnapshot`, `BuildManifest`, `BuildPayloadSchema`, capability builders | internal | moved | use the facade (`Manifest`, `PredictBytes`); the payload schema stays reachable as `pulse schema` and the `pulse://schema` MCP resource | U02 |

## Other packages

| Old | New | Kind | How to adapt | Unit |
|---|---|---|---|---|
| `synth` | public, narrowed: `Spec`, `FieldSpec`, `Options`, `Result`, `Profile`, `ProfileOptions`, distribution-spec types, `Synth`, `SynthBytes`; generators, capture and detectors internal | narrowed | none — `synth.Synth(fs, spec, out, opts)` and `synth.SynthBytes` stay public for fixture building | U02 |
| `errors` | public, narrowed: `CodedError`, `Code` + every code constant, `Lookup`, `LookupResult`, `Fixup`, `SortedCodeNames`; metadata tables internal | narrowed | none | U02 |
| `types` | public in full; Go names **and** JSON field names frozen | kept | none | — |
| `imports`, `template`, `examples` | internal; root aliases for facade-returned types | moved | use the root spellings (`pulse.ImportSpec`, `pulse.ImportResult`, `pulse.ImportEntry`, `pulse.Example`, …) | U02 |
| — | `pulse.Version()` | added | read the real Pulse build version (the release tag, the `go install` module version, or `devel`) | U01 |
| `mcpserve.Options.Version` empty → `"1.0.0"` | empty → `pulse.Version()` | behaviour change | none; set `Options.Version` to keep a custom identity | U01 |
| `gosdk.Config.Version` empty → `""` | empty → `pulse.Version()` | behaviour change | none; set `Config.Version` to keep a custom identity | U01 |
| `mcp/gosdk`, `mcpserve` | public, narrowed to `gosdk.Register` + `gosdk.Config`, and `mcpserve.Options` + the serve entry point | narrowed | none | U02 |
| `mcp` core, `mcp/toolmeta`, `skills` | internal | moved | use the manifest (`(*Pulse).Manifest`, `pulse manifest --json`) or the MCP tools | U02 |

## Third-party dependency

- `afero.Fs` is a frozen third-party type in the v1 API (`Options.FS`, the `io` factory). No change; no Pulse-owned filesystem interface replaces it.
