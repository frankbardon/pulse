---
name: synth-models
description: Synth `--fit-models` — per-numeric linear model capture, predictor selection, thin-level shrinkage, the composed latent draw, correlated residuals, and the fidelity recovery sections that score whether the captured structure survived generation. Split out of the synthetic-data topical.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, models, residual_correlations, fidelity, latent scale, predictor selection]
---

# Multi-predictor synthetic models

`--fit-models` = **several drivers conditioning one numeric at once**. The conditional-pair arms (`synthetic-data`) are pick-one: each overwrites its target, so on a wide cohort every categorical claims the same numeric and all but the first are dropped. One additive model accounts for all of them in one expression.

Contract surface for that machinery. Modes / distributions / marginals / correlations / claim order / determinism: `synthetic-data`. `rules[]` / `constraints[]`: `synth-structural-rules`. Calibration figures and closed design questions: `docs/src/cli/synth-calibration.md`.

## Capture (`--fit-models`)

One least-squares linear model per numeric field, regressed on the categorical **levels** and `set_*` **options** selection admitted, through `processing/regression`'s streaming OLS engine.

Cohort read ONCE; fits do NOT run per row. The scan only retains a bounded Algorithm-R snapshot (`modelResidualCap`, 10,000, on its own RNG stream off `--seed` so `--conditional`'s reservoir is never perturbed); selection, fitting and residuals run over that snapshot afterwards — forced, since a frequency ranking does not exist until rows have been seen. **Wire consequence: `n_obs` is the fit's support WITHIN the snapshot (≤10,000), not the cohort's row count** (`Profile.RowCount` is that).

One reference level per categorical drops into the intercept (full level set + intercept is rank-deficient). Set options are all kept — a multi-select is not a partition.

```
{field, intercept, predictors, references, n_obs, r2, residual_std, shrinkage_alpha}
  predictors[] = {kind, field, level, coefficient}    kind ∈ categorical_level | set_option | numeric
  references[] = the level each categorical dropped into the intercept
```

- Coefficients are addressed by **(field, level)**. The solver's internal design-column name is `json:"-"` — serialising it would freeze that encoding into the file format.
- `references[]` is why a reader holding *k−1* of *k* levels need not guess the baseline. Set-only model ⇒ none.
- `shrinkage_alpha` `omitempty`; absent ⇔ unpenalized OLS.
- Fitted **residual reservoir** (`FieldModel.Residuals` / `ResidualPresent`) also `json:"-"` — reach it via `synth.Profile.FittedModels()`, never a re-read document.
- No flag ⇒ no `models` key ⇒ byte-identical to a pre-flag document. An unsolvable design, or a fit that would serialise a non-finite float, loses only ITS model, named in `Profile.Warnings`. Never a refusal.

## Predictor selection — automatic, absolute, no knob

Two mechanisms, this order (`synth/profile_models_select.go`).

**(1) Top-K collapse — structural.** A candidate categorical contributes one column per top-`--top-k` level plus ONE catch-all carried as an ordinary level named `"other"` (`otherCategoryLabel`) — the same collapse `Categorical.Top` and `categorical_pairs` apply, so collapsed buckets mean one thing throughout a document. This is what makes the flag WORK, not a refinement: unbounded, a wide survey cohort blows past `maxModelColumns`, every target is skipped, and `--fit-models` produces NOTHING on real data. The catch-all is never the reference level.

**(2) Variance-explained floor — statistical**, over the collapsed set. A candidate FIELD enters iff the **adjusted** share of the target's total sum of squares between the candidate's groups reaches `synth.minVarianceExplained` (**0.01**). Package constant, not a `ProfileOptions` field (`TestModelSelection_NoUserFacingOverride`). **NOT significance** — at large n everything is significant, so a p-value gate is "all predictors enter" in disguise, silently, since every coefficient it admits is real and merely negligible. An absolute floor is n-invariant by construction (`TestModelSelection_AdmissionInvariantToRowCount`). Adjusted, not raw, η².

**Scoring is MARGINAL** — one candidate at a time against the RAW target, never against another's residual — so two collinear candidates (a banding beside the exact value; a region nested in a DMA) both clear and both enter; incremental scoring would make the admitted set depend on candidate order. Redundancy is the **solver's** job: a design refused as rank-deficient is refitted with its weakest-scoring admitted predictor dropped, up to `maxModelRefitRounds` (4) times. A functional-dependency probe is NOT attempted — the dependency that bites is LINEAR, not functional.

Selection also prunes columns degenerate ON THE ROWS THE FIT ADMITS: selection scores pairwise, the fit deletes listwise, so a level with pairwise support but no listwise rows is an identically-zero column the solver refuses. A single-level candidate drops by the same arithmetic, not a special case.

**MAIN EFFECTS only — no interactions.** One coefficient per (field, level), summed; cannot express "brand X matters only in region Y". A real limit: one pairwise interaction between two collapsed 33-column fields is 1,089 columns, and the scoring rule, the wire format and the recovery refit all assume one term per (field, level).

**A target no candidate clears is NOT skipped** — it gets a **zero-predictor model** (own mean + spread, `r2: 0`), warned `carries no predictors: …`, never `skipped: …`: a complete model and a failed capture mean opposite things. `maxModelColumns` (256) is a backstop against a pathological schema, not a working limit (the accumulator is O(p²) per row).

## Thin-level shrinkage

Selection picks FIELDS; this decides how far to trust their individual LEVELS. Top-K bounds RANK, not SUPPORT; listwise deletion cuts again; a coefficient from 4 rows is noise generation reproduces as a confident invented offset.

Trigger: a design column whose **listwise** support (rows the fit ADMITS, not cohort frequency) falls below `synth.minLevelObservations` (**50**) ⇒ the whole fit switches to the engine's ridge, `Penalty: "l2"`, `alpha = 50 / n_obs`. Its own constant, NOT `MinPairObservations` (30): that asks "enough co-occurrences for a correlation to mean anything", this asks "enough rows for a free coefficient to describe the level rather than the sample".

`alpha` is a pseudo-count — the engine adds `n·alpha` to each Gram diagonal, so a level keeps `n_j/(n_j + 50)` of its free coefficient. **It SCALES with thinness rather than switching on at the threshold**, which is why one global alpha suffices. Shrinkage is toward the field's **reference level** (what a dummy coefficient measures); a thin level collapsing onto the baseline is the conservative reading. Never refused, never dropped. `shrinkage_alpha` rides the wire because a shrunk and a free coefficient are different kinds of number and nothing else says which you hold; `alpha × n_obs` recovers the pseudo-count.

Three invisible consequences, none fixable by a different alpha:

1. Well-supported columns move too, by ≈`50/n_j` — bounded, small, not zero.
2. GATED on a thin column being present ⇒ clean designs stay exact OLS, at the cost of a bounded discontinuity at the boundary.
3. A penalized Gram is positive-definite ⇒ the rank-deficiency refusal the refit uses as its redundancy signal does NOT fire; collinear predictors are jointly shrunk instead of one being dropped.

Warnings reuse `thinSupportWarning`, doubly bounded: aggregated by (field, level) across targets (thinness is a LEVEL property, so per-(target, level) emission restates one finding once per model), then `maxThinLevelWarnings` (20) thinnest-first plus a counted summary. Lower `--top-k` to fold rare levels into `"other"`.

## The composed draw

A numeric carrying a `Spec.Models` entry is NOT drawn from its marginal and overwritten. It is composed in one step at the END of `drawRow`:

```
value = Q( Φ( μ(row) + σ·z ) )      μ = intercept + Σ fired coefficients   (raw data scale,
                                        standardised by the field's own mean/std)
                                    σ = residual_std / std,  z ~ N(0,1)
```

`Φ` / `Q` are `copula.go`'s own `phi` / `quantileFor` — the functions the correlation stage uses, so a modelled and a correlated field agree on what the marginal means. Standardising first is load-bearing: an OLS fit is in DATA units, `Φ` takes only a standard normal. **For a plain `normal` target the round trip collapses to `prediction + residual_std·z`**, the ordinary OLS draw; routing through `Φ`/`Q` anyway is what lets a `uniform`/`lognormal`/`mixture`/`discrete`/`bernoulli` target keep its own marginal while the predictor drives it.

**A term fires on an exact match only; a non-firing term contributes ZERO.** The dummy-coded reading, not a convenience default: predictors are read against a dropped reference folded into the intercept, so "nothing fired for this field" IS "this row sits at the baseline". A level the fit never saw reads as the reference; a NULL predictor contributes zero too, since listwise deletion means the coefficients say nothing about such rows.

**The top-K catch-all is NOT an unseen level — it has its own firing term.** `predictors[].kind` is a three-value wire vocabulary (`categorical_level`, `set_option`, `numeric`); the catch-all is a **`categorical_level` whose `level` is the literal `"other"`**. Those rows are real and often DOMINANT: `--conditional` collapses out-of-top-K values to that spelling, `categoricalPairSampler` resamples straight out of them, and the model stage runs last so it reads what the row carries. The field's own marginal cannot produce it (`SpecFromProfile` builds a categorical from `Categorical.Top`, which truncates and RENORMALISES rather than appending a bucket), so the pair stage is the whole path.

Getting that kind wrong is SILENT and costs the WHOLE model — `modelSpecFromProfile` refuses a model wholesale on one unusable predictor rather than leaving survivors read against a vanished baseline. A `default:` arm mapping the catch-all to `numeric` is how most captured models went silently unapplied for a whole development cycle, with a plausible cohort generated either way; `modelPredictorKind` now names every `dummyColumnKind` and `TestModelPredictorKind_CoversEveryDummyColumnKind` pins the enum's cardinality. `numeric` stays REFUSED at translation (a genuine scalar predictor has no generation-time term); a document carrying the old spelling is **not** rehabilitated on read — re-capture is the answer, since read-side tolerance would freeze the bug into the file format.

**Exactly ONE clamp**, on the final data-scale value: the model's own `min`/`max` (the target's observed bounds, on `FieldModelSpec`) win, the field's marginal clamp is the fallback — so model stage and copula stage cannot disagree about the admissible range.

**Ordering and determinism.** The model stage runs LAST in `drawRow`, after every pair stage and the correlator, because its predictors are categorical levels and set bits those stages can still rewrite. Drawers sort into **schema field order** at setup (never `models` array order, never map order) and each consumes exactly one normal per row *unconditionally*, before any branch, so the stream depends only on the number of surviving models. `residual_std` of 0 still consumes its draw and multiplies by zero. No `models` ⇒ zero drawers ⇒ byte-identical to pre-model output.

## Latent-scale effects — read this before reading a coefficient

**A coefficient shifts the LATENT Gaussian, not the value.** `μ` lives on the standard-normal scale; the value is `Q` of the shifted latent. Only an affine `Q` — i.e. `normal` — carries a coefficient to the data scale unchanged.

Under a **`mixture`, `lognormal` or any non-normal `Q`** the same coefficient moves the value by an amount depending on where the row landed: near a bimodal trough it moves MASS between modes and barely moves values inside either; in a tail it moves the value a lot. **`bernoulli`** is the sharpest case (a step, so the draw is a probit and the coefficient is neither a probability change nor a value change), `discrete`'s staircase the same. Neither has a point inverse, so recovery uses the calibrated probit score.

**Direction and monotonicity hold; magnitude in data units does not.** A coefficient of 0.556 on a 0–10 NPS field is NOT "0.556 points on the scale". Do not multiply one into data units; do not compare two coefficients across targets with different `Q`s as one currency. `latent_scale` on a fidelity entry lets a reader multiply back; the unit making one tolerance meaningful across every field is one target standard deviation.

The value-space alternative — draw the marginal, then ADD the prediction — destroys the fitted marginal it was meant to protect. Rejected; do not quietly reintroduce it.

## Shape-fitted targets accept conditioning

`--fit-shape` and conditioning are NOT mutually exclusive, and FOUR independent sites must agree on that (the shape pre-claim in `resolveConflicts`, `modelSpecFromProfile`, `fieldMoments`, `quantileFor`) — any one alone silently strips the relationship, which is why a change to one looks like it works and then fails at the next.

The construction always admitted it: `Q(Φ(μ+σ·z))` takes an ARBITRARY marginal in `Q`. A captured mixture carries **exact moments** (law of total variance) and a **quantile function** (`synth/mixture_quantile.go`); the fitted mixture becomes `Q`, the predictors shift the latent, the two compose with nothing dropped and no warning. Effects are latent-scale, hence non-linear in value space — above.

**The quantile inverse is a FIXED-COUNT bisection, deliberately.** `mixtureQuantileBisections` (64) steps over a bracket `min(μ_i) − 40·max(σ_i) .. max(μ_i) + 40·max(σ_i)`, where `Φ` has saturated at exactly 0 and 1 so no root escapes. **No convergence test, no early exit** — a tolerance-terminated inverse makes the answer depend on how many steps a given `p` needed, and byte-determinism is not negotiable. Bisection over Newton additionally because the density underflows between well-separated modes and a Newton step there explodes.

An UNMODELLED `--fit-shape` field is untouched: draws its own mixture, holds its pre-claim, still excludes a pair or correlation naming it with the `captured shape (--fit-shape)` warning.

## The correlated residual draw

**Capture (`--residual-correlations`, requires `--fit-models`).** The correlation structure among fitted model RESIDUALS. Once a numeric's systematic variation is explained by its predictors, what is free to move at generation time is the residual — and a raw correlation double-counts every predictor two targets share. Computed off `FieldModel.Residuals` in memory after the fits: **no extra cohort read**.

Shape `{fields, pairs, unmeasured}` — sorted participant set, `{a,b,rho,n}` per MEASURED pair, `{a,b,n,reason}` per unmeasurable pair; exhaustive, non-overlapping. **Full submatrix, never top-K**: the consumer is a joint draw over every participant at once and a matrix is not a ranked list — an omitted pair is a hole the factorization must fill, not a weak pair. `--include-correlations` / `--correlation-top-k` keep their meaning (top-K pairs of RAW values); this shares neither flag and is NOT implied by `--fit-models`, being quadratic in modelled fields.

**Measured-zero and unmeasured are structurally different, never conventionally different.** A dense N×N array has one slot per pair, so "unmeasured" would have nowhere to live but a sentinel — and 0 is a perfectly ordinary measurement. Two lists keep the distinction across a JSON round trip, and an unmeasured entry carries **no `rho` key at all**. Two closed reasons: `insufficient_overlap` (fewer than `minResidualPairObservations` = 3 rows carry both residuals; two points always give ±1) and `no_variance` (overlapped, one side's residual constant — 0/0 is not 0). That floor is NOT `MinPairObservations` (30): 30 answers "stable enough to trust" and its answer is a WARNING; this answers "is there a measurement at all" and its answer is a GAP. They compose.

**Participants = every model carrying a residual vector, INCLUDING zero-predictor models** — such a target still has a residual (its whole deviation from its own mean), and excluding it drops real structure for an unrelated reason. A model with a NIL residual vector (a `Profile` decoded from a document) is not a participant.

**Generation.** A modelled field named in `Spec.ResidualCorrelations` reads its own component of ONE correlated standard-normal vector drawn per row (`synth/residual_draw.go`) as the `z` of `Q(Φ(μ + σ·z))`; every other drawer takes its own fresh normal. That is the whole of how a field is **both conditioned and correlated** — predictors move `μ`, the shared vector correlates the residual, neither overwrites the other. Cholesky, assume-and-record policy and ridge report are the SAME machinery the value-scale matrix uses (`factorCorrelations`): one construction, two consumers, so a policy cannot land on one scale and miss the other.

Component order is **drawer order (= schema field order)**, never `residual_correlations` array order, or the cohort would depend on how the request was serialized. The vector consumes exactly one normal per participant, in component order, BEFORE the first drawer runs; a non-participant still consumes its own draw where it always did. Fewer than two surviving participants ⇒ no correlator, no draw, so a spec declaring none stays byte-identical.

For a `normal` target the latent→value map is affine ⇒ realized value-scale residual correlation is the captured figure exactly. Under `lognormal` / `mixture` / `discrete` / `bernoulli` the RANK correlation is exact and Pearson attenuates.

**A value-scale `correlations` entry naming a modelled field is refused, permanently.** Realizing it would overwrite the model's output; rerouting it into the residual would apply every shared predictor a second time. `resolveConflicts` words it *is not applied … capture residual correlations (`profile create --residual-correlations`) to correlate a modelled field* — deliberately NOT a `conditional relationship conflict`, because no pair claimed the field.

**The zero-predictor target is the feature's real boundary.** Selection emits a zero-predictor model for a target nothing explained; capture admits it as a participant; `modelSpecFromProfile` DROPS it at translation, because a field nothing explains is better served by its captured conditional pair than by an empty model — so its captured residual pairs reach the spec as nothing. Reversing the drop would retire those fields' pairs in exchange, a worse trade, so the boundary is pinned by test rather than closed.

## What models retire

**Numeric-target conditional pairs retire PER TARGET, never per document.** A numeric landing a model on the Spec is left out of `Spec.CategoricalNumericPairs` / `SetNumericPairs` — one additive model already accounts for every predictor, whereas those arms each overwrite the same numeric and all but the first are dropped as conflicts. A model also PRE-CLAIMS its target in `resolveConflicts`, so a hand-authored spec declaring both loses the pair and is told which.

Those two guards make **`models` and the two numeric-target fidelity sections disjoint by construction**: a modelled numeric has NO entry in `categorical_numeric_pairwise` / `set_numeric_pairwise`, ever, since the pick-one sampler they score never runs for it. `categorical_pairwise` / `set_categorical_pairwise` / `set_set_pairwise` are untouched.

A model **dropped in translation** (unknown predictor kind, or a zero-predictor model) leaves its field's pair STANDING and warns `model for numeric field %q not applied: …`. Under a per-document rule such a field lost the pair too and was reconstructed from nothing at all — strictly worse than before the flag existed.

The three **non-numeric-target** arms (`CategoricalPairs`, `SetCategoricalPairs`, `SetSetPairs`) still arbitrate pick-one; a linear model targets a numeric and subsumes nothing they describe. `--conditional` + `--fit-models` keeps both halves. Absent `models`, every arm populates exactly as before.

Both recovery sections below belong to `--fidelity-report` (its framing, marginals and `pairwise` section: `synthetic-data`), are `omitempty`, and score ONLY relationships generation actually applied.

## Model recovery (`models`)

For every applied linear model, a refit on the `_synthetic` partition: captured coefficient beside recovered, per predictor. The only section asking whether captured *structure* survived rather than whether rows *look* like the source — the instrument for the silent structure-loss class this feature has shipped twice, each time with every marginal healthy while the conditioning was gone. It also settles the compression question: a modelled numeric's mean spread across a predictor's levels is visibly compressed against source, and recovered ≈ captured means that gap is the legitimate partial-effect-vs-marginal-contrast one (widened because predictors draw from their own marginals, so source confounding is absent by design), while recovered *itself* attenuated means a real fault. Nothing else tells them apart.

- **Latent scale, both sides.** Each generated value is inverted through `latentFor` (the algebraic inverse of the `quantileFor` the draw used) before the refit, and the captured coefficient is divided by the target's marginal std — precisely the division `buildModelDrawers` performs. **The two switches MUST move together** (round-trip-gated per distribution) or a distribution silently drops out of the section. Regressing raw values would compare a value-space effect against a latent coefficient and report every `--fit-shape` target as badly recovered while generation was exactly correct. `latent_scale` rides each entry.
- **A STAIRCASE target (`bernoulli`, `discrete`) recovers through a CALIBRATED probit score, marked `scale: "probit_score"`.** A step `Q` has no point inverse, so the score is `Φ⁻¹` of each level's cumulative midpoint — a monotone re-expression, not an estimate — and systematically ATTENUATED. Honest because the SAME score is computed TWICE: once from the generated values, once as the conditional mean the CAPTURED model implies per row (`E[score | m]`, same residual). OLS is linear in its response ⇒ `E[b_observed | X] == b_expected` EXACTLY under faithful generation, so dividing by `b_expected / captured` REMOVES the attenuation rather than estimating it. That ratio ships as `score_retention` and divides the standard error too — low retention is a WIDE BAND, not a suspect number. **No K gate** (`K = 2` is not degenerate, merely the case retaining least); below `minScoreRetention` (0.05) the entry ships an `error`. A staircase entry reports **no recovered intercept** — its cut points come from the captured marginal, which generation holds exactly.
- **Only applied models.** `BuildModelFidelity` re-runs `resolveConflicts` AND `buildModelDrawers` against the same `*Spec` `generate()` was given — both pure functions of the Spec — reproducing generation's surviving set including the drawer compiler's warn-and-skip refusals. A field with no model has NO entry, not an entry with empty values.
- **Unidentified terms are `error`, never a computed-looking delta.** A predictor whose level the output cohort never carries cannot be checked. **Not** the `"other"` catch-all, which fires: the design's top-K and `Categorical.Top`'s rank on DIFFERENT bases (design = frequency within the rows the fit listwise-admitted for that target; marginal = the whole cohort), both keep the same count, and they disagree.
- **Zero-predictor models are `marginal: true`, not failures** — a complete model, so no `predictors` array (an empty one would read as failure) and the intercept comparison only.
- **Flagging band.** `flagged` fires only when the gap exceeds **both** `synth.ModelRecoveryTolerance` (0.10 latent sd) **and** twice the refit's own standard error. `n_fired` separates "fired thousands of times and recovered nothing" (a fault) from "never fired" (nothing to recover).
- **Same engine, same adapter, unpenalized.** `processing/regression` through `dummyRecord`, exactly as capture did — a second estimator makes every gap ambiguous between "generation lost structure" and "two fitters disagree" — and unpenalized even when capture shrank its own, since the captured coefficient being compared against is the shrunken one. An ordered-probit refit would be more EFFICIENT, not more correct.
- **A flag on a QUANTIZED target is about the COHORT, not the instrument.** The composed draw holds the marginal only while the generated latent is standard normal (see Boolean marginals), so a modelled target's realised conditioning is systematically a little weaker than the captured model asked for. Survey data is mostly binary and small-integer, so this is the common case.

Gates: `TestBuildModelFidelity_DiscreteTargetRecoversItsCapturedCoefficients`, `TestBuildModelFidelity_DiscreteTargetFlagsBrokenGeneration`, `TestBuildModelFidelity_BooleanTargetRecoversAtKEqualsTwo`, `TestBuildModelFidelity_InvertibleTargetKeepsTheLatentScale`.

## Residual-correlation recovery (`model_residual_correlations`)

Same question BETWEEN fields: per applied residual correlation, captured rho beside recovered. **Deliberately not folded into `pairwise`, and the two section names are the load-bearing distinction** — `pairwise` scores `Spec.Correlations` (value-scale, realized by the copula), this scores `Spec.ResidualCorrelations` (residual-scale, realized by the residual correlator). Different numbers over the same two fields, and `resolveConflicts` excludes a modelled field from `Spec.Correlations` precisely so the two arms fire on DISJOINT field sets.

The recovered side uses the **refit's** residuals, not residuals against the captured coefficients — those fold every coefficient gap `models` already reports into a correlation and state one finding twice. Scale is latent for the reason the coefficients are.

**Bounded because quadratic:** `compared` / `flagged` / `mean_delta` / `max_delta` cover EVERY pair; `pairs` lists only the worst `maxResidualRecoveryPairs` (20) by absolute delta with `omitted` counting the rest, so an unlisted pair recovered at least as well as the last listed. `flagged` fires on `ResidualRecoveryTolerance` (0.10) ALONE — a correlation is unit-free and its SE is under 0.02 over thousands of rows, so a noise-scaled band would collapse onto the floor. Unmeasured pairs ride a separate list carrying `captured_rho` and no recovered slot (`insufficient_overlap` / `no_variance`, plus a recovery-only `no_model_fit`).

**A STAIRCASE endpoint lands in `no_model_fit`, deliberately.** A coefficient can be calibrated because OLS is linear in its response; a correlation cannot — the factor relating two score residuals' correlation to the correlation of the residuals that drove the draws depends on the PAIR's joint distribution rather than either marginal, and no projection removes it. On a survey cohort of staircase targets that leaves `compared` at 1, the rest counted under `no_model_fit` — counted, never silent. **Quote the participant count with the figure**: the total is C(applied models, 2), so the same finding reads as a different fraction on a cohort with a different applied-model count. Closing it needs a polychoric-style bivariate calibration or an ordered-probit refit; `processing/regression` has neither (`binomial`+`probit` is a reserved, unimplemented link).

**Cost.** The synthetic partition is decoded ONCE (`decodeSyntheticRows`) and every section rides that slice — a per-entry decode reintroduces the O(pairs × records) blowup that cache exists to fix. Refits consume at most `modelRecoveryRowCap` (10,000) rows, capture's own snapshot size, so neither side is estimated from more rows than the other.

## See

- `synthetic-data` — modes, distributions, marginals, correlations, the conflict claim order, the determinism contract, the rest of the fidelity report.
- `synth-structural-rules` — a rule that DETERMINES a field pre-claims it ahead of the model claim.
- `docs/src/cli/synth-calibration.md` — the measurements behind every rule here, and the closed design questions.
- `regression-modeling` — the `REG_OLS` engine both capture and the recovery refit drive.
- `docs/src/cli/profile-create.md` / `docs/src/cli/synth-from-profile.md` — the CLI surface.
