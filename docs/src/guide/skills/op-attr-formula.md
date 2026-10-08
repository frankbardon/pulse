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

| Param | Accepted field types |
|---|---|
| `Field` | any referenced field type |
| `Label` | required — new column name |

## Output

One `float64` per record (bools → `1.0` / `0.0`). Null binds `nil`: guard `x ?? 0` / `x == nil ? a : b`; unguarded arithmetic on nil → `PROCESSING_RUNTIME` (no invented number).

## Gotchas

- **No in-slot chaining** — no other attribute's label; stage via Compose / ProcessChain.
- Compiled once; bad syntax / types → `PROCESSING_RUNTIME` at build.
- Categorical → STRING (`==` / `in`); `set_*` → `[]string` (`"a" in tags`, `has_any(tags, "a", "b")`).
- No `sqrt` / `log` / `exp` / trig — use `**` or FEAT.
- `%` on a field: float `math.Mod` (`score % 1`, dividend's sign); `% 0` → NaN, as `/ 0` → ±Inf.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`attribute-composition`](attribute-composition.md), [`expression-language`](expression-language.md) (embedder `ExprFunctions`, `lookup(...)`)
