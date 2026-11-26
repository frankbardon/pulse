---
name: op-synth-exponential
description: Exponential samples with rate parameter lambda; mean is 1/lambda.
kind: operator
category: SYNTH
operator: exponential
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, time-series, distribution-shape]
---

Synth distributions emit per-row values; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `lambda` | float | `1.0` | Rate parameter; must be `> 0`. Mean = `1/lambda`. |

## Inputs

- field `type:` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Output is always `> 0`.

## Output

Per-row `float64` sample from `Exp(lambda)` (machine-independent). The tail is unbounded: under a low `lambda` an integer field saturates at its type's maximum (`u4` 15, `u8` 255).

<!-- generated: contract -->

## Gotchas

- `lambda <= 0` → `SERVICE_VALIDATION` before any row is written.
- Memoryless: the hazard rate is constant. For inter-arrival times, sum the draws downstream (a running-sum window).
- No clamp param — bound via `constraints` (e.g. `delay_s < 86400`) if a runaway tail breaks the field's range.
- Common pattern: `exponential` lifetimes / wait-times, log-transformed for distribution-shape work.

## See

- Skills: `synthetic-data`, `op-synth-poisson`, `op-synth-pareto`
