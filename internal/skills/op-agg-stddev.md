---
name: op-agg-stddev
description: Population standard deviation via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_STDDEV
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Weighted: √(m2_w / Σw).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — population stddev (n-denominator); `decimal128` input yields a decimal-scaled result. Per-group when wired under a grouper.

<!-- generated: reading-the-output -->

## Components

- `mean` — Welford running mean
- `m2` — Sum of squared deviations
- `variance` — `m2 / n`
- `stddev` — √variance

<!-- generated: contract -->

## Gotchas

- Population stddev (`n` denominator), not sample (`n-1`).
- `decimal128` is supported, but not by Welford: a decimal two-pass (mean, then Σ(x−μ)², then decimal `Sqrt`). An overflowing intermediate drops the WHOLE aggregate to an f64 pass and warns `PULSE_DECIMAL_PRECISION_LOSS`. The decimal claim therefore rests partly on an f64 fallback.
- Single-row group → 0; no value (or Σw = 0) → `null` (NaN), the components' `mean` / `variance` too.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
