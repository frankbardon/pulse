```yaml
name: op-attr-reg-influence
description: Per-row Cook's distance or DFFITS from an unpenalized OLS refit — influence diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_INFLUENCE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row how much it moves a straight-line fit: how far the fitted values would shift if that one row were left out.

Questions it answers:

- Which rows would change the model's answer if they were dropped?
- Is the fitted trend carried by a handful of unusual accounts?

Use something else:

- `ATTR_REG_LEVERAGE` when you want how unusual each row's predictor values are, whatever its target.

## Params

`target` / `predictors` are top-level keys, not `params`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent variable field. |
| `predictors` | list | (required) | Independent variable fields. |
| `measure` | enum | `cooks` | `params.measure`: `cooks` (R `cooks.distance`) or `dffits` (R `dffits`). |
| `weight` | slot weight | inherited | `null` opts out; frequency only. |

## Inputs

`target` / `predictors` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — new column name; default `ATTR_REG_INFLUENCE_<target>`

## Output

One `float64` per record. With h the leverage, p′ = p + 1, df = n − p′, r = e / (s·√(1 − h)) and t = r·√((df − 1)/(df − r²)):

- `cooks` — r²·h / (p′·(1 − h)), never negative.
- `dffits` — t·√(h / (1 − h)), signed like the residual.

**Null (never 0)** when the target or any predictor is null, NaN or ±Inf, for a row the weighted refit excludes, and where undefined (df ≤ 1, h ≥ 1).

## Reading the output

- `value`: How much the row moves the fitted line. With measure cooks (the default): Cook's distance, the shift of every fitted value when the row is left out, scaled by p times the model's residual variance, where p counts the coefficients including the intercept; it is never negative and grows with both the row's residual and its leverage. With measure dffits: the shift of the row's own fitted value when it is left out, in standard errors of that fit, signed like the residual.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED` (an influence measure or studentized residual is one observation's figure (a frequency weight is w identical observations), and a sampling weight defines no observation to delete).
- Streaming: streamable.

## Gotchas

- **Unpenalized OLS only** — any `penalty` is `PROCESSING_CONFIG`; an unknown `measure` too. No `params.fit`.
- A frequency weight is w identical rows: the figure is one copy's (h/w, df = Σw − p′). A probability weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Screens (D > 1, 4/n, |DFFITS| > 2√(p′/n)) are rules of thumb, not tests.
- Two-pass — pre-pass fits OLS, pass 2 emits per row.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-row-scores`](regression-row-scores.md), [`op-attr-reg-leverage`](op-attr-reg-leverage.md), [`op-attr-reg-residual`](op-attr-reg-residual.md)
