---
name: synth-marginals
description: Why a profiled `packed_bool` reconstructs as `bernoulli` and a small integer as `discrete` rather than a clamped normal — the three writers each arm covers, probit and ordered-probit model draws, and what that does to recovery and correlation.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, profile create, bernoulli, discrete, packed_bool, u4, marginals]
requires: [capability:synth]
---

# Synth marginals: booleans and small integers

A boolean or an integer column lands in the profiler's NUMERIC accumulator, and the obvious reading of that summary — `normal(mean, std)` clamped to the observed range — is wrong on the wire, severely and silently, because the writer quantizes it. Both arms below replace that reading, and share one shape that is the part to get right:

- The arm runs **AHEAD of `--fit-shape`** — a mixture fits such a column happily and reproduces its shares no better, at a bisection per draw.
- Capture is UNCONDITIONAL (no flag): this is the DEFAULT reconstruction being wrong, not an enhancement.
- **All THREE writers that can reach the field are covered — fixing one leaves the others wrong with no signal**: its own sampler, a conditional pair, and the model draw. A pair's target marginal is resolved from the target's own `FieldSpec`, never from a wire flag, so pair and marginal cannot disagree.

### Boolean marginals

**A `packed_bool` reconstructs as `bernoulli` with `p` = the observed mean.** The field holds ONE BIT, the writer must reduce a continuous draw to 0/1, and no threshold over a clamped normal lands right (a 20% boolean generated at 69%, a 50% one at 84%). `bernoulli` needs no threshold — the mean IS `p`.

Three writers: its own sampler draws `bernoulli`; a conditional pair draws each cell's captured `Mean` as a PREVALENCE, not a location (`Std` unused); a model draws through the step `Q`, making it a **probit** — `P(1 | row) = Φ((μ − Φ⁻¹(1−p)) / σ)`, so coefficients order rows, not probabilities. A pair's old `bernoulli` wire flag is RETIRED: still parsed, read by nothing, and warned when it disagrees with the target's marginal.

Consequences: `p` of exactly 0 or 1 has zero variance, so a model on it is DROPPED with a warning rather than producing an infinite `1/std` (the constant is deliberately not floored). A bernoulli target has no POINT latent inverse, so `latentFor` refuses it and recovery runs on the calibrated probit score (`synth-models`). The continuous arm survives only for a hand-authored `normal` on a `packed_bool`: biased, not correct, and `toBool` rounds at 0.5 there so it is at least not inverted.

**A MODELLED boolean's prevalence is held at `p` only while the generated latent is standard normal.** `Φ(u)` is uniform only when `Var(u) == 1`: true at FIT time by construction, not at GENERATION time, where predictors come from their own reconstructed marginals. The realised prevalence therefore carries a small systematic bias — not the sampler and not the write path, both measured exact. The same mechanism compresses a `normal` target's SD and shifts a `discrete` target's level shares, and a gated field inherits it: a `null_together` block lands on the modelled field's REALISED rate, not its captured one. Gate: `TestSynthModel_BooleanPrevalenceFollowsTheGeneratedLatentVariance`.

### Small-integer marginals

**An integer column with at most 64 observed levels reconstructs as `discrete` — its own exact per-level histogram.** The boolean defect one type wider: the writer stores `floor(v+0.5)`, so a clamped normal is QUANTIZED on the way to the file — the bell flattens the scale's real shape and the clamp piles asymmetric mass on the near bound, while the MEAN comes back roughly right, which is how it survived. A level's observed count IS its weight.

**Which types claim the arm is not a taste judgement.** `isIntegerQuantizedFieldType` is exactly the set of `writeFieldValueForField` arms applying `Floor(f+0.5)` — so `f32`/`f64` (the float is stored), `decimal128` (exact at its scale), `date` (owns `uniform_date`) and `packed_bool` (`bernoulli` runs ahead) are excluded. A type list ALONE is insufficient (a `u16` share-of-wallet is in the defect, a `u64` ID must not be), so the observed-level cap separates them.

**`maxDiscreteLevels` (64, package constant, no flag) is where capture ABANDONS the histogram, never truncates it** — a top-64-of-3,000 histogram makes every share a share of an arbitrary subset — and the ABSENCE of the `discrete` key IS that record, structurally rather than conventionally. Above the cap the field keeps the clamped normal. The boundary is PRAGMATIC, not a fidelity cliff: the clamped normal's per-level RELATIVE error is scale-invariant; only its absolute error shrinks (~1/K).

Three writers: its own sampler draws the staircase; a conditional pair locates the cell's captured moments ON that staircase — `value = Q(Φ((cellMean−fieldMean)/fieldStd + (cellStd/fieldStd)·z))`, the SAME construction the model stage uses; a model draws through the staircase `Q`, an **ordered probit** (predictors shift the latent, the histogram holds exactly, direction and ordering carry, scale-point magnitude does not).

Consequences: no POINT latent inverse, so `latentFor` refuses it as it refuses `bernoulli` and recovery runs on the probit score. Value-scale correlation on a `discrete` participant attenuates (`synth-correlations`). And the **pre-rounding gotcha DISAPPEARS** for these fields — the row value already IS the stored integer, so `{"set_expr": {"nps": "round(nps)"}}` is a no-op. That advice stays live only for `f32`/`f64`, an integer column over the cap, and a hand-authored continuous distribution on an integer field (`synth-rule-expressions`).

## See

- `op-synth-bernoulli` · `op-synth-discrete` · `type-packed-bool`.
- `synth-profile-capture` — the full reconstruction table.
- `synth-models` — the composed model draw and the probit-score recovery.
