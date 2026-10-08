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

| Param | Accepted field types |
|---|---|
| `Field` | unused — the expression names fields. |

Categorical → STRING; `set_*` → `[]string` (`has_any(tags, "a")`); `decimal128` → `Decimal128`.

## Output

Row-level predicate. No emitted column.

## Components

Floor only: universal `{n_in, n_out, n_null_input}` (`response-components`). `n_null_input` stays 0 — no fixed input field. Mergeable.

## Gotchas

- Compiled once; bad syntax / types → `PROCESSING_RUNTIME` at build.
- Null binds `nil`: `==` false, `!=` true, `x ?? 0` fills; `>` / `+` / `len` on nil → predicate UNKNOWN, row dropped.
- Error / non-bool on a non-null row → `PROCESSING_RUNTIME`.
- `%` on a field: float `math.Mod` (`age % 2 == 0`, dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.
- No attribute output — filters run first.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `expression-language` (embedder `ExprFunctions`, `LookupTables`), `aggregation-design`, `response-components`
