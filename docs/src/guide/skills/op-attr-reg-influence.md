```yaml
name: op-attr-reg-influence
description: Per-row Cook's distance or DFFITS from an unpenalized OLS refit or a bound fit's design — influence diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_INFLUENCE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row how much it moves a straight-line fit: how far the fitted values would shift if that one row were left out.

Questions it answers:

- Which rows would change the model's answer if they were dropped?
- Is the fitted trend carried by a handful of unusual accounts?

Use something else:

- `ATTR_REG_LEVERAGE` when you want how unusual each row's predictor values are, whatever its target.

## Params

Refit keys are top-level, not `params`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `fit` | string | — | `params.fit`: a bound fit's figure ([`regression-row-scores`](regression-row-scores.md)). |
| `measure` | enum | `cooks` | `cooks` (R `cooks.distance`) or `dffits` (R `dffits`). |
| `weight` | slot weight | inherited | `null` opts out; frequency only. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — default `<TYPE>_<target>`, with `fit` `<TYPE>_<fit>`

## Output

One `float64` per record; h the leverage, p′ = p + 1, r / t the standardized / studentized residual:

- `cooks` — r²·h / (p′·(1 − h)), ≥ 0.
- `dffits` — t·√(h / (1 − h)).

With `fit` (GLM forms: [`regression-row-scores`](regression-row-scores.md)) **dffits is signed by g′(μ): negative for a positive residual under a gamma inverse link.** **Null (never 0)** on a null / NaN / ±Inf input, a row the weighted refit excludes, and where undefined (df ≤ 1, h ≥ 1).

## Reading the output

- `value`: How much the row moves the fitted line. With measure cooks (the default): Cook's distance, the shift of every fitted value when the row is left out, scaled by p times the model's residual variance, where p counts the coefficients including the intercept; it is never negative and grows with both the row's residual and its leverage. With measure dffits: the shift of the row's own fitted value when it is left out, in standard errors of that fit, signed like the residual.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED` (an influence measure or studentized residual is one observation's figure (a frequency weight is w identical observations), and a sampling weight defines no observation to delete).
- Streaming: streamable.

## Gotchas

- Refit: unpenalized OLS only (`penalty`, an unknown `measure`: `PROCESSING_CONFIG`); two-pass.
- `fit`: an unweighted, unpenalized OLS / GLM fit with `vcov: true`, else `PULSE_MODEL_SCORE_INVALID` (`details.requires`). Row-local.
- A frequency weight: one copy's figure (h/w, df = Σw − p′); probability: `PULSE_WEIGHT_UNSUPPORTED`.
- Screens (D > 1, 4/n, |DFFITS| > 2√(p′/n)) are rules of thumb.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-row-scores`](regression-row-scores.md), [`op-attr-reg-leverage`](op-attr-reg-leverage.md), [`op-attr-reg-residual`](op-attr-reg-residual.md)
