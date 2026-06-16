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
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x predicate. Must return `bool` — non-bool result → `PROCESSING_RUNTIME`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | unused — expression names fields directly. May be empty. |

Categorical → STRING; `set_*` → `[]string`; `decimal128` → `Decimal128`.

## Output

Row-level predicate. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. `n_null_input` is not field-specific (no fixed input axis). Mergeable.

## Gotchas

- Cannot reference attribute output — filters run before attributes. Compose / ProcessChain to filter on derived columns.
- Embedder extensions: `Options.Extensions.ExprFunctions` + `LookupTables` are visible. See `extension-points`.
- Runtime panic / type mismatch → `PROCESSING_RUNTIME` — drops the row.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `aggregation-design`, `response-components`, `extension-points`
