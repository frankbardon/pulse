---
name: op-synth-discrete
description: Exact per-level histogram for an integer column; what a profile reconstructs every u4/u8/u16/u32/u64 field from.
kind: operator
category: SYNTH
operator: discrete
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, cohort-analysis]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | list of numbers | — | The levels, **strictly ascending**. Required, non-empty. |
| `weights` | list of numbers | uniform | Non-negative, same length as `values`. Raw counts are fine — normalised at parse. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `u4`/`u8`/`u16`/`u32`/`u64` (the arm `profile create` targets); legal on `f32`/`f64`. |

## Output

One declared level per row, at its own share. Marginal exact — no threshold, no rounding bias. No `Response.Components`.

## Gotchas

- **The only honest marginal for a coded scale.** `profile create` uses it for every integer column with ≤64 observed levels, ahead of `--fit-shape`. A clamped normal keeps the mean and flattens the shape: a 1–7 item with 25.26% at level 1 generated 13.73%; a 0–10 NPS with 32.00% at 10 generated 22.53%.
- **Over 64 levels the capture ABANDONS it** (never truncates) and the field falls back to the clamped normal. The missing `discrete` key in the profile *is* the record — no warning, no knob.
- **A modelled target is an ORDERED PROBIT.** The staircase `Q` shifts the latent, not the value: direction and ordering carry, scale-point magnitude does not, and the fidelity `models` section reports `error` rather than a delta.
- Non-ascending or duplicate `values` → `SERVICE_VALIDATION`. Sorting silently would break `Q`'s monotonicity and the copula's rank ordering.
- A rule reading the field needs no `round()` guard — the row value already is the stored integer.

## See

- Skills: `synthetic-data`, `op-synth-bernoulli`, `op-synth-weighted-categorical`, `type-u4`
