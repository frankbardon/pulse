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

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row the value a straight-line model predicts for its target from its predictors.

Questions it answers:

- What order value would we expect for each customer given their visits and tenure?
- Which stores sell above or below what their size and footfall predict?

Use something else:

- `REG_OLS` when you want the coefficients, R-squared and p-values of the model.
- `REG_GLM` when the target is yes/no or a count.

## Params

| Name | Type | Description |
|---|---|---|
| `Target` | string | Dependent field (required). |
| `Predictors` | []string | Independent fields (required). |
| `Penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `Alpha` | float | Regularization strength. |
| `L1Ratio` | float | Elasticnet mix. |
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Target` / `Predictors` | numeric (no `decimal128`) |
| `Label` | required — new column name |

## Output

One `float64` per record — the model's prediction ŷᵢ. NaN-free over the filter-passing rows used to fit.

## Reading the output

- `value`: The target value the fitted straight-line model predicts for the row from its predictors: intercept + sum(coefficient * predictor), in the target's units. The model is fitted by least squares to the rows that passed the filters and have the target and every predictor.
  - Caveat: The model describes association in these rows: a fitted value is what rows with these predictor values look like here, not what would happen to a row if a predictor were changed; a causal reading needs a design that supports it.

## Gotchas

- Two-pass: prepass refits independently per ATTR_REG_* slot (Option A — no fit sharing).
- `Mergeable` per-shard; predict reports streamability.
- Penalized fits (`l1`/`l2`/`elasticnet`) reuse the same machinery; mis-tuned `Alpha`/`L1Ratio` shrinks coefficients toward zero.

## See

- `pulse_examples_search tags=[regression]`
- Skills: [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-residual`](op-attr-reg-residual.md)
