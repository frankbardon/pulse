---
id: U02b
slug: extension-contract
title: "Embedders author custom operators against a public contract, and the engine is fully internal"
track: API & release
size: L
status: done
depends_on: [U02]
soft_depends_on: []
blocks: [U04, U07, U15, U20, U32, U34]
todo_items: [185, 186, 187, 188, 189]
branch: extension-contract
---

# U02b — extension-contract

**Outcome:** Embedders author custom operators against a public contract, and the engine is fully internal.

**Track:** API & release · **Size:** L · **Depends on:** [U02](U02-public-surface.md) · **Unblocks:** [U04](U04-profiles-model.md), [U07](U07-guidance-metadata.md), [U15](U15-linalg-core.md), [U20](U20-observability.md), [U32](U32-docs-audit.md)

## Summary

Before this unit an extension operator was written against `processing`'s interfaces and its concrete record type, so `processing` cannot go internal without stranding every extension author. This unit adds a new public package, `extend`, holding the extension-authoring contract: the operator interfaces, the factory types and a small read-only `Record` interface. Registration through `pulse.Options.Extensions` stays the same shape; the engine adapts an `extend` operator onto its internal one. Built-ins keep their concrete fast path, so the interface-dispatch cost falls on extension operators only. Parity tests prove an operator behaves identically built in and adapted. Then `processing`, `processing/feature` and `processing/window` become fully internal.

New operators in U04, U07, U15 and U20 land after `processing` is internal, which is why those units depend on this one.

## References

**Theme documents (read before starting):**
- [api-and-release 00 — Public Go surface](../v1.0.0-api-and-release/00-public-surface.md) — Model, Decisions (`extend`, `processing`, `processing/feature`, `processing/window` rows), Facade additions, What leaks through the facade, Unit split
- [api-and-release 03 — Embedder migration guide](../v1.0.0-api-and-release/03-embedder-migration.md) — Engine packages → internal (every row tagged U02b)
- [api-and-release 02 — Stability policy](../v1.0.0-api-and-release/02-stability-policy.md) — `extend` is on the public package list
- `docs/src/internals/extension-points.md` — the current recipe this unit rewrites

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#185** (1. API surface & release pipeline › Public Go surface › Extension contract) Public `extend` package: aggregator, online aggregator, grouper + streaming variants, filterer builder / filter func, attribute and test interfaces, window and feature computers, factory types
- [x] **#186** (1. API surface & release pipeline › Public Go surface › Extension contract) `extend.Record`: a small read-only record interface (values, nulls, wide and set accessors), sized by an inventory of what built-in operators read
- [x] **#187** (1. API surface & release pipeline › Public Go surface › Extension contract) Registration adapts `extend` operators onto the engine; built-ins keep the concrete fast path; built-in vs adapted parity tests
- [x] **#188** (1. API surface & release pipeline › Public Go surface › Extension contract) `processing`, `processing/feature`, `processing/window` fully internal; any interim root aliases from U02 removed
- [x] **#189** (1. API surface & release pipeline › Public Go surface › Extension contract) `extension-points.md` and the `adding-*` recipes rewritten against `extend`

## Scope

**In scope**
- New public package `github.com/frankbardon/pulse/extend`: `Aggregator`, `OnlineAggregator`, `Grouper` + streaming variants, `FiltererBuilder` / `FilterFunc`, attribute and test interfaces, window and feature computers, factory types, and `Record`
- Every `pulse.Options.Extensions` registration slot whose signature names a `processing` type re-expressed in `extend` types (the first story inventories them; the synth-distribution registration only reserves its namespace, and overlay kinds are not an extension category)
- Adapters from `extend` operators to the engine's internal interfaces, preserving probe-validation (`PULSE_EXTENSION_STREAMABLE_MISMATCH`, `PULSE_EXTENSION_FACTORY_PANIC`, `PULSE_EXTENSION_FANOUT_MISMATCH`), the `FieldInputs` hook and lazy table resolution
- Built-in vs adapted parity tests
- Moving `processing`, `processing/feature`, `processing/window` under `internal/` (or finishing the move U02 started), and removing any interim root aliases
- Extension docs: `extension-points.md`, the `adding-*` recipes, CLAUDE.md "Extension Points"

**Out of scope**
- Porting built-in operators onto `extend`: built-ins keep the concrete record path
- New extension categories or new registration slots
- Freezing the engine's internal record representation: `Record` is an interface precisely so the engine can change underneath it after v1
- `CohortReader` / `CohortBuilder` and `PredictResult.CrosstabFusable` ([U02c](U02c-cohort-facade.md))
- Any wire, manifest-shape or file-format change; `format_version` stays `"1.1"`

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test|docs(extension-contract/E<n>-S<m>): …`; close each epic with `milestone(extension-contract/E<n>): vertical slice complete — <epic title>`.

### E1 — One extension aggregator runs through `extend`
- S1: inventory every accessor built-in operators and the extension test suites read from a record (values, nulls, wide values, sets, …) and every `Extensions` slot that names a `processing` type; fix the `extend.Record` method set from it
- S2: `extend` package with `Record`, the aggregator interfaces and factory type; registration adapter; built-in vs adapted parity test for one aggregator
- S3: benchmark the adapted path against the built-in path; record the dispatch cost in the unit PR

### E2 — Every extension category is authored through `extend`
- S1: groupers (+ streaming and fan-out variants), filterers, attributes, tests
- S2: windows, features, and the remaining registration slots from the E1 inventory
- S3: parity tests per category; the `TestExtensions_*` suite ported to `extend` and green

### E3 — The engine is internal
- S1: move `processing`, `processing/feature`, `processing/window` under `internal/`; remove interim root aliases; boundary gates on the new paths
- S2: `extension-points.md`, `adding-*` recipes, CLAUDE.md "Extension Points" and `.claude/reference/*.md` paths rewritten against `extend`; migration-guide rows tagged U02b corrected to the landed spelling

## Acceptance criteria

- [x] No `processing` package is importable from outside the module
- [x] An extension operator of every category is authored using only `pulse`, `extend`, `types`, `encoding` and `errors`
- [x] For every category, the same operator registered built in and adapted through `extend` produces identical results, components and streaming behaviour (parity tests)
- [x] Probe-validation, `FieldInputs` projection and lazy table resolution behave as before for extension operators
- [x] Built-in operator benchmarks show no regression
- [x] Every [03](../v1.0.0-api-and-release/03-embedder-migration.md) row tagged U02b is true of the branch
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New built-in vs adapted parity tests, one family per extension category
- `TestExtensions_*` suite (including `TestExtensions_ComponentSchemaParity`) green against `extend` registrations
- `TestPredictNoExecutionImports` and the other import-boundary gates green on the final paths
- The U02 API-compatibility job covers `extend`

## Update Demand companions

- `docs/src/internals/extension-points.md` and every `adding-*` recipe that names a `processing` interface or path
- CLAUDE.md "Extension Points" (surface line and interface names) and "Architecture" (`processing/` now internal)
- `.claude/reference/update-demand.md` rows naming `processing` extension paths (`pulse.Options.Extensions` row, `FieldInputsFunc` row, extension `ComponentSchema` row)
- [03-embedder-migration.md](../v1.0.0-api-and-release/03-embedder-migration.md): correct any U02b row whose landed spelling differs

## Human inputs & decisions

- **Open, decided in E1-S1:** the exact `extend.Record` method set, fixed by the accessor inventory.
- **Decided:** the package is named `extend`; `processing` is fully internal; built-ins keep the concrete fast path.

## Notes

- **`Record` coverage risk.** The interface may not cover every accessor extension-style code uses (sets, wide values, nulls). E1-S1's inventory sizes it before any interface is written, and the parity tests guard behaviour.
- **Interface dispatch cost** applies to extension operators only; built-ins keep the concrete fast path.
- **Interim aliases.** If U02 moved `processing` with temporary root aliases for the extension types, this unit removes them; if U02 left `processing` in place, this unit performs the move.

## Landed deviations

- **`Components()` self-emission kept.** An operator's own `Components()` method still emits when no `ComponentsFunc` is registered, in every emitting category (a deliberate deviation from strict "emission only through `ComponentsFunc`"); it is not probe-validated (owned by [U34](U34-extension-validation.md), #195).
- **Streamability follows the declaration.** Extension aggregators, groupers, attributes and row tests route on the declared `Streamable` flag; predict agrees. Features decide on the returned value; `FeatureRegistration.Streamable` is not probe-validated (owned by [U34](U34-extension-validation.md), #196). A grouper declaring `Streamable: true` without a keying sibling is `PULSE_EXTENSION_STREAMABLE_MISMATCH`.
- **Behaviour fixes.** Built-in `ATTR_ZSCORE` + `GROUP_CATEGORY` now predicts `Streamable=false` (matches runtime); the predict two-pass list includes `ATTR_REG_FITTED` / `ATTR_REG_RESIDUAL` / `ATTR_REG_LEVERAGE`; streaming grouped Components floor fixed for bucket-less groupers, and the parallel reducers now carry the same (record, bucket) assignment count so the merged floor matches; a streamable extension with `ComponentsFunc` streams instead of silently buffering; a two-pass attribute (built-in or `two_pass` extension) now declines the fused crosstab — before, a built-in `ATTR_ZSCORE` crosstab failed `PROCESSING_INTERNAL` and a `two_pass` extension one fused and returned cells valued against empty stats.
- **Limits.** Extension aggregators merge only when the registration declares `Mergeable` (`extend.MergeableAggregator`; `PULSE_EXTENSION_MERGEABLE_MISMATCH`); extension groupers merge the same way on a declared `Mergeable` (`extend.MergeableGrouper`, needed only when the grouper emits components); extension grouper Components figures emit on every grouped path. Per-group AGGREGATOR Components (operator figures inside each group) — DELIVERED by [U17](U17-response-shaping-core.md) E1 (#193) for built-ins and extensions alike, on every grouped arm; single-key extension groupers take fused crosstab through an adapter-synthesized `KeyFor` (still omitted from `extend`); an extension crosstab CELL aggregator takes fused crosstab on a declared `MarginReducibility` (fusable class + `Mergeable`; `PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH`), undeclared it runs buffered.
- **Decimal.** Extension aggregators are admitted on `decimal128` targets (they read `DecimalValue`) and stream / merge there per their declared `Streamable` / `Mergeable` flags; built-ins over decimal stay buffered and serial.
- **Dispatch cost.** Adapted vs built-in `AGG_SUM`, 50K rows: streaming 6.12 vs 5.88 ms, buffered 18.60 vs 18.72 ms (geomean +1.75%, not significant).
- **Open follow-ups, each owned.** ~~Per-group aggregator Components: [U17](U17-response-shaping-core.md) (#193)~~ — delivered by U17 E1. Predict-side chain validation (`ValidateChain` / `ValidateChainWithExtensions` have only test callers; a chain-predict entry point must pass the `ExtensionsSnapshot`): [U34](U34-extension-validation.md) (#194). Self-`Components()` and feature `Streamable` probe validation: [U34](U34-extension-validation.md) (#195, #196). Synth-distribution `extend` factory shape (the registration only reserves a namespace): [U34](U34-extension-validation.md) (#197).
