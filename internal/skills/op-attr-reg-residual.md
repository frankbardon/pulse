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

Top-level keys, not `params` — except `params.fit`; `field` unread.

| Name | Type | Description |
|---|---|---|
| `target` | field | Dependent field (required without `params.fit`). |
| `predictors` | list | Independent fields (required without `params.fit`). |
| `penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | Regularization strength. |
| `l1_ratio` | float | Elasticnet mix. |
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. With `params.fit` skipped (the fit's own weight applied when it ran). |
| `params.fit` | string | Score a bound fit (`fits` / `models` entry) instead of refitting: `y − score`, y the fit's target<!-- feature: REG_GLM -->; a REG_GLM's response residual<!-- /feature -->. No `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` beside it. |

## Inputs

| Param | Accepted field types |
|---|---|
| `target` / `predictors` | numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>` |

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null**, so a downstream aggregation counts the row in `n_null`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

<!-- generated: reading-the-output -->

## Gotchas

- Two-pass: runs an independent OLS fit per slot. With `params.fit` it is row-local (streams beside groupers, merges) and fits nothing.
- `params.fit` follows the scoring rule: an unknown name is `PULSE_MODEL_UNKNOWN`; an extension fit whose registration has no Scorer is `PULSE_MODEL_SCORE_INVALID` `no_scorer`; a refit field beside it is `slot_fields`; a failed fit nulls every row with `PULSE_FIT_UNAVAILABLE`. Predict refuses identically.
- Large residuals flag response-space outliers.
- Penalized residuals bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-fitted`
