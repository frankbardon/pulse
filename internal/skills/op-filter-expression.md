---
name: op-filter-expression
description: Keep records for which an expr-lang expression evaluates truthy against record fields.
kind: operator
category: FILTER
operator: FILTER_EXPRESSION
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, feature-engineering, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x `bool` predicate. |

## Inputs

- `Field` — unused — the expression names fields.

Categorical → STRING; `set_*` → `[]string` (`has_any(tags, "a")`); `decimal128` → an exact decimal (`price * 1.1 > cost`, `decimal-formulas`); `date` → epoch days, `datetime` → epoch seconds — day helpers need `to_day` (`calendar-formulas`).

## Output

Row-level predicate. No emitted column.

<!-- generated: contract -->

## Gotchas

- Compiled once; bad syntax / names / types → `PULSE_FORMULA_COMPILE_ERROR` at predict and build (reason: `details.compile_error`).
- Null binds `nil`: `==` false, `!=` true, `x ?? 0` fills; `>` / `+` / `len` on nil → predicate UNKNOWN, row dropped.
- A result typed non-bool (`x + 1`) → `PULSE_FORMULA_COMPILE_ERROR` at predict and build; an untyped (`any`) result non-bool on a non-null row, or an eval error → `PROCESSING_RUNTIME`.
- `%` on a field: float `math.Mod` (`age % 2 == 0`, dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.
- Reads source fields, feature labels<!-- feature: ATTR_PREVIOUS_STATE --> and previous-state labels (`ATTR_PREVIOUS_STATE`)<!-- /feature -->, which run before filters; any later label is an unknown name, its message naming the window or attribute slot.
<!-- feature: capability:params -->
- Pass values as `params`: `panel == params.from_wave` with `"params": {"from_wave": "a"}`, never spliced text (`request-params`).
<!-- /feature -->
- `coef()` of a compose fit that failed on its data (`compose-fits`) refuses the request at build with the fit's own code + `details.fit`: a filter verdict cannot be null.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `expression-language` (maths, trig and vector functions, `ExprFunctions`, `LookupTables`), `aggregation-design`, `response-components`
