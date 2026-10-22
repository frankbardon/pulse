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
| `ftol` | float | 1e-06 | SLSQP's ftol (SciPy's and semopy's default 1e-6), a number above 0: the run stops when the objective moves by less. |
| `max_iter` | int | — | Iteration cap, a positive integer: default 10000 for slsqp (semopy's setting), 500 for fisher. |
| `grad_tol` | float | 1e-10 | Fisher scoring's stop, a number above 0: every free gradient entry below it in absolute value. |
| `missing` | `listwise` | `listwise` | Listwise only. |
| `max_drop_share` | float | — | Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1). |
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
- Singular S: fatal `PULSE_MATRIX_SINGULAR`.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: `matrix-results`, `multivariate-design`, `op-mat-factor`, `op-mat-reliability`
