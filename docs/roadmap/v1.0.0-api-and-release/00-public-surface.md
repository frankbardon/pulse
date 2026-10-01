# 00 — Public Go surface

**Status:** decided · U02 landed (amendments below) · **Target:** v1.0.0 · **Owner of the decision:** project maintainer, informed by a catalog of downstream usage ([appendix](#appendix-downstream-usage-catalog))

## Why this must happen before v1.0.0

Under Go module semantics, tagging `v1.0.0` promises that **no exported identifier in any non-`internal/` package** breaks until `v2`, and a `v2` means a new import path. Today every top-level package except `cmd/` and `internal/` is importable:

```
descriptor/  encoding/  errors/  examples/  fs/  imports/  io/  mcp/  mcpserve/
processing/  service/  skills/  synth/  template/  types/  (+ the root package pulse)
```

CLAUDE.md treats `pulse.go` as *the* public API, but the module layout does not enforce that. Anything left exported at v1.0.0 is frozen, including packages that are clearly implementation detail (`processing`, `service`). Moving a package under `internal/` after v1 is a breaking change.

## Principle

The classification follows the CLAUDE.md design principle:

> **Embedder-first, consumer-agnostic.** Library embedders are a first-class audience: the public Go surface is sized for them, and real downstream usage catalogs are valid evidence for what it must cover. Pulse never depends on a consumer — no reverse imports, no consumer names in contract docs or code, and every public symbol is justified by a general embedder use case, not one consumer's convenience. Harnesses discover Pulse via `pulse manifest --json` + the embedded skills.

So the catalog decides *what must be covered*, and each row below is justified by a general embedder use case (writing cohorts, mirroring the wire payload, mounting MCP, authoring operators), never by the catalog's author.

## Model (decided: C, hybrid)

- **Nouns public.** Vocabulary packages — the wire payload (`types`), the schema and file format (`encoding`), result shapes (`descriptor`), error codes (`errors`), synth specs (`synth`) — are public. They are already frozen in practice by the `.pulse` format promise and the payload-schema promise; freezing their Go spelling adds little new constraint.
- **Verbs via the facade.** Operations are methods on `*pulse.Pulse`. Verbs are where engine internals leak (options structs, snapshots, plans), so a verb lives on the facade unless an embedder genuinely needs it as a standalone package function.
- **Engine internal.** `processing`, `service`, the I/O adapters, the MCP core and every support package move under `internal/`. Extension authors get a new purpose-built public package, `extend`, instead of reaching into `processing`.

## Process

1. **Catalog** — done. The maintainer catalogued every Pulse import in the downstream library; summary in the [appendix](#appendix-downstream-usage-catalog).
2. **Classify** — done; the decisions table below. Classes:
   - **Public** — frozen at v1, covered by `STABILITY.md`.
   - **Public, narrowed** — stays importable; only the kept subset stays exported, the remainder moves to an `internal/` twin.
   - **Internal** — moves under `internal/`. Root aliases re-export any type a facade signature needs.
   - **New** — a public package that does not exist yet.
3. **Move** in one milestone, before any other v1 work lands on the same files — see [Unit split](#unit-split).
4. **Freeze with tooling.** After the move, a pinned `apidiff -m` job runs in CI against the latest tag — advisory until the `v1.0.0` tag, blocking after (`api-break-ok` label is the only override). Because `apidiff` compares a root alias into `internal/` by name only, the blocking `TestPublicAPIGolden` (every public package's exported shape, aliases expanded into their targets' fields and methods) closes that gap, and an external embedder smoke module (`make smoke`) compiles the migration-guide spellings.

## Decisions

Every package under the module root has a class. "Forced public" in the rationale column means the package is named in an exported root signature today (see [What leaks through the facade](#what-leaks-through-the-facade)); "freely internal" means nothing exported references it.

| Package | Class (decided) | Kept subset | Internal remainder | Rationale (general embedder use case) |
|---|---|---|---|---|
| root `pulse` | Public | the facade: `New`, `Options`, `Pulse` and its methods, request/response aliases, extension registration types, labels / lookup / range tables | — | the primary API; every verb lives here. Gains the [facade additions](#facade-additions); loses `(*Pulse).Service()` |
| `types` | Public (in full) | everything | — | embedders build requests and mirror the wire payload, often sending request JSON straight from a service or web page. **Go names AND JSON field names are frozen** — the payload-schema golden guards JSON, the API check guards Go |
| `encoding` | Public, narrowed (split in place; remainder in `internal/encoding`) | `Schema`, `Field`, `FieldType` + every `FieldType*` constant, `ParseFieldType`, `ParseDate`, `Dictionary`, `WidenReport`, `SetWidthHeadroom`; raw-byte primitives `ReadHeader`, `ReadSchema`, `WriteSchema`, schema geometry (`RecordByteSize`, per-field byte offset and bit position, `BitmapByteSize`), `ReadFieldValue` / `WriteFieldValue`, bitmap helpers; **gap identifiers (U02):** `WriteHeader` (writes `0x01`), `MagicBytes`, `HeaderSize`, `FormatVersion` + `FormatVersionV1` / `V2`, `Read/WriteBit`, `Read/WriteNibble`, `Decimal128` + read/write, `SetMask` + read/write, `ParseDateTime` / `FormatDateTime` / `DateTimeToDay`, read-only `Group` / `GroupMember` / `GroupKind`; `RecordLocator` reduced to geometry (`Schema`, `RecordRegionStart`, `Stride`, `TotalRecords`, `Offset(i)`) | `DecodePlan`, `FieldFilter`, wide plans, projection, the map-filling `ReadRecordAt`, `Read/WritePreamble`, archives, groups encode/decode, the sidecar index, index-manifest I/O (`SidecarIndexPath`, `IndexManifestPath`, `Read/Write/NewIndexManifest`) | embedders inspect schemas and write cohorts byte-for-byte; the layout is already promised readable forever. **Raw-byte primitives are supported for ungrouped `0x01` cohorts only** — group dictionaries are too intricate to hand-write, so grouped (`0x02`) cohorts go through `CohortWriter`. Forced public (`ResolveCanonicalSchema`, `Cohort` methods, extension `Accepts`) |
| `io` | Public, narrowed (**alias facade**: implementation in `internal/io`, contracts in the `internal/iocore` leaf) | `ImportJob` / `ExportJob` / `ConvertJob` + `NewImportJob` / `NewExportJob`, their reports, `RowError`, `Reader` / `Writer` + optional interfaces, forced types `GroupDecl`, `GroupReport`, `GroupDetection`, `DedupReport`, `LabelResolver`, and the forced closure (`Transfer*`, `DedupJob`, `PredictReport`, `ImportProjection`, `GroupCandidate`, `InferenceWarning`, `LabelWarning`); **promoted factory** `io.NewReader(format, fs, path, ReaderOptions)`, `io.NewReaderFromBytes`, `io.NewWriter(format, fs, path, WriterOptions)`, `io.NewWriterToBuffer` (→ `BufferWriter`); typed string-backed `io.Format` constants + `FormatFromPath`, `Formats`, `CanRead` / `CanWrite`; knobs on typed sub-structs — as landed, `ReaderOptions.Excel{Sheet}`, `ReaderOptions.SPSS{Charset, MissingMode}`, `WriterOptions.SPSS{…}` only | everything in the subpackages below; inference, set-cell helpers, `NewConvertJob` | embedders import and export tabular data with a format chosen at runtime; one factory keeps format adapters free to change. Forced public (`Import`, `Export`, `Convert`, `Dedup` signatures) |
| `io/csv`, `io/tsv`, `io/ndjson`, `io/jsonarray`, `io/jsonshared`, `io/arrow`, `io/parquet`, `io/excel`, `io/spss` | Internal | — | all | reached through the `io` factory and the `Reader` / `Writer` interfaces. Freely internal |
| `io/format`, `io/exportoverlay`, `io/nullcell`, `io/settristate`, `io/setwide` | Internal | — | all | adapter helpers; `io/format` is deleted, its dispatch promoted into the `io` factory (`io/format.go`, `io/factory.go`). Freely internal |
| `descriptor` | Public, narrowed (split in place; remainder in `internal/descriptor`) | `Envelope`, `EnvelopeEntry`, `NewEnvelope`, `NewEnvelopeWithRequest`, `InspectResult`, `InspectOptions`, `PredictResult`, `Manifest` + sub-types, `ComponentSchema`, `ComponentKey`, `ShardInfo` | `PredictOptions`, `ExtensionsSnapshot`, `BuildManifest`, `BuildPayloadSchema`, capability builders, `InspectFromBytes`, `PredictFromBytes` | embedders re-emit the standard envelope and read manifest / inspect / predict results. Builders and the snapshot are implementation; byte-level inspect and predict become facade methods. Forced public (`Inspect`, `Predict`, `Manifest`, extension `ComponentSchema`) |
| `errors` | Public **(amended: left whole, not split)** | everything — `CodedError`, `Code` + every code constant, constructors, `HasCode`, `WrapCodedError`, `Lookup`, `LookupResult`, `Fixup`, `Metadata`, `ByDomain`, `Search`, `AllCodes`, `AllDomains`, `ParseCode`, `SortedCodeNames`, `Detail*` keys | — | code strings reach end users inside envelopes; embedders branch on them. Forced public. Splitting the metadata tables out bought little and cost a twin package |
| `synth` | Public, narrowed (**alias facade** over `internal/synth`) | `Spec`, `FieldSpec`, `Options`, `Result`, `Profile`, `ProfileOptions`, distribution-spec types and `Dist*` constants, `Synth`, `SynthBytes`; **gap identifiers (U02):** `ProfileFile`, `ProfileBytes`, `SpecFromProfile`, `ParseSpec`, `WriteSpec`, the `FidelityReport` family | generators, capture, structural-rule detectors, `Build*` fidelity builders, rule-file plumbing, tuning constants | embedders build test fixtures without a facade instance. Forced public (`Synth`, `Profile` aliases) |
| `extend` | **New**, public | extension-authoring contract: `Aggregator`, `OnlineAggregator`, `Grouper` + streaming variants, `FiltererBuilder` / `FilterFunc`, attribute and test interfaces, window / feature computers, factory types, a small read-only `Record` interface | — | embedders author custom operators against a contract that does not freeze the engine's record representation. Built-ins keep their concrete fast path. Delivered by U02b |
| `processing` | Internal (**interim: still public until U02b**) | — | all | engine. Forced public today only through extension factories, `MemberSet` and `DateRangeSpec`; those move to `extend` and root-native types. Test-only helpers `ApplyOverlays`, `CompileDateRanges`, `NewCrosstabHostViewWithComponents` go internal with no replacement; `CanFuseCrosstab` is replaced by `PredictResult.CrosstabFusable` |
| `processing/feature`, `processing/window` | Internal (interim: still public until U02b) | — | all | forced public today only through `FEAT_*` / `WIN_*` extension factories, which move to `extend` |
| `processing/regression`, `processing/arena` | Internal | — | all | freely internal |
| `service` | Internal | — | all | orchestration. Its 13 facade-returned result types stay reachable as root aliases; `(*Pulse).Service()` is removed |
| `fs` | Internal | — | all | freely internal; embedders pass an `afero.Fs` through `Options.FS` (unchanged) |
| `imports` | Internal | — | all | root aliases `ImportSpec`, `ImportResult`, `ImportEntry` cover the facade |
| `template` | Internal | — | all | root aliases cover the template facade methods |
| `examples` | Internal | — | all | root aliases `Example`, `ExampleSummary`; reached through `ExampleGet` / `ExamplesSearch` and MCP |
| `skills` | Internal | — | all | reached through MCP and the manifest. Freely internal |
| `mcp` (core) | Internal | — | all | the SDK-free tool catalog; embedders mount it through `mcp/gosdk` |
| `mcp/gosdk` | Public, narrowed (unexported in place) | `Register`, `Config`, `RegisteredTools`, `RegisteredPrompts`, the URI and prompt-name constants | `Config.Core()` and everything else; the extension snapshot reaches it through `internal/facadebridge` | embedders mount Pulse's tools into their own MCP server |
| `mcp/toolmeta` | Internal | — | all | leaf metadata shared by `descriptor` and the core. Freely internal |
| `mcpserve` | Public (amended: already minimal, unchanged) | `Options`, `Serve`, `ServeStdio` | — | embedders run a ready-made Pulse MCP server |
| `cmd/pulse`, `internal/cli`, `internal/shardfixtures`, `internal/spsstest` | (unchanged) | — | — | the binary and existing `internal/` packages are outside the Go promise already |

### Amendments at implementation (U02)

Recorded when U02 landed; the rows above already carry them.

| Decision | Planned | Landed | Why |
|---|---|---|---|
| `errors` | public, narrowed (metadata tables internal) | left whole | the "remainder" was the code metadata embedders already read through `Lookup` / `Search` / `ByDomain`; a twin package bought no freedom |
| Gap identifiers | not classified | kept public in `encoding` (header / bit / nibble / decimal / set-mask / datetime primitives, `Group*` nouns, `RecordLocator` fields), `io` (forced closure types), `synth` (`ProfileFile`, `ProfileBytes`, `SpecFromProfile`, `ParseSpec`, `WriteSpec`, `FidelityReport` family), `errors` (everything) | a raw ungrouped write must cover all 20 field types; forced closures cannot be aliased away |
| `IndexArtifacts(cohort) []string` | index sidecars only, no ctx / error | `CohortArtifacts(ctx, cohort) ([]string, error)` covering `.idx`, `.indexes.json`, `.spss.json`, `.meta.json` | moving a cohort needs every Pulse-owned sidecar; callers must also preserve the cohort's mtime |
| `InspectBytes` / `PredictBytes` | `(data, opts)` / `(data, req)` | ctx first, `*descriptor.Envelope` return; `PredictBytes` fills snapshot + `Strict` + `EchoRequest` from `Options`; `InspectBytes` has no `Strict` / `EchoRequest` (inspect has neither) | avoids the result-only-wrapper-drops-warnings trap `Inspect` already has |
| API-compat check | `gorelease` or `apidiff` | pinned `apidiff -m` (advisory, `api-break-ok` label) **plus** blocking `TestPublicAPIGolden` | verified: neither tool sees fields or methods behind a root alias into `internal/`; the golden expands every alias |
| Narrowing technique | an `internal/` twin per package | `encoding` / `descriptor` split in place; `io` / `synth` whole-package internal behind an **alias facade** (`io` contracts in the `internal/iocore` leaf, gated by `TestIOImportBoundary*`); `mcp/gosdk` unexported in place; `errors` / `mcpserve` untouched | `io`'s factory imports every adapter, so adapters cannot import `io`; `synth`'s kept types are woven through the generator |
| Root aliases for leaked `encoding` types | not planned | `pulse.CohesionWarning`, `pulse.GroupIndexHeadroom`, `pulse.SidecarIndex` plus its whole field closure — `pulse.SidecarIndexKeySpec`, `pulse.SidecarIndexBucket`, `pulse.SidecarIndexEntry`, `pulse.CohortFingerprint` (maintainer decisions, 2026-10-01) | they sit inside facade results (`VerifyResult`, `SetWidthHeadroom`, `BuildIndexResult.Index`) after `encoding` narrowed; aliasing the closure lets an external module name and walk the index, not just select through it |
| `ExportJob.Hash` / `ConvertJob.Hash` | not classified | **kept public** (maintainer decision, 2026-10-01) | the job hash is the stable identity embedders key caches and dedup on; it freezes with the job types |
| Ordering | factory (E1) before the moves (E2) | the `io` structural move into `internal/io` + `internal/iocore` landed with the factory story; the later narrowing story only trimmed the facade | the factory and the cycle break could not be built separately |
| Template target / var-type constants | not classified (aliases only; compare `.String()`) | root constants `pulse.TemplateTarget*` and `pulse.TemplateVar*` for every value, each equal to the internal value; `TestTemplateConstants_RootSetComplete` fails on an internal value with no root spelling | an alias carries the type but not its constants; embedders could not name a target without a string compare |
| `processing` interim | keep in place or move with aliases | kept public in place; U02b moves it | avoids throwaway aliases and double path churn for extension authors |

### Root aliases into internal packages

Go permits a root alias of an `internal/` type, and callers can use it — so facade-returned types keep their spelling without keeping their package public. Decided aliases: the 13 `service` result types (`ComposeOptions`, `Row`, `RowIter`, `BuildIndexResult`, `VerifyIndexResult`, `IndexFreshnessReason`, `IndexInfo`, `CreateShardArchiveResult`, `AddShardResult`, `SetWidening`, `GroupReconciliation`, `VerifyResult`, `ShardEntry`), `imports.Spec` / `Result` / `Entry` (as `ImportSpec` / `ImportResult` / `ImportEntry`), and the template and examples result types. As landed, the set also includes `ImportSidecar`, `Template` / `TemplateSummary` / `RenderedTemplate` / `TemplateTarget` / `TemplateVarType` / `TemplateVariable`, and — by maintainer decision (2026-10-01) — the encoding types leaked inside facade results: `CohesionWarning`, `GroupIndexHeadroom` and `SidecarIndex` (`encoding.Index`) together with the index's field closure `SidecarIndexKeySpec`, `SidecarIndexBucket`, `SidecarIndexEntry` and `CohortFingerprint`. An aliased type's fields and methods freeze exactly as if its package were public; `apidiff` cannot see behind the alias, so `TestPublicAPIGolden` covers them.

`DateRangeSpec`, `MemberSet` and `LoadMemberSetResult` are the exception: they become **root-native types** (not aliases into `processing`), spelling unchanged.

### Fixed points

- **Raw-byte primitives = ungrouped `0x01` only.** The `encoding` primitive set promises byte-level writes of ungrouped cohorts. Grouped (`0x02`) raw writes are permanently out of the v1 public primitive set; `CohortWriter` handles grouped cohorts. Reading stays covered for both versions by the file-format promise.
- **`afero.Fs` is frozen.** It appears in `Options.FS` and the `io` factory signatures; it is accepted as a frozen third-party type in the v1 API. No Pulse-owned filesystem interface replaces it.

## Facade additions

Decided here; implemented by the units named.

| Addition | Replaces / covers | Unit |
|---|---|---|
| `Options.DisableCrosstabFusion bool` | `(*Pulse).Service()` + `SetDisableCrosstabFusion`, its only known use; `Service()` is removed | U02 |
| `(*Pulse).InspectBytes(ctx, data, *InspectOptions) (*Envelope, error)` | `descriptor.InspectFromBytes`; envelope returned whole. Takes no `Strict` / `EchoRequest` — inspect has neither | U02 |
| `(*Pulse).PredictBytes(ctx, data, req) (*Envelope, error)` | `descriptor.PredictFromBytes`; the instance fills the extension snapshot, `Strict` and `EchoRequest` (previously unset ⇒ extension operators treated unknown) | U02 |
| `(*Pulse).CohortArtifacts(ctx, cohort) ([]string, error)` (renamed from the planned `IndexArtifacts`) | the index-manifest helpers; every existing Pulse-owned sidecar (`.<keyhash>.idx`, `.indexes.json`, `.spss.json`, `.meta.json`) to move with a cohort — the move must preserve the cohort's mtime, which the sidecars fingerprint | U02 |
| root-native `DateRangeSpec`, `MemberSet`, `LoadMemberSetResult` | aliases into `processing` | U02 |
| `io` factory + typed `io.Format` constants + `PULSE_IO_FORMAT_UNSUPPORTED` | direct `io/<fmt>` constructors, `io/format.FromExt` | U02 |
| `CohortReader` (`Schema()`, `Len()`, `RecordAt(i)`) and `CohortWriter` (schema + append rows; handles grouped cohorts) | map-filling `ReadRecordAt`; the recommended path over raw primitives | U02c |
| `PredictResult.CrosstabFusable` (no-execute) + runtime parity test, mirroring `Streamable` / `CanStreamRequest` | `processing.CanFuseCrosstab` | U02c |
| `extend` package | `processing` extension interfaces and factories | U02b |

`(*Pulse).Lookup` is unchanged.

## What leaks through the facade

Research over the exported root signatures (`main` @ `b3d15ba`) found:

- **Forced public today:** `types`, `encoding`, `errors`, `descriptor`, `processing` (+ `feature`, `window`), `service`, `io`, `imports`, `synth`, `template`, `examples`. Each is named in an exported root signature or alias.
- **Freely internal today:** `fs`, `skills`, `mcp/toolmeta`, `processing/regression`, `processing/arena`, every `io/<fmt>` and helper subpackage. `mcp/gosdk` and `mcpserve` import root rather than the reverse, so they are free from a leak standpoint; they stay public as embedder products.
- **Closing the forced set** therefore needs: root aliases (`service`, `imports`, `template`, `examples`), root-native `DateRangeSpec` / `MemberSet`, removing `Service()`, and — the hard one — the `extend` package for extension factories and the `Record` they operate on.
- **Transitive closure of the public set is clean:** `types` imports nothing from Pulse; `descriptor` exposes only `types` + `encoding`; `encoding` exposes `errors`; `io` exposes `encoding`, `errors`, `types`.

## Unit split

The decided scope is too large for one Flow unit, so U02 is split (suffix IDs; U03–U33 keep their numbers):

| Unit | Size | Scope | Depends on |
|---|---|---|---|
| [U02 public-surface](../units/U02-public-surface.md) | L | apply the classification: moves, root aliases, narrowing of `encoding` / `io` / `descriptor` / `errors` / `synth` / `mcp`; leak-closing replacements (`Options.DisableCrosstabFusion`, `InspectBytes` / `PredictBytes`, `io` factory + typed formats, `CohortArtifacts`, root-native `DateRangeSpec` / `MemberSet`); gate path updates; the API-compatibility CI job; CLAUDE.md, `.claude/reference` and `docs/src/internals` path updates | — |
| [U02b extension-contract](../units/U02b-extension-contract.md) | L | the public `extend` package, registration adapters, built-in vs adapted parity tests; `processing`, `processing/feature`, `processing/window` fully internal; extension-points docs | U02 |
| [U02c cohort-facade](../units/U02c-cohort-facade.md) | M | `CohortReader` / `CohortWriter`; `PredictResult.CrosstabFusable` + parity gate and its payload-schema companions (additive, `format_version` stays `"1.1"`) | U02 |

If U02 lands before `extend` exists, it either keeps `processing` where it is or moves it with temporary root aliases for the extension types; U02b removes any interim aliases.

## Embedder migration

Every contract difference above — old spelling, new spelling, kind and how to adapt — is recorded in [03-embedder-migration.md](03-embedder-migration.md). It is the hand-off document for downstream embedders as U02, U02b and U02c land.

## Interaction with other themes

- **Feature profiles** and **guided analysis** add new public types (`Profile`, `Purpose`, `Interpretation`). They are born in their final package — a public noun package or the root — never in a package this audit makes internal.
- **New operators** in U04, U07, U15 and U20 are born against `extend` (those units depend on U02b), so no new operator is written against the soon-internal `processing` interfaces.
- `descriptor.ExtensionsSnapshot` and the planned `InstanceSnapshot` are implementation types. Decided: internal; the facade fills them.
- The docs audit (U32) depends on U02, U02b and U02c so it audits the final surface.

## Deliverables

- [x] Downstream catalog completed (maintainer)
- [x] Classification decided per package
- [x] Package moves / narrowing done; facade re-exports added (U02)
- [ ] `extend` package; `processing` fully internal (U02b)
- [ ] `CohortReader` / `CohortWriter`; `PredictResult.CrosstabFusable` (U02c)
- [x] API-compatibility check in CI against the latest release tag (U02: advisory `apidiff` + blocking `TestPublicAPIGolden`)
- [x] Public package list recorded in the `STABILITY.md` draft ([02](02-stability-policy.md)); the root file lands with U33
- [ ] Embedder migration guide handed off ([03](03-embedder-migration.md)) — U01 and U02 rows landed and compiled by the smoke module; U02b / U02c rows pending

## Appendix: downstream usage catalog

The catalog that satisfied step 1, produced by the maintainer from **Orbit**, the downstream library that writes `.pulse` files and serves Pulse queries to its own consumers. This appendix is the only place in `docs/` the downstream is named; every decision above stands on a general embedder use case. Method calls were matched by name, not type (common names checked by hand), so the decisions are package-level and the migration guide covers every listed name either way.

| Area | What the downstream uses | Decision |
|---|---|---|
| root `pulse` (32 production files) | `New`, `Options` (`FS`, `Extensions`, `AutoLabels`, `EchoRequest`, `ShardWorkers`, `DecodeWorkers`, `DisableComponents`, `DisableDefaults`, `DisableProjection`), `Pulse`; request/response types incl. `ComposeOptions`, `DateRangeSpec`, `BuildIndexResult`, `SetWidening`; `Extensions`, `ExprFunction`, `LabelTable`, `LabelBinding`, `LabelModeAugment`, `LookupTable`, `RangeTable`, `LoadMemberSetFromReader`; query, file-reading, index/filter (`BuildIndex`, `FilterToFile*`, `Export`) and shard-archive methods | all kept; `service` result types become root aliases, `DateRangeSpec` root-native |
| `types` | core request / crosstab / label types, overlay types and pairwise kinds, `AGG_*` / `FILTER_*` / `GROUP_*` constants; request JSON sent near-verbatim from its own service and web pages | public in full; Go and JSON names frozen |
| `encoding` | `Schema`, `Field`, `FieldType`, `ParseFieldType`, `ParseDate`, field-type constants; `ReadHeader`, `ReadSchema`, `WriteSchema`, `NewRecordLocator`, `RecordLocator` for a byte-level fill that reads records by index and writes raw record bytes | schema nouns and raw primitives kept (ungrouped `0x01`); `RecordLocator` narrowed to geometry; `CohortReader` / `CohortWriter` added |
| `encoding` index manifests | `SidecarIndexPath`, `IndexManifestPath`, `Read/Write/NewIndexManifest` | internal; `(*Pulse).CohortArtifacts` |
| `descriptor` | `Envelope`, `EnvelopeEntry`, `NewEnvelope`, `NewEnvelopeWithRequest`, `InspectOptions`, `InspectResult`, `PredictResult`, `Manifest` (re-exported through its own public package) | kept |
| `descriptor` byte-level | `InspectFromBytes`, `PredictFromBytes`, `PredictOptions` | `(*Pulse).InspectBytes` / `PredictBytes`; `PredictOptions` internal |
| `errors` | `CodedError`, `Lookup`, `LookupResult`, `SortedCodeNames`; code strings reach end users | kept |
| `io` | `NewImportJob`, `NewExportJob`, `ImportReport`, `ExportReport`, `RowError`, `Writer` | kept |
| `io/<fmt>` | `io/csv.NewReader` / `NewWriter`; `NewWriter` from `tsv`, `ndjson`, `jsonarray`, `excel`, `arrow` | internal; `io.NewReader` / `io.NewWriter` factory |
| tests / examples only | `processing.CanFuseCrosstab`; `processing.ApplyOverlays`, `CompileDateRanges`, `NewCrosstabHostViewWithComponents`; `synth.Spec` / `FieldSpec` / `Options` / `Synth`; `(*Pulse).Service()`; `(*Pulse).Lookup` | `PredictResult.CrosstabFusable`; test helpers internal with no replacement; `synth` kept; `Options.DisableCrosstabFusion`; `Lookup` unchanged |
| not used | packages `fs`, `service`, `template`, `imports`, `mcp`, `mcpserve`, `cmd`; ~36 `*Pulse` methods (templates, watching, error / example lookup, imports, index admin, `Convert`, `Profile`, …) | packages per the decisions table; the methods stay on the facade |

The catalog's own conclusion — `pulse`, `types`, `encoding`, `descriptor` and `errors` carry the most weight, because breaks there reach the downstream's public API and its importers — matches the public set above.
