```yaml
name: op-agg-variance
description: Population variance via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_VARIANCE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
```

## Use when

Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group.

Questions it answers:

- How much does the order value vary within each channel, in squared units?
- Which sites show the most variable readings?

Use something else:

- `AGG_STDDEV` when you want the spread in the field's own units, which is easier to read.

## Params

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted it is the population m2_w / Σw. Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64` — population variance (n-denominator); `decimal128` input yields a decimal-scaled result (scale doubled). Per-group when wired under a grouper.

## Reading the output

- `value`: The average squared distance of a value from the group's mean, in the field's units squared (dollars squared, minutes squared). Its square root is AGG_STDDEV, which is in the field's own units and easier to read.
  - Caveat: Squared units make the size hard to judge directly. For pooling or planning a study's size use AGG_WELFORD's variance (dividing by n - 1), which those formulas expect.
  - Caveat: Extreme values weigh heavily, since each distance is squared.

## Components

Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. `n`/`n_null` stay raw counts.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | `m2 / n` |

- Mergeability: `Mergeable` (Chan parallel-merge)
- Streaming: per-chunk Welford

## Gotchas

- Population variance (`n`), not sample (`n-1`).
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)²). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
