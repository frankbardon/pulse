---
name: op-agg-sum
description: Sum the numeric values of a field across the input set.
kind: operator
category: AGG
operator: AGG_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [financial, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Weighted: Σw·x.

## Inputs

`Field` — admitted types: see `## Contract`.

Decimal128 sums stay in decimal (banker-rounded precision propagation); all other numerics emit float64.

## Output

Scalar `float64` (decimal128 preserved when input is decimal). One row per group when wired under a grouper.

## Components

- `sum` — Running sum of non-null values

<!-- generated: contract -->

## Gotchas

- Smart default for numeric fields when `Type` omitted.
- Overflow on huge u64 sums silently promotes through float64.
- Order-free binned sum: serial, any `DecodeWorkers` / `ShardWorkers` count and any shard split give the same bits (not correctly rounded; error ≲ 7ε·|sum|).
- Empty input (all null / no rows) sums to `0` (`"0.00"` on `decimal128`), never null: the empty sum is defined. Detect emptiness by `n == 0` in the components floor.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[financial]`
- Skills: `aggregation-design`, `response-components`
