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

## Use when

Whether a theory of how measures hang together holds: the hidden qualities they reflect, how those drive each other, how well it fits.

Questions it answers:

- Do these brand-image items load on the five personality dimensions we designed them for, and does the model fit?
- How strongly does the overall quality we measure through these three sub-scales drive satisfaction and loyalty?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `model` | string | — | lavaan. |
| `meanstructure` | bool | false | Means; `x ~ 1` too. |
| `group` | string | — | Multi-group fit. |
| `group_reference` | string | — | Primary level. |
| `group_equal` | list | — | Invariance. |
| `group_partial` | list | — | Freed rows. |
| `resample` | `bootstrap` | — | SE, p, CI. |
| `bootstrap_iters` | int | 1000 | B. |
| `rng_seed` | int | 0 | — |
| `ci_level` | float | 0.95 | — |
| `estimator` | enum | `ml` | Ratings: [`sem-ordinal`](sem-ordinal.md). |
| `ordered` | list | — | — |
| `parameterization` | enum | `delta` | — |
| `solver` | enum | `slsqp` | `fisher`. |
| `information` | enum | `dense` | — |
| `ftol` | float | 1e-06 | — |
| `max_iter` | int | — | — |
| `grad_tol` | float | 1e-10 | — |
| `singular` | enum | `nearest` | — |
| `missing` | enum | `listwise` | — |
| `max_drop_share` | 0–1 | — | Warn. |
| `fit` | string | — | Bound fit. |

## Inputs

The model's observed variables.

## Output

`primary`: Σ(θ̂). `auxiliary.estimates`: rows `lval op rval` × estimate, std, SE, z, p (+ `ci_*`); `residual_covariance` S − Σ; `implied_means` / `observed_means`. `vectors`: `parameter`, `free`; `scalars`: fit indices.

## Reading the output

- `primary.values`: Each cell is the covariance the fitted model implies between the row's and the column's measure, in their own units. Compared with the observed covariance (auxiliary.residual_covariance is the difference), it shows where the model reproduces the data and where it does not.
- `scalars.chi2_p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Components

- `min_pair_n` / `max_pair_n` never (listwise).

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds `p_adjusted` and `significant_adjusted` beside `estimates` `p_value`, every row with a p one hypothesis; families `request`, `matrix` (default).
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Unused / non-member: `bad_params`.
- lavaan rules: [`sem-modeling`](sem-modeling.md); `:=`: [`sem-defined-parameters`](sem-defined-parameters.md); `resample`: [`sem-bootstrap`](sem-bootstrap.md).
- Non-PD S: `PULSE_MATRIX_NEAREST_PD`; constant member / `refuse`: `PULSE_MATRIX_SINGULAR`; empty level: `PULSE_MATRIX_GROUP_LEVEL_EMPTY` (`:=` on it: `empty_group`).

## See

- `pulse_examples_search tags=[matrix, multivariate]`
- Skills: [`sem-modeling`](sem-modeling.md), [`matrix-results`](matrix-results.md), [`multivariate-design`](multivariate-design.md), [`op-mat-factor`](op-mat-factor.md)
