```yaml
name: op-reg-mod-resample
description: Spec-level resampling modifier (jackknife — leave-one-out, grouped or by cluster — / bootstrap) that composes with an OLS or GLM fit; replaces analytical std errors with empirical estimates. Non-empty value forces the buffered path.
kind: operator
category: REG
operator: REG_RESAMPLE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, resampling, jackknife, buffered-pipeline]
```

Overwrites `StdErrors` / `PValues` in `Response.Regressions[i]`; the point estimate stays the full-data fit.

## Params

Flat keys on the host spec. A **wrapper, not a fit**: the host refits per replicate (leave-one-out `n`, delete-a-group `g`, bootstrap `bootstrap_iters`).

| Name | Type | Default | Description |
|---|---|---|---|
| `resample` | enum | — | `""`, `jackknife`, `bootstrap` (non-parametric). |
| `bootstrap_iters` | int | `1000` | Bootstrap replicates, used as given (`<= 0` → `1000`). |
| `jackknife_groups` | int | — | Jackknife only: delete-a-group over `g` seeded random groups (2 ≤ g ≤ fitted rows, sizes within one). Absent = leave-one-out. |
| `jackknife_by` | field | — | Jackknife only: delete-a-cluster, one replicate per distinct value (≥ 2); a null, NaN or ±Inf value drops the row from the fit. Exclusive with `jackknife_groups` (`PROCESSING_CONFIG`). |
| `rng_seed` | int | — | Seeds the bootstrap and the `jackknife_groups` draw. |

## Inputs

Inherits `target` / `predictors` from an OLS or GLM host (any penalty); the Bayesian fit refuses it — credible intervals already convey uncertainty.

## Output

- `StdErrors[name]` — jackknife `√((g−1)/g · Σ(β₍ₖ₎ − β̄)²)` over its `g` replicates (leave-one-out: `g` = the fitted rows; a row the fit drops is no replicate); bootstrap sample std of replicates.
- A delete-a-group jackknife echoes `jackknife_groups` (`g`; the distinct `jackknife_by` values); leave-one-out omits it.
- `PValues[name]` — jackknife pivotal; bootstrap percentile-method two-sided.
- `Resample` echoes back; `Coefficients` unchanged. Under `l1` / `elasticnet` it supplies the null SE / p (penalized refits) and silences `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.
- Bootstrap echoes `bootstrap_requested` (replicates run) and `bootstrap_succeeded` (refits that succeeded; SE / p / vcov use only these). A failed refit is skipped, so `succeeded < requested` flags an unstable fit; under 10 successes is `PROCESSING_REGRESSION_NO_CONVERGE`.

## Gotchas

- Any `resample` forces buffered; predict reports the downgrade.
- Leave-one-out costs n² — `PULSE_ADVISORY_JACKKNIFE_COST` at ≥ 10,000 records; use `jackknife_groups: 50` or a cluster in `jackknife_by`.
- Every jackknife replicate must refit: one failure (e.g. separation once a group is gone) fails the slot with that refit's code, `details.jackknife_group` naming it. `g` above the fitted rows, or one cluster, is `PROCESSING_REGRESSION_INSUFFICIENT_DATA`.
- Composes with `selection` — the resample wraps the selected-feature fit.
- `rng_seed: 0` is deterministic but cohort-dependent; pin non-zero to reproduce across cohorts.
- Jackknife is weighted (JK1); bootstrap under a weight → `PULSE_WEIGHT_UNSUPPORTED`.

## See

- `pulse_examples_search tags=[jackknife]`
- Skills: [`regression-modeling`](regression-modeling.md), [`op-reg-ols`](op-reg-ols.md), [`op-reg-glm`](op-reg-glm.md), [`op-reg-mod-selection`](op-reg-mod-selection.md)
