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

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Expression` | string | (required) | `expr-lang/expr` v1.17.x string evaluated per row. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field referenced in the expression (numeric, categorical_*, date, packed_bool, set_*, decimal128) |
| `Label` | required — new column name |

## Output

One `float64` per record (booleans coerce to `1.0` / `0.0`). Null reference without `??` guard → `PROCESSING_RUNTIME` drops the row.

## Gotchas

- **No in-slot chaining** — cannot reference another attribute's label. Stage via Compose / ProcessChain.
- Categorical surfaces as STRING (`==` / `in`); `set_*` as `[]string` (`contains`, `has_any`, `popcount`).
- No `sqrt` / `log` / `exp` / trig — use `**` or pre-compute via FEAT.
- Inject via `Options.Extensions.ExprFunctions`; tables via `lookup(...)`. See `extension-points`.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `extension-points`
