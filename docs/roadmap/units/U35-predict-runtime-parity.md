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
todo_items: [198, 199, 201, 207, 213, 214, 215, 221, 248, 249, 253, 254, 255, 256, 259, 260, 267, 372, 378, 379, 391, 393, 394, 400, 404, 418, 419, 427, 431, 432, 436, 437, 439, 440, 442, 448]
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

- [x] **#198** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Predict refuses every operator × field type the runtime refuses: the "predict looser" half of the `knownTypeDivergence` ledger (`field_type_acceptance_test.go`) is emptied. Done (E2-S2 + E2-S3): every pair predict let through and the runtime refused now refuses on both sides with one code, the decimal-on-aggregator case included (`PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`, an error on both sides); the ledger is deleted
- [x] **#199** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) The runtime refuses, rather than silently computes on, a field it cannot read: the "predict stricter, runtime wrong" half of the ledger (`FEAT_POLY`, `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` and the `WIN_*` value windows on categorical, set, decimal, `packed_bool` or `datetime` columns), plus tier-1 tests on `set_*` fields
- [ ] **#201** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Remaining silent predict / runtime gaps: crosstab cell-aggregator validity in predict, one label set for the label-collision check, label bindings on ProcessChain stages ≥ 1, windowed record rows under projection
- [ ] **#213** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) `Response.overlays` joins the feature gate (`gatedSlots`)
- [ ] **#214** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) A joined slot or stage derives its `return` precision-exact set from the defaults-resolved request
- [ ] **#215** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) `finalizeMergedPartial`'s empty-partial path emits the zero-n aggregation entries the serial path emits
- [x] **#207** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) Shard-archive cohesion compares `Nullable`: `AddShard`, `shard verify`, the `NewCohortBuilder` anchored-append pre-check and the archive reader refuse a shard whose per-field nullability differs from the canonical schema, instead of decoding it under the canonical flags
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
- S2: moved to [U54](U54-multi-grouper.md) (#200, owner decision 2026-10-11)
- S3: shard cohesion compares `Nullable` in `AddShard`, `shard verify`, the builder's anchored-append pre-check and the archive reader; a parity row for nullability joins `TestCohortBuilder_AppendPrecheckParity` (#207)

### E2 — Predict refuses what the runtime refuses
- S1: predict gains the runtime's operator × field-type checks (#198); the ledger's "predict looser" entries are deleted as each is fixed
- S2: crosstab cell-aggregator validity, one label set for the collision check, ProcessChain stage ≥ 1 label bindings, windowed record rows under projection (#201)

## Acceptance criteria

- [x] `knownTypeDivergence` returns `""` for every operator × field type pair, and the ledger is deleted
- [x] No request with more than one `Groups` entry silently drops a group (delivered by [U54](U54-multi-grouper.md): every grouper executes, composite rows)
- [ ] Every #201 gap has a predict-vs-runtime test
- [x] A shard whose field nullability differs from the canonical schema is refused with a coded error (`PULSE_SHARD_SCHEMA_MISMATCH` or a sibling) at every entry point, never decoded (#207)
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
- [ ] **#300** — A sweep rank cannot read a figure that serialises with `omitempty` when it is exactly 0 (e.g. `RegressionResult.residual_std_err`): the key vanishes, so the slot is excluded as `missing` instead of ranked first. Read the Go value, or drop `omitempty` on such figures. Found in U40 (E2-S2).

## Inherited from U41

Filed from [U41 handed on](U41-derived-cohort.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#372** — Bit-packed reads agree on legacy cohorts: export (`internal/io/export.go`) reads the whole byte of a `u4` / `packed_bool` field while the engine `RecordReader` family and the SPSS writer read the stored `BitPosition`, so a legacy cohort with a nonzero stored `BitPosition` exports different values than it processes. **Required resolution: proper reads — every reader (engine, export, SPSS, lookup) decodes legacy cohorts correctly and identically; no re-import.** Found in U41 (E1-S5).
- [ ] **#378** — A compose `sweep` body inside a stored request template: the renderer consumes the sweep's `{{axis}}` placeholders; extend the `for_each` scope rule in `internal/template/for_each.go` to `sweep.axes`. Found in U41 (E3-S4).
- [ ] **#379** — Cohort-bound MCP schemas refuse a cohort field whose name contains a `{{ident}}` token on an item without `for_each` (the placeholder `else` branch); admit literal field names that match the pattern. Found in U41 (E3-S4).

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#391** — `internal/mergegate`'s built-in row-local list includes `ATTR_CODE_IN` and the `ATTR_SET_*` attributes (pre-existing omission). Found in U42 (E1-S2).
- [ ] **#393** — `FilterToFileWithRequest` and `SampleWithRequest` load `Request.models`; today a `coef()` there is refused as `PULSE_MODEL_UNKNOWN` (loud, never a wrong number). Found in U42 (E3-S2).
- [ ] **#394** — A direct test of model binding on the parallel-decode path (`parallel_reduce.go` → `BuildFiltersWithModels`), which needs an mmap'd OS file; the shard path is tested. Found in U42 (E2-S3).

## Inherited from U43

Filed from [U43 handed on](U43-post-aggregation-ratios.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#400** — Predict-compose does not run per-slot `Request.Overlays` validation: a slot-level `OVERLAY_FORMULA` on a non-crosstab host is refused at runtime (`PULSE_OVERLAY_KIND_UNKNOWN`) but predict-compose is silent (pre-existing). Found in U43 (E2-S2).
- [ ] **#404** (10. Vector & matrix — operators › Follow-ups from U25) Per-`Groups` statistical tests: `TEST_HOTELLING_T2`, `TEST_MANOVA`, `TEST_BOX_M`, `TEST_MARDIA`, `TEST_BARTLETT_SPHERICITY` and the fitted attributes on multi-entry `Groups`, decided with #200

## Inherited from U26

Filed from [U26 handed on](U26-vector-expr-functions.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#418** (10. Vector & matrix — operators › Follow-ups from U26) Overlay-formula callee parity: request-host `OVERLAY_FORMULA` predict reports an unknown callee (neither built-in nor embedder) as `PULSE_OVERLAY_FORMULA_INVALID_IDENT` while the runtime gives a parse error; compose-host predict checks no unknown callee beyond the vector built-ins. Found in U26 (E2-S2).
- [ ] **#419** (10. Vector & matrix — operators › Follow-ups from U26) Static `PROCESSING_VECTOR_LENGTH_MISMATCH` at predict for `dot` / `dist` / `cosine` over named vectors of known, differing lengths (runtime already refuses). Found in U26 (E2-S2).

## Inherited from U27

Filed from [U27 handed on](U27-vector-metrics-aggregates.md#handed-on-from-u27); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#427** (10. Vector & matrix — operators › Follow-ups from U27) A `u64` `key_field` value above 2^53 is matched via its float64 value (records carry numbers as float64; the same limit as `ATTR_CODE_IN`), so two distinct large keys can collide. No unit owns exact `u64` record access, so it is filed here: give record access an exact `u64` path, or refuse such a key. Found in U27 (E3-S4).

## Inherited from U54

Filed from the U54 series-overlay refusal follow-up ([U54](U54-multi-grouper.md)); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#431** (Grouped analysis › Follow-ups from U54) `OVERLAY_INDEX_VS_ROLLING_MEAN` / `OVERLAY_ZSCORE_VS_ROLLING` with `params.window: 1` answer NaN from the second present point on: `rollingCarrier`'s eviction divides by `count - 1 == 0` (`internal/processing/overlay_index_vs_rolling_mean.go`), while predict accepts window 1 (`validateOverlayRollingWindowParam` checks only `> 0`). Fix the window-1 eviction (or refuse window 1 at predict and runtime alike).
- [ ] **#432** (Grouped analysis › Follow-ups from U54) `OVERLAY_FORMULA` on a grouped Process (SERIES) host passes predict — the SERIES namespace `value` / `total` / `prior` / `baseline` validates — but the runtime refuses it `PULSE_OVERLAY_KIND_UNKNOWN`: `seriesOverlayHandlers` has no FORMULA entry. Either wire a SERIES FORMULA handler or refuse it at predict; a wired handler's `prior` is a lag-1 read, so it needs the multi-grouper refusal (`refuseCompositeSeriesHost`) the prior / rolling kinds carry. The Compose-predict side is #400.

## Inherited from U46

Filed from [U46](U46-grouped-analysis.md); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#436** (Grouped analysis › Follow-ups from U46) predict-compose / predict-chain report an unknown overlay `field` on both compared stages as two errors where the runtime raises one; align the count. Found in U46 (E1-S4).
- [ ] **#437** (Grouped analysis › Follow-ups from U46) `MaxEstimatedMemory` ignores the per-group state a grouped tier-1 test holds (tests x groups), though `MaxGroups` grades the product; decide whether the memory estimate should count it. Found in U46 (E3-S1).
- [x] **#439** (Grouped analysis › Follow-ups from U46) An ungrouped (SCALAR) Process host: predict accepts overlay kinds (e.g. `OVERLAY_FORMULA`) the runtime silently returns no layer for; refuse identically on both sides. Found in U46 (E4-S15). Done in U30 E7-S5: `overlayhost.ScalarSpecRefusal` refuses every Process kind on predict and every runtime arm.
- [ ] **#440** (Grouped analysis › Follow-ups from U46) Request-host MATRIX `OVERLAY_FORMULA` with an unknown identifier: predict `PULSE_OVERLAY_FORMULA_INVALID_IDENT` vs runtime `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; reuse `internal/formulaident` on the Request host. Found in U46 (E4-S15).
- [x] **#442** (Grouped analysis › Follow-ups from U46) A Compose overlay spec with no targets passes predict-compose but the runtime refuses `PULSE_OVERLAY_TARGET_UNKNOWN` (every single-target kind); refuse on predict. Found in U46 (E4-S15). Done in U30 E7-S4: `ComposeSlotRefusals` refuses an empty `targets` on predict-compose and the runtime alike.
- [ ] **#448** (Grouped analysis › Follow-ups from U46) Moment pairwise kinds (`OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` / `_WEIGHTED_TWO_MEANS_Z`) over an `AGG_MODE` cell are refused only by the runtime cell-shape rule; predict may be silent. Found in U46 (E4-S17).
