---
name: op-filter-range
description: Keep records whose numeric field value falls within [low, high] inclusive.
kind: operator
category: FILTER
operator: FILTER_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[2]string` | (required) | `[low, high]` — both parsed via `strconv.ParseFloat`. Exactly two entries; any other count → `PROCESSING_CONFIG`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `decimal128` |

## Output

Row-level predicate. Pass when `low ≤ value ≤ high`; drop otherwise. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks; counters fold by simple addition.

## Gotchas

- Inclusive bounds on both ends. Open intervals require `FILTER_EXPRESSION`.
- Null rows fail the predicate (dropped).
- `date` interpreted as the numeric day-since-epoch — pass numeric strings, not formatted dates.
- For non-numeric fields use `FILTER_INCLUDE` / `FILTER_EXPRESSION`.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-expression`
