---
id: U11
slug: weighting-descriptive
title: "Weighted counts, percentages and crosstabs are correct by default when a weight is set"
track: Statistical integrity
size: L
status: done
depends_on: [U04]
soft_depends_on: []
blocks: [U12, U16, U22]
todo_items: [50, 51, 52, 53, 54, 57, 58]
branch: weighting-descriptive
---

# U11 — weighting-descriptive

**Outcome:** Weighted counts, percentages and crosstabs are correct by default when a weight is set.

**Track:** Statistical integrity · **Size:** L · **Depends on:** [U04](U04-profiles-model.md) · **Unblocks:** [U12](U12-weighting-inferential.md), [U16](U16-matrix-result.md), [U22](U22-recommend-explain.md)

## Summary

Introduce first-class weighting and apply it to descriptive figures: `Request.Weight` + per-slot override (incl. `null`), `kind`, validation, weighted aggregators (incl. weighted percentiles, `AGG_WEIGHTED_MEAN` alias), weighted crosstab cells/margins, share/index overlays, components/manifest/predict/extension plumbing, and SPSS weight capture.

> **Already shipped (v0.39.1, forward-ported to `main`; status unchanged, nothing ticked here).** `AGG_WEIGHTED_MEAN` Components already carry `m2_weighted`, `sum_weights_sq`, `weighted_variance` and `n_eff` (Kish) alongside `sum_weights`, and `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` already tests weighted-mean crosstab cells with a required `n_basis` (`weights` | `kish`). Naming to reconcile: the shipped key is `sum_weights`, while this unit plans `w_sum` — U11 owns choosing one spelling (and any alias or deprecation) when it lands the Components floor. **Resolved: `sum_weights` everywhere, no `w_sum` alias** (the `w_sum` spellings below are the original plan).

## References

**Theme documents (read before starting):**
- [statistical-integrity 01 — Weighting](../v1.0.0-statistical-integrity/01-weighting.md) — Request surface, Weight validation, What becomes weight-aware (descriptive rows), Execution & contract

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#50** (4. Statistical integrity › Weighting) `Request.Weight`, per-slot `weight` (incl. `null`), `Options.DefaultWeight`, `kind` frequency / probability
- [x] **#51** (4. Statistical integrity › Weighting) Weight validation, `n_weight_invalid`, `PULSE_WEIGHT_INVALID_ROWS`
- [x] **#52** (4. Statistical integrity › Weighting) Weighted aggregators incl. percentiles; `AGG_WEIGHTED_MEAN` alias
- [x] **#53** (4. Statistical integrity › Weighting) Weighted crosstab cells and margins; unweighted base via `margin_aggregations`
- [x] **#54** (4. Statistical integrity › Weighting) Weighted share / index overlays
- [x] **#57** (4. Statistical integrity › Weighting) Components `w_sum` / `n_eff`; manifest `weight_aware`; predict reporting; extension `WeightAware`
- [x] **#58** (4. Statistical integrity › Weighting) SPSS weight-variable capture and suggestion

## Scope

**In scope**
- Request/Options/slot weight surface; `kind` frequency/probability
- Validation + `PULSE_WEIGHT_INVALID_ROWS`
- Weighted `AGG_*` (non-weightable ones refuse explicit weights)
- Weighted crosstab + unweighted base via `margin_aggregations`
- Weighted share/index overlays
- Components `w_sum`/`n_eff`, manifest `weight_aware`, predict, extension `WeightAware`
- SPSS weight variable → sidecar → suggestion
- Unity and frequency-expansion parity gates for these operators

**Out of scope**
- Tests, significance overlays, attributes, regressions (U12)
- Design-based variance (out of v1)

## Epics & stories

> As shipped the unit ran as five epics — E1 a weighted request (surface, resolver, core aggregators, parity gates), E2 every aggregator weighted or refusing (shape and counting aggregators, inferential refusals, external reference fixtures), E3 weighted crosstabs on both arms plus share / index overlays, E4 extensions, the feature gate and the SPSS suggestion, E5 documentation — not the E1–E3 outline below, which is kept as the original plan.

Each epic is a vertical slice. Commit with `feat|fix|perf|test(weighting-descriptive/E<n>-S<m>): …`; close each epic with `milestone(weighting-descriptive/E<n>): vertical slice complete — <epic title>`.

### E1 — Requests can carry a weight
- S1: surface + precedence + validation + warning
- S2: components floor, manifest `weight_aware`, predict per-slot reporting, extension `WeightAware`

### E2 — Descriptive figures honour it
- S1: weighted aggregators incl. weighted percentiles; alias
- S2: weighted crosstab cells and margins (buffered + fused arms)
- S3: weighted share/index overlays
- S4: SPSS weight capture and suggestion

### E3 — Weighting is provably right
- S1: `TestWeightUnityParity` + `TestWeightFrequencyExpansionParity` for every operator touched here
- S2: reference fixtures for weighted percentages and means

## Acceptance criteria

- [x] All-ones weights reproduce unweighted output byte-for-byte for every weight-aware operator in scope
- [x] Integer frequency weights equal physically duplicated rows
- [x] A weighted crosstab's margins equal weighted raw-row margins on both buffered and fused arms
- [x] Invalid weights are excluded and counted, never coerced
- [x] No request without a weight changes output
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestWeightUnityParity`
- `TestWeightFrequencyExpansionParity`

## Update Demand companions

- Atomic skills `## Params` for every weight-aware operator
- New topical skill `skills/weighting.md`
- Payload-schema golden; CLAUDE.md Output Format Contract (Components `w_sum`/`n_eff`)
- `response-components.md` (floor change)
- `update-demand.md` row for `Request.Weight`
- `errors/fixup_metadata.go`

## Human inputs & decisions

- None.

## Notes

- Weighted sums and means merge exactly; streaming and shard eligibility must not regress (assert with `CanMergeRequest` tests).

## Shipped: deviations and decisions

Contract: `.claude/reference/weighting.md`; agent skill `skills/weighting.md`; user guide `docs/src/library/weighting.md`; embedder rows in `03-embedder-migration.md` (Changes from U11).

- **Spelling.** The floor key is `sum_weights` (kept from `AGG_WEIGHTED_MEAN`); there is no `w_sum` alias. The floor is `sum_weights` / `n_eff` (probability only) / `n_weight_invalid`, present only on a weighted slot; counts stay raw ints.
- **Per-slot weight beyond aggregations.** `types.SlotWeight` also rides tests, regressions, attributes, overlays and `types.Group` (groups and crosstab axes) — mainly so `"weight": null` can opt an inferential slot out under `Options.DefaultWeight` (the quantile grouper could otherwise never run on such an instance).
- **Inferential surfaces refuse, not skip.** Built-in tests, regressions, the four reference-distribution attributes, `GROUP_QUANTILE`, `AGG_CI_*` and every `Inferential` overlay (except the weighted pairwise two-means z) are `PULSE_WEIGHT_UNSUPPORTED` while ANY weight is in force, the instance default included; `weight: null` opts out. This is the list U12 lifts. A REQUEST weight with a window is `PROCESSING_CONFIG`; the default is skipped.
- **Overlay host rule.** An inferential overlay refuses only when a weight reaches the OVERLAY (its own slot, the request, the default). A host weighted only by its own slot weight (or `AGG_WEIGHTED_MEAN`'s `weight_field`) does not refuse it — the shipped pairwise `n_source` modes read such hosts on purpose. The weighted pairwise two-means z also accepts a weighted `AGG_AVERAGE` cell.
- **Weighted quantile rule.** Hmisc 5.3.0 `wtd.quantile(type = "quantile")`: x₍ₖ₎ is the first value whose cumulative weight reaches the 1-based rank; `probability` weights rescaled so Σw = n (`normwt = TRUE`, scale-invariant), `frequency` weights raw (type 7 on the expansion). Cumulative weights within 1e-9·max(1, |c|) of an integer are snapped to it so the answer is the exact-arithmetic one on every platform; on such a knife edge Pulse can differ from Hmisc's own float output. Replaced E2-S1's first rule ("exceeds the 0-based rank"), which differed from Hmisc wherever cumulative weights are fractional.
- **`AGG_WEIGHTED_MEAN` is an alias** of weighted `AGG_AVERAGE`: exact Σwx/Σw (a ≤ few-ULP shift, tests at 1e-12 relative), `weight_field` optional, negative / NaN / ±Inf weights excluded and warned, and over `decimal128` now `PULSE_WEIGHT_UNSUPPORTED` on both predict and runtime. Its `weight_field` is deliberately not gated by `capability:weighting`.
- **Non-finite figures are `null` on the wire** (E3 fix): `types.MarshalFinite` plus `MarshalJSON` on sixteen result types, so a 0/0 figure no longer fails the whole serialisation. Result-side float slots in the payload schema widened to `["number", "null"]`; `format_version` stays `"1.1"`.
- **Extensions (E4-S1).** `WeightAware` on aggregator, attribute and test registrations; `extend.Record.Weight()` (an interface-method API break for test doubles). A WeightAware extension TEST runs weighted (not refused like a built-in); a WeightAware ATTRIBUTE still receives invalid-weight rows, with `Weight()` reporting false; an extension aggregator over `decimal128` is not refused (it owns its weighted form); only WeightAware tests are probed with a weight, so existing test registrations keep working. Non-aware: an aggregator is refused `PULSE_EXTENSION_NOT_WEIGHT_AWARE` under an explicit weight, an attribute or test under any weight.
- **Feature gate.** `capability:weighting` hides every `weight` key, the schema / manifest / predict surfaces and `suggested_weight`; `Options.DefaultWeight` under a hiding profile fails `pulse.New` with `PULSE_FEATURE_PROFILE_DEPENDENCY` rather than being ignored.
- **SPSS suggestion.** Inspect and predict read the SPSS sidecar (header + schema + sidecar metadata, never records) and report `suggested_weight` with `kind: "probability"`; it is never applied, and the docs note that `WEIGHT BY` is frequency-like.
- **Reference fixtures.** Every weight-aware aggregator is pinned at 1e-12 to statsmodels / numpy / scipy and R Hmisc values generated offline (`internal/service/testdata/weight_reference/`); neither Python nor R runs in CI.

## Handed on

| Item | Owner |
|---|---|
| Weighted inference: lift the refusal list above (tests, regressions, reference attributes, `GROUP_QUANTILE`, `AGG_CI_*`, inferential overlays) | U12 |
| `pulse api predict` (CLI) does not echo `suggested_weight`: it reads the request with `os.ReadFile` + `PredictBytes`, which has no cohort path for the sidecar; needs a path-based `PredictEnvelope` | new (CLI predict parity) |
| `LookupResult`, `SampleResult` and `pulse.Row` have no `MarshalJSON`, so a non-finite float there still fails `encoding/json` (marshal them with `types.MarshalFinite`) | U36 / new |
| `OVERLAY_INDEX_VS_STAGE` (chain) and `OVERLAY_INDEX_VS_POP` (facet) are untested on weighted hosts — their hosts carry no top-level weight in U11 | new (when chain / facet weighting lands) |
| A nested per-slot `weight` in a request template is refused only at execution, not at render | new (templates) |
| `types.MarshalFinite`'s reflective walk is unmeasured on large responses | U19 / new (perf) |
| No `internal/examples/` entry carries a `weight` (no fixture cohort has a weight column) | new (examples) |
| Facet weighting, top-level Compose / chain weights, windows and `decimal128` weighting | out of scope (post-U12) |
