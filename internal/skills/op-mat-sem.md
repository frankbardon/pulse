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

Members = the model's observed variables, by label.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `model` | string | — | `f =~ a + b`, `y ~ f`, `a ~~ b`, `x ~ 1`; `l*x`, `0.5*x`. |
| `meanstructure` | bool | false | Means (lavaan rules). |
| `group` | string | — | Multi-group fit. |
| `group_reference` | string | — | Primary level. |
| `solver` | `slsqp` \| `fisher` | `slsqp` | semopy's SLSQP (early stop); `fisher` converges. |
| `information` | `dense` \| `rank2` | `dense` | `rank2`: faster. |
| `ftol` | float | 1e-06 | SLSQP objective-change stop. |
| `max_iter` | int | — | Cap: 10000 slsqp, 500 fisher. |
| `grad_tol` | float | 1e-10 | Fisher stop: max \|gradient\|. |
| `singular` | `nearest` \| `refuse` | `nearest` | `cov_nearest` if Cholesky fails. |
| `missing` | `listwise` | `listwise` | Listwise only. |
| `max_drop_share` | 0–1 | — | Drop-share warning. |
| `fit` | string | — | Listwise only. |

## Inputs

Integer / float members.

## Output

`primary`: Σ(θ̂). `auxiliary.estimates`: rows `lval op rval` × `estimate`, `std_estimate`, `std_err`, `z_value`, `p_value` (+ `ci_lower`, `ci_upper` if lavaan); `residual_covariance` S − Σ; means: `implied_means`, `observed_means`. `vectors`: `parameter`, `free`; `scalars`: fit indices.

<!-- generated: reading-the-output -->

## Components

- `min_pair_n` / `max_pair_n` never (listwise).

<!-- generated: contract -->

## Gotchas

- Unused / non-member variable: `bad_params`.
- Default: semopy's early stop.
- Means (`meanstructure`, `~ 1`), `group`: lavaan, `sem-modeling`.
- S not factoring: `PULSE_MATRIX_NEAREST_PD`; constant member / `refuse`: fatal `PULSE_MATRIX_SINGULAR`.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `sem-modeling`, `matrix-results`, `multivariate-design`, `op-mat-factor`, `op-mat-reliability`
