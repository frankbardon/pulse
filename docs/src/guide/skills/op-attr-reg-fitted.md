```yaml
name: op-attr-reg-fitted
description: Per-row fitted value ŷᵢ = Xᵢ · β + β₀ from an OLS refit during the attribute prepass.
kind: operator
category: ATTR
operator: ATTR_REG_FITTED
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, buffered-pipeline]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row the value a straight-line model predicts for its target from its predictors.

Questions it answers:

- What order value would we expect for each customer given their visits and tenure?
- Which stores sell above or below what their size and footfall predict?

Use something else:

- `REG_OLS` when you want the coefficients, R-squared and p-values of the model.
- `REG_GLM` when the target is yes/no or a count.

## Params

Refit keys are top-level, not `params`; `fit` is `params.fit` ([`regression-row-scores`](regression-row-scores.md)).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Regularization strength. |
| `l1_ratio` | float | — | Elasticnet mix. |
| `fit` | string | — | Score a bound fit (`fits` / `models` entry) instead of refitting: the fit's score (a GLM's fitted mean); no refit keys beside it. |
| `weight` | slot weight | inherited | `null` opts out; a WLS refit; ignored with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — the model's prediction ŷᵢ. **Null (never 0) when any predictor is null**, so a downstream aggregation counts the row in `n_null`; a null target alone still gets ŷᵢ.

## Reading the output

- `value`: The target value the fitted straight-line model predicts for the row from its predictors: intercept + sum(coefficient * predictor), in the target's units. The model is fitted by least squares to the rows that passed the filters and have the target and every predictor.
  - Caveat: The model describes association in these rows: a fitted value is what rows with these predictor values look like here, not what would happen to a row if a predictor were changed; a causal reading needs a design that supports it.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Two-pass: each slot refits on its own (no fit sharing). With `params.fit` it is row-local and equals the bound model's score bit for bit; refusals and a failed fit (`PULSE_FIT_UNAVAILABLE`, every row null): [`regression-row-scores`](regression-row-scores.md).
- A penalty shrinks coefficients toward zero when `alpha` / `l1_ratio` is mis-tuned.

## See

- `pulse_examples_search tags=[regression]`
- Skills: [`regression-row-scores`](regression-row-scores.md), [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-residual`](op-attr-reg-residual.md)
