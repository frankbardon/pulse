---
name: op-group-rounded
description: Round each numeric value down to a multiple of `interval` (floor, not nearest) and group by that scalar.
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
| `interval` | float | `1` | Rounding increment; the slot's top-level `interval`, not `params`. Absent or `0` (unset) = 1; negative → `PROCESSING_CONFIG`, at predict and at run time. |

## Inputs

`field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

Floored numeric key per row. Each value rounds DOWN to a multiple of `interval` (`19`→`10`, `-3`→`-10`), never to the nearest; key is that scalar (`±Inf` / NaN: `"+Inf"`, `"-Inf"`, `"NaN"` on every architecture). Rows emit in numeric key order (`"-5"`, `"9.5"`, `"10"`) on every arm (streaming, shards, fused crosstab). An explicit `sort` or crosstab-axis `include` wins; a `sort` on the key column orders by value too.

## Components

- `precision` — Rounding increment (`interval`)
- `edges` — Sorted rounded scalars separating buckets
- `buckets` — `{key, low, high, count}` per emission
- Fans out under `ShardWorkers` / `DecodeWorkers` with the serial bits, whatever the key count; at very high cardinality (keys ≳ 70% of each partition's rows) the fan-out gains nothing, which predict cannot see<!-- feature: capability:process_chain -->; passes the chain stage gate<!-- /feature -->.
- Eligible for the fused crosstab.

<!-- generated: contract -->

## Gotchas

<!-- feature: GROUP_RANGE -->
- Same partition as `GROUP_RANGE` at equal width; only the key differs (`"10"` vs `"10-20"`).
<!-- /feature -->
- `include` on `groups` is refused (`PROCESSING_CONFIG`) — filter the source field; a crosstab axis keeps it for order.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-range`
