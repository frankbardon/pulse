---
name: op-filter-false
description: Keep records where the field is logically false. Strict packed_bool by default; opt into JS falsiness.
kind: operator
category: FILTER
operator: FILTER_FALSE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | empty (strict) | Omit, or `["strict"]`, requires `packed_bool` and matches raw `0`. `["truthy"]` enables JavaScript-style falsiness across any scalar field type (a `vec_f32` / `vec_f64` field is refused, `PULSE_VECTOR_FIELD_NOT_SCALAR`). Other values → `PROCESSING_CONFIG`. |

## Inputs

- `field` — strict mode: `packed_bool` only. Truthy mode: any scalar field type.

## Output

Row-level predicate. Pass when value is false / falsy. No emitted column.

<!-- generated: contract -->

## Gotchas

- Strict mode on non-`packed_bool` → `PULSE_FIELD_TYPE_UNSUPPORTED`. Fix: `values: ["truthy"]`.
- Truthy kept-set: `0`, `NaN`, `""`, `null` → kept. Null rows DROP in strict but PASS in truthy — null is JS-falsy, while strict treats null as "no observation".

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-true`
