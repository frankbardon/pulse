---
id: U02
slug: public-surface
title: "The public Go API is deliberate, and CI guards it"
track: API & release
size: L
status: done
depends_on: []
soft_depends_on: []
blocks: [U02b, U02c, U04, U07, U15, U20, U32]
todo_items: [1, 2, 3, 4, 180, 181, 182, 183, 184]
branch: public-surface
---

# U02 — public-surface

**Outcome:** The public Go API is deliberate, and CI guards it.

**Track:** API & release · **Size:** L · **Depends on:** none · **Unblocks:** [U02b](U02b-extension-contract.md), [U02c](U02c-cohort-facade.md), [U04](U04-profiles-model.md), [U07](U07-guidance-metadata.md), [U15](U15-linalg-core.md), [U20](U20-observability.md), [U32](U32-docs-audit.md)

## Summary

Apply the decided classification. Every package the decisions table marks internal moves under `internal/`; `encoding`, `io`, `descriptor`, `errors`, `synth`, `mcp/gosdk` and `mcpserve` are narrowed to their kept subset. Facade-returned types keep their spelling through root aliases, and the leaks that aliases cannot close get facade replacements: `Options.DisableCrosstabFusion`, `InspectBytes` / `PredictBytes`, the `io` factory with typed formats, `CohortArtifacts` (planned as `IndexArtifacts`), and root-native `DateRangeSpec` / `MemberSet`. Then an API-compatibility check runs in CI so the frozen surface cannot break silently.

The catalog and the classification (#1, #2) are already done. The extension-authoring contract (`extend`) is [U02b](U02b-extension-contract.md); `CohortReader` / `CohortWriter` and `PredictResult.CrosstabFusable` are [U02c](U02c-cohort-facade.md).

## References

**Theme documents (read before starting):**
- [api-and-release 00 — Public Go surface](../v1.0.0-api-and-release/00-public-surface.md) — Model, Decisions, Root aliases into internal packages, Fixed points, Facade additions, What leaks through the facade, Unit split
- [api-and-release 03 — Embedder migration guide](../v1.0.0-api-and-release/03-embedder-migration.md) — every row tagged U02
- [api-and-release 02 — Stability policy](../v1.0.0-api-and-release/02-stability-policy.md) — the public package list the `STABILITY.md` draft freezes

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#1** (1. API surface & release pipeline › Public Go surface) Downstream usage catalog completed (maintainer)
- [x] **#2** (1. API surface & release pipeline › Public Go surface) Classification per package decided (public / public-narrowed / internal)
- [x] **#3** (1. API surface & release pipeline › Public Go surface) Package moves and narrowing done; facade re-exports added
- [x] **#4** (1. API surface & release pipeline › Public Go surface) API-compatibility check (`gorelease` / `apidiff`) in CI against the latest tag
- [x] **#180** (1. API surface & release pipeline › Public Go surface) `io` factory: `io.NewReader` / `NewReaderFromBytes` / `NewWriter` / `NewWriterToBuffer` with typed `io.Format` constants and format knobs on typed sub-structs; `io/<fmt>` and `io/format` internal
- [x] **#181** (1. API surface & release pipeline › Public Go surface) `Options.DisableCrosstabFusion` replaces `(*Pulse).Service()`, which is removed
- [x] **#182** (1. API surface & release pipeline › Public Go surface) `(*Pulse).InspectBytes` / `PredictBytes` replace `descriptor.InspectFromBytes` / `PredictFromBytes`; the instance fills the extension snapshot
- [x] **#183** (1. API surface & release pipeline › Public Go surface) `(*Pulse).CohortArtifacts(ctx, cohort) ([]string, error)` (planned as `IndexArtifacts`); the index-manifest helpers move internal
- [x] **#184** (1. API surface & release pipeline › Public Go surface) Root-native `DateRangeSpec`, `MemberSet`, `LoadMemberSetResult` (spelling unchanged)

## Scope

**In scope**
- Moves under `internal/`: `service`, `fs`, `imports`, `template`, `examples`, `skills`, `mcp` core, `mcp/toolmeta`, `processing/regression`, `processing/arena`, every `io/<fmt>` subpackage, `io/format`, `io/exportoverlay`, `io/nullcell`, `io/settristate`, `io/setwide`
- `processing`, `processing/feature`, `processing/window`: the interim (see Notes) — either left in place for U02b, or moved with temporary root aliases for the extension types
- Narrowing `encoding` (raw-byte primitives for ungrouped `0x01` only; `RecordLocator` reduced to geometry), `io`, `descriptor`, `errors`, `synth`, `mcp/gosdk`, `mcpserve` to the kept subsets in the decisions table
- Root aliases: the 13 `service` result types, `ImportSpec` / `ImportResult` / `ImportEntry`, the template and examples result types
- Leak-closing replacements: `Options.DisableCrosstabFusion`, `InspectBytes` / `PredictBytes`, the `io` factory + typed `io.Format`, `CohortArtifacts`, root-native `DateRangeSpec` / `MemberSet` / `LoadMemberSetResult`
- Test-only `processing.ApplyOverlays`, `CompileDateRanges`, `NewCrosstabHostViewWithComponents` lose their public spelling, with no replacement
- Import-boundary gates updated to the moved paths
- API-compatibility CI job, advisory until the `v1.0.0` tag exists
- Path updates in CLAUDE.md, `.claude/reference/`, `docs/src/internals/` and `.claude/agents/`

**Out of scope**
- The `extend` package, registration adapters and making `processing` fully internal ([U02b](U02b-extension-contract.md))
- `CohortReader` / `CohortWriter` and `PredictResult.CrosstabFusable` ([U02c](U02c-cohort-facade.md))
- Public raw-byte writing of grouped (`0x02`) cohorts — permanently outside the v1 primitive set
- A Pulse-owned filesystem interface replacing `afero.Fs` (frozen as-is)
- Behaviour changes beyond the decided replacements: no wire, file-format or operator change; `format_version` stays `"1.1"`. The one intended behaviour difference is that `PredictBytes` fills the extension snapshot, which `PredictFromBytes` left unset
- Writing the root `STABILITY.md` (that is U33; the package list is already in the [02](../v1.0.0-api-and-release/02-stability-policy.md) draft)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test|docs(public-surface/E<n>-S<m>): …`; close each epic with `milestone(public-surface/E<n>): vertical slice complete — <epic title>`.

### E1 — The facade closes every leak
Replacements land first, while the old spellings still exist, so each one is tested side by side before anything moves.
- S1: `Options.DisableCrosstabFusion`; remove `(*Pulse).Service()`; migrate its callers and tests
- S2: `(*Pulse).InspectBytes` / `PredictBytes` (extension snapshot filled from the instance); `(*Pulse).CohortArtifacts`
- S3: `io` factory + typed `io.Format` constants + typed option sub-structs; CLI import / export switches routed through it
- S4: root-native `DateRangeSpec`, `MemberSet`, `LoadMemberSetResult`

### E2 — Internals move out of the public API
- S1: move the internal set under `internal/`, adding root aliases for every facade-returned type; apply the `processing` interim choice
- S2: narrow `encoding` / `io` / `descriptor` / `errors` / `synth` / `mcp/gosdk` / `mcpserve` to their kept subsets
- S3: import-boundary gates on the new paths; CLAUDE.md "Architecture", `.claude/reference/*.md`, `docs/src/internals/*` and `.claude/agents/*.md` path updates

### E3 — CI guards the surface
- S1: `apidiff` (or `gorelease`) job against the latest tag covering every public package and every root alias; advisory until `v1.0.0`, with a documented override for intentional pre-1.0 breaks

## Acceptance criteria

- [x] Only the packages on the [02](../v1.0.0-api-and-release/02-stability-policy.md) public list are importable, plus `processing` (+ `feature`, `window`) if the interim leaves them in place for U02b
- [x] Every [03](../v1.0.0-api-and-release/03-embedder-migration.md) row tagged U02 is true of the branch: an embedder applying exactly those adaptations builds
- [x] All existing tests and goldens pass unchanged; `format_version` stays `"1.1"`
- [x] `PredictBytes` on an instance with registered extensions treats extension operators as known (test)
- [x] Every path `CohortArtifacts` returns moves with a cohort and the moved cohort's `Lookup` still hits (test)
- [x] The API-compat CI job runs on PRs and reports an incompatible change to a public package or root alias
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Existing import-boundary gates (`TestPredictNoExecutionImports`, `TestMCPCore_NoSDKImport`, `TestTemplatePackage_ImportBoundary`) updated to the new paths and still green
- `TestFromExt_Matrix` and `TestManifestImportCapability` green against the `io` factory
- New tests for each facade replacement (S1–S4 of E1)
- New CI job: API compatibility

## Update Demand companions

- CLAUDE.md "Architecture" block, "Design principles" facade method list (`InspectBytes`, `PredictBytes`, `CohortArtifacts`), and every path reference in `.claude/reference/*.md` (the registered I/O format row in `update-demand.md` names the `io/format` dispatch)
- `docs/src/internals/*` recipes, including `adding-io-format.md` and `packages.md`
- `docs/src/library/options.md` (`DisableCrosstabFusion`)
- `.claude/agents/*.md` path mentions
- [03-embedder-migration.md](../v1.0.0-api-and-release/03-embedder-migration.md): correct any U02 row whose landed spelling differs

## Human inputs & decisions

- **Downstream catalog:** delivered (#1); the classification is decided (#2). No blocking input remains.
- **Open, implementer's call:** whether U02 moves `processing` itself (with temporary root aliases for the extension types) or leaves the move to U02b.

## Notes

- Land this before other units touch the moved packages, to avoid rebasing every in-flight unit across a mass move.
- **`processing` interim.** Moving `processing` internal before U02b ships `extend` would break extension authors mid-sequence. Either keep it in place or move it with temporary root aliases; U02b removes any interim aliases.
- **Catalog matching risk (accepted).** The catalog matched less-common method names by name, not type, so a symbol could be misattributed. The decisions are package-level and the migration guide covers every listed name either way.
- **Raw-byte primitives freeze (accepted).** Freezing `ReadHeader` / `ReadSchema` / `WriteSchema`, the schema geometry and `Read/WriteFieldValue` constrains future ungrouped writer changes; the layout is already promised readable forever and the format version is a function of schema content.
- An aliased type's fields and methods freeze exactly as if its package were public, so the API check must cover root aliases, not just public packages.
- Pre-1.0, the compat check is advisory until the v1.0.0 tag exists.

## Delivered — deviations from the plan

Landed on branch `public-surface` (single PR, label `api-break-ok`). Executed as four epics: **E1** guards first (`TestPublicAPIGolden`, the advisory `apidiff` job), **E2** the facade replacements, **E3** the moves and narrowing, **E4** the embedder smoke module and these docs. Deviations, each also recorded in [00](../v1.0.0-api-and-release/00-public-surface.md) (Amendments at implementation) and [03](../v1.0.0-api-and-release/03-embedder-migration.md):

- **`IndexArtifacts` → `CohortArtifacts(ctx, cohort) ([]string, error)`**, covering every Pulse-owned sidecar (`.<keyhash>.idx`, `.indexes.json`, `.spss.json`, `.meta.json`). A moved cohort must keep its mtime, which the sidecars fingerprint.
- **`errors` left whole** rather than narrowed; `mcpserve` was already minimal and is unchanged.
- **Gap identifiers** the decisions table did not classify are kept public: the `encoding` header / bit / nibble / decimal / set-mask / datetime primitives, the `Group*` nouns and `RecordLocator` fields; the `io` forced closure types; the `synth` profile and spec I/O functions and the `FidelityReport` family.
- **Narrowing technique:** `encoding` and `descriptor` split in place; `io` and `synth` are alias facades over `internal/io` (+ the `internal/iocore` contracts leaf) and `internal/synth`. A new gate, `TestIOImportBoundary*`, keeps `internal/io/**` from importing the public `io`.
- **API compatibility:** pinned `apidiff -m` (advisory until a stable `v1.0.0` tag, `api-break-ok` label) plus the blocking `TestPublicAPIGolden`, because `apidiff` cannot see fields or methods behind a root alias into `internal/`. A compile-checked embedder smoke module (`internal/embeddersmoke`, `make smoke`) builds every U01 / U02 migration-guide row.
- **Ordering:** the `io` structural move into `internal/io` + `internal/iocore` landed with the factory story (E2), ahead of the narrowing epic, because the factory and the import-cycle break could not be built apart.
- **Extra root aliases** by maintainer decision: `CohesionWarning`, `GroupIndexHeadroom`, `SidecarIndex` and its field closure `SidecarIndexKeySpec` / `SidecarIndexBucket` / `SidecarIndexEntry` / `CohortFingerprint` (leaked `encoding` types inside facade results), plus `ImportSidecar` and the `Template*` / `RenderedTemplate` set.
- **`ExportJob.Hash` / `ConvertJob.Hash` stay public** by maintainer decision.
- **Dependabot** watches the nested smoke module (`/internal/embeddersmoke`) alongside the root module.
- **Removed without replacement:** `io.NewConvertJob` (use `&io.ConvertJob{}`), `gosdk.Config.Core()`, and the `synth` capture / rule / fidelity-builder functions and tuning constants listed in 03.
- **`processing` interim:** `processing`, `processing/feature` and `processing/window` stay public in place for [U02b](U02b-extension-contract.md).
- **New coded error** `PULSE_IO_FORMAT_UNSUPPORTED` for the factory.
- **Docs:** CLAUDE.md's Architecture tree and MCP layer-split paragraph moved to `.claude/reference/architecture.md` to fit the 50,000-byte budget.

**Carried forward:** the `template` target / var-type constants have no root spelling.
