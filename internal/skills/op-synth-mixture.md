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

Synth distributions emit per-row values; no `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `means` | list[float] | required | Per-component means; length sets the component count (`>= 2`). |
| `stds` | list[float] | required | Per-component std devs; each `> 0`, length must match `means`. |
| `weights` | list[float] | uniform | Mixing weights; length must match `means`, non-negative, sum `> 0`. |

Selection mirrors `weighted_categorical`: draw an index off the cumulative-weight table, then draw `N(means[i], stds[i]²)`.

## Inputs

Field `type:` — numeric `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`. Cast at write time.

## Output

Per-row `float64` from the selected component's Gaussian. No `min`/`max` clamp — use `constraints` for hard bounds.

## Gotchas

- Fewer than 2 `means` → `SERVICE_VALIDATION` (use `normal` for one component).
- `stds`/`weights` length mismatch, any `std <= 0`, or negative/all-zero `weights` → `SERVICE_VALIDATION`.
- `profile create --fit-shape` fits and emits `mixture` automatically, but fixed at 2 components via BIC-vs-normal selection (`synth/shape.go`) — no sweep over component count. 3+ components are hand-written only.
- Excluded from `correlations`: unmodelled mixtures are pre-claimed before the copula bids, modelled ones refused there permanently; a modelled mixture correlates through `residual_correlations` instead.
- Composes with `--fit-models`: a shape-fitted target draws through its model with the mixture as `Q` via a fixed-count bisection inverse, so coefficients are LATENT-scale — non-linear in value space. See `synthetic-data`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-normal`, `op-synth-weighted-categorical`
