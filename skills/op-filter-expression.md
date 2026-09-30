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

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x predicate returning `bool`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | unused — expression names fields directly. May be empty. |

Categorical → STRING; `set_*` → `[]string` (`has_any(tags, "a")`); `decimal128` → `Decimal128`.

## Output

Row-level predicate. No emitted column.

## Components

Floor only: universal `{n_in, n_out, n_null_input}` (`response-components`). `n_null_input` stays 0 — no fixed input field. Mergeable.

## Gotchas

- Compiled once; syntax / type error → `PROCESSING_RUNTIME` before any row.
- Null binds `nil`: `x == nil`, `x ?? 0`; `==` false, `!=` true. `>` / `+` / `len` on nil → whole predicate UNKNOWN, row dropped (guard `x != nil && x > 5`).
- Error or non-bool on a non-null row → `PROCESSING_RUNTIME`.
- Cannot reference attribute output — filters run before attributes.
- Embedder `ExprFunctions` + `LookupTables` visible.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `aggregation-design`, `response-components`; `docs/src/internals/extension-points.md`
