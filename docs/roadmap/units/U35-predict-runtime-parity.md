---
id: U35
slug: predict-runtime-parity
title: "Predict and runtime agree on every built-in, and the runtime never answers with a wrong number"
track: API & release
size: XL
status: done
depends_on: []
soft_depends_on: [U02c]
blocks: [U32]
todo_items: [198, 199, 201, 207, 213, 214, 215, 221, 225, 226, 229, 248, 249, 253, 254, 255, 256, 259, 260, 267, 299, 300, 313, 372, 378, 379, 391, 393, 394, 400, 404, 418, 419, 427, 431, 432, 436, 437, 439, 440, 442, 448, 475, 476]
branch: predict-runtime-parity
---

# U35 — predict-runtime-parity

**Outcome:** Predict and runtime agree on every built-in, and the runtime never answers with a wrong number.

**Track:** API & release · **Size:** XL · **Soft-depends on:** [U02c](U02c-cohort-facade.md) · **Unblocks:** [U32](U32-docs-audit.md)

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

- [x] **#198** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Predict refuses every operator × field type the runtime refuses: the "predict looser" half of the `knownTypeDivergence` ledger (`internal/integration/field_type_acceptance_test.go`) is emptied. Done (E2-S2 + E2-S3): every pair predict let through and the runtime refused now refuses on both sides with one code, the decimal-on-aggregator case included (`PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`, an error on both sides); the ledger is deleted
- [x] **#199** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) The runtime refuses, rather than silently computes on, a field it cannot read: the "predict stricter, runtime wrong" half of the ledger (`FEAT_POLY`, `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` and the `WIN_*` value windows on categorical, set, decimal, `packed_bool` or `datetime` columns), plus tier-1 tests on `set_*` fields
- [x] **E2-S4 (owner-added, 2026-10-14)** Numeric readers refuse a categorical: the 16 arithmetic aggregators (`AGG_SUM` … `AGG_CI_UPPER`), `AGG_RATIO`'s numerator / denominator and `FEAT_LOG` / `SQRT` / `BUCKETIZE` refuse a categorical (its dictionary index) or `set_*` field `PULSE_FIELD_TYPE_UNSUPPORTED` on both sides; `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` narrowed to the facet's numeric-statistic warning (owner `capability:facet`); feature param refusals are `PROCESSING_CONFIG` on both sides; tier-1 `TEST_*` skill Field lists match the table (`TestAtomicSkillFieldTypesMatchFieldAcceptTable`)
- [x] **E4-S3 (owner-added, 2026-10-14)** Pairwise overlays use the sample variance and the last overlay / vector predict gaps close: `OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` over an unweighted `AGG_VARIANCE` / `AGG_STDDEV` cell read m2/(n−1) (equal to `AGG_WELFORD` within 1e-12 relative; n < 2 → the pair skips); an `OVERLAY_FORMULA` with no usable formula is `PULSE_OVERLAY_PARAM_MISSING` on every FORMULA arm, predict and runtime; predict reports the build's static vector built-in refusals (`vectors.StaticIssues`), the pairwise kinds' `PULSE_OVERLAY_COMPONENTS_REQUIRED` with components disabled, and a SERIES FORMULA `baseline_position` past a categorical-bounded partition (`PULSE_OVERLAY_REF_UNKNOWN`)
- [x] **E4-S4 (owner-added, 2026-10-14)** Compose `OVERLAY_T_CELL` / `OVERLAY_Z_CELL` use the sample variance: the dispatcher hands each handler its slots' crosstab cell types (`composeCellTypes`), so an unweighted `AGG_VARIANCE` / `AGG_STDDEV` cell reads m2/(n−1) (`overlayhost.SampleVariance`) and the layer equals the `AGG_WELFORD` one (n < 2 → `null` cell + `PULSE_OVERLAY_REF_ZERO`, as Welford)
- [x] **#201** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Remaining silent predict / runtime gaps: crosstab cell-aggregator validity in predict, one label set for the label-collision check, label bindings on ProcessChain stages ≥ 1, windowed record rows under projection (sub-gap (a), crosstab cell / margin aggregator validity, done in U35 E2 + E3-S1, inside Compose slots too; sub-gaps (b) one label set — `descriptor.OutputLabels` on both sides — and (c) stage ≥ 1 labels — now applied — done in U35 E3-S2; (d) windowed record rows — a windows-only request's rows now carry the full record under the default projection (`NeededFields` widens that shape), done in U35 E3-S3)
- [x] **#213** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) `Response.overlays` joins the feature gate (`gatedSlots`). Done (E5-S1): `Response` / `ComposedResponse` / `ChainResponse` / `FacetResult` `overlays` ride their request root's `overlays` gate, so an instance with no runnable overlay kind lists no overlay path in its payload schema or `return` presets (`TestReturnPathsMatchSchema`, `TestReturnPresetsFor_Instance`); the shared Request / Response gate now also lists the MATRIX_RESULT host (`capability:matrices`), whose `OVERLAY_MATRIX_GROUP_SUMMARY` a matrices-only profile used to refuse as `PULSE_REQUEST_UNKNOWN_FIELD` (`TestMatrixGroupSummary_MatricesOnlyInstance`)
- [x] **#214** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) A joined slot or stage derives its `return` precision-exact set from the defaults-resolved request. Verified already correct (E5-S1): `processWithJoin` defaults a shallow clone that shares the caller's `*Aggregation` pointers, so a defaulted count Type reaches the request the facade resolves `Plan.Exact` from (a chain stage defaults in place); `TestReturn_PrecisionJoinedDefaultCount` (Process join, Compose slot join, chain stage) is the regression gate — a deep copy in the join path fails it
- [x] **#215** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) `finalizeMergedPartial`'s empty-partial path emits the zero-n aggregation entries the serial path emits. Done (E5-S2): a merge no partition published is seeded with the zero-record partition state (`newPartitionPartial`), so the ungrouped zero-n row + per-slot zero-n `Components.Aggregations` and the grouped zero-bucket tail match serial (`TestFinalizeMergedPartial_EmptyPartialMatchesSerial`, `TestDecodeWorkers_EmptyCohortMatchesSerial`, `TestShardWorkers_ZeroRecordShardsMatchSerial`)
- [x] **#221** (Inherited from U18) The Compose components veto is weight-basis aware per slot. Done (E5-S2): `Service.composeSlotVetoes` resolves a per-slot level from the per-kind audit (`composeOverlaySlotVeto`): the floor kinds keep `components.crosstab` alone over a weighted slot (χ²-vs-ref: targets only) and nothing over an unweighted one, panel `cell_n_unweighted` / `row_margin_distinct_within` always keep it, every other kind keeps every sub-part (`TestComposeSlotVetoes_WeightBasis`, root `TestReturnComposeVetoWeightAware`, backstop `TestComposeOverlayFloorsPresent_CatchesMisvetoedFloorKind`)
- [x] **#207** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Shard-archive cohesion compares `Nullable`: `AddShard`, `shard verify`, the `NewCohortBuilder` anchored-append pre-check and the archive reader refuse a shard whose per-field nullability differs from the canonical schema, instead of decoding it under the canonical flags
- [x] **#248** (7. Guided analysis › Follow-ups from U22) Bound Recommend reads a shard archive header-only: the archive path of `descx.Predict` does `io.ReadAll`, so a bound Recommend on an archive reads every shard. Done (U35 E7-S3): the facade predicts / inspects through the open handle and `descriptor.Predict` / `Inspect` read an archive by random access (`io.ReaderAt`): the zip central directory, the canonical schema entry and each shard's header; `PeekShardRecordCount` counts a stored shard from its central-directory size, never reading its records; an anchor reads its one entry; `PredictFacet` / `PredictChain` now judge an archive (they read its zip bytes as a `.pulse` header and refused `ENCODING_INVALID`); the memory estimate still charges `ArchiveBytes` (`TestArchivePredict_ReadsHeadersOnly`, `TestPredictRoots_ArchiveMatchesSingleFile`)
- [x] **#249** (7. Guided analysis › Follow-ups from U22) Recommend drafts every serving operator: `AGG_RATIO` (the planner omitted the `field` the wire requires — predict and runtime both refuse its absence, the operator only ignores its value), `AGG_WEIGHTED_MEAN` (no weight binding), `ATTR_REG_*` (required params the manifest does not declare); see `unmappedOnFixture` in `internal/guide/bind_test.go`. Done (U35 E7-S3): `AGG_RATIO` drafts its numerator as `field` (wire rule kept; description corrected); `ATTR_REG_FITTED` / `RESIDUAL` / `LEVERAGE` declare top-level `target` / `predictors` (+ penalty knobs) and draft like a regression; `AGG_WEIGHTED_MEAN` binds `weight_field` to the cohort's suggested weight, else drafts with a `weight_field` need (dropped when an instance default weight resolves); `unmappedOnFixture` keeps only the two intentional `compare_groups` rows. (`FEAT_BUCKETIZE` already drafted with a `boundaries` need.)
- [x] **#253** (7. Guided analysis › Follow-ups from U23) Compose predict does not judge slot operators by name: an unknown or hidden operator in a Compose slot predicts valid, so the Explain doc's "predicts each slot" claim is inaccurate until per-slot predict runs. Done (U35 E3-S1): every Compose slot runs the Process-predict request body (`validateRequestBody`), located `details.request`; a hidden or unknown slot operator is refused as `Predict` refuses it
- [x] **#254** (7. Guided analysis › Follow-ups from U23) Chain predict does not validate stage label bindings. Done (U35 E3-S2): predict-chain runs the request body on every stage (labels, operators, accepted types, stage overlays), located `details.stage`; the runtime applies a stage ≥ 1 binding instead of dropping it
- [x] **#255** (7. Guided analysis › Follow-ups from U23) `PredictCompose` / `PredictFacet` / `PredictChain` and `pulse api predict-compose` / `-facet` / `-chain` ignore Strict and EchoRequest (the validators read neither); wire them, then add `--strict` / `--echo-request`
- [x] **#256** (7. Guided analysis › Follow-ups from U23) Bare MCP `pulse_predict` returns no `errors` / `warnings`, so `valid: false` carries no reason (the alternative roots do carry them); additive fix
- [x] **#259** (7. Guided analysis › Follow-ups from U23) The engine accepts `weight` on a facet overlay and silently ignores it (facets are never weighted); refuse it. Done (E5-S3): a set `weight` on a `FacetRequest.Overlays` spec is `PULSE_WEIGHT_UNSUPPORTED` at predict and runtime (`overlayhost.FacetSpecRefusal`); `null` / absent run
- [x] **#260** (7. Guided analysis › Follow-ups from U23) Bound `joins` field-name enums in the MCP input schemas cover only the left cohort; right-side and joined field names fall outside them (the description says so, predict resolves them); widen the enums from the join target's schema Done in U35 E6-S3: `pulse_inspect` takes `joins: [{right, as}]`; bind-on-inspect opens each target through the instance FS and `mcp.BindForInstanceWithJoins` widens the request-carrying tools' field enums with the joined names and binds `right_field` (`TestBind_JoinTarget*`, `TestRegister_BindOnInspectJoinTargets`).
- [x] **#267** (10. Vector & matrix — operators › Follow-ups from U24) Predict `row_buffer_bytes` is an upper bound for ordinary groupers only: a fan-out grouper (`GROUP_SET_PER_ELEMENT` and kin) copies a row into every label bucket, so the true buffer can exceed it; add the fan-out multiplicity, and confirm `limits.MatrixStateBytes` counts the buffered rank-method row store. Done (U35 E7-S2): `descriptor.FanOutMultiplicity` scales `row_buffer_bytes`, the estimate's row work (`limits.MatrixRowWorkBytes`, x10 — store growth + rank finalize) and per-value state; the row store was already counted separately from `MatrixStateBytes`.

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
- S2: moved to [U54](U54-multi-grouper.md) (#200, owner decision 2026-10-11)
- S3: shard cohesion compares `Nullable` in `AddShard`, `shard verify`, the builder's anchored-append pre-check and the archive reader; a parity row for nullability joins `TestCohortBuilder_AppendPrecheckParity` (#207)

### E2 — Predict refuses what the runtime refuses
- S1: predict gains the runtime's operator × field-type checks (#198); the ledger's "predict looser" entries are deleted as each is fixed
- S2: crosstab cell-aggregator validity, one label set for the collision check, ProcessChain stage ≥ 1 label bindings, windowed record rows under projection (#201)
- S4 (owner-added): numeric readers refuse a categorical; the categorical-aggregation warning retired to the facet; feature param codes agree; `TEST_*` skill Field lists held to the table

### Owner-added stories (beyond the original 21)

- **E2-S4** — numeric readers refuse a categorical (above)
- **E4-S3** — pairwise overlays use the sample variance; FORMULA with no formula is `PULSE_OVERLAY_PARAM_MISSING`; the last predict gaps for static vector built-ins, components-disabled pairwise kinds and SERIES `baseline_position`
- **E4-S4** — Compose `OVERLAY_T_CELL` / `OVERLAY_Z_CELL` use the sample variance (equal to the `AGG_WELFORD` layer within 1e-12 relative)
- **E7-S5** — join fan-out memory (#229 join half): dictionary-bound predict finding + runtime build-side backstop

## Acceptance criteria

- [x] `knownTypeDivergence` returns `""` for every operator × field type pair, and the ledger is deleted
- [x] No request with more than one `Groups` entry silently drops a group (delivered by [U54](U54-multi-grouper.md): every grouper executes, composite rows)
- [x] Every #201 gap has a predict-vs-runtime test
- [x] A shard whose field nullability differs from the canonical schema is refused with a coded error (`PULSE_SHARD_SCHEMA_MISMATCH` or a sibling) at every entry point, never decoded (#207)
- [x] `format_version` stays `"1.1"`
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestFieldTypeAcceptance_PredictMatchesRuntime` (exact ledger; an entry that stops diverging fails)
- `TestPredictNoExecutionImports`
- New predict-vs-runtime tests per #200 / #201 gap

## Update Demand companions

- `errors/fixup_metadata.go` + `internal/descriptor/error_owners.go` for any new refusal code
- The atomic `op-*` skill of every operator whose accepted types change
- `.claude/reference/predict-inspect.md` if predict's contract changes; `byte-layout.md` (projected decode) if the windowed-row fix touches the contract
- `.claude/reference/byte-layout.md` (shard cohesion) + `skills/cohort-schema-design.md` (Sharded) if the cohesion rule's wording changes (#207)

## Delivery notes

- **#313** was pulled from [U45](U45-binary-and-decimal.md) and delivered here (E2-S3, one code per refusal).
- **#393**: the `SampleWithRequest` half is moot (`SampleRequest` has no filter surface); only `FilterToFileRequest.Models` was built — the unit's one new public symbol.
- **#432** was built, not just refused: the SERIES `OVERLAY_FORMULA` handler runs (E4-S2). The refusal half was already consistent after U30; #418's Compose half and #432's refusal consistency were likewise fixed by U30.
- **#214** was verified already correct (E5-S1), with a regression test; no production change.
- **#229**: the join half is done (E7-S5); the projection half is a non-goal (an over-estimate is safe).
- `format_version` stays `"1.1"`; every break is listed in [`03-embedder-migration.md`](../v1.0.0-api-and-release/03-embedder-migration.md) "Changes from U35". Ships with U30 in `v1.0.0-alpha.13` (tag at finalize, after human approval).

## Handed on from U35

Open items found while delivering this unit, filed as TODOs under their owner units (TODO ids, not GitHub issues).

| Item | Owner |
|---|---|
| #456 `ATTR_SCALE_SCORE` / `ATTR_PROFILE_SIMILARITY` merge admission needs a tally-merge hook on the shard + parallel-decode reducers (perf; serial path correct) | [U51](U51-archive-scale.md) |
| #457 no dedicated synth-writer test that a non-nullable field's bitmap bit stays 0 | [U52](U52-synth-realism.md) |
| #458 join `ON` key for `u64` > 2^53 still matches on its float echo (`encoding/join_keys.go`) | [U51](U51-archive-scale.md) (re-homed from U34) |
| #459 map-decoder fallback `Record`s carry no exact `u64` word | [U34](U34-extension-validation.md) (done: `extend.Record.Uint64Value`) |
| #460 `Options.AutoLabels` not applied on chain stages >= 1 | [U44](U44-strict-input.md) |
| #461 `PredictCompose` archive slots read the whole archive (not header-only); resolves with #455 | [U51](U51-archive-scale.md) |
| #462 facet runtime warnings carry no details (`FacetResult.Warnings` is `[]string`) | [U44](U44-strict-input.md) |
| #230 extension `FansOut` grouper has no memory bound — already inside the extension state-size hint | [U34](U34-extension-validation.md) |
| #463 four op skills over the soft 1200 B budget (`op-attr-reg-fitted` / `-residual` / `-leverage`, `op-agg-ratio`) | [U32](U32-docs-audit.md) |

Owner-unit choices for #458, #459, #460 and #462 are the closest fit, not an owner decision: re-home them if a better unit exists.

## Handed on from U25

| Item | Source |
|---|---|
| Per-Groups tests | [U25](U25-multivariate-tests-segmentation.md) #404 |

## Handed on from U26

| Item | Source |
|---|---|
| Overlay-formula unknown-callee parity (request and compose hosts) | [U26](U26-vector-expr-functions.md) #418 |
| Static vector length mismatch at predict | [U26](U26-vector-expr-functions.md) #419 |

## Human inputs & decisions

- **Decided (owner, 2026-10-11):** multi-entry `Groups` executes as composite keys; #200 moved to [U54](U54-multi-grouper.md), which runs right after U27.

## Inherited from U03 / U05

Pre-existing gaps, none caused by those units and none fixed there. Each makes predict promise a result the runtime refuses, or makes the runtime answer with a wrong number instead of an error. U03 recorded them in its PR (#301) and U05 in its unit doc ("Notes: pre-existing gaps found incidentally").

- **Field-type acceptance ledger (#198, #199, U45 #313) — closed.** The `knownTypeDivergence` ledger is deleted (E2-S3): `TestFieldTypeAcceptance_PredictMatchesRuntime` now requires predict and `Process` to accept the same types and, where both refuse, to raise ONE identical code, the accepted-types table's (`internal/fieldaccept`). The "predict looser" pairs were refused in predict; the "runtime wrong" pairs (a categorical's dictionary index read as a quantity, a set's bitmask, decimal over its float echo) were refused in the runtime, never widened; the narrower manifest declarations whose runtime figures proved correct (`decimal128` / `datetime` / `packed_bool` on the quantity readers) were widened. U45 #313 (codes that say what they mean) was delivered here.
- **Tier-1 tests on `set_*` fields read `n = 0`** rather than being refused (#199).
- **Only `Groups[0]` executed** (#200, DONE in [U54](U54-multi-grouper.md): every arm now executes every grouper). Historical note: `internal/processing/processor.go` reads `req.Groups[0]`, and later entries are silently ignored. `parallel_reduce.go` and `shard_reduce.go` assume a single grouper too.
- **Predict accepts a bad crosstab cell aggregator** (#201). A never-registered, or decimal128-unsupported, `Crosstab.Cell` aggregator passes predict, but the runtime refuses it. The fusion rules [U02c](U02c-cohort-facade.md) moves to a no-execute home sit beside this check.
- **Label-binding collision check** (#201). Predict and the runtime pass it different label sets.
- **ProcessChain stage ≥ 1 ignores label bindings** (#201). They are silently dropped, where they should be applied or refused.
- **Windowed record rows differ under projection** (#201). They carry only the projected fields where the unprojected path carries the full row, which contradicts the output-transparent projection contract (`.claude/reference/byte-layout.md`, projected decode).

## Inherited from U02c

- **Shard cohesion ignores `Nullable`** (#207). Found while adding the U02c anchored-append pre-check (PR #311): `AddShard`'s structural cohesion compares name, type, order and set rung but never the per-field nullable flag, and the pre-check reuses that code, so it inherits the gap. The dangerous direction is an archive field declared non-nullable receiving a shard whose field is nullable: when the bitmap width still matches (other nullable fields exist), the stride check passes and the archive reader, which decodes an anchored shard under the CANONICAL schema, would ignore that field's null bits, so a null could come back as a value (inferred from the code path, not yet reproduced — write the failing test first). Fix in the shared cohesion check so every caller picks it up; decide whether non-nullable→nullable widening is a legal rewrite (like set widening) or a refusal.

## Inherited from U17

- **`Response.overlays` is not feature-gated** (#213). *Historical, fixed in E5-S1:* `Response` / `ComposedResponse` / `ChainResponse` / `FacetResult` `overlays` now follow their request root's `overlays` gate (see the #213 item above).
- **Joined slot / stage `Plan.Exact`** (#214). *Historical, verified correct in E5-S1:* the join path defaults a shallow clone that shares the caller's `*Aggregation` pointers, so the precision-exact set already derives from the defaults-resolved request (`TestReturn_PrecisionJoinedDefaultCount` pins it). The Compose-level (`overlays` root) plan still has no `Exact` set of its own; give it one if an overlay payload ever carries a float count.
- **`finalizeMergedPartial` empty partial** (#215). A merged (parallel decode / shard) run whose partial is empty emits no `Aggregations` block while the serial path emits zero-n entries, ungrouped and grouped. Pre-existing; found by E1's per-group parity gates.

## Inherited from U18

- **Compose components veto is not weight-basis aware** (#221). U18 follow-up PR #320 made the request-overlay veto precise: `descx.OverlayReadsHostComponents(kind, cellBasis)` keeps `components.crosstab` for χ² / Fisher layers only on a weighted host (frequency reads `sum_weights`, probability also `n_eff`); pairwise kinds always. The Compose veto (`composeSlotVetoes` / `composeSlotContext`, `internal/service/compute_plan.go`) still keeps EVERY `Components` sub-part of every slot a Compose overlay names, whatever the kind or weighting. Apply the same rule per slot: resolve each named slot's crosstab-cell basis with `descx.CrosstabCellWeightBasis(req, defaultWeight, inst)`, keep only `components.crosstab`, and only when the overlay kind reads floors on that basis — first verify per Compose kind (`internal/processing/overlay_compose_handlers.go`) which ones read `CellComponents` triples on an UNWEIGHTED slot (the mean kinds' Welford-triple path does — `extractCellComponentsTriple`), since those must keep vetoing. Compute saving only; the fold-time invariant `Service.composeOverlayFloorsPresent` (PROCESSING_INTERNAL) backstops a wrong rule for the floor kinds. Gates to extend: `TestReturnSkipsComputation_Compose`, root `TestReturnKeptNumberInvariant`, `TestReturnComposeParallelTimeoutKeepsComponentsVeto`.

## Inherited from U19

- [x] **Simple `Facet` has no limits pre-flight** (#225). U19 wired the `MaxEstimatedMemory` pre-flight into the rich facet path after `checkFacetFieldRefs` (`internal/service/facet_rich.go`), but `pulse.Facet(path, field)` builds no request and skips it. Route it through the same estimate (`descx.EstimateFacetMemory`) and extend `TestLimitsPredictRuntimeParity`. Done (U35 E7-S1): the simple `Facet`'s streamed path runs `FacetLimitRefusal` before the iterator opens (`TestLimitsPreflight_MaxEstimatedMemoryRefusesBeforeDecode` host `simple facet`); the categorical fast path reads no record and skips it (`TestFacet_DictionaryPathSkipsMemoryPreflight`).
- [x] **No predict limit findings for Compose / chain / facet** (#226). `ValidateCompose` / `ValidateChain` and the facet validator compute no `LimitFindings`; `TestLimitsPredictRuntimeParity` treats `max_compose_slots`, `max_chain_stages` and facet group counts as unknowable at predict. Add the findings (via `internal/descriptor.RequestLimitFindings`) where a facade predict surface exists or is added. Done (U35 E7-S1): `LimitFindings` on the Compose, chain and facet predict results, located by `LimitFinding.Details`; predict-chain refuses `MaxChainStages`; chain stages >= 1 carry no certain memory finding; `TestLimitsPredictRuntimeParity` judges every host through its own predict root.
- [x] **Memory estimate precision** (#229). U19's model (`internal/limits/memory.go`) is records x (3 x stride + `RecordBytes(schema)`) at full schema width: calibration (`TestMemoryEstimate_Calibration`, table in `.claude/reference/predict-inspect.md`) shows 1.33x-4.94x over peak heap, worst on projected buffered runs. It also assumes at most one right match per left row, so a 1:N fan-out join can exceed the estimate (the one under-estimate). Model projection and join fan-out (e.g. from the right key dictionary) without losing the upper-bound property. Join half done (U35 E7-S5): the only SOUND header-only bound is left x right, and the dictionary bound left x (right − D + 1) holds only when every entry occurs, so predict grades the fan-out finding `possible` (`descx.JoinRowsBound`; a one-row right side is the only provably unique key) and the runtime refuses the measured fan-out after the build, before the probe (`descx.JoinFanOutRefusal`, `HashJoinIterator.MaxMatches`); gates `TestLimitsPredictRuntimeParity` (join / Compose slot / chain stage 0 cases), `TestLimits_JoinFanOut_PossibleAndBackstop`, `TestJoinFanOutBackstop_RefusesBeforeProbe`, calibration case `join fan-out` (1.20). The projection half is a non-goal by decision (an over-estimate is safe).

## Inherited from U22

- **#248** — Bound Recommend reads a shard archive header-only: the archive path of `descx.Predict` does `io.ReadAll`, so a bound Recommend on an archive reads every shard. Found in U22 (PR #331); see [U22 handed on](U22-recommend-explain.md#handed-on).
- **#249** — Recommend drafts every serving operator: `AGG_RATIO` (the planner omitted the `field` the wire requires — predict and runtime both refuse its absence, the operator only ignores its value), `AGG_WEIGHTED_MEAN` (no weight binding), `ATTR_REG_*` (required params the manifest does not declare); see `unmappedOnFixture` in `internal/guide/bind_test.go`. Found in U22 (PR #331); see [U22 handed on](U22-recommend-explain.md#handed-on).

## Inherited from U40

- [x] **#299** — An empty Compose (no `requests`, no `sweep`) predicts valid but the runtime refuses it with `SERVICE_VALIDATION`; make predict refuse it with the same code. Done (U35 E3-S1): same code and message as `Service.expandCompose`. Found in U40 (E1-S3).
- [x] **#300** — A sweep rank cannot read a figure that serialises with `omitempty` when it is exactly 0 (e.g. `RegressionResult.residual_std_err`): the key vanishes, so the slot is excluded as `missing` instead of ranked first. Read the Go value, or drop `omitempty` on such figures. Found in U40 (E2-S2). Done in U35 E6-S2: rank re-reads a missing final segment off the typed `Response` (`internal/sweep/rank_typed.go`); an `omitempty` numeric figure that is exactly 0 ranks as 0 when its applicability rule says the fit computed it (`residual_std_err` / `r2` / `adj_r2` on `REG_OLS` / `REG_BAYES_LINEAR`, `deviance` / `null_deviance` / `pseudo_r2` on `REG_GLM`); an uncomputed or unlisted figure and a nil entry stay missing. Wire `omitempty` unchanged.

## Inherited from U41

Filed from [U41 handed on](U41-derived-cohort.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#372** — Bit-packed reads agree on legacy cohorts: export (`internal/io/export.go`) reads the whole byte of a `u4` / `packed_bool` field while the engine `RecordReader` family and the SPSS writer read the stored `BitPosition`, so a legacy cohort with a nonzero stored `BitPosition` exports different values than it processes. **Required resolution: proper reads — every reader (engine, export, SPSS, lookup) decodes legacy cohorts correctly and identically; no re-import.** Found in U41 (E1-S5).
- [x] **#378** — A compose `sweep` body inside a stored request template: the renderer consumes the sweep's `{{axis}}` placeholders; extend the `for_each` scope rule in `internal/template/for_each.go` to `sweep.axes`. Found in U41 (E3-S4). Done in U35 E6-S2: an object's `sweep.axes` names scope over that sweep's `request`, `overlays` and `label` (never its `axes` / `mode` / `rank`), reusing the literal-name and `PULSE_TEMPLATE_AXIS_COLLISION` checks; fixture `internal/template/testdata/channel-sweep.json`.
- [x] **#379** — Cohort-bound MCP schemas refuse a cohort field whose name contains a `{{ident}}` token on an item without `for_each` (the placeholder `else` branch); admit literal field names that match the pattern. Found in U41 (E3-S4). Done in U35 E6-S3: the `else` mirror is `not {pattern, not {enum}}`, so a literal enum member like `{{x}}` validates without `for_each` (`TestBind_LiteralPlaceholderFieldName`).

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#391** — `internal/mergegate`'s built-in row-local list includes `ATTR_CODE_IN` and the `ATTR_SET_*` attributes (pre-existing omission). Found in U42 (E1-S2). Done (E5-S3): `ATTR_CODE_IN`, `ATTR_SET_HAS`, `ATTR_SET_POPCOUNT` join the list; `ATTR_SCALE_SCORE` / `ATTR_PROFILE_SIMILARITY` stay off (per-run Components / warning tallies the reducers do not fold)
- [x] **#393** — `FilterToFileWithRequest` and `SampleWithRequest` load `Request.models`; today a `coef()` there is refused as `PULSE_MODEL_UNKNOWN` (loud, never a wrong number). Found in U42 (E3-S2). Done in U35 E6-S1: `FilterToFileRequest.Models` loads models for the predicate's `coef()` (gated by `capability:models`), each file's content SHA-256 joining the dedup predicate hash. The `SampleWithRequest` half is moot: `SampleRequest` has no filter surface (Cohort, N, Labels), so nothing there can call `coef()`.
- [x] **#394** — A direct test of model binding on the parallel-decode path (`parallel_reduce.go` → `BuildFiltersWithModels`), which needs an mmap'd OS file; the shard path is tested. Found in U42 (E2-S3). Done in U35 E6-S1: `TestCoefFilter_ParallelDecodeMatchesSerial` (`fit_bound_parallel_test.go`) runs a `coef()` `FILTER_EXPRESSION` through the parallel-decode reducer at 1/2/4 workers against the serial run.

## Inherited from U43

Filed from [U43 handed on](U43-post-aggregation-ratios.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#400** — Predict-compose does not run per-slot `Request.Overlays` validation: a slot-level `OVERLAY_FORMULA` on a non-crosstab host is refused at runtime (`PULSE_OVERLAY_KIND_UNKNOWN`) but predict-compose is silent (pre-existing) (the runtime code is now `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). Done (U35 E3-S1): slot overlays go through `ValidateOverlays`, located `details.request`. Found in U43 (E2-S2).
- [x] **#404** (10. Vector & matrix — operators › Follow-ups from U25) Per-`Groups` statistical tests: `TEST_HOTELLING_T2`, `TEST_MANOVA`, `TEST_BOX_M`, `TEST_MARDIA`, `TEST_BARTLETT_SPHERICITY` and the fitted attributes on multi-entry `Groups`, decided with #200. Done (E5-S3): `PULSE_MATRIX_SINGULAR` / `_INSUFFICIENT_N` join `sparseTestCodes`; the five run per group (equal to the filtered run, composite keys included) and warn rather than abort on a degenerate group; fitted attributes stay cohort-wide by design

## Inherited from U26

Filed from [U26 handed on](U26-vector-expr-functions.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#418** (10. Vector & matrix — operators › Follow-ups from U26) Overlay-formula callee parity: request-host `OVERLAY_FORMULA` predict reports an unknown callee (neither built-in nor embedder) as `PULSE_OVERLAY_FORMULA_INVALID_IDENT` while the runtime gives a parse error; compose-host predict checks no unknown callee beyond the vector built-ins. Found in U26 (E2-S2).
- [x] **#419** (10. Vector & matrix — operators › Follow-ups from U26) Static `PROCESSING_VECTOR_LENGTH_MISMATCH` at predict for `dot` / `dist` / `cosine` over named vectors of known, differing lengths (runtime already refuses). Found in U26 (E2-S2).

## Inherited from U27

Filed from [U27 handed on](U27-vector-metrics-aggregates.md#handed-on-from-u27); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#427** (10. Vector & matrix — operators › Follow-ups from U27) A `u64` `key_field` value above 2^53 is matched via its float64 value (records carry numbers as float64; the same limit as `ATTR_CODE_IN`), so two distinct large keys can collide. No unit owns exact `u64` record access, so it is filed here: give record access an exact `u64` path, or refuse such a key. Found in U27 (E3-S4).

## Inherited from U54

Filed from the U54 series-overlay refusal follow-up ([U54](U54-multi-grouper.md)); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#431** (Grouped analysis › Follow-ups from U54) `OVERLAY_INDEX_VS_ROLLING_MEAN` / `OVERLAY_ZSCORE_VS_ROLLING` with `params.window: 1` answer NaN from the second present point on: `rollingCarrier`'s eviction divides by `count - 1 == 0` (`internal/processing/overlay_index_vs_rolling_mean.go`), while predict accepts window 1 (`validateOverlayRollingWindowParam` checks only `> 0`). Fix the window-1 eviction (or refuse window 1 at predict and runtime alike). — FIXED (E4-S2): window-1 eviction resets to the empty sample (window 1 of the index = `OVERLAY_INDEX_VS_PRIOR`); `OVERLAY_ZSCORE_VS_ROLLING` refuses `window < 2` on both sides (`overlayhost.RollingWindowFloorRefusal`)
- [x] **#432** (Grouped analysis › Follow-ups from U54) `OVERLAY_FORMULA` on a grouped Process (SERIES) host passes predict — the SERIES namespace `value` / `total` / `prior` / `baseline` validates — but the runtime refuses it `PULSE_OVERLAY_KIND_UNKNOWN`: `seriesOverlayHandlers` has no FORMULA entry. Either wire a SERIES FORMULA handler or refuse it at predict; a wired handler's `prior` is a lag-1 read, so it needs the multi-grouper refusal (`refuseCompositeSeriesHost`) the prior / rolling kinds carry. The Compose-predict side is #400. — BUILT (E4-S2): the refusal half was already consistent (U30 `ScalarSpecRefusal` / shape refusal); the SERIES handler now runs (`processing.applyFormulaSeriesWith`, shared gate `formulaident.RequestSpecRefusal`); `refuseCompositeSeriesHost` no longer exists (U54), so a composite host runs FORMULA per partition like the prior / rolling kinds

## Inherited from U46

Filed from [U46](U46-grouped-analysis.md); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#436** (Grouped analysis › Follow-ups from U46) predict-compose / predict-chain report an unknown overlay `field` on both compared stages as two errors where the runtime raises one; align the count. Done (U35 E3-S2): one error on both sides — compose stops at the first slot (reference, then targets), chain at the first stage (target, then ref). Found in U46 (E1-S4).
- [x] **#437** (Grouped analysis › Follow-ups from U46) `MaxEstimatedMemory` ignores the per-group state a grouped tier-1 test holds (tests x groups), though `MaxGroups` grades the product; decide whether the memory estimate should count it. Found in U46 (E3-S1). Done (U35 E7-S2): test instances x 1024 + levels x 256 + buffered-test values x 32 (`TestEstimateMemory_Terms`, calibration).
- [x] **#439** (Grouped analysis › Follow-ups from U46) An ungrouped (SCALAR) Process host: predict accepts overlay kinds (e.g. `OVERLAY_FORMULA`) the runtime silently returns no layer for; refuse identically on both sides. Found in U46 (E4-S15). Done in U30 E7-S5: `overlayhost.ScalarSpecRefusal` refuses every Process kind on predict and every runtime arm.
- [x] **#440** (Grouped analysis › Follow-ups from U46) Request-host MATRIX `OVERLAY_FORMULA` with an unknown identifier: predict `PULSE_OVERLAY_FORMULA_INVALID_IDENT` vs runtime `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; reuse `internal/formulaident` on the Request host. Found in U46 (E4-S15).
- [x] **#442** (Grouped analysis › Follow-ups from U46) A Compose overlay spec with no targets passes predict-compose but the runtime refuses `PULSE_OVERLAY_TARGET_UNKNOWN` (every single-target kind); refuse on predict. Found in U46 (E4-S15). Done in U30 E7-S4: `ComposeSlotRefusals` refuses an empty `targets` on predict-compose and the runtime alike.
- [x] **#448** (Grouped analysis › Follow-ups from U46) Moment pairwise kinds (`OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` / `_WEIGHTED_TWO_MEANS_Z`) over an `AGG_MODE` cell are refused only by the runtime cell-shape rule; predict may be silent. Found in U46 (E4-S17).

## Handed on from U47

Filed from [U47](U47-survey-tables.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#475** Optional `PULSE_ADVISORY_*` for an `ATTR_FORMULA` that can yield null without a guard: predict cannot know, so an advisory is a contract call (owner). Found in U47 (E4-S5)
- [ ] **#476** `OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` on a multi-measure crosstab whose measures have mixed cell types is refused whole; refuse per measure, predict and runtime alike. Found in U47 (E4-S5)
