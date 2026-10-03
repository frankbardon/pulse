---
id: U12
slug: weighting-inferential
title: "Significance tests and models are correct on weighted survey data"
track: Statistical integrity
size: M
status: not-started
depends_on: [U11]
soft_depends_on: []
blocks: []
todo_items: [55, 56, 59]
branch: weighting-inferential
---

# U12 — weighting-inferential

**Outcome:** Significance tests and models are correct on weighted survey data.

**Track:** Statistical integrity · **Size:** M · **Depends on:** [U11](U11-weighting-descriptive.md) · **Unblocks:** —

## Summary

Extend weighting to inference: weighted `TEST_*` and significance overlays using Kish `n_eff` for probability weights, weighted attributes (`ATTR_ZSCORE`/`PERCENTILE`/`NORMALIZED`), weighted `GROUP_QUANTILE`, and weighted regressions (WLS / weighted IRLS). Completes the weighting gates and fixtures.

> **Already shipped (v0.39.1, forward-ported to `main`; status unchanged, nothing ticked here).** The weighted pairwise two-means z overlay `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` exists, with a required `n_basis` param: `weights` (`n = Σw`) or `kish` (`n = n_eff`). It reads `m2_weighted`, `sum_weights_sq`, `weighted_variance` and `n_eff` off `AGG_WEIGHTED_MEAN` cell Components. The remaining overlays, tests, attributes and regressions in this unit are not started. The existing `sum_weights` key versus the planned `w_sum` naming is U11's to reconcile.

## References

**Theme documents (read before starting):**
- [statistical-integrity 01 — Weighting](../v1.0.0-statistical-integrity/01-weighting.md) — What becomes weight-aware (inferential rows), kind=probability / Kish n_eff

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#55** (4. Statistical integrity › Weighting) Weighted tests and significance overlays with Kish `n_eff`
- [ ] **#56** (4. Statistical integrity › Weighting) Weighted attributes, `GROUP_QUANTILE`, regressions
- [ ] **#59** (4. Statistical integrity › Weighting) `TestWeightUnityParity`, `TestWeightFrequencyExpansionParity`, reference fixtures; `weighting.md` skill

## Scope

**In scope**
- Weighted tests + significance overlays with `n_eff`
- Weighted attributes, `GROUP_QUANTILE`, `REG_*`
- Windows refuse explicit weights
- Reference fixtures for t-test and χ²

**Out of scope**
- Design-based variance

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(weighting-inferential/E<n>-S<m>): …`; close each epic with `milestone(weighting-inferential/E<n>): vertical slice complete — <epic title>`.

### E1 — Inference uses the right sample size
- S1: `n_eff` plumbing; weighted `TEST_*`
- S2: weighted significance overlays (prop-z, t, z, pairwise, χ²)

### E2 — Row scores and models honour weights
- S1: weighted attributes + `GROUP_QUANTILE`
- S2: weighted regressions
- S3: parity gates extended; reference fixtures

## Acceptance criteria

- [ ] Weighted t-test and χ² match published reference results on fixtures
- [ ] Under `kind: probability`, overlay summaries report `n_eff` and p-values use it
- [ ] Unity/frequency parity gates cover every weight-aware operator
- [ ] Windows given an explicit weight refuse with `PROCESSING_CONFIG`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Parity gates extended to all weight-aware operators

## Update Demand companions

- Atomic skills for every touched test/overlay/attribute/regression
- `weighting.md` inference section
- Purpose/Interpretation updated where weighting changes how output reads (`n_eff`)

## Human inputs & decisions

- Reviewer glance at the `n_eff` semantics (the statistics reviewer who signs off U08's guidance at [U33](U33-v1-release.md), #206)
