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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `lambda` | float | `1.0` | Rate parameter; must be `> 0`. Mean = `1/lambda`. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Output is always `> 0`. |

## Output

Per-row `float64` sample from `Exp(lambda)`, computed as `rng.ExpFloat64() / lambda`. Tail is unbounded — heavy enough to inflate `u4`/`u8` overflow risk under low lambda.

## Gotchas

- `lambda <= 0` → `SERVICE_VALIDATION` at spec parse.
- Memoryless property: hazard rate is constant. Pair with `monotonic_from` for inter-arrival timestamps (cumulative sum captured downstream via a window operator).
- No clamp param — bound via `constraints` (e.g. `delay_s < 86400`) if a runaway tail breaks the host field's cast.
- Common pattern: `exponential` lifetimes / wait-times feeding into `FEAT_LOG` for distribution-shape work.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-poisson`, `op-synth-pareto`
