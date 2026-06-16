---
name: op-synth-pareto
description: Heavy-tailed Pareto samples; xm is the scale (minimum), alpha the shape (tail weight).
kind: operator
category: SYNTH
operator: pareto
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape, outlier-detection]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `xm` | float | `1.0` | Scale (minimum emitted value); must be `> 0`. |
| `alpha` | float | `1.5` | Shape; must be `> 0`. Larger alpha → lighter tail. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Output is always `>= xm`. |

## Output

Per-row `float64` sample from a Pareto Type-I distribution. Drawn via inverse CDF: `xm / U^(1/alpha)` where `U = 1 - rng.Float64()`.

## Gotchas

- `alpha <= 1` → no finite mean. `alpha <= 2` → no finite variance. Document the choice if downstream tests assume moments.
- `xm <= 0 || alpha <= 0` → `SERVICE_VALIDATION` at spec parse.
- Tail dominates the buffered moments — `AGG_AVERAGE` over Pareto cohorts converges slowly; prefer `AGG_MEDIAN` for centre estimates.
- Common synthetic-wealth pattern: `xm = 10_000`, `alpha = 1.16` reproduces an 80/20 Pareto split.
- No clamp param — use `constraints` to bound the tail when needed (e.g. `wealth < 1e9`).

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-exponential`, `op-synth-lognormal`
