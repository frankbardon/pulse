```yaml
name: op-filter-range
description: Keep records whose numeric field value falls within [low, high] inclusive.
kind: operator
category: FILTER
operator: FILTER_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps only the rows whose number lies between a low and a high limit, both limits included.

Questions it answers:

- What do adults aged 18 to 34 say?
- What is the average order once impossible values above 10,000 are left out?

Use something else:

- `FILTER_EXPRESSION` when you need an open limit, such as strictly above a value.
- `FILTER_DATE_RANGES` when the field is a date and you want named periods.

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | (required) | `[low, high]`, number strings. Any other count → `PROCESSING_CONFIG` at run time; predict passes it. |

## Inputs

`field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

Row-level predicate. Pass when `low ≤ value ≤ high`; drop otherwise. No emitted column.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n_in`, `n_out`, `n_null_input`.

## Gotchas

- Inclusive bounds on both ends; there is no open bound, so never use a sentinel like `255`. One-sided ("at least 40"): `{"type": "FILTER_EXPRESSION", "expression": "age >= 40"}`.
- Null rows fail the predicate (dropped).
- `date` interpreted as the numeric day-since-epoch — pass numeric strings, not formatted dates.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-expression`](op-filter-expression.md)
