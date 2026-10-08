```yaml
name: op-agg-stddev
description: Population standard deviation via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_STDDEV
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
```

## Use when

Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group.

Questions it answers:

- How much do delivery times vary around the average in each region?
- Which product line's order values vary the most in absolute terms (divide by each line's average to compare relative consistency)?

## Params

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted it is √(m2_w / Σw). Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — population stddev (n-denominator); `decimal128` input yields a decimal-scaled result. Per-group when wired under a grouper.

## Reading the output

- `value`: The typical distance of a value from the group's mean, in the field's own units. 0 means every value is the same; for a roughly bell-shaped field about two thirds of the values sit within one standard deviation of the mean.
  - Caveat: It is the spread of the rows, not the precision of the mean: for how precisely the average is known, read AGG_CI_LOWER / AGG_CI_UPPER.
  - Caveat: Each distance is squared before averaging, so a few extreme values inflate it; for a skewed field a percentile range describes the spread better.

## Components

Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. `n`/`n_null` stay raw counts.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | `m2 / n` |
| `stddev` | float64 | √variance |

- Mergeability: `Mergeable` (Chan parallel-merge)
- Streaming: per-chunk; merge via Chan combine

## Gotchas

- Population stddev (`n` denominator), not sample (`n-1`).
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)², then decimal `Sqrt`). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
