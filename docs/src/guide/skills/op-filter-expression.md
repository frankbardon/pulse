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

| Param | Accepted field types |
|---|---|
| `Field` | unused — the expression names fields. |

Categorical → STRING; `set_*` → `[]string` (`has_any(tags, "a")`); `decimal128` → `Decimal128`.

## Output

Row-level predicate. No emitted column.

## Components

Floor only: universal `{n_in, n_out, n_null_input}` ([`response-components`](response-components.md)). `n_null_input` stays 0 — no fixed input field. Mergeable.

## Gotchas

- Compiled once; bad syntax / types → `PROCESSING_RUNTIME` at build.
- Null binds `nil`: `==` false, `!=` true, `x ?? 0` fills; `>` / `+` / `len` on nil → predicate UNKNOWN, row dropped.
- Error / non-bool on a non-null row → `PROCESSING_RUNTIME`.
- `%` on a field: float `math.Mod` (`age % 2 == 0`, dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.
- No attribute output — filters run first.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`expression-language`](expression-language.md) (embedder `ExprFunctions`, `LookupTables`), [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
