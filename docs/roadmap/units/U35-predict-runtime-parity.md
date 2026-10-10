---
id: U35
slug: predict-runtime-parity
title: "Predict and runtime agree on every built-in, and the runtime never answers with a wrong number"
track: API & release
size: M
status: not-started
depends_on: []
soft_depends_on: [U02c]
blocks: [U32]
todo_items: [198, 199, 200, 201, 207, 213, 214, 215, 221, 248, 249, 253, 254, 255, 256, 259, 260, 267]
branch: predict-runtime-parity
---

# U35 — predict-runtime-parity

**Outcome:** Predict and runtime agree on every built-in, and the runtime never answers with a wrong number.

**Track:** API & release · **Size:** M · **Soft-depends on:** [U02c](U02c-cohort-facade.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

U03 and U05 found pre-existing places where predict promises a result the runtime refuses, or where the runtime silently computes on bytes it cannot interpret. None was caused by those units and none was fixed there. This unit closes them before the v1 freeze, so an agent that trusts predict is never surprised and no request returns a confident wrong number. Extension-registration validation stays in [U34](U34-extension-validation.md).

Numbering note: appended as U35 after U34 rather than renumbered.

## References

**Read before starting:**
- `.claude/reference/predict-inspect.md` — the predict import ban; predict reads header + schema only
- `.claude/reference/execution-modes.md` — projected decode, crosstab, ProcessChain
- `.claude/reference/byte-layout.md` — projected decode is output-transparent
- PR #301 (U03) body and [U05](U05-profiles-enforcement.md) "Notes: pre-existing gaps found incidentally" — the original findings

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#198** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Predict refuses every operator × field type the runtime refuses: the "predict looser" half of the `knownTypeDivergence` ledger (`field_type_acceptance_test.go`) is emptied
- [ ] **#199** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) The runtime refuses, rather than silently computes on, a field it cannot read: the "predict stricter, runtime wrong" half of the ledger (`FEAT_POLY`, `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` and the `WIN_*` value windows on categorical, set, decimal, `packed_bool` or `datetime` columns), plus tier-1 tests on `set_*` fields
- [ ] **#200** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) A request with more than one `Groups` entry executes every group or is refused; today only `Groups[0]` runs
- [ ] **#201** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Remaining silent predict / runtime gaps: crosstab cell-aggregator validity in predict, one label set for the label-collision check, label bindings on ProcessChain stages ≥ 1, windowed record rows under projection
- [ ] **#213** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) `Response.overlays` joins the feature gate (`gatedSlots`)
- [ ] **#214** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) A joined slot or stage derives its `return` precision-exact set from the defaults-resolved request
- [ ] **#215** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) `finalizeMergedPartial`'s empty-partial path emits the zero-n aggregation entries the serial path emits
- [ ] **#207** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Shard-archive cohesion compares `Nullable`: `AddShard`, `shard verify`, the `NewCohortBuilder` anchored-append pre-check and the archive reader refuse a shard whose per-field nullability differs from the canonical schema, instead of decoding it under the canonical flags
- [ ] **#248** (7. Guided analysis › Follow-ups from U22) Bound Recommend reads a shard archive header-only: the archive path of `descx.Predict` does `io.ReadAll`, so a bound Recommend on an archive reads every shard
- [ ] **#249** (7. Guided analysis › Follow-ups from U22) Recommend drafts every serving operator: `AGG_RATIO` (predict refuses a missing `field` the runtime ignores), `AGG_WEIGHTED_MEAN` (no weight binding), `ATTR_REG_*` and `FEAT_BUCKETIZE` (required params the manifest does not declare); see `unmappedOnFixture` in `internal/guide/bind_test.go`
- [ ] **#253** (7. Guided analysis › Follow-ups from U23) Compose predict does not judge slot operators by name: an unknown or hidden operator in a Compose slot predicts valid, so the Explain doc's "predicts each slot" claim is inaccurate until per-slot predict runs
- [ ] **#254** (7. Guided analysis › Follow-ups from U23) Chain predict does not validate stage label bindings
- [ ] **#255** (7. Guided analysis › Follow-ups from U23) `PredictCompose` / `PredictFacet` / `PredictChain` and `pulse api predict-compose` / `-facet` / `-chain` ignore Strict and EchoRequest (the validators read neither); wire them, then add `--strict` / `--echo-request`
- [ ] **#256** (7. Guided analysis › Follow-ups from U23) Bare MCP `pulse_predict` returns no `errors` / `warnings`, so `valid: false` carries no reason (the alternative roots do carry them); additive fix
- [ ] **#259** (7. Guided analysis › Follow-ups from U23) The engine accepts `weight` on a facet overlay and silently ignores it (facets are never weighted); refuse it
- [ ] **#260** (7. Guided analysis › Follow-ups from U23) Bound `joins` field-name enums in the MCP input schemas cover only the left cohort; right-side and joined field names fall outside them (the description says so, predict resolves them); widen the enums from the join target's schema
- [ ] **#267** (10. Vector & matrix — operators › Follow-ups from U24) Predict `row_buffer_bytes` is an upper bound for ordinary groupers only: a fan-out grouper (`GROUP_SET_PER_ELEMENT` and kin) copies a row into every label bucket, so the true buffer can exceed it; add the fan-out multiplicity, and confirm `limits.MatrixStateBytes` counts the buffered rank-method row store

## Scope

**In scope**
- Predict gains the runtime constructors' field-type checks, without importing `internal/processing/` (`TestPredictNoExecutionImports`)
- The runtime refuses, with a coded error, every field type it cannot read
- Multi-entry `Groups`: execute every entry or refuse at predict and runtime alike
- Each #201 gap fixed with a predict-vs-runtime test
- Shard cohesion compares per-field `Nullable` everywhere a shard meets the canonical schema (#207)

**Out of scope**
- Widening predict to match a wrong runtime (never — fix the runtime)
- Extension operators ([U34](U34-extension-validation.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test(predict-runtime-parity/E<n>-S<m>): …`; close each epic with `milestone(predict-runtime-parity/E<n>): vertical slice complete — <epic title>`.

### E1 — The runtime never computes on a field it cannot read
- S1: refuse unreadable types in `FEAT_POLY`, `REG_*`, `WIN_*` value windows and tier-1 tests on `set_*` (#199); delete the ledger's "predict stricter, runtime wrong" entries
- S2: multi-entry `Groups` executes every group or is refused (#200)
- S3: shard cohesion compares `Nullable` in `AddShard`, `shard verify`, the builder's anchored-append pre-check and the archive reader; a parity row for nullability joins `TestCohortBuilder_AppendPrecheckParity` (#207)

### E2 — Predict refuses what the runtime refuses
- S1: predict gains the runtime's operator × field-type checks (#198); the ledger's "predict looser" entries are deleted as each is fixed
- S2: crosstab cell-aggregator validity, one label set for the collision check, ProcessChain stage ≥ 1 label bindings, windowed record rows under projection (#201)

## Acceptance criteria

- [ ] `knownTypeDivergence` returns `""` for every operator × field type pair, and the ledger is deleted
- [ ] No request with more than one `Groups` entry silently drops a group
- [ ] Every #201 gap has a predict-vs-runtime test
- [ ] A shard whose field nullability differs from the canonical schema is refused with a coded error (`PULSE_SHARD_SCHEMA_MISMATCH` or a sibling) at every entry point, never decoded (#207)
- [ ] `format_version` stays `"1.1"`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestFieldTypeAcceptance_PredictMatchesRuntime` (exact ledger; an entry that stops diverging fails)
- `TestPredictNoExecutionImports`
- New predict-vs-runtime tests per #200 / #201 gap

## Update Demand companions

- `errors/fixup_metadata.go` + `internal/descriptor/error_owners.go` for any new refusal code
- The atomic `op-*` skill of every operator whose accepted types change
- `.claude/reference/predict-inspect.md` if predict's contract changes; `byte-layout.md` (projected decode) if the windowed-row fix touches the contract
- `.claude/reference/byte-layout.md` (shard cohesion) + `skills/cohort-schema-design.md` (Sharded) if the cohesion rule's wording changes (#207)

## Human inputs & decisions

- **Open:** multi-entry `Groups` — execute them (and how: nested, independent) or refuse. Decide in E1-S2.

## Inherited from U03 / U05

Pre-existing gaps, none caused by those units and none fixed there. Each makes predict promise a result the runtime refuses, or makes the runtime answer with a wrong number instead of an error. U03 recorded them in its PR (#301) and U05 in its unit doc ("Notes: pre-existing gaps found incidentally").

- **Field-type acceptance ledger (#198, #199).** `knownTypeDivergence` in `field_type_acceptance_test.go` (gate `TestFieldTypeAcceptance_PredictMatchesRuntime`) is exact: an entry that stops diverging fails the gate, so each fix deletes its entry. "Predict looser" covers about 600 pairs, for example `set_*` on value operators, `decimal128` on non-decimal aggregators (predict only warns `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`), and the date-range operators on non-temporal fields. "Predict stricter, runtime wrong" is the dangerous half: the engine regresses on a categorical's dictionary index, expands a set's bitmask in `FEAT_POLY`, and windows whatever bytes the row carries. Fix those in the runtime; never widen predict to match.
- **Tier-1 tests on `set_*` fields read `n = 0`** rather than being refused (#199).
- **Only `Groups[0]` executes** (#200). `internal/processing/processor.go` reads `req.Groups[0]`, and later entries are silently ignored. `parallel_reduce.go` and `shard_reduce.go` assume a single grouper too.
- **Predict accepts a bad crosstab cell aggregator** (#201). A never-registered, or decimal128-unsupported, `Crosstab.Cell` aggregator passes predict, but the runtime refuses it. The fusion rules [U02c](U02c-cohort-facade.md) moves to a no-execute home sit beside this check.
- **Label-binding collision check** (#201). Predict and the runtime pass it different label sets.
- **ProcessChain stage ≥ 1 ignores label bindings** (#201). They are silently dropped, where they should be applied or refused.
- **Windowed record rows differ under projection** (#201). They carry only the projected fields where the unprojected path carries the full row, which contradicts the output-transparent projection contract (`.claude/reference/byte-layout.md`, projected decode).

## Inherited from U02c

- **Shard cohesion ignores `Nullable`** (#207). Found while adding the U02c anchored-append pre-check (PR #311): `AddShard`'s structural cohesion compares name, type, order and set rung but never the per-field nullable flag, and the pre-check reuses that code, so it inherits the gap. The dangerous direction is an archive field declared non-nullable receiving a shard whose field is nullable: when the bitmap width still matches (other nullable fields exist), the stride check passes and the archive reader, which decodes an anchored shard under the CANONICAL schema, would ignore that field's null bits, so a null could come back as a value (inferred from the code path, not yet reproduced — write the failing test first). Fix in the shared cohesion check so every caller picks it up; decide whether non-nullable→nullable widening is a legal rewrite (like set widening) or a refusal.

## Inherited from U17

- **`Response.overlays` is not feature-gated** (#213). `internal/descriptor/request_slots.go` (`gatedSlots`) has no entry for it, so an instance hiding the overlay features still lists `overlays` paths in its payload schema and in the `return` presets (`standard` / `minimal` expand them). Gate it like `matrices`, and let `TestReturnPathsMatchSchema` / `TestReturnPresetsFor_Instance` cover a profile without overlays.
- **Joined slot / stage `Plan.Exact`** (#214). A join gives the slot or stage defaults on a service-internal clone, so the `return` precision-exact set derives from the un-defaulted request (`Process` included). A count column that exists only after defaults would round. Resolve the plan from the defaults-resolved request on that path. The Compose-level (`overlays` root) plan likewise has no `Exact` set of its own: give it one if an overlay payload ever carries a float count.
- **`finalizeMergedPartial` empty partial** (#215). A merged (parallel decode / shard) run whose partial is empty emits no `Aggregations` block while the serial path emits zero-n entries, ungrouped and grouped. Pre-existing; found by E1's per-group parity gates.

## Inherited from U18

- **Compose components veto is not weight-basis aware** (#221). U18 follow-up PR #320 made the request-overlay veto precise: `descx.OverlayReadsHostComponents(kind, cellBasis)` keeps `components.crosstab` for χ² / Fisher layers only on a weighted host (frequency reads `sum_weights`, probability also `n_eff`); pairwise kinds always. The Compose veto (`composeSlotVetoes` / `composeSlotContext`, `internal/service/compute_plan.go`) still keeps EVERY `Components` sub-part of every slot a Compose overlay names, whatever the kind or weighting. Apply the same rule per slot: resolve each named slot's crosstab-cell basis with `descx.CrosstabCellWeightBasis(req, defaultWeight, inst)`, keep only `components.crosstab`, and only when the overlay kind reads floors on that basis — first verify per Compose kind (`internal/processing/overlay_compose_handlers.go`) which ones read `CellComponents` triples on an UNWEIGHTED slot (the mean kinds' Welford-triple path does — `extractCellComponentsTriple`), since those must keep vetoing. Compute saving only; the fold-time invariant `Service.composeOverlayFloorsPresent` (PROCESSING_INTERNAL) backstops a wrong rule for the floor kinds. Gates to extend: `TestReturnSkipsComputation_Compose`, root `TestReturnKeptNumberInvariant`, `TestReturnComposeParallelTimeoutKeepsComponentsVeto`.

## Inherited from U19

- **Simple `Facet` has no limits pre-flight** (#225). U19 wired the `MaxEstimatedMemory` pre-flight into the rich facet path after `checkFacetFieldRefs` (`internal/service/facet_rich.go`), but `pulse.Facet(path, field)` builds no request and skips it. Route it through the same estimate (`descx.EstimateFacetMemory`) and extend `TestLimitsPredictRuntimeParity`.
- **No predict limit findings for Compose / chain / facet** (#226). `ValidateCompose` / `ValidateChain` and the facet validator compute no `LimitFindings`; `TestLimitsPredictRuntimeParity` treats `max_compose_slots`, `max_chain_stages` and facet group counts as unknowable at predict. Add the findings (via `internal/descriptor.RequestLimitFindings`) where a facade predict surface exists or is added.
- **Memory estimate precision** (#229). U19's model (`internal/limits/memory.go`) is records x (3 x stride + `RecordBytes(schema)`) at full schema width: calibration (`TestMemoryEstimate_Calibration`, table in `.claude/reference/predict-inspect.md`) shows 1.33x-4.94x over peak heap, worst on projected buffered runs. It also assumes at most one right match per left row, so a 1:N fan-out join can exceed the estimate (the one under-estimate). Model projection and join fan-out (e.g. from the right key dictionary) without losing the upper-bound property.

## Inherited from U22

- **#248** — Bound Recommend reads a shard archive header-only: the archive path of `descx.Predict` does `io.ReadAll`, so a bound Recommend on an archive reads every shard. Found in U22 (PR #331); see [U22 handed on](U22-recommend-explain.md#handed-on).
- **#249** — Recommend drafts every serving operator: `AGG_RATIO` (predict refuses a missing `field` the runtime ignores), `AGG_WEIGHTED_MEAN` (no weight binding), `ATTR_REG_*` and `FEAT_BUCKETIZE` (required params the manifest does not declare); see `unmappedOnFixture` in `internal/guide/bind_test.go`. Found in U22 (PR #331); see [U22 handed on](U22-recommend-explain.md#handed-on).

## Inherited from U40

- [ ] **#299** — An empty Compose (no `requests`, no `sweep`) predicts valid but the runtime refuses it with `SERVICE_VALIDATION`; make predict refuse it with the same code. Found in U40 (E1-S3).
