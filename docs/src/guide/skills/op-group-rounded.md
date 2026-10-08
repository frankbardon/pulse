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
| `interval` | float | (required) | Rounding increment; set on `Group.Interval`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64` (no `decimal128`) |

## Output

Floored numeric key per row. Each value rounds DOWN to a multiple of `Interval` (`19`→`10`, `-3`→`-10`), never to the nearest; key is that scalar.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `precision` | float64 | Rounding increment (Group.Interval) |
| `edges` | []float64 | Sorted rounded scalars separating buckets |
| `buckets` | []bucket | `{key, low, high, count}` per emission |

- Mergeability: `Mergeable`
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

- Same partition as `GROUP_RANGE` at equal width; only the key differs (`"10"` vs `"10-20"`).
- Rejects categorical/decimal128 at construction.
- `Group.Include` not honoured — filter source field instead.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`op-group-range`](op-group-range.md)
