---
id: U34
slug: extension-validation
title: "Extension registrations are validated as strictly as built-ins, and chain predict knows them"
track: API & release
size: M
status: done
depends_on: [U02b]
soft_depends_on: []
blocks: [U32]
todo_items: [194, 195, 196, 197, 216, 230, 374, 409, 451, 459, 559]
branch: extension-validation
---

# U34 — extension-validation

**Outcome:** Extension registrations are validated as strictly as built-ins, and chain predict knows them.

**Track:** API & release · **Size:** S · **Depends on:** [U02b](U02b-extension-contract.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

[U02b](U02b-extension-contract.md) landed the `extend` contract with deliberate gaps, each recorded in its Landed deviations. This unit closed them so an embedder mistake surfaces at `pulse.New` instead of late or never: an operator's own `Components()` keys and a feature's `Streamable` are probe-validated, extension aggregators declare `CountSemantic` and `ValueState`, chain predict is proven equal to runtime admission, `io.LateKeyRefuser` is public, `extend.Record` reads exact `u64` words, and the never-functional synth-distribution slot is retired. The remaining gaps are stated as deliberate v1 decisions in one place.

Numbering note: the unit is U34 because U33 (v1-release) was already allocated; it is appended rather than renumbered, and [U32](U32-docs-audit.md) lists it as a dependency so the docs audit covers the final extension surface.

## References

- [U02b extension-contract](U02b-extension-contract.md) (Landed deviations, struck where closed)
- [api-and-release 03 — Changes from U34](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u34-extension-validation) (every break and addition; the release-note source)
- `docs/src/internals/extension-points.md` ("Deliberate v1 gaps")

**TODO items delivered** (all ticked in [`TODO.md`](../TODO.md)): **#194** (chain predict sees extensions), **#195** (self-emitted `Components()` keys), **#196** (feature `Streamable`), **#197** (synth-distribution slot retired), **#216** (`CountSemantic`), **#230** (`ValueState`), **#374** (`io.LateKeyRefuser`), **#409** and **#451** (done earlier in U25 / U29), **#459** (exact `u64`).

## Shipped

Planned epics:

- **E1 — An embedder's mistake is refused at `pulse.New`.** S1 probe-validates self-emitted `Components()` keys (#195); S2 probes feature `Streamable` two-way (#196); S3 adds `AggregatorRegistration.CountSemantic` (#216) and `ValueState` (#230).
- **E2 — Chain predict, io and Record say exactly what the runtime does.** S1: `PredictChain` ≡ `ProcessChain` parity test for every extension operator, and the test-only `ValidateChain*` wrappers deleted (#194); S2: public `io.LateKeyRefuser` (#374); S3: `extend.Record.Uint64Value`, the exact word carried on every decode path (#459).
- **E3 — The contract is final and the roadmap is closed out.** S1 retires the synth-distribution slot (#197); S2 is the contract pass (a "Deliberate v1 gaps" section in `extension-points.md`); S3 is this roadmap upkeep.

In-effort scope additions (owner decision, 2026-10-19), recorded here because they were not in the original plan:

- In-group fix `a2e9589b`: a nil operator `Factory` is refused uniformly at validation (`PULSE_EXTENSION_PARAM_INVALID`, reason `missing_factory`).
- E3-S4 `ed1f25bb`: `FilterToFile` runs extension filterers and `FILTER_TRUE` / `FILTER_FALSE` / `FILTER_SET_*`.
- E3-S5 `b255c083`: `FilterToFile` runs `FILTER_DATE_RANGES` zoned exactly as `Process` does.
- E3-S6 `f31dda67`: the `FilterToFile` dedup hash keys on the content of every referenced table and never reuses an output for a predicate that carries embedder code.

## Decisions

- **#197: retired, not built.** The slot's `Factory` was never read; `Extensions.SynthDistributions`, `pulse.DistributionRegistration`, `descriptor.ExtensionsManifest.SynthDistributions` and the manifest key `extensions.synth_distributions` are removed and `SYNTH` is no longer an extension category. `format_version` stays `"1.1"` (owner decision: the key always listed `[]`). A distribution authoring contract may arrive additively in v1.x.
- **#459: built.** The exact `u64` word rides every decode path rather than documenting the float64 echo as final.
- **New checks are hard refusals** at `pulse.New` and `CheckFeatureProfile`; there is no warn-first tier.
- **Deliberate v1 gaps** (documented in `extension-points.md`): named tables (`LookupTables`, `ReferenceVectors`, `LabelTables`, `RangeTables`) are not features, so no profile hides them and they sit outside `feature_set_digest`; an extension test's `Interpretation` is checked for structure only; extension operators are not zone-capable and `extend.Zone` is not public; `CountSemantic` has no numeric probe; `ValueState` is a flag, not a byte figure; `BlockMerger` is not reachable through `extend`.
- **Split out of this unit:** #231 (operation panic recovery) became [U56](U56-operation-panic-recovery.md); #458 (exact `u64` join `ON` key) moved to [U51](U51-archive-scale.md).

## Acceptance criteria

- [x] No `ValidateChain*` function is reachable only from tests (the wrappers are deleted; `PredictChain` is the production caller)
- [x] Chain predict and runtime `ProcessChain` agree on every extension operator admitted or refused
- [x] A `Components()` key outside the registration's `ComponentSchema` fails at `pulse.New`
- [x] A feature registration whose `Streamable` disagrees with its factory's value fails at `pulse.New`
- [x] The synth-distribution slot is documented as not an extension category
- [x] `format_version` stays `"1.1"`
- [x] Unit Definition of Done met

## Human inputs & decisions

- **Decided:** synth distributions are not an extension category (#197 retired).
- **Decided:** a feature profile cannot hide `LookupTables` or `ReferenceVectors` in v1 (deliberate gap, not a TODO item).
- **Decided:** extension test `Interpretation` stays structure-checked only; extension operators stay not zone-capable.

## Handed on

| Item | To | Why |
|---|---|---|
| #231 Operation panic recovery at the facade | [U56](U56-operation-panic-recovery.md) | not extension validation; blocks U32 |
| #458 join `ON` key over `u64` > 2^53 matches on its float64 echo | [U51](U51-archive-scale.md) | join-key and archive-scale work, not extension validation |
| Extension `weight_kinds`, `BlockMerger` through `extend`, a public `extend.Zone` | v1.x backlog (no unit) | additive, on demand |
| Distribution authoring contract for synth | v1.x backlog (no unit) | additive; slot retired in v1 |
