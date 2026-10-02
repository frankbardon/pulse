---
id: U11
slug: weighting-descriptive
title: "Weighted counts, percentages and crosstabs are correct by default when a weight is set"
track: Statistical integrity
size: L
status: not-started
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

> **Already shipped (v0.39.1, forward-ported to `main`; status unchanged, nothing ticked here).** `AGG_WEIGHTED_MEAN` Components already carry `m2_weighted`, `sum_weights_sq`, `weighted_variance` and `n_eff` (Kish) alongside `sum_weights`, and `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` already tests weighted-mean crosstab cells with a required `n_basis` (`weights` | `kish`). Naming to reconcile: the shipped key is `sum_weights`, while this unit plans `w_sum` — U11 owns choosing one spelling (and any alias or deprecation) when it lands the Components floor.

## References

**Theme documents (read before starting):**
- [statistical-integrity 01 — Weighting](../v1.0.0-statistical-integrity/01-weighting.md) — Request surface, Weight validation, What becomes weight-aware (descriptive rows), Execution & contract

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#50** (4. Statistical integrity › Weighting) `Request.Weight`, per-slot `weight` (incl. `null`), `Options.DefaultWeight`, `kind` frequency / probability
- [ ] **#51** (4. Statistical integrity › Weighting) Weight validation, `n_weight_invalid`, `PULSE_WEIGHT_INVALID_ROWS`
- [ ] **#52** (4. Statistical integrity › Weighting) Weighted aggregators incl. percentiles; `AGG_WEIGHTED_MEAN` alias
- [ ] **#53** (4. Statistical integrity › Weighting) Weighted crosstab cells and margins; unweighted base via `margin_aggregations`
- [ ] **#54** (4. Statistical integrity › Weighting) Weighted share / index overlays
- [ ] **#57** (4. Statistical integrity › Weighting) Components `w_sum` / `n_eff`; manifest `weight_aware`; predict reporting; extension `WeightAware`
- [ ] **#58** (4. Statistical integrity › Weighting) SPSS weight-variable capture and suggestion

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

- [ ] All-ones weights reproduce unweighted output byte-for-byte for every weight-aware operator in scope
- [ ] Integer frequency weights equal physically duplicated rows
- [ ] A weighted crosstab's margins equal weighted raw-row margins on both buffered and fused arms
- [ ] Invalid weights are excluded and counted, never coerced
- [ ] No request without a weight changes output
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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
