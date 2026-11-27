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

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does (`synthetic-data`).

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `p` | float | `0.5` | Probability of emitting `1`; must lie in `[0, 1]`. |

## Inputs

Field `type:` — `packed_bool` (1 bit per row), unsigned int (`u4`/`u8`/`u16`/`u32`/`u64`), or `f32`/`f64`.

## Output

Per-row `1.0` with probability `p`, else `0.0`; cast to the declared field type at write time.

<!-- generated: contract -->

## Gotchas

- **The only honest marginal for a `packed_bool`.** `profile create` reconstructs every `packed_bool` as `bernoulli` with `p` = observed mean, ahead of `--fit-shape`. A continuous marginal cannot round-trip through one bit: the writer thresholds it at 0.5, and a thresholded clamped normal reproduces the wrong prevalence.
- **A modelled `bernoulli` target is a PROBIT:** `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`. Coefficients order rows and hold the marginal exactly; they are NOT probability changes. The fidelity `models` section scores these on the interval-midpoint probit score, not a latent inversion.
- `p` outside `[0, 1]` → `SERVICE_VALIDATION` before any row is written. `p` of exactly 0 or 1 is legal and exact but has zero variance, so a model on that target is dropped with a warning.
- For `> 2` experiment arms use `weighted_categorical`.
- Observed `p_hat` converges at `O(1/sqrt(n))` — small cohorts diverge from declared `p`.
- Same spec and seed ⇒ the same rows.

## See

- Skills: `synth-marginals`, `op-synth-weighted-categorical`, `op-synth-constant`
