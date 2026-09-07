---
name: op-synth-mixture
description: Mixture-of-normals samples — per-row component draw by weight, then a Gaussian from that component; reproduces bimodal/multimodal shapes.
kind: operator
category: SYNTH
operator: mixture
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape]
---

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `means` | list[float] | required | Per-component means; length sets the component count (`>= 2`). |
| `stds` | list[float] | required | Per-component standard deviations; each `> 0`, length must match `means`. |
| `weights` | list[float] | uniform | Per-component mixing weight; length must match `means`. Non-negative; sum `> 0`. |

Component selection mirrors `weighted_categorical`: draw an index via the cumulative-weight table, then draw `N(means[i], stds[i]²)`.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Cast at write time. |

## Output

Per-row `float64` sample from the selected component's Gaussian. No `min`/`max` clamp — use `constraints` for hard bounds.

## Gotchas

- Fewer than 2 `means` → `SERVICE_VALIDATION` (use `normal` for one component).
- `stds`/`weights` length mismatch with `means` → `SERVICE_VALIDATION`.
- Any `std <= 0`, or negative/all-zero `weights` → `SERVICE_VALIDATION`.
- v1 component count is a schema-mode declaration only — no automatic component-count fitting from a captured profile yet.
- Not one of the closed-form marginals `correlations` supports (`normal`/`uniform`/`lognormal`/`exponential`) — naming `mixture` there refuses with `SERVICE_VALIDATION`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-normal`, `op-synth-weighted-categorical`
