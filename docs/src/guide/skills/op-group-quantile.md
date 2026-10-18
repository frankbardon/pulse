```yaml
name: op-group-quantile
description: Partition records into N equal-population quantile buckets (Q1..Q4 / D1..D10 / P1..P100).
kind: operator
category: GROUP
operator: GROUP_QUANTILE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
```

## Use when

Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom.

Questions it answers:

- How do the top 25% of spenders differ from the bottom 25%?
- What is the average basket in each decile of customer value?

Use something else:

- `GROUP_RANGE` when you want bands of a fixed width, such as every 10 years of age.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | int | 4 | Number of buckets; set on `Group.Interval`. 4 (quartiles), 10 (deciles), 100 (percentiles). |
| `fit` | enum | `global` | `global`: cut once over the filtered set, any position. `within`: recut per bucket of the groupers before it (crosstab: rows, then columns); a no-op outermost. A refit's warnings would name the bucket in `details.group_key` (cuts raise none). |

Weight (both kinds; `"weight": null` opts out): buckets cut at the weighted order statistics (the weighted-percentile rule; `probability` rescaled to the row count). A heavy row spanning a cut lands whole in the higher bucket; `count` / `total_n` stay raw rows.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

## Output

Bucket label per row (`Qk` / `Dk` / `Pk`). Rows emit in bucket order (`D1`, `D2`, …, `D10`, never `D1`, `D10`, `D2`) — grouped rows, `groups` composites, crosstab axes; keys stay unpadded. An explicit `sort` wins (sorting the key column compares strings).

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `n_quantiles` | int | Equal-population buckets |
| `method` | string | Interpolation (`"linear"`) |
| `edges` | []float64 | Sorted cutpoints |
| `buckets` | []bucket | `{key, low, high, count}` |

- Mergeability: `None` — `BufferedComponents=true`; needs sorted full input
- Streaming: `Streamable=false` — terminal-only.

## Gotchas

- Forces buffered execution — disables fused crosstab.
- Rejects categorical and `set_*` at construction (`PULSE_FIELD_TYPE_UNSUPPORTED`).
- `Group.Include` not honoured — buckets are derived ranks.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`op-group-range`](op-group-range.md)
