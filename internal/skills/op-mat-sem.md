---
name: op-mat-sem
description: Structural equation model (CFA, path model, higher-order factors) in lavaan / semopy syntax, fitted by maximum likelihood to the members' covariance — semopy's estimates, SEs, z, p, standardized estimates and fit indices (chi-square, CFI, TLI, RMSEA, AIC, BIC); listwise, frequency-weighted.
kind: operator
category: MAT
operator: MAT_SEM
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, multivariate]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `model` | string | — | `f =~ a + b`, `y ~ f`, `a ~~ b`; `l*x`, `0.5*x`. |
<!-- feature: capability:sem_meanstructure -->
| `meanstructure` | bool | false | Means; `x ~ 1` too. |
<!-- /feature -->
<!-- feature: capability:sem_groups -->
| `group` | string | — | Multi-group fit. |
| `group_reference` | string | — | Primary level. |
| `group_equal` | list | — | Invariance. |
| `group_partial` | list | — | Freed rows. |
<!-- /feature -->
<!-- feature: capability:sem_bootstrap -->
| `resample` | `bootstrap` | — | SE, p, CI. |
| `bootstrap_iters` | int | 1000 | B. |
| `rng_seed` | int | 0 | — |
| `ci_level` | float | 0.95 | — |
<!-- /feature -->
| `solver` | enum | `slsqp` | `fisher`: to optimum. |
| `information` | enum | `dense` | `rank2`: faster. |
| `ftol` | float | 1e-06 | SLSQP stop. |
| `max_iter` | int | — | Cap. |
| `grad_tol` | float | 1e-10 | Fisher stop. |
| `singular` | enum | `nearest` | `cov_nearest` fallback. |
| `missing` | enum | `listwise` | Listwise only. |
| `max_drop_share` | 0–1 | — | Drop warning. |
| `fit` | string | — | Bound fit. |

## Inputs

Integer / float members: the model's observed variables, by label.

## Output

`primary`: Σ(θ̂). `auxiliary.estimates`: rows `lval op rval` × `estimate`, `std_estimate`, `std_err`, `z_value`, `p_value` (+ `ci_*`, lavaan); `residual_covariance` S − Σ<!-- feature: capability:sem_meanstructure -->; `implied_means` / `observed_means`<!-- /feature -->. `vectors`: `parameter`, `free`; `scalars`: fit indices.

<!-- generated: reading-the-output -->

## Components

- `min_pair_n` / `max_pair_n` never (listwise).

<!-- generated: contract -->

## Gotchas

- Unused / non-member variable: `bad_params`.
- lavaan rules: `sem-modeling`<!-- feature: capability:sem_bootstrap -->; `resample`: `sem-bootstrap`<!-- /feature -->.
- S not factoring: `PULSE_MATRIX_NEAREST_PD`; constant member / `refuse`: fatal `PULSE_MATRIX_SINGULAR`.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `sem-modeling`, `matrix-results`, `multivariate-design`, `op-mat-factor`, `op-mat-reliability`
