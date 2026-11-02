```yaml
name: op-agg-variance
description: Variance via Welford's online algorithm, population by default (ddof 0), sample with ddof 1.
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

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | int | 0 | `0` population m2/n; `1` sample m2/(n−1). Any other value → `PROCESSING_CONFIG`. |

Weighted: m2_w / (Σw − ddof).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — variance with denominator n − ddof; `decimal128` input yields a decimal-scaled result (scale doubled). Per-group when wired under a grouper.

## Reading the output

- `value`: The average squared distance of a value from the group's mean, in the field's units squared (dollars squared, minutes squared). Its square root is AGG_STDDEV, which is in the field's own units and easier to read.
  - Caveat: Squared units make the size hard to judge directly. For pooling or planning a study's size use the sample form (params.ddof 1, or AGG_WELFORD's variance), which those formulas expect.
  - Caveat: Extreme values weigh heavily, since each distance is squared.

## Components

- `mean` — Welford running mean
- `m2` — Sum of squared deviations (raw)
- `variance` — `m2 / (n − ddof)` (= the scalar)

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `mean`, `m2`, `variance`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Default is the population form (`n`); most tools report the sample form — set `ddof: 1`.
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)²). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0 under `ddof: 0`, `null` under `ddof: 1` (or Σw ≤ 1); no value (or Σw = 0) → `null` (NaN), the components' `mean` / `variance` too.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
