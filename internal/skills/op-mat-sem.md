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
| `model` | string | — | `f =~ a + b`, `y ~ f`, `a ~~ b`; `l*x`, `0.5*x`. |
| `solver` | `slsqp` \| `fisher` | `slsqp` | semopy's SLSQP, its early stop included; `fisher` converges. |
| `information` | `dense` \| `rank2` | `dense` | `rank2`: faster, same figures. |
| `ftol` | float | 1e-06 | SLSQP stop: the objective moves by less. |
| `max_iter` | int | — | Iteration cap: 10000 slsqp, 500 fisher. |
| `grad_tol` | float | 1e-10 | Fisher stop: every free \|gradient\| below it. |
| `singular` | `nearest` \| `refuse` | `nearest` | semopy's: `cov_nearest` if Cholesky fails. |
| `missing` | `listwise` | `listwise` | Listwise only. |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | string | — | Listwise only. |

## Inputs

Integer / float members.

## Output

`primary`: Σ(θ̂). `auxiliary.estimates`: rows `lval op rval` × `estimate`, `std_estimate`, `std_err`, `z_value`, `p_value`; `residual_covariance` S − Σ. `vectors`: `parameter`, `free`. `scalars`: fit indices (`chi2`, `cfi`, `rmsea`, …).

<!-- generated: reading-the-output -->

## Components

- `min_pair_n` / `max_pair_n` never (listwise).

<!-- generated: contract -->

## Gotchas

- Unused member or non-member variable: `bad_params`.
- Default stops where semopy does.
- S not factoring: `PULSE_MATRIX_NEAREST_PD`; constant member or `refuse`: fatal `PULSE_MATRIX_SINGULAR`.
- Standardized fit: vector `standardize: "zscore"` (S = correlation).
- Derived observed variable: name the request's attribute / feature label as a member.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `matrix-results`, `multivariate-design`, `op-mat-factor`, `op-mat-reliability`
