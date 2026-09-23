---
name: op-agg-variance
description: Population variance via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_VARIANCE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — population variance (n-denominator); `decimal128` input yields a decimal-scaled result (scale doubled). Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | `m2 / n` |

- Mergeability: `Mergeable` (Chan parallel-merge)
- Streaming: per-chunk Welford

## Gotchas

- Population variance (`n`), not sample (`n-1`). For sample variance use `AGG_WELFORD`.
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)²). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
