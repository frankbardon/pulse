---
name: op-group-rounded
description: Round each numeric value down to a multiple of Interval (floor, not nearest) and group by that scalar.
kind: operator
category: GROUP
operator: GROUP_ROUNDED
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | float | (required) | Rounding increment; set on `Group.Interval`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

## Output

Floored numeric key per row. Each value rounds DOWN to a multiple of `Interval` (`19`→`10`, `-3`→`-10`), never to the nearest; key is that scalar. Rows emit in numeric key order (`"-5"`, `"9.5"`, `"10"`) on every arm (streaming, shards, fused crosstab). An explicit `sort` or crosstab-axis `include` wins; sorting the key column compares strings.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `precision` | float64 | Rounding increment (Group.Interval) |
| `edges` | []float64 | Sorted rounded scalars separating buckets |
| `buckets` | []bucket | `{key, low, high, count}` per emission |

- Mergeability: `Mergeable` — fans out under `ShardWorkers` / `DecodeWorkers` with the serial bits<!-- feature: capability:process_chain -->; passes the chain stage gate<!-- /feature -->
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

<!-- feature: GROUP_RANGE -->
- Same partition as `GROUP_RANGE` at equal width; only the key differs (`"10"` vs `"10-20"`).
<!-- /feature -->
- Rejects categorical and `set_*` at construction (`PULSE_FIELD_TYPE_UNSUPPORTED`).
- `Group.Include` not honoured — filter source field instead.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-range`
