---
name: op-agg-average
description: Arithmetic mean of a numeric field across the input set.
kind: operator
category: AGG
operator: AGG_AVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, data-quality]
---

<!-- generated: use-when -->

## Params

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted it is Σw·x / Σw. Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, `u4`; a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

<!-- feature: capability:weighting -->Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. <!-- /feature -->`n`/`n_null` stay raw counts.
Weighted adds `sum_weighted`, `weighted_mean`, `m2_weighted`, `sum_weights_sq`, `weighted_variance` (m2/(Σw−1); `null` if Σw ≤ 1).

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum` | float64 | Running sum; mean = sum / n |

- Mergeability: `Mergeable`
- Streaming: per-chunk emits running mean via (sum, n)

## Gotchas

- Null inputs skipped — mean is over `n` non-null, not the cohort.
- Binned sum over exact `n`: the same bits on every worker count and shard split; weighted `m2_weighted` / `weighted_variance` are Welford — within ULP, not bitwise.
- No non-null value in the bucket (or Σw = 0) → `null` (NaN), never 0.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
