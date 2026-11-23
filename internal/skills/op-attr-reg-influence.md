---
name: op-attr-reg-influence
description: Per-row Cook's distance or DFFITS from an unpenalized OLS refit or a bound fit's design — influence diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_INFLUENCE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
---

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

Refit keys are top-level, not `params`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `fit` | string | — | `params.fit`: a bound fit's figure. |
| `measure` | enum | `cooks` | `cooks` (R `cooks.distance`) or `dffits` (R `dffits`). |
| `carryover` | bool | `true` | Carry adstock (`regression-order`). |
| `order` | object | — | `{field, partition?}`; needed to carry. |
| `weight` | slot weight | inherited | `null` opts out; frequency only. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — default `<TYPE>_<target>`, with `fit` `<TYPE>_<fit>`

## Output

One `float64` per record; h leverage, p′ = p + 1, r / t standardized / studentized residual:

- `cooks` — r²·h / (p′·(1 − h)), ≥ 0.
- `dffits` — t·√(h / (1 − h)).

With `fit` **dffits is signed by g′(μ): negative for a positive residual under a gamma inverse link.** **Null (never 0)** on a null / NaN / ±Inf input, a row the weighted refit excludes, or undefined (df ≤ 1, h ≥ 1).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Refit: unpenalized OLS (`penalty`, a bad `measure`: `PROCESSING_CONFIG`); two-pass.
- `fit`: qualifying fits, refusals: `regression-influence`.
- Frequency weight (refit or `fit`): one copy's figure, df = Σw − p′; probability refused.
- Pearson-based, as R. Screens D > 1, 4/n, |DFFITS| > 2√(p′/n).

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-influence`, `regression-row-scores`, `op-attr-reg-leverage`, `op-attr-reg-residual`
