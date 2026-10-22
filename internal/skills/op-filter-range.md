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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[2]string` | (required) | `[low, high]` — both parsed via `strconv.ParseFloat`. Exactly two entries; any other count → `PROCESSING_CONFIG`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1) — bounds in the stored unit (epoch days / seconds, 0/1); a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

## Output

Row-level predicate. Pass when `low ≤ value ≤ high`; drop otherwise. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks; counters fold by simple addition.

<!-- generated: contract -->

## Gotchas

- Inclusive bounds on both ends. <!-- feature: FILTER_EXPRESSION -->Open intervals require `FILTER_EXPRESSION`.<!-- /feature -->
- Null rows fail the predicate (dropped).
- `date` interpreted as the numeric day-since-epoch — pass numeric strings, not formatted dates.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-expression`
