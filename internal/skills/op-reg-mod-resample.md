---
name: op-reg-mod-resample
description: Spec-level resampling modifier (jackknife — leave-one-out, grouped or by cluster — / bootstrap) that composes with an OLS, GLM, multinomial or model-regression fit; replaces analytical std errors with empirical estimates. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_RESAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, resampling, jackknife, buffered-pipeline]
---

Overwrites `StdErrors` / `PValues` in `Response.Regressions[i]`; the point estimate stays the full-data fit.

<!-- generated: use-when -->

## Params

Flat keys on the host spec. A **wrapper, not a fit**: the host refits per replicate (leave-one-out `n`, delete-a-group `g`, bootstrap `bootstrap_iters`).

| Name | Type | Default | Description |
|---|---|---|---|
| `resample` | enum | — | `""`, `jackknife`, `bootstrap` (non-parametric). |
| `bootstrap_iters` | int | `1000` | Bootstrap replicates, used as given (`<= 0` → `1000`). |
| `jackknife_groups` | int | — | Jackknife only: delete-a-group over `g` seeded random groups (2 ≤ g ≤ fitted rows, sizes within one). Absent = leave-one-out. |
| `jackknife_by` | field | — | Jackknife only: delete-a-cluster, one replicate per distinct value (≥ 2); a null or non-finite value drops the row. Exclusive with `jackknife_groups` (`PROCESSING_CONFIG`). |
| `rng_seed` | int | — | Seeds the bootstrap and the `jackknife_groups` draw. |

## Inputs

Inherits `target` / `predictors` from an OLS, GLM (any penalty) or model host<!-- feature: REG_ORDINAL --> (`REG_ORDINAL` keeps the full fit's levels)<!-- /feature --><!-- feature: REG_MULTINOMIAL -->, or `REG_MULTINOMIAL` (keeps its categories; any penalty)<!-- /feature -->; the Bayesian fit refuses it.

## Output

- `StdErrors[name]` — jackknife `√((g−1)/g · Σ(β₍ₖ₎ − β̄)²)` over its `g` replicates (leave-one-out: `g` = the fitted rows; a row the fit drops is no replicate); bootstrap sample std of replicates.
- NB re-estimates θ per replicate; `ThetaStdErr` is θ̂'s replicate SE.
- A delete-a-group jackknife echoes `jackknife_groups` (`g`; the distinct `jackknife_by` values); leave-one-out omits it.
- `PValues[name]` — jackknife pivotal; bootstrap percentile-method two-sided.
- `Resample` echoes back; `Coefficients` unchanged; `margins` are re-estimated per replicate (`regression-margins`). Under `l1` / `elasticnet` it supplies the null SE / p (penalized refits) and silences `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.
- Bootstrap echoes `bootstrap_requested` and `bootstrap_succeeded` (SE / p / vcov use only successes). A failed refit is skipped (`succeeded < requested`: unstable fit); under 10 successes is `PROCESSING_REGRESSION_NO_CONVERGE`.

## Gotchas

- Forces buffered (predict reports it).
- Leave-one-out costs n²: `PULSE_ADVISORY_JACKKNIFE_COST` at ≥ 10,000 records; use `jackknife_groups` or `jackknife_by`.
- Every jackknife replicate must refit: one failure (e.g. separation) fails the slot with that refit's code, `details.jackknife_group` naming it. `g` above the fitted rows, or one cluster, is `PROCESSING_REGRESSION_INSUFFICIENT_DATA`.
- Wraps a `selection`'s selected-feature fit.
- `rng_seed: 0` is deterministic but cohort-dependent (`params` key order, `vcov`, `margins` never move it); pin non-zero across cohorts.
- Jackknife is weighted (JK1); bootstrap under a weight → `PULSE_WEIGHT_UNSUPPORTED`.

## See

- `pulse_examples_search tags=[jackknife]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`, `op-reg-mod-selection`
