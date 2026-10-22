---
name: op-group-quantile
description: Partition records into N equal-population quantile buckets (Q1..Q4 / D1..D10 / P1..P100).
kind: operator
category: GROUP
operator: GROUP_QUANTILE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | int | 4 | Number of buckets; set on `Group.Interval`. 4 (quartiles), 10 (deciles), 100 (percentiles). |
| `fit` | enum | `global` | `global`: cut once over the filtered set, any position. `within`: recut per bucket of the groupers before it (crosstab: rows, then columns); a no-op outermost. A refit's warnings would name the bucket in `details.group_key` (cuts raise none). |

Weight (both kinds; `"weight": null` opts out): buckets cut at the weighted order statistics (the weighted-percentile rule; `probability` rescaled to the row count). A heavy row spanning a cut lands whole in the higher bucket; `count` / `total_n` stay raw rows.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

Bucket label per row (`Qk` / `Dk` / `Pk`). Rows emit in bucket order (`D1`, `D2`, …, `D10`, never `D1`, `D10`, `D2`) — grouped rows, `groups` composites, crosstab axes; keys stay unpadded. An explicit `sort` wins (sorting the key column compares strings).

## Components

- `n_quantiles` — Equal-population buckets
- `method` — Interpolation (`"linear"`)
- `edges` — Sorted cutpoints
- `buckets` — `{key, low, high, count}`
- `BufferedComponents`: needs the sorted full input.

<!-- generated: contract -->

## Gotchas

- Forces buffered execution — disables fused crosstab.
- `Group.Include` not honoured — buckets are derived ranks.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `grouper-design`, `response-components`, `op-group-range`
