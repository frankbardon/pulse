---
name: op-synth-lognormal
description: Log-normal samples parameterised by log-space mu and sigma; output strictly positive.
kind: operator
category: SYNTH
operator: lognormal
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape, financial]
---

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does (`synthetic-data`).

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `mu` | float | `0` | Log-space mean (mean of `ln(X)`). |
| `sigma` | float | `1` | Log-space standard deviation; must be `> 0`. |

The defaults are the standard log-normal. The emitted mean is `exp(mu + sigma²/2)`.

## Inputs

field `type:` — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Output is always `> 0`.

## Output

Per-row `float64` sample `exp(mu + sigma * Z)` where `Z ~ N(0, 1)`. Cast to declared field type at write time.

<!-- generated: contract -->

## Gotchas

- Right tail is heavy; larger `sigma` increases dispersion exponentially. Use `pareto` for true power-law tails.
- `sigma <= 0` → `SERVICE_VALIDATION` before any row is written.
- No built-in clamp — pair with a `constraint` (`amount < 1_000_000`) to bound outliers.
- Synthetic money: `lognormal` on a `decimal128` field; each draw is written at the field's `scale`.

## See

- Skills: `synthetic-data`, `op-synth-normal`, `op-synth-pareto`
