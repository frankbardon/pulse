```yaml
name: op-mat-sem
description: Structural equation model (CFA, path model, higher-order factors) in lavaan / semopy syntax, fitted by maximum likelihood to the members' covariance — semopy's estimates, SEs, z, p, standardized estimates and fit indices (chi-square, CFI, TLI, RMSEA, AIC, BIC); listwise, frequency-weighted.
kind: operator
category: MAT
operator: MAT_SEM
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, multivariate]
```

Members = the model's observed variables, by label.

## Use when

Whether a theory of how measures hang together holds: the hidden qualities they reflect, how those drive each other, how well it fits.

Questions it answers:

- Do these brand-image items load on the five personality dimensions we designed them for, and does the model fit?
- How strongly does the overall quality we measure through these three sub-scales drive satisfaction and loyalty?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `model` | string | — | `f =~ a + b`, `y ~ f`, `a ~~ b`, `x ~ 1`; `l*x`, `0.5*x`. |
| `meanstructure` | bool | false | Means (lavaan rules). |
| `group` | string | — | Multi-group fit. |
| `group_reference` | string | — | Primary level. |
| `group_equal` | list | — | Invariance. |
| `group_partial` | list | — | Freed rows. |
| `solver` | `slsqp` \| `fisher` | `slsqp` | semopy's SLSQP (early stop); `fisher` converges. |
| `information` | `dense` \| `rank2` | `dense` | `rank2`: faster. |
| `ftol` | float | 1e-06 | SLSQP objective-change stop. |
| `max_iter` | int | — | Cap: 10000 slsqp, 500 fisher. |
| `grad_tol` | float | 1e-10 | Fisher stop: max \|gradient\|. |
| `singular` | `nearest` \| `refuse` | `nearest` | `cov_nearest` fallback. |
| `missing` | `listwise` | `listwise` | Listwise only. |
| `max_drop_share` | 0–1 | — | Drop warning. |
| `fit` | string | — | Listwise only. |

## Inputs

Integer / float members.

## Output

`primary`: Σ(θ̂). `auxiliary.estimates`: rows `lval op rval` × `estimate`, `std_estimate`, `std_err`, `z_value`, `p_value` (+ Wald `ci_*`, lavaan); `residual_covariance` S − Σ; `implied_means` / `observed_means`. `vectors`: `parameter`, `free`; `scalars`: fit indices.

## Reading the output

- `primary.values`: Each cell is the covariance the fitted model implies between the row's and the column's measure, in their own units. Compared with the observed covariance (auxiliary.residual_covariance is the difference), it shows where the model reproduces the data and where it does not.
- `scalars.chi2_p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Components

- `min_pair_n` / `max_pair_n` never (listwise).

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Unused / non-member variable: `bad_params`.
- Means, `group`: lavaan ([`sem-modeling`](sem-modeling.md)).
- S not factoring: `PULSE_MATRIX_NEAREST_PD`; constant member / `refuse`: fatal `PULSE_MATRIX_SINGULAR`.

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: [`sem-modeling`](sem-modeling.md), [`matrix-results`](matrix-results.md), [`multivariate-design`](multivariate-design.md), [`op-mat-factor`](op-mat-factor.md), [`op-mat-reliability`](op-mat-reliability.md)
