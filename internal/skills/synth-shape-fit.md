---
name: synth-shape-fit
description: Synth `--fit-shape` — the two-component mixture capture and its keep rule, and how a shape-fitted numeric still accepts model conditioning through its mixture quantile, with a fixed-count bisection for determinism.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [profile create, synth from-profile, fit-shape, mixture, models]
requires: [capability:synth]
---

# Synth shape fitting

## Capture (`--fit-shape`)

`--fit-shape` (`numeric.shape`, numeric only): a 2-component Gaussian mixture (`internal/synth/shape.go`) kept only when it beats plain normal on **BIC** AND the means are ≥ `0.75*avgStd` apart. `fitTwoComponentEM` runs a FIXED 50 iterations from a deterministic percentile init (no RNG) with a std floor at 5% of overall std. Fixed at 2 components, no sweep. `SpecFromProfile` then emits `mixture` instead of `normal`.

A `packed_bool` or a capped small integer never reaches this arm: their own `bernoulli` / `discrete` arms run AHEAD of it (`synth-marginals`).

## Shape-fitted targets accept conditioning

`--fit-shape` and conditioning are NOT mutually exclusive, and FOUR independent sites must agree on that (the shape pre-claim in `resolveConflicts`, `modelSpecFromProfile`, `fieldMoments`, `quantileFor`) — any one alone silently strips the relationship, which is why a change to one looks like it works and then fails at the next.

The construction always admitted it: `Q(Φ(μ+σ·z))` takes an ARBITRARY marginal in `Q`. A captured mixture carries **exact moments** (law of total variance) and a **quantile function** (`internal/synth/mixture_quantile.go`); the fitted mixture becomes `Q`, the predictors shift the latent, the two compose with nothing dropped and no warning. Effects are latent-scale, hence non-linear in value space — `synth-model-draw` (Latent-scale effects).

**The quantile inverse is a FIXED-COUNT bisection, deliberately.** `mixtureQuantileBisections` (64) steps over a bracket `min(μ_i) − 40·max(σ_i) .. max(μ_i) + 40·max(σ_i)`, where `Φ` has saturated at exactly 0 and 1 so no root escapes. **No convergence test, no early exit** — a tolerance-terminated inverse makes the answer depend on how many steps a given `p` needed, and byte-determinism is not negotiable. Bisection over Newton additionally because the density underflows between well-separated modes and a Newton step there explodes.

An UNMODELLED `--fit-shape` field is untouched: draws its own mixture, holds its pre-claim, still excludes a pair or correlation naming it with the `captured shape (--fit-shape)` warning.

## See

- `synth-model-draw` · `synth-models` · `synth-conflicts` · `op-synth-mixture`.
