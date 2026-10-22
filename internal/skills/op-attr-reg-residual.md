---
name: op-attr-reg-residual
description: Per-row residual yᵢ − ŷᵢ from an OLS refit during the attribute prepass.
kind: operator
category: ATTR
operator: ATTR_REG_RESIDUAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

`target` / `predictors` / `penalty` / `alpha` / `l1_ratio` are top-level keys, not `params`; `fit` is `params.fit`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Regularization strength. |
| `l1_ratio` | float | — | Elasticnet mix. |
| `fit` | string | — | Score a bound fit (`fits` / `models` entry) instead of refitting: `y − score`, y the fit's target<!-- feature: REG_GLM -->; a REG_GLM's response residual<!-- /feature -->. No `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` beside it. |
| `weight` | slot weight | inherited | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. Skipped with `fit` (the fit's own weight applied when it ran). |

## Inputs

`target` / `predictors` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null**, so a downstream aggregation counts the row in `n_null`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-pass: runs an independent OLS fit per slot. With `params.fit` it is row-local (streams beside groupers, merges) and fits nothing.
- `params.fit` follows the scoring rule: an unknown name is `PULSE_MODEL_UNKNOWN`; an extension fit whose registration has no Scorer is `PULSE_MODEL_SCORE_INVALID` `no_scorer`; a refit field beside it is `slot_fields`; a failed fit nulls every row with `PULSE_FIT_UNAVAILABLE`. Predict refuses identically.
- Large residuals flag response-space outliers.
- Penalized residuals bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-fitted`
