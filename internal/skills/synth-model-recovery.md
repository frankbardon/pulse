---
name: synth-model-recovery
description: The synth fidelity report's `models` section — refitting each applied model on the generated rows, comparing on the latent scale (or a calibrated probit score for step targets), flagging bands, and how to read a flag on a quantized target.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, fidelity report, models, recovery]
requires: [capability:synth]
---

# Synth model recovery

Both recovery sections (this one and `synth-residual-recovery`) belong to `--fidelity-report` (its framing, marginals and `pairwise` section: `synth-fidelity-report`), are `omitempty` — a report for a profile captured without `--fit-models` is byte-identical — and score ONLY relationships generation actually applied.

## Model recovery (`models`)

For every applied linear model, a refit on the `_synthetic` partition: captured coefficient beside recovered, per predictor. The only section asking whether captured *structure* survived rather than whether rows *look* like the source — the instrument for the silent structure-loss class this feature has shipped twice, each time with every marginal healthy while the conditioning was gone. It also settles the compression question: a modelled numeric's mean spread across a predictor's levels is visibly compressed against source, and recovered ≈ captured means that gap is the legitimate partial-effect-vs-marginal-contrast one (widened because predictors draw from their own marginals, so source confounding is absent by design), while recovered *itself* attenuated means a real fault. Nothing else tells them apart.

- **Latent scale, both sides.** Each generated value is inverted through `latentFor` (the algebraic inverse of the `quantileFor` the draw used) before the refit, and the captured coefficient is divided by the target's marginal std — precisely the division `buildModelDrawers` performs. **The two switches MUST move together** (round-trip-gated per distribution) or a distribution silently drops out of the section. Regressing raw values would compare a value-space effect against a latent coefficient and report every `--fit-shape` target as badly recovered while generation was exactly correct. `latent_scale` rides each entry; `scale` says `"latent"`.
- **A STAIRCASE target (`bernoulli`, `discrete`) recovers through a CALIBRATED probit score, marked `scale: "probit_score"`.** A step `Q` has no point inverse, so the score is `Φ⁻¹` of each level's cumulative midpoint — a monotone re-expression, not an estimate — and systematically ATTENUATED. Honest because the SAME score is computed TWICE: once from the generated values, once as the conditional mean the CAPTURED model implies per row (`E[score | m]`, same residual). OLS is linear in its response ⇒ `E[b_observed | X] == b_expected` EXACTLY under faithful generation, so dividing by `b_expected / captured` REMOVES the attenuation rather than estimating it. That ratio ships as `score_retention` and divides the standard error too — low retention is a WIDE BAND, not a suspect number. **No K gate** (`K = 2` is not degenerate, merely the case retaining least); below `minScoreRetention` (0.05) the entry ships an `error`. A staircase entry reports **no recovered intercept** — its cut points come from the captured marginal, which generation holds exactly.
- **Only applied models.** `BuildModelFidelity` re-runs `resolveConflicts` AND `buildModelDrawers` against the same `*Spec` `generate()` was given — both pure functions of the Spec — reproducing generation's surviving set including the drawer compiler's warn-and-skip refusals. A field with no model has NO entry, not an entry with empty values. A rule that determines a field retires its model, so the entry disappears too.
- **Unidentified terms are `error`, never a computed-looking delta.** A predictor whose level the output cohort never carries cannot be checked. **Not** the `"other"` catch-all, which fires: the design's top-K and `Categorical.Top`'s rank on DIFFERENT bases (design = frequency within the rows the fit listwise-admitted for that target; marginal = the whole cohort), both keep the same count, and they disagree.
- **Zero-predictor models are `marginal: true`, not failures** — a complete model, so no `predictors` array (an empty one would read as failure) and the intercept comparison only.
- **Flagging band.** `flagged` fires only when the gap exceeds **both** `internal/synth.ModelRecoveryTolerance` (0.10 latent sd) **and** twice the refit's own standard error. `n_fired` separates "fired thousands of times and recovered nothing" (a fault) from "never fired" (nothing to recover).
- **Same engine, same adapter, unpenalized.** `internal/processing/regression` through `dummyRecord`, exactly as capture did — a second estimator makes every gap ambiguous between "generation lost structure" and "two fitters disagree" — and unpenalized even when capture shrank its own, since the captured coefficient being compared against is the shrunken one. An ordered-probit refit would be more EFFICIENT, not more correct.
- **A flag on a QUANTIZED target is about the COHORT, not the instrument.** The composed draw holds the marginal only while the generated latent is standard normal (`synth-marginals`, Boolean marginals), so a modelled target's realised conditioning is systematically a little weaker than the captured model asked for. Survey data is mostly binary and small-integer, so this is the common case.

**Cost.** The synthetic partition is decoded ONCE (`decodeSyntheticRows`) and every section rides that slice — a per-entry decode reintroduces the O(pairs × records) blowup that cache exists to fix. Refits consume at most `modelRecoveryRowCap` (10,000) rows, capture's own snapshot size, so neither side is estimated from more rows than the other.

Gates: `TestBuildModelFidelity_DiscreteTargetRecoversItsCapturedCoefficients`, `TestBuildModelFidelity_DiscreteTargetFlagsBrokenGeneration`, `TestBuildModelFidelity_BooleanTargetRecoversAtKEqualsTwo`, `TestBuildModelFidelity_InvertibleTargetKeepsTheLatentScale`.

## See

- `synth-models` · `synth-model-draw` · `synth-residual-recovery` · `synth-fidelity-report`.
