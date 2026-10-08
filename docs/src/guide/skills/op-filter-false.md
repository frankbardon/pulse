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
| `Values` | `[]string` | empty (strict) | Omit, or `["strict"]`, requires `packed_bool` and matches raw `0`. `["truthy"]` enables JavaScript-style falsiness across any field type. Other values → `PROCESSING_CONFIG`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | strict mode: `packed_bool` only. Truthy mode: any cohort field type. |

## Output

Row-level predicate. Pass when value is false / falsy. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per [`response-components`](response-components.md) contract. Mergeable across chunks.

## Gotchas

- Strict mode on non-`packed_bool` → `PROCESSING_CONFIG`. Fix: `Values=["truthy"]`.
- Truthy kept-set: `0`, `NaN`, `""`, `null` → kept. Null rows DROP in strict but PASS in truthy — null is JS-falsy, while strict treats null as "no observation".

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-true`](op-filter-true.md)
