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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `p` | float | `0.5` | Probability of emitting `1`; must lie in `[0, 1]`. |

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `packed_bool` (1 bit per row, one byte in the writer), unsigned int (`u4`/`u8`/`u16`/`u32`/`u64`), or `f32`/`f64`. |

## Output

Per-row sample: `1.0` with probability `p`, else `0.0`. Cast to declared field type at write time. `packed_bool` packs into the bit-shared neighbour byte.

## Gotchas

- **The only honest marginal for a `packed_bool`.** `profile create` reconstructs every `packed_bool` field as `bernoulli` with `p` = the observed mean, ahead of `--fit-shape`. A continuous marginal cannot round-trip through one bit: the writer must threshold, and a clamped normal thresholded anywhere reproduces the wrong prevalence (measured on a 90-boolean survey cohort: mean prevalence error 0.47, 89 of 90 fields off by more than 0.05; an 11% attribute generated at 64%).
- **A modelled `bernoulli` target is a PROBIT.** `quantileFor`'s step `Q` makes `value = Q(Φ(μ + σz))` into `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`. Coefficients order rows and hold the marginal exactly; they are NOT probability changes and must never be read as such. The fidelity report's `models` section reports `error` rather than a delta for these — a 0/1 value does not identify its latent.
- `p` outside `[0, 1]` → `SERVICE_VALIDATION` at spec parse. `p` of exactly 0 or 1 is legal and exact, but leaves zero variance, so a model on that target is dropped with a warning.
- For experiment-arm assignment, pair with `weighted_categorical` for `> 2` arms.
- Empirical proportion converges at rate `O(1/sqrt(n))` — small-sample cohorts will show observed `p_hat` materially different from declared `p`.
- Determinism: same `(spec, opts.Seed)` produces identical bit pattern.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-weighted-categorical`, `op-synth-constant`
