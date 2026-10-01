---
name: op-synth-bernoulli
description: Bernoulli samples emitting 0 or 1 with probability p; pairs with packed_bool or unsigned int fields.
kind: operator
category: SYNTH
operator: bernoulli
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, proportion-analysis]
---

Synth distributions emit per-row values; no `Response.Components`.

## Params

`p` — float, default `0.5`, probability of emitting `1`; must lie in `[0, 1]`.

## Inputs

Field `type:` — `packed_bool` (1 bit per row), unsigned int (`u4`/`u8`/`u16`/`u32`/`u64`), or `f32`/`f64`.

## Output

Per-row `1.0` with probability `p`, else `0.0`; cast to the declared field type at write time. `packed_bool` packs into the bit-shared neighbour byte.

## Gotchas

- **The only honest marginal for a `packed_bool`.** `profile create` reconstructs every `packed_bool` as `bernoulli` with `p` = observed mean, ahead of `--fit-shape`. A continuous marginal cannot round-trip through one bit — the writer must threshold, and a thresholded clamped normal reproduces the wrong prevalence (measured on a 90-boolean cohort: mean error 0.47, 89 of 90 fields off by >0.05; an 11% attribute generated at 64%).
- **A modelled `bernoulli` target is a PROBIT.** `quantileFor`'s step `Q` makes `value = Q(Φ(μ + σz))` into `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`. Coefficients order rows and hold the marginal exactly; they are NOT probability changes. The fidelity `models` section scores these on the interval-midpoint probit score, not a latent inversion.
- `p` outside `[0, 1]` → `SERVICE_VALIDATION` at spec parse. `p` of exactly 0 or 1 is legal and exact but has zero variance, so a model on that target is dropped with a warning.
- For `> 2` experiment arms use `weighted_categorical`.
- Observed `p_hat` converges at `O(1/sqrt(n))` — small cohorts diverge from declared `p`.
- Same `(spec, opts.Seed)` ⇒ identical bit pattern.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-weighted-categorical`, `op-synth-constant`
