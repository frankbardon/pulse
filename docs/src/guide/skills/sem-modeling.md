```yaml
name: sem-modeling
description: Structural equation modelling with MAT_SEM — which convention a fit follows (semopy's covariance-only default or lavaan's) and how to read the outputs. The meanstructure param and the ~ 1 syntax for intercepts and latent means. Multi-group fits and the invariance ladder (group_equal, group_partial, c(...) modifiers).
kind: design
type: guide
applies_to: process, compose, predict
covers: [SEM, structural equation model, CFA, mean structure, intercepts, latent means, multi-group, measurement invariance, group_equal, lavaan, semopy, fit indices]
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

## Multi-group fits

`params.group` names a categorical, `packed_bool` or `u4` / `u8` field. Its levels are the groups of ONE joint fit (lavaan's `group =`):

- Configural (no `group_equal`): every parameter is free per group. A labelled parameter (`a*x`) is one value in every group, as lavaan reads a label.
- A group fit always models the means and follows lavaan.
- The levels are the field's values that hold rows, in dictionary order (else ascending). A null group drops the row. Predict's `group_levels` bounds them (`unknown` on an integer field).
- `estimates` rows are `[level] lval op rval`. `vectors.group` and `vectors.label` give each row's level and model label.
- `primary` is the `group_reference` level's Σ̂ (default: the first level). Per level, the auxiliaries are `implied_covariance.<level>`, `residual_covariance.<level>`, `implied_means.<level>` and `observed_means.<level>`.
- The scalars are joint (`chi2` sums the groups, `rmsea` carries lavaan's √G), plus `chi2_group_<level>` and `n_group_<level>` (Σw under a frequency weight).
- Refused: request `groups` with `group`, a vector that standardizes, another field type, a one-entry dictionary.
- The slot fails alone (`PULSE_MATRIX_INSUFFICIENT_N`) on fewer than two levels with rows, on a level with no more rows than observed variables, or on a reference level without rows.

## Invariance

- `group_equal` holds lavaan `group.equal` classes equal across groups: `loadings`, `intercepts`, `means`, `residuals`, `residual.covariances`, `lv.variances`, `lv.covariances`, `regressions`. Metric = `["loadings"]`, scalar = `["loadings","intercepts"]`.
- Equal intercepts (without `means`) free the latent means outside the `group_reference` level, which stays at 0. They read in marker units: the non-reference level's shift on each factor.
- `group_partial` frees rows from their class, spelled as lavaan does: `v2~1`, `f=~x2`, `x1~~x2`. A freed marker loading is free outside the reference.
- A marker loading stays 1 in every group. A labelled row keeps its label's equality, not its class's.
- `c(a1,a2)*x` / `c(0.5,NA)*x` set a label or value per level, in level order (categorical or `packed_bool` field only). `NA*x` frees a loading (on a first indicator: no marker).
- Ladder: one slot per step, or a zip sweep over `group_equal` (a comma-separated string, `""` = configural): axes `step` [configural, metric, scalar] and `eq` [`""`, `loadings`, `loadings,intercepts`], label `{{step}}`. Compare `chi2`, `dof`, `cfi`, `rmsea` step to step; Pulse computes no Δχ² test.

## Members

- Standardized: vector `standardize: "zscore"` (not with `group`).
- Derived variable: an attribute / feature label as a member.

## See

- [`op-mat-sem`](op-mat-sem.md)
- [`multivariate-design`](multivariate-design.md) — which multivariate operator answers which question.
- [`matrix-results`](matrix-results.md) — the slot, encodings and refusals.
