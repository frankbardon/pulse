---
name: op-attr-reg-fitted
description: Per-row fitted value ŷᵢ = Xᵢ · β + β₀ from an OLS refit during the attribute prepass.
kind: operator
category: ATTR
operator: ATTR_REG_FITTED
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, buffered-pipeline]
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
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. |
| `params.fit` | string | Score a bound fit (`fits` / `models` entry) instead of refitting: the fit's score<!-- feature: REG_GLM --> — a REG_GLM's fitted mean<!-- /feature -->; an extension regression's through its Scorer. No `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` beside it. |

## Inputs

| Param | Accepted field types |
|---|---|
| `target` / `predictors` | numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>` |

## Output

One `float64` per record — the model's prediction ŷᵢ. **Null (never 0) when any predictor is null**, so a downstream aggregation counts the row in `n_null`; a null target alone still gets ŷᵢ.

<!-- generated: reading-the-output -->

## Gotchas

- Two-pass: prepass refits independently per ATTR_REG_* slot (Option A — no fit sharing). With `params.fit` it is row-local and equals the bound model's score bit for bit<!-- feature: ATTR_REG_PREDICT --> (ATTR_REG_PREDICT's)<!-- /feature -->; refusals: unknown name `PULSE_MODEL_UNKNOWN`, Scorer-less extension fit `no_scorer`, refit field beside it `slot_fields`; a failed fit nulls every row (`PULSE_FIT_UNAVAILABLE`).
- `Mergeable` per-shard; predict reports streamability.
- Penalized fits (`l1`/`l2`/`elasticnet`) reuse the same machinery; mis-tuned `alpha`/`l1_ratio` shrinks coefficients toward zero.

## See

- `pulse_examples_search tags=[regression]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`
