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

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, `nullable_*` |

Decimal128 sums stay in decimal (banker-rounded precision propagation); all other numerics emit float64.

## Output

Scalar `float64` (decimal128 preserved when input is decimal). One row per group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum` | float64 | Running sum of non-null values |

- Mergeability: `Mergeable`
- Streaming: per-chunk; orchestrator sums

## Gotchas

- Smart default for numeric fields when `Type` omitted.
- Set-typed fields (`set_*`) NOT supported — use `AGG_SET_CARDINALITY_SUM`.
- Overflow on huge u64 sums silently promotes through float64.

## See

- `pulse_examples_search tags=[financial]`
- Skills: `aggregation-design`, `response-components`
