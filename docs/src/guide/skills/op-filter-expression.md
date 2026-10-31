```yaml
name: op-filter-expression
description: Keep records for which an expr-lang expression evaluates truthy against record fields.
kind: operator
category: FILTER
operator: FILTER_EXPRESSION
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, feature-engineering, streaming-friendly]
```

## Use when

Keeps the rows for which a written yes-or-no condition is true, for rules the other filters cannot state, such as two fields together.

Questions it answers:

- Which customers spent over 500 and joined this year?
- Which records have an end date before their start date?

Use something else:

- `FILTER_INCLUDE` when you only need to keep a list of values.
- `FILTER_RANGE` when you only need a low and a high limit.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x `bool` predicate. |

## Inputs

- `Field` — unused — the expression names fields.

Categorical → STRING; `set_*` → `[]string` (`has_any(tags, "a")`); `decimal128` → an exact decimal (`price * 1.1 > cost`, [`decimal-formulas`](decimal-formulas.md)); `date` → epoch days, `datetime` → epoch seconds — day helpers need `to_day` ([`calendar-formulas`](calendar-formulas.md)).

## Output

Row-level predicate. No emitted column.

## Contract

- Types: any type.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n_in`, `n_out`, `n_null_input`.

## Gotchas

- Compiled once; bad syntax / names / types → `PULSE_FORMULA_COMPILE_ERROR` at predict and build (reason: `details.compile_error`).
- Null binds `nil`: `==` false, `!=` true, `x ?? 0` fills; `>` / `+` / `len` on nil → predicate UNKNOWN, row dropped.
- A result typed non-bool (`x + 1`) → `PULSE_FORMULA_COMPILE_ERROR` at predict and build; an untyped (`any`) result non-bool on a non-null row, or an eval error → `PROCESSING_RUNTIME`.
- `%` on a field: float `math.Mod` (`age % 2 == 0`, dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.
- Reads source fields, feature labels and previous-state labels (`ATTR_PREVIOUS_STATE`), which run before filters; a later label is unknown, its message naming the window, attribute or aggregation.
- Pass values as `params`: `panel == params.from_wave` with `"params": {"from_wave": "a"}`, never spliced text ([`request-params`](request-params.md)).
- `coef()` of a compose fit that failed on its data ([`compose-fits`](compose-fits.md)) refuses the request at build with the fit's own code + `details.fit`: a filter verdict cannot be null.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`expression-language`](expression-language.md) (maths, trig and vector functions, `ExprFunctions`, `LookupTables`), [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
