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

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted it is Σw·x. Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, `u4`; a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

Decimal128 sums stay in decimal (banker-rounded precision propagation); all other numerics emit float64.

## Output

Scalar `float64` (decimal128 preserved when input is decimal). One row per group when wired under a grouper.

## Components

<!-- feature: capability:weighting -->Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. <!-- /feature -->`n`/`n_null` stay raw counts.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum` | float64 | Running sum of non-null values |

- Mergeability: `Mergeable`
- Streaming: per-chunk; orchestrator sums

<!-- generated: contract -->

## Gotchas

- Smart default for numeric fields when `Type` omitted.
- Set-typed fields (`set_*`) NOT supported.
- Overflow on huge u64 sums silently promotes through float64.
- Order-free binned sum: serial, any `DecodeWorkers` / `ShardWorkers` count and any shard split give the same bits (not correctly rounded; error ≲ 7ε·|sum|).
- Empty input (all null / no rows) sums to `0` (`"0.00"` on `decimal128`), never null: the empty sum is defined. Detect emptiness by `n == 0` in the components floor.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[financial]`
- Skills: `aggregation-design`, `response-components`
