```yaml
name: sem-modeling
description: Structural equation modelling with MAT_SEM — which convention a fit follows (semopy's covariance-only default or lavaan's once a mean structure is in play), the ~ 1 syntax for intercepts and latent means, and how to read the mean-structure outputs.
kind: design
type: guide
applies_to: process, compose, predict
covers: [SEM, structural equation model, CFA, mean structure, intercepts, latent means, lavaan, semopy, fit indices]
requires: [MAT_SEM]
```

# Structural equation modelling

`MAT_SEM` fits a lavaan / semopy model description to one vector's members ([`op-mat-sem`](op-mat-sem.md) for every param). This skill is the why: which convention a fit follows, and how to model means.

## Which convention applies

| The slot sets... | Convention | What follows |
|---|---|---|
| no lavaan-only feature (a covariance-only model) | semopy 2.3.11's default fit | SLSQP with semopy's early stop at `ftol` 1e-6, variances bounded at 0, semopy's `calc_stats` (`loglik` is the minimised discrepancy, GFI = NFI), estimates columns `estimate` … `p_value` |
| `params.meanstructure: true`, or any `x ~ 1` / `f ~ 1` line | lavaan's ML fit (`meanstructure = TRUE`, expected information, `fixed.x`) | Fisher scoring to the optimum (`solver` defaults to `fisher`; `slsqp` stays available), variances unbounded, lavaan's χ² and fit indices, Wald `ci_lower` / `ci_upper` columns |

Under lavaan conventions:

- `chi2` = N × `objective`; `dof` counts the means (p(p+1)/2 + p moments, less an observed exogenous block's own moments);
- the baseline is the independence model (variances and means free, covariances 0);
- `rmsea` divides by N; `cfi`, `tli`, `nfi` are lavaan's;
- `gfi` / `agfi` are lavaan's `gfi_lisrel` / `agfi_lisrel` (lavaan's own `agfi`);
- `loglik` is the log-likelihood, and `aic` / `bic` derive from it.

The same covariance model can report a different `chi2` on the two paths. Compare fits on ONE convention: set `meanstructure` on every slot of a comparison.

## Modelling means

- `meanstructure: true` frees every observed intercept and fixes every latent mean to 0. An observed exogenous variable's mean (a pure predictor) is fixed to its sample mean (lavaan's `fixed.x`) and hidden from the table, as its variance is.
- `x ~ 1` frees an intercept, `x ~ 0*1` fixes it, `x ~ a*1` labels it (equal across every `a*1`). `f ~ 1` frees a latent mean.
- To identify a free latent mean, fix its marker indicator's intercept: `f ~ 1` plus `x1 ~ 0*1`.
- A `~ 1` line switches the mean structure on whatever `meanstructure` says.

## Reading the outputs

- `auxiliary.estimates` gains `x ~1` rows: intercepts for observed variables, means for latents. `std_estimate` is the estimate over the variable's model-implied SD.
- `auxiliary.implied_means` (μ(θ̂)) and `observed_means` (the listwise sample means) carry one `mean` column per member. Their gap is the mean misfit.
- `ci_lower` / `ci_upper` are 95% Wald intervals (estimate ± 1.96 SE). They are `null` on a fixed row.

## See

- [`op-mat-sem`](op-mat-sem.md)
- [`multivariate-design`](multivariate-design.md) — which multivariate operator answers which question.
- [`matrix-results`](matrix-results.md) — the slot, encodings and refusals.
