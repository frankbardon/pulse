```yaml
name: synth-models
description: Entry point for synth `--fit-models` — per-numeric linear model capture and its wire shape, the latent-scale reading of a coefficient, what a model retires, and which focused skill covers selection, the draw, residual correlations and recovery.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, models, residual_correlations, fidelity, latent scale, predictor selection]
requires: [capability:synth]
```

# Multi-predictor synthetic models

`--fit-models` = **several drivers conditioning one numeric at once**. The conditional-pair arms ([`synth-profile-capture`](synth-profile-capture.md)) are pick-one: each overwrites its target, so on a wide cohort every categorical claims the same numeric and all but the first are dropped. One additive model accounts for all of them in one expression.

Contract surface for that machinery. Calibration figures and closed design questions: `docs/src/cli/synth-calibration.md`.

| Question | Skill |
|---|---|
| Which predictors enter; how thin levels are shrunk | [`synth-model-selection`](synth-model-selection.md) |
| How a modelled value is drawn | [`synth-model-draw`](synth-model-draw.md) |
| A `--fit-shape` mixture target under a model | [`synth-shape-fit`](synth-shape-fit.md) |
| Correlating modelled fields' residuals (`--residual-correlations`) | [`synth-residual-correlations`](synth-residual-correlations.md) |
| Did captured coefficients survive? (`models` fidelity section) | [`synth-model-recovery`](synth-model-recovery.md) |
| Did residual correlations survive? (`model_residual_correlations`) | [`synth-residual-recovery`](synth-residual-recovery.md) |
| Claim order; a rule that retires a model | [`synth-conflicts`](synth-conflicts.md) · [`synth-rule-claims`](synth-rule-claims.md) |

## Capture (`--fit-models`)

One least-squares linear model per numeric field, regressed on the categorical **levels** and `set_*` **options** selection admitted, through `internal/processing/regression`'s streaming OLS engine.

Cohort read ONCE; fits do NOT run per row. The scan only retains a bounded Algorithm-R snapshot (`modelResidualCap`, 10,000, on its own RNG stream off `--seed` so `--conditional`'s reservoir is never perturbed); selection, fitting and residuals run over that snapshot afterwards — forced, since a frequency ranking does not exist until rows have been seen. **Wire consequence: `n_obs` is the fit's support WITHIN the snapshot (≤10,000), not the cohort's row count** (`Profile.RowCount` is that).

One reference level per categorical drops into the intercept (full level set + intercept is rank-deficient). Set options are all kept — a multi-select is not a partition.

**A `set_*` predictor is read through `setMaskFromWideEntry`, at EVERY rung** — the snapshot holds an `encoding.SetMask`, and a `.(uint64)` assertion on a wide rung reads `present == false`: MISSING, so every row is deleted listwise and the target reports `carries no predictors`, an ordinary outcome that carries no signal. The design-column reader (`dummyRecord`) and the recovery refit's reader (`recoveryIndicator`) share the helper ([`synth-set-fields`](synth-set-fields.md)).

```
{field, intercept, predictors, references, n_obs, r2, residual_std, shrinkage_alpha}
  predictors[] = {kind, field, level, coefficient}    kind ∈ categorical_level | set_option | numeric
  references[] = the level each categorical dropped into the intercept
```

- Coefficients are addressed by **(field, level)**. The solver's internal design-column name is `json:"-"` — serialising it would freeze that encoding into the file format.
- `references[]` is why a reader holding *k−1* of *k* levels need not guess the baseline. Set-only model ⇒ none.
- `shrinkage_alpha` `omitempty`; absent ⇔ unpenalized OLS ([`synth-model-selection`](synth-model-selection.md)).
- Fitted **residual reservoir** (`FieldModel.Residuals` / `ResidualPresent`) also `json:"-"` — reach it via `synth.Profile.FittedModels()`, never a re-read document.
- No flag ⇒ no `models` key ⇒ byte-identical to a pre-flag document. An unsolvable design, or a fit that would serialise a non-finite float, loses only ITS model, named in `Profile.Warnings`. Never a refusal.

## Latent-scale effects

**A coefficient shifts the LATENT Gaussian, not the value** — the draw is `value = Q(Φ(μ + σ·z))`. Only an affine `Q` (`normal`) carries it to the data scale unchanged. **Direction and monotonicity hold; magnitude in data units does not**: a coefficient of 0.556 on a 0–10 NPS field is NOT "0.556 points". Never multiply one into data units or compare coefficients across targets with different `Q`s. Restate this wherever a coefficient is surfaced. Full reading: [`synth-model-draw`](synth-model-draw.md).

## What models retire

**Numeric-target conditional pairs retire PER TARGET, never per document.** A numeric landing a model on the Spec is left out of `Spec.CategoricalNumericPairs` / `SetNumericPairs` — one additive model already accounts for every predictor, whereas those arms each overwrite the same numeric and all but the first are dropped as conflicts. A model also PRE-CLAIMS its target in `resolveConflicts`, so a hand-authored spec declaring both loses the pair and is told which.

Those two guards make **`models` and the two numeric-target fidelity sections disjoint by construction**: a modelled numeric has NO entry in `categorical_numeric_pairwise` / `set_numeric_pairwise`, ever, since the pick-one sampler they score never runs for it. `categorical_pairwise` / `set_categorical_pairwise` / `set_set_pairwise` are untouched.

A model **dropped in translation** (unknown predictor kind, or a zero-predictor model) leaves its field's pair STANDING and warns `model for numeric field %q not applied: …`. Under a per-document rule such a field lost the pair too and was reconstructed from nothing at all — strictly worse than before the flag existed.

The three **non-numeric-target** arms (`CategoricalPairs`, `SetCategoricalPairs`, `SetSetPairs`) still arbitrate pick-one; a linear model targets a numeric and subsumes nothing they describe. `--conditional` + `--fit-models` keeps both halves. Absent `models`, every arm populates exactly as before.

## See

- The focused skills in the table above.
- [`synthetic-data`](synthetic-data.md) — modes, distributions, the determinism rule; [`synth-fidelity-report`](synth-fidelity-report.md) — the rest of the report.
- [`regression-modeling`](regression-modeling.md) — the OLS engine both capture and the recovery refit drive.
- `docs/src/cli/profile-create.md` / `docs/src/cli/synth-from-profile.md` — the CLI surface.
