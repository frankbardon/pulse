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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `mu` | float | `0.0` | Log-space mean (mean of `ln(X)`). |
| `sigma` | float | `1.0` | Log-space standard deviation; must be `> 0`. |

Spec defaults mirror the standard log-normal. Mean of the emitted distribution is `exp(mu + sigma²/2)`.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Output is always `> 0`, so signed-clamp casts are safe. |

## Output

Per-row `float64` sample `exp(mu + sigma * Z)` where `Z ~ N(0, 1)`. Cast to declared field type at write time.

## Gotchas

- Right tail is heavy; small `sigma` increases dispersion exponentially. Use `pareto` for true power-law tails.
- `sigma <= 0` → `SERVICE_VALIDATION` at spec parse.
- No built-in clamp — pair with a `constraint` (`amount < 1_000_000`) to bound outliers.
- Common synthetic-money pattern: `lognormal` over `f64`, then cast to `decimal128` with a constraint enforcing the precision floor.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-normal`, `op-synth-pareto`
