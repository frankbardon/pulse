---
name: op-agg-stddev
description: Standard deviation via Welford's online algorithm, population by default (ddof 0), sample with ddof 1.
kind: operator
category: AGG
operator: AGG_STDDEV
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ddof` | int | 0 | `0` population √(m2/n); `1` sample √(m2/(n−1)). Any other value → `PROCESSING_CONFIG`. |

Weighted: √(m2_w / (Σw − ddof)).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — stddev with denominator n − ddof; `decimal128` input yields a decimal-scaled result. Per-group when wired under a grouper.

<!-- generated: reading-the-output -->

## Components

- `mean` — Welford running mean
- `m2` — Sum of squared deviations (raw)
- `variance` — `m2 / (n − ddof)`
- `stddev` — √variance (= the scalar)

<!-- generated: contract -->

## Gotchas

- Default is the population form (`n`); most tools report the sample form — set `ddof: 1`.
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)², then decimal `Sqrt`). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0 under `ddof: 0`, `null` under `ddof: 1` (or Σw ≤ 1); no value (or Σw = 0) → `null` (NaN), the components' `mean` / `variance` too.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
