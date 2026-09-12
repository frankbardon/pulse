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

- `values` — list of numbers, required, non-empty, **strictly ascending**.
- `weights` — list of numbers, default uniform. Non-negative, `len(values)` long. Raw counts fine — normalised at parse.

## Inputs

Field `type:` — `u4`/`u8`/`u16`/`u32`/`u64` (the arm `profile create` targets); also legal on `f32`/`f64`.

## Output

One declared level per row at its own share. Marginal exact — no threshold, no rounding bias.

## Gotchas

- **The only honest marginal for a coded scale.** `profile create` uses it for every integer column with ≤64 observed levels, ahead of `--fit-shape`. A clamped normal keeps the mean and flattens the shape: a 1–7 item at 25.26% on level 1 generated 13.73%.
- **Over 64 levels the capture ABANDONS it** (never truncates) and the field falls back to the clamped normal. The missing `discrete` key in the profile *is* the record — no warning, no knob.
- **A modelled target is an ORDERED PROBIT.** The staircase `Q` shifts the latent, not the value: direction and ordering carry, scale-point magnitude does not. `latentFor` refuses a staircase, so the fidelity `models` section scores both sides on the interval-midpoint probit score rather than inverting.
- Non-ascending or duplicate `values` → `SERVICE_VALIDATION`; silent sorting would break `Q`'s monotonicity and the copula's rank order.
- A rule reading the field needs no `round()` guard — the row value already is the stored integer.

## See

- Skills: `synthetic-data`, `op-synth-bernoulli`, `op-synth-weighted-categorical`, `type-u4`
