```yaml
name: op-group-rounded
description: Round each numeric value down to a multiple of Interval (floor, not nearest) and group by that scalar.
kind: operator
category: GROUP
operator: GROUP_ROUNDED
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple.

Questions it answers:

- How many orders are there at each price point, to the nearest lower 10?
- What is the count of scores in each block of 5 points?

Use something else:

- `GROUP_RANGE` when you want the band's both edges in its name.
- `GROUP_QUANTILE` when you want groups of equal size.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | float | `1` | Rounding increment; set on `Group.Interval`. ≤ 0 is treated as 1. |

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

Floored numeric key per row. Each value rounds DOWN to a multiple of `Interval` (`19`→`10`, `-3`→`-10`), never to the nearest; key is that scalar. Rows emit in numeric key order (`"-5"`, `"9.5"`, `"10"`) on every arm (streaming, shards, fused crosstab). An explicit `sort` or crosstab-axis `include` wins; sorting the key column compares strings.

## Components

- `precision` — Rounding increment (Group.Interval)
- `edges` — Sorted rounded scalars separating buckets
- `buckets` — `{key, low, high, count}` per emission
- Fans out under `ShardWorkers` / `DecodeWorkers` with the serial bits, whatever the key count; at very high cardinality (keys ≳ 70% of each partition's rows) the fan-out gains nothing, which predict cannot see; passes the chain stage gate.
- `StreamableGrouper`: eligible for fused crosstab.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `total_n`, `n_null`, `precision`, `edges`, `buckets`.

## Gotchas

- Same partition as `GROUP_RANGE` at equal width; only the key differs (`"10"` vs `"10-20"`).
- `Group.Include` on `groups` is refused (`PROCESSING_CONFIG`) — filter the source field; a crosstab axis keeps it for order.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`op-group-range`](op-group-range.md)
