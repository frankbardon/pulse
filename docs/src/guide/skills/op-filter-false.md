```yaml
name: op-filter-false
description: Keep records where Field is logically false. Strict packed_bool by default; opt into JS falsiness.
kind: operator
category: FILTER
operator: FILTER_FALSE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps only the rows where a yes/no field is no, such as lapsed accounts or unfinished surveys.

Questions it answers:

- What do the customers who did not renew have in common?
- How many interviews were left unfinished, by region?

Use something else:

- `FILTER_TRUE` when you want the rows where the field is yes.
- `FILTER_NULL` when you want only the rows where the field is missing.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | empty (strict) | Omit, or `["strict"]`, requires `packed_bool` and matches raw `0`. `["truthy"]` enables JavaScript-style falsiness across any scalar field type (a `vec_f32` / `vec_f64` field is refused, `PULSE_VECTOR_FIELD_NOT_SCALAR`). Other values → `PROCESSING_CONFIG`. |

## Inputs

- `Field` — strict mode: `packed_bool` only. Truthy mode: any scalar field type.

## Output

Row-level predicate. Pass when value is false / falsy. No emitted column.

## Contract

- Types (`field`): `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n_in`, `n_out`, `n_null_input`.

## Gotchas

- Strict mode on non-`packed_bool` → `PULSE_FIELD_TYPE_UNSUPPORTED`. Fix: `Values=["truthy"]`.
- Truthy kept-set: `0`, `NaN`, `""`, `null` → kept. Null rows DROP in strict but PASS in truthy — null is JS-falsy, while strict treats null as "no observation".

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-true`](op-filter-true.md)
