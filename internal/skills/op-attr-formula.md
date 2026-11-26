---
name: op-attr-formula
description: Per-row expression evaluation against the record's fields via expr-lang.
kind: operator
category: ATTR
operator: ATTR_FORMULA
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `expression` | string | (required) | `expr-lang/expr` text, evaluated per row. |

## Inputs

- `field` — unread
- `label` — derived field name; default `ATTR_FORMULA_`

## Output

One `float64` per record (bools → `1.0` / `0.0`); a `nil` result → null (`n_null`; predict: `PULSE_ADVISORY_FORMULA_NULLABLE_RESULT`). `x * 2` on a null `x` → `PROCESSING_RUNTIME` (predict: `PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND`); use `x ?? 0`.

<!-- generated: contract -->

## Gotchas

- Reads schema fields and EARLIER attribute labels (declared order); stage across requests via compose or a process chain.
- **`total(expr)`** (this operator only): the post-filter cohort sum on every row (`x * 1000 / total(x)`); two-pass, frequency weights only. Detail and refusals: `formula-totals`.
- The text goes in the `expression` slot; `params.expression` → `PULSE_REQUEST_UNKNOWN_FIELD`; an empty slot is `PROCESSING_CONFIG`.
<!-- feature: capability:params -->
- `income * params.rate` reads the request constant `"params": {"rate": 1.1}` (`request-params`).
<!-- /feature -->
- Bad syntax / names / types → `PULSE_FORMULA_COMPILE_ERROR` on predict and at build (`details.compile_error`).
- `decimal128` is exact (`decimal-formulas`); the output takes the nearest f64. A bare `datetime` in `weekday` / `month_index` / `date_trunc` fails compile: wrap it in `to_day` (`calendar-formulas`).
- A literal `coef("m", …)` of a compose fit that failed on its data: null on every row + `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; a non-literal fit name reaching it is refused with the fit's code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `formula-totals`, `attribute-composition`, `expression-language` (functions, binding, `lookup(...)`), `decimal-formulas`, `calendar-formulas`
