```yaml
name: op-agg-sum
description: Sum the numeric values of a field across the input set.
kind: operator
category: AGG
operator: AGG_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [financial, streaming-friendly]
```

## Use when

Total of a numeric field, over all rows or per group.

Questions it answers:

- What is total revenue by region?
- How many units did each product line sell in total?

Use something else:

- `AGG_AVERAGE` when you want the typical value per row rather than the total.
- `AGG_WEIGHTED_MEAN` when rows carry weights and you want a weighted average.
- `WIN_RUNNING_SUM` when you want the total so far, row by row.

## Params

Weighted: Σw·x.

## Inputs

`Field` — admitted types: see `## Contract`.

Decimal128 sums stay in decimal (banker-rounded precision propagation); all other numerics emit float64.

## Output

Scalar `float64` (decimal128 preserved when input is decimal). One row per group when wired under a grouper.

## Components

- `sum` — Running sum of non-null values

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `sum`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Smart default for numeric fields when `Type` omitted.
- Overflow on huge u64 sums silently promotes through float64.
- Order-free binned sum: serial, any `DecodeWorkers` / `ShardWorkers` count and any shard split give the same bits (not correctly rounded; error ≲ 7ε·|sum|).
- Empty input (all null / no rows) sums to `0` (`"0.00"` on `decimal128`), never null: the empty sum is defined. Detect emptiness by `n == 0` in the components floor.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[financial]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
