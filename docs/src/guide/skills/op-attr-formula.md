```yaml
name: op-attr-formula
description: Per-row expression evaluation against the record's fields via expr-lang.
kind: operator
category: ATTR
operator: ATTR_FORMULA
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, streaming-friendly]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds a number to every row computed from the row's own fields with an expression, such as price * qty.

Questions it answers:

- What is each order's line total from its price and quantity?
- Which rows meet a combined rule, as a 0/1 column for counting?

Use something else:

- `FILTER_EXPRESSION` when you want to keep or drop rows by a rule rather than add a column.
- `FEAT_LOG` when you want the logarithm of a field.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x string evaluated per row. |

## Inputs

- `Field` — any referenced field type
- `Label` — required — new column name

## Output

One `float64` per record (bools → `1.0` / `0.0`); a `nil` result → null (`n_null`). Arithmetic on a null field → `PROCESSING_RUNTIME` at runtime only (predict reads no record): guard every nullable field, `x ?? 0`.

## Contract

- Types: any type.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Reads source fields and EARLIER attribute labels (declared order); stage across requests via Compose / ProcessChain.
- **`total(expr)`** (this operator only): the cohort-wide post-filter sum, the same on every row (`x * 1000 / total(x)`); two-pass, frequency weights only. Detail and refusals: [`formula-totals`](formula-totals.md).
- The text goes in the `expression` slot; `params.expression` is not read — an empty slot is `PROCESSING_CONFIG`, predict too.
- Bad syntax / names / types → `PULSE_FORMULA_COMPILE_ERROR` on predict and at build (`details.compile_error`).
- `decimal128` is exact ([`decimal-formulas`](decimal-formulas.md)); the column takes the nearest f64. A bare `datetime` in `weekday` / `month_index` / `date_trunc` fails compile: wrap it in `to_day` ([`calendar-formulas`](calendar-formulas.md)).
- A literal `coef("m", …)` of a compose fit that failed on its data: null on every row + `PULSE_FIT_UNAVAILABLE` `{fit, attribute, code}`; a non-literal fit name reaching it is refused with the fit's code.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`formula-totals`](formula-totals.md), [`attribute-composition`](attribute-composition.md), [`expression-language`](expression-language.md) (functions, binding, `lookup(...)`), [`decimal-formulas`](decimal-formulas.md), [`calendar-formulas`](calendar-formulas.md)
