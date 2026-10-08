```yaml
name: synth-model-draw
description: How synth generates a modelled numeric — the composed latent draw, firing terms and the "other" catch-all, the single clamp, draw ordering, and why a coefficient is latent-scale and non-linear in value space.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, models, latent scale]
requires: [capability:synth]
```

# Synth model draw

## The composed draw

A numeric carrying a `Spec.Models` entry is NOT drawn from its marginal and overwritten. It is composed in one step at the END of `drawRow` (`internal/synth/model_draw.go`):

```
value = Q( Φ( μ(row) + σ·z ) )      μ = intercept + Σ fired coefficients   (raw data scale,
                                        standardised by the field's own mean/std)
                                    σ = residual_std / std,  z ~ N(0,1)
```

`Φ` / `Q` are `copula.go`'s own `phi` / `quantileFor` — the functions the correlation stage uses, so a modelled and a correlated field agree on what the marginal means. Standardising first is load-bearing: an OLS fit is in DATA units, `Φ` takes only a standard normal. **For a plain `normal` target the round trip collapses to `prediction + residual_std·z`**, the ordinary OLS draw; routing through `Φ`/`Q` anyway is what lets a `uniform`/`lognormal`/`mixture`/`discrete`/`bernoulli` target keep its own marginal while the predictor drives it. `z` comes from the residual correlator when the field participates ([`synth-residual-correlations`](synth-residual-correlations.md)), else a fresh normal.

**A term fires on an exact match only; a non-firing term contributes ZERO.** The dummy-coded reading, not a convenience default: predictors are read against a dropped reference folded into the intercept, so "nothing fired for this field" IS "this row sits at the baseline". A level the fit never saw reads as the reference; a NULL predictor contributes zero too, since listwise deletion means the coefficients say nothing about such rows.

**The top-K catch-all is NOT an unseen level — it has its own firing term.** `predictors[].kind` is a three-value wire vocabulary (`categorical_level`, `set_option`, `numeric`); the catch-all is a **`categorical_level` whose `level` is the literal `"other"`**. Those rows are real and often DOMINANT: `--conditional` collapses out-of-top-K values to that spelling, `categoricalPairSampler` resamples straight out of them, and the model stage runs last so it reads what the row carries. The field's own marginal cannot produce it (`SpecFromProfile` builds a categorical from `Categorical.Top`, which truncates and RENORMALISES rather than appending a bucket), so the pair stage is the whole path.

Getting that kind wrong is SILENT and costs the WHOLE model — `modelSpecFromProfile` refuses a model wholesale on one unusable predictor rather than leaving survivors read against a vanished baseline. A `default:` arm mapping the catch-all to `numeric` is how most captured models went silently unapplied for a whole development cycle, with a plausible cohort generated either way; `modelPredictorKind` now names every `dummyColumnKind` and `TestModelPredictorKind_CoversEveryDummyColumnKind` pins the enum's cardinality. `numeric` stays REFUSED at translation (a genuine scalar predictor has no generation-time term); a document carrying the old spelling is **not** rehabilitated on read — re-capture is the answer, since read-side tolerance would freeze the bug into the file format.

**Exactly ONE clamp**, on the final data-scale value: the model's own `min`/`max` (the target's observed bounds, on `FieldModelSpec`) win, the field's marginal clamp is the fallback — so model stage and copula stage cannot disagree about the admissible range.

**Ordering and determinism.** The model stage runs LAST among the draw stages, after every pair stage and the correlator, because its predictors are categorical levels and set bits those stages can still rewrite. Drawers sort into **schema field order** at setup (never `models` array order, never map order) and each consumes exactly one normal per row *unconditionally*, before any branch, so the stream depends only on the number of surviving models. `residual_std` of 0 still consumes its draw and multiplies by zero. A target whose marginal std is 0 is dropped with a warning (never an infinite `1/std`). No `models` ⇒ zero drawers ⇒ byte-identical to pre-model output.

## Latent-scale effects — read this before reading a coefficient

**A coefficient shifts the LATENT Gaussian, not the value.** `μ` lives on the standard-normal scale; the value is `Q` of the shifted latent. Only an affine `Q` — i.e. `normal` — carries a coefficient to the data scale unchanged.

Under a **`mixture`, `lognormal` or any non-normal `Q`** the same coefficient moves the value by an amount depending on where the row landed: near a bimodal trough it moves MASS between modes and barely moves values inside either; in a tail it moves the value a lot. **`bernoulli`** is the sharpest case (a step, so the draw is a probit and the coefficient is neither a probability change nor a value change), `discrete`'s staircase the same ([`synth-marginals`](synth-marginals.md)). Neither has a point inverse, so recovery uses the calibrated probit score.

**Direction and monotonicity hold; magnitude in data units does not.** Do not multiply a coefficient into data units; do not compare two across targets with different `Q`s as one currency. `latent_scale` on a fidelity entry lets a reader multiply back; the unit making one tolerance meaningful across every field is one target standard deviation.

The value-space alternative — draw the marginal, then ADD the prediction — destroys the fitted marginal it was meant to protect. Rejected; do not quietly reintroduce it.

A `--fit-shape` mixture target draws through this same construction with the mixture as `Q`: [`synth-shape-fit`](synth-shape-fit.md).

## See

- [`synth-models`](synth-models.md) · [`synth-model-selection`](synth-model-selection.md) · [`synth-residual-correlations`](synth-residual-correlations.md) · [`synth-model-recovery`](synth-model-recovery.md).
- [`synth-shape-fit`](synth-shape-fit.md) · [`synth-determinism`](synth-determinism.md).
