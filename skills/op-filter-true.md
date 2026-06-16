---
name: op-filter-true
description: Keep records where Field is logically true. Strict packed_bool by default; opt into JS truthiness.
kind: operator
category: FILTER
operator: FILTER_TRUE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | empty (strict) | Omit, or `["strict"]`, requires `packed_bool` and matches raw `1`. `["truthy"]` enables JavaScript-style coercion across any field type. Other values → `PROCESSING_CONFIG`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | strict mode: `packed_bool` only. Truthy mode: any cohort field type. |

## Output

Row-level predicate. Pass when value is true / truthy. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks.

## Gotchas

- Strict mode on non-`packed_bool` → `PROCESSING_CONFIG` with the suggested `Values=["truthy"]` fix.
- Truthy mode coerces: `0`, `NaN`, `""`, `null` → falsy; everything else → truthy. Null rows DROP in both directions.
- `decimal128` truthiness uses `Sign() != 0`.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-false`
