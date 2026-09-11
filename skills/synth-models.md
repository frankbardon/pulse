---
name: synth-models
description: Synth `--fit-models` — per-numeric linear model capture, predictor selection, thin-level shrinkage, the composed latent draw, correlated residuals, and the fidelity recovery sections that score whether the captured structure survived generation. Split out of the synthetic-data topical.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth, models, residual_correlations, fidelity, latent scale, predictor selection]
---

# Multi-predictor synthetic models

`--fit-models` is how **several drivers condition one numeric at once**. The conditional-pair arms (`synthetic-data`) are pick-one: each overwrites the numeric it targets, so on a wide cohort every categorical claims the same target and all but the first are dropped. One additive model accounts for all of them in one expression.

This file is the CONTRACT surface for that machinery. Modes, distributions, marginals, correlations, the conflict claim order and the determinism contract are in `synthetic-data`; `rules[]` / `constraints[]` in `synth-structural-rules`; the calibration figures and closed design questions in `docs/src/cli/synth-calibration.md`.

## Capture (`--fit-models`)

One least-squares linear model per numeric field, regressed on the categorical **levels** and `set_*` **options** selection admitted, through `processing/regression`'s streaming OLS engine.

The cohort is read ONCE and the fits do NOT run per row: the scan only retains a bounded Algorithm-R snapshot (`modelResidualCap`, 10,000, on its own RNG stream off `--seed` so `--conditional`'s reservoir is never perturbed), and selection, fitting and residuals run over that snapshot afterwards — forced, since a frequency ranking does not exist until rows have been seen. **On the wire: `n_obs` is the fit's own support WITHIN the snapshot (≤10,000), not the cohort's row count** (`Profile.RowCount` is that).

One reference level per categorical is dropped into the intercept (a full level set plus an intercept is rank-deficient); set options are all kept — a multi-select is not a partition.

```
{field, intercept, predictors, references, n_obs, r2, residual_std, shrinkage_alpha}
  predictors[] = {kind, field, level, coefficient}    kind ∈ categorical_level | set_option | numeric
  references[] = the level each categorical dropped into the intercept
```

A coefficient is addressed by **(field, level)** — the solver's internal design-column name is `json:"-"` and never reaches the document, because serialising it would freeze that encoding into the file format. `references[]` is why a reader holding *k−1* of *k* levels need not guess the baseline; a set-only model carries none. `shrinkage_alpha` `omitempty` — absent ⇔ unpenalized OLS. The fitted **residual reservoir** (`FieldModel.Residuals` / `ResidualPresent`) is also `json:"-"`: read it off `synth.Profile.FittedModels()`, never from a re-read document.

No flag ⇒ no `models` key ⇒ byte-identical to a pre-flag document. An unsolvable design, or a fit that would serialise a non-finite float, loses only ITS model and is named in `Profile.Warnings` — never a refusal.

## Predictor selection — automatic, absolute, no knob

Two mechanisms in this order (`synth/profile_models_select.go`).

**(1) Top-K collapse — structural.** A candidate categorical contributes one column per top-`--top-k` level plus ONE catch-all carried as an ordinary level named `"other"` (`otherCategoryLabel`) — the same collapse `Categorical.Top` and `categorical_pairs` apply, so a document's collapsed buckets mean one thing throughout. This is what makes the flag WORK rather than a refinement of it: unbounded, a wide survey cohort blows past `maxModelColumns` and every target is skipped, so `--fit-models` produces NOTHING on real data. The catch-all is never the reference level.

**(2) Variance-explained floor — statistical**, over the collapsed set. A candidate FIELD enters iff the **adjusted** share of the target's total sum of squares between the candidate's groups reaches `synth.minVarianceExplained` (**0.01**). Package constant tuned in code, deliberately not a `ProfileOptions` field (`TestModelSelection_NoUserFacingOverride`). **NOT statistical significance** — at large n every candidate is significant, so a p-value gate is "all predictors enter" in disguise, and silently, since every coefficient it admits is real and merely negligible. An absolute floor is n-invariant by construction (`TestModelSelection_AdmissionInvariantToRowCount`). Adjusted, not raw, η².

**Scoring is MARGINAL** — one candidate at a time against the RAW target, never against another's residual — so two collinear candidates (a banding beside the exact value; a region nested in a DMA) both clear the floor and both enter. Incremental scoring would make the admitted set depend on candidate order. Redundancy is resolved by the **solver**: a design refused as rank-deficient is refitted with its weakest-scoring admitted predictor dropped, up to `maxModelRefitRounds` (4) times. A functional-dependency probe is NOT attempted — the dependency that bites is LINEAR, not functional.

Selection also prunes columns degenerate ON THE ROWS THE FIT ADMITS: selection scores pairwise, the fit deletes listwise, and a level with pairwise support but no listwise rows is an identically-zero column the solver refuses. A single-level candidate drops by the same arithmetic, not a special case.

**MAIN EFFECTS only — no interactions.** One coefficient per (field, level), summed; it cannot express "brand X matters only in region Y". A real limit: one pairwise interaction between two collapsed 33-column fields is 1,089 columns, and the scoring rule, the wire format and the recovery refit all assume one term per (field, level).

**A target no candidate clears is NOT skipped** — it gets a **zero-predictor model** (own mean + spread, `r2: 0`), warned as `carries no predictors: …` and never as `skipped: …`, because a complete model and a failed capture mean opposite things. `maxModelColumns` (256) is a backstop against a pathological schema, not a working limit (the accumulator is O(p²) per row).

## Thin-level shrinkage

Selection picks predictor FIELDS; a field can explain a target convincingly while retained LEVELS rest on a handful of rows, and a coefficient from 4 observations is noise generation reproduces as a confident invented offset. Top-K bounds RANK, not SUPPORT, and listwise deletion cuts again.

A design column whose **listwise** support (rows the fit ADMITS, not cohort frequency) falls below `synth.minLevelObservations` (**50**) switches the whole fit to the engine's ridge (`Penalty: "l2"`, `alpha = 50 / n_obs`). Its own constant, deliberately NOT `MinPairObservations` (30): that answers "enough co-occurrences for a correlation to mean anything", this answers "enough rows for a free coefficient to describe the level rather than the sample".

**Shrinkage SCALES with thinness rather than switching on at the threshold**, which is what makes one global alpha sufficient. `alpha` is a pseudo-count: the engine adds `n·alpha` to each Gram diagonal, so a level keeps `n_j/(n_j + 50)` of its free coefficient. It shrinks toward the field's **reference level** — what a dummy coefficient measures — and a thin level collapsing onto the baseline is the conservative reading. A thin level is NEVER refused and never dropped. `shrinkage_alpha` rides the wire because a shrunk and a free coefficient are different kinds of number and nothing else says which you hold; `alpha × n_obs` recovers the pseudo-count.

Three consequences invisible in the output, none of them fixable by a different alpha:

1. Well-supported columns inside a penalized fit move too, by ≈`50/n_j` — bounded, small, not zero.
2. It is GATED on a thin column being present, so a clean design stays exact OLS, at the cost of a bounded discontinuity at the boundary.
3. A penalized Gram is positive-definite, so the rank-deficiency refusal the refit uses as its redundancy signal does NOT fire — collinear predictors are jointly shrunk instead of one being dropped.

Warnings reuse `thinSupportWarning` and are **doubly bounded**: aggregated by (field, level) across targets (thinness is a property of the LEVEL, so per-(target, level) emission restates one finding once per model), then capped at `maxThinLevelWarnings` (20) thinnest-first with a counted summary. Lower `--top-k` to fold rare levels into `"other"`.

## The composed draw

A numeric carrying a `Spec.Models` entry is NOT drawn from its marginal and overwritten. It is composed in one step at the END of `drawRow`:

```
value = Q( Φ( μ(row) + σ·z ) )      μ = intercept + Σ fired coefficients   (raw data scale,
                                        standardised by the field's own mean/std)
                                    σ = residual_std / std,  z ~ N(0,1)
```

`Φ` / `Q` are `copula.go`'s own `phi` / `quantileFor` — the same functions the correlation stage uses, so a modelled and a correlated field agree on what the marginal means. Standardising first is load-bearing: an OLS fit is in DATA units, `Φ` takes only a standard normal. **For a plain `normal` target the round trip collapses to `prediction + residual_std·z`** — the ordinary OLS draw; routing through `Φ`/`Q` anyway is what lets a `uniform`/`lognormal`/`mixture`/`discrete`/`bernoulli` target keep its own marginal while the predictor drives it.

**A term fires only on an exact match; a term that does not fire contributes ZERO.** The dummy-coded reading, not a convenience default: predictors are read against a dropped reference folded into the intercept, so "nothing fired for this field" IS "this row sits at the baseline". A level the fit never saw reads as the reference; a NULL predictor contributes zero too, since listwise deletion means the coefficients say nothing about such rows.

**The top-K catch-all is NOT an unseen level — it has its own firing term.** `predictors[].kind` is a three-value wire vocabulary (`categorical_level`, `set_option`, `numeric`) and the catch-all is a **`categorical_level` whose `level` is the literal `"other"`**, the same spelling every collapsed bucket uses. Such rows are real and often DOMINANT: `--conditional`'s capture collapses out-of-top-K values to that spelling, `categoricalPairSampler` resamples straight out of them, and the model stage runs last so it reads what the row carries. The field's own marginal cannot produce it — `SpecFromProfile` builds a categorical from `Categorical.Top`, which truncates and RENORMALISES rather than appending a bucket — so the pair stage is the whole path.

Getting that kind wrong is SILENT and costs the WHOLE model: `modelSpecFromProfile` refuses a model wholesale on one unusable predictor rather than leaving survivors read against a vanished baseline. A `default:` arm mapping the catch-all to `numeric` is how most captured models went silently unapplied for a whole development cycle, with a plausible cohort generated either way; `modelPredictorKind` now names every `dummyColumnKind` and `TestModelPredictorKind_CoversEveryDummyColumnKind` pins the enum's cardinality. `numeric` stays REFUSED at translation (a genuine scalar predictor has no generation-time term), and a document carrying the old spelling is **not** rehabilitated on read — re-capture is the answer; read-side tolerance would freeze the bug into the file format.

**Clamping is exactly ONE clamp** on the final data-scale value: the model's own `min`/`max` (the target's observed bounds, on `FieldModelSpec`) win, with the field's marginal clamp as fallback, so the model stage and the copula stage cannot disagree about the admissible range.

**Ordering and determinism.** The model stage runs LAST in `drawRow`, after every pair stage and the correlator, because its predictors are categorical levels and set bits those stages can still rewrite. Drawers are sorted into **schema field order** at setup — never `models` array order, never map order — and each consumes exactly one normal per row *unconditionally*, before any branch, so the stream depends only on the number of surviving models. `residual_std` of 0 still consumes its draw and multiplies by zero. No `models` ⇒ zero drawers ⇒ byte-identical to pre-model output.

## Latent-scale effects — read this before reading a coefficient

**A coefficient shifts the LATENT Gaussian, not the value.** `μ` lives on the standard-normal scale; the value is `Q` of the shifted latent. Only an affine `Q` — i.e. `normal` — carries a coefficient through to the data scale unchanged.

Under a **`mixture`, `lognormal` or any non-normal `Q`** the same coefficient moves the value by an amount depending on where the row landed: near a bimodal trough it moves MASS between modes and barely moves values inside either; in a tail it moves the value a lot. A **`bernoulli`** `Q` is the sharpest case (a step, so the draw is a probit and the coefficient is neither a probability change nor a value change) and a `discrete` staircase the same; both also have no point inverse, so recovery uses the calibrated probit score.

**Direction and monotonicity hold. Magnitude in data units does not.** A coefficient of 0.556 on a 0–10 NPS field is NOT "0.556 points on the scale". Do not multiply one into data units, and do not compare two coefficients across targets with different `Q`s as one currency. `latent_scale` on a fidelity entry is what lets a reader multiply back; the unit that makes one tolerance meaningful across every field is one target standard deviation.

The value-space alternative — draw the marginal, then ADD the prediction — destroys the fitted marginal it was meant to protect. Rejected; do not quietly reintroduce it.

## Shape-fitted targets accept conditioning

`--fit-shape` and conditioning are NOT mutually exclusive, and FOUR independent sites must agree on that (the shape pre-claim in `resolveConflicts`, `modelSpecFromProfile`, `fieldMoments`, `quantileFor`) — any one alone silently strips the relationship, which is why a change to one looks like it works and then fails at the next.

The construction always admitted it: `Q(Φ(μ+σ·z))` takes an ARBITRARY marginal in `Q`. A captured mixture carries **exact moments** (law of total variance) and a **quantile function** (`synth/mixture_quantile.go`); the fitted mixture becomes `Q`, the predictors shift the latent, the two compose with nothing dropped and no warning. Effects are latent-scale, hence non-linear in value space — see above.

**The quantile inverse is a FIXED-COUNT bisection, deliberately.** `mixtureQuantileBisections` (64) steps over a bracket `min(μ_i) − 40·max(σ_i) .. max(μ_i) + 40·max(σ_i)`, where `Φ` has saturated at exactly 0 and 1 so no root escapes. **No convergence test and no early exit** — a tolerance-terminated inverse makes the answer depend on how many steps a given `p` needed, and byte-determinism is not negotiable. Bisection over Newton additionally because the density underflows between well-separated modes and a Newton step there explodes.

An UNMODELLED `--fit-shape` field is untouched: it draws its own mixture, holds its pre-claim, and still excludes a pair or correlation naming it with the `captured shape (--fit-shape)` warning.

## The correlated residual draw

**Capture (`--residual-correlations`, requires `--fit-models`).** The correlation structure among the fitted model RESIDUALS. Once a numeric's systematic variation is explained by its predictors, what is free to move at generation time is the residual — and a raw correlation double-counts every predictor two targets share. Computed off `FieldModel.Residuals` in memory after the fits; **no extra cohort read**.

Shape `{fields, pairs, unmeasured}` — the sorted participant set, `{a,b,rho,n}` per MEASURED pair, `{a,b,n,reason}` per unmeasurable pair; the three are exhaustive and non-overlapping. **Full submatrix, never top-K**: the consumer is a joint draw over every participant at once, and a matrix is not a ranked list — an omitted pair is a hole the factorization must fill, not a weak pair. `--include-correlations` / `--correlation-top-k` keep their meaning (top-K pairs of RAW values); this shares neither flag and is NOT implied by `--fit-models`, being quadratic in modelled fields.

**Measured-zero and unmeasured are structurally different, never conventionally different.** A dense N×N array has one slot per pair, so "unmeasured" would have nowhere to live but a sentinel — and 0 is a perfectly ordinary measurement. Two lists make the distinction survive a JSON round trip, and an unmeasured entry carries **no `rho` key at all**. Two closed reasons: `insufficient_overlap` (fewer than `minResidualPairObservations` = 3 rows carry both residuals; two points always give ±1) and `no_variance` (overlapped, but one side's residual is constant — 0/0 is not 0). That floor is NOT `MinPairObservations` (30): 30 answers "stable enough to trust" and its answer is a WARNING; this answers "is there a measurement at all" and its answer is a GAP. They compose.

**Participants are every model carrying a residual vector, INCLUDING zero-predictor models** — such a target still has a residual (its whole deviation from its own mean), and excluding it would drop real structure for an unrelated reason. A model with a NIL residual vector (a `Profile` decoded from a document) is not a participant.

**Generation.** A modelled field named in `Spec.ResidualCorrelations` reads its own component of ONE correlated standard-normal vector drawn per row (`synth/residual_draw.go`) as the `z` of `Q(Φ(μ + σ·z))`; every other drawer takes its own fresh normal. That is the whole of how a field is **both conditioned and correlated**: predictors move `μ`, the shared vector correlates the residual, neither overwrites the other. The Cholesky, the assume-and-record policy and the ridge report are the SAME machinery the value-scale matrix uses (`factorCorrelations`) — one construction, two consumers, so a policy cannot land on one scale and miss the other.

Component order is **drawer order (= schema field order)**, never `residual_correlations` array order, or the cohort would depend on how the request was serialized. The vector consumes exactly one normal per participant, in component order, BEFORE the first drawer runs; a non-participant still consumes its own draw where it always did. Fewer than two surviving participants ⇒ no correlator and no draw at all, which keeps a spec declaring none byte-identical.

For a `normal` target the latent→value map is affine, so the realized value-scale residual correlation is the captured figure exactly; under `lognormal` / `mixture` / `discrete` / `bernoulli` the RANK correlation is exact and Pearson attenuates.

**A value-scale `correlations` entry naming a modelled field is refused, permanently.** Realizing it would overwrite the model's output; rerouting it into the residual would apply every shared predictor a second time. `resolveConflicts` words it *is not applied … capture residual correlations (`profile create --residual-correlations`) to correlate a modelled field* and deliberately NOT as a `conditional relationship conflict`, because no pair claimed the field.

**The zero-predictor target is the feature's real boundary.** Selection emits a zero-predictor model for a target nothing explained; capture admits it as a participant; `modelSpecFromProfile` DROPS it at translation, because a field nothing explains is better served by its captured conditional pair than by an empty model — so its captured residual pairs reach the spec as nothing. Reversing the drop would retire those fields' pairs in exchange, a worse trade, so the boundary is pinned by test rather than closed.

## What models retire

**Numeric-target conditional pairs retire PER TARGET, never per document.** For a numeric that lands a model on the Spec, `SpecFromProfile` leaves that field out of `Spec.CategoricalNumericPairs` / `SetNumericPairs` — one additive model already accounts for every predictor, whereas those arms each overwrite the same numeric and all but the first are dropped as conflicts. A model additionally PRE-CLAIMS its target in `resolveConflicts`, so a hand-authored spec declaring both loses the pair and is told which.

A model **dropped in translation** (unknown predictor kind, or a zero-predictor model) leaves its field's pair STANDING and warns `model for numeric field %q not applied: …`. Under a per-document rule such a field lost the pair too and was reconstructed from nothing at all — strictly worse than before the flag existed.

The three **non-numeric-target** arms (`CategoricalPairs`, `SetCategoricalPairs`, `SetSetPairs`) are untouched and still arbitrate pick-one. `--conditional` + `--fit-models` keeps both halves. Absent `models`, every arm populates exactly as before.

The two recovery sections below are part of `--fidelity-report` (its framing, marginals and `pairwise` section are in `synthetic-data`). Both are `omitempty` and score ONLY relationships generation actually applied.

## Model recovery (`models`)

For every linear model generation applied, a refit of that model on the `_synthetic` partition: captured coefficient beside recovered, per predictor. The only section asking whether the captured *structure* survived rather than whether the rows *look* like the source — the instrument for the silent structure-loss class this feature has shipped twice, in each of which every marginal stayed healthy while the conditioning was gone. It also settles the compression question: a modelled numeric's mean spread across a predictor's levels is visibly compressed against source, and recovered ≈ captured means that gap is the legitimate partial-effect-vs-marginal-contrast one (widened because predictors draw from their own marginals, so source confounding is absent by design), while recovered *itself* attenuated means a real fault. Nothing else in the report tells them apart.

- **Latent scale, both sides.** Each generated value is inverted through `latentFor` — the algebraic inverse of the `quantileFor` the draw used — before the refit, and the captured coefficient is divided by the target's marginal std, precisely the division `buildModelDrawers` performs. **The two switches MUST move together** (round-trip-gated per distribution) or a distribution silently drops out of the section. Regressing raw values would compare a value-space effect against a latent coefficient and report every `--fit-shape` target as badly recovered while generation was exactly correct. `latent_scale` rides each entry.
- **A STAIRCASE target (`bernoulli`, `discrete`) is recovered through a CALIBRATED probit score, marked `scale: "probit_score"`.** A step `Q` has no point inverse, so the score is `Φ⁻¹` of each level's cumulative midpoint — a monotone re-expression, not an estimate — and it is systematically ATTENUATED. What makes it honest is computing the SAME score TWICE: once from the generated values, once as the conditional mean the CAPTURED model implies per row (`E[score | m]`, same residual). OLS is linear in its response, so `E[b_observed | X] == b_expected` EXACTLY under faithful generation, and dividing by `b_expected / captured` REMOVES the attenuation rather than estimating it. That ratio ships as `score_retention` and divides the standard error too — low retention is a WIDE BAND, not a suspect number. **No K gate** (`K = 2` is not degenerate, merely the case retaining least); below `minScoreRetention` (0.05) the entry ships an `error`. A staircase entry reports **no recovered intercept**: its cut points come from the captured marginal, which generation holds exactly.
- **Only applied models.** `BuildModelFidelity` re-runs `resolveConflicts` AND `buildModelDrawers` against the same `*Spec` `generate()` was given — both pure functions of the Spec — so the section reproduces generation's own surviving set including the drawer compiler's warn-and-skip refusals. A field with no model has NO entry, not an entry with empty values.
- **Unidentified terms are `error`, never a computed-looking delta.** A predictor whose level the output cohort never carries cannot be checked. This is **not** the `"other"` catch-all, which fires: the design's top-K and `Categorical.Top`'s rank on DIFFERENT bases (design = frequency within the rows the fit listwise-admitted for that target; marginal = the whole cohort), both keep the same count, and they disagree.
- **Zero-predictor models are `marginal: true`, not failures** — a complete model, so no `predictors` array (an empty one would read as failure) and the intercept comparison only.
- **Flagging band.** `flagged` fires only when the gap exceeds **both** `synth.ModelRecoveryTolerance` (0.10 latent sd) **and** twice the refit's own standard error. `n_fired` separates "fired thousands of times and recovered nothing" (a fault) from "never fired" (nothing to recover).
- **Same engine, same adapter, unpenalized.** `processing/regression` through `dummyRecord`, exactly as capture did — a second estimator would make every gap ambiguous between "generation lost structure" and "two fitters disagree" — and unpenalized even when capture shrank its own, since the captured coefficient being compared against is the shrunken one. An ordered-probit refit would be more EFFICIENT, not more correct.
- **A flag on a QUANTIZED target is about the COHORT, not the instrument.** The composed draw holds the marginal only while the generated latent is standard normal (see Boolean marginals), so a modelled target's realised conditioning is systematically a little weaker than the captured model asked for. Survey data is mostly binary and small-integer, so this is the common case.

Gates: `TestBuildModelFidelity_DiscreteTargetRecoversItsCapturedCoefficients`, `TestBuildModelFidelity_DiscreteTargetFlagsBrokenGeneration`, `TestBuildModelFidelity_BooleanTargetRecoversAtKEqualsTwo`, `TestBuildModelFidelity_InvertibleTargetKeepsTheLatentScale`.

## Residual-correlation recovery (`model_residual_correlations`)

The same question BETWEEN fields: for every residual correlation generation applied, captured rho beside recovered. **Deliberately not folded into `pairwise`, and the two section names are the load-bearing distinction** — `pairwise` scores `Spec.Correlations` (value-scale, realized by the copula), this scores `Spec.ResidualCorrelations` (residual-scale, realized by the residual correlator). Different numbers over the same two fields, and `resolveConflicts` excludes a modelled field from `Spec.Correlations` precisely so the two arms fire on DISJOINT field sets.

The recovered side uses the **refit's** residuals, not residuals against the captured coefficients — those would fold every coefficient gap `models` already reports into a correlation and state one finding twice. **Bounded because quadratic:** `compared` / `flagged` / `mean_delta` / `max_delta` cover EVERY pair, while `pairs` lists only the worst `maxResidualRecoveryPairs` (20) by absolute delta with `omitted` counting the rest, so an unlisted pair recovered at least as well as the last listed. `flagged` fires on `ResidualRecoveryTolerance` (0.10) ALONE — a correlation is unit-free and its SE is under 0.02 over thousands of rows, so a noise-scaled band would collapse onto the floor. Unmeasured pairs ride a separate list carrying `captured_rho` and no recovered slot (`insufficient_overlap` / `no_variance`, plus a recovery-only `no_model_fit`).

**A STAIRCASE endpoint lands in `no_model_fit`, deliberately.** A coefficient can be calibrated because OLS is linear in its response; a correlation cannot — the factor relating two score residuals' correlation to the correlation of the residuals that drove the draws depends on the PAIR's joint distribution rather than on either marginal, and no projection removes it. On a survey cohort of staircase targets that leaves `compared` at 1, the rest counted under `no_model_fit` — counted, never silent. **Quote the participant count with the figure**: the total is C(applied models, 2), so the same finding reads as a different fraction on a cohort with a different applied-model count. Closing it needs a polychoric-style bivariate calibration or an ordered-probit refit; `processing/regression` has neither (`binomial`+`probit` is a reserved, unimplemented link).

**`models` and the two numeric-target pair sections are disjoint by construction.** A numeric reached by a linear model has NO entry in `categorical_numeric_pairwise` or `set_numeric_pairwise`, ever — the pick-one sampler those sections score does not run for it. Two independent guards make that structural: `SpecFromProfile` omits a modelled target's captured pair, and `resolveConflicts` pre-claims every modelled field ahead of the categorical-numeric stage. `categorical_pairwise` / `set_categorical_pairwise` / `set_set_pairwise` are untouched.

**Cost.** The synthetic partition is decoded ONCE (`decodeSyntheticRows`) and every section rides that slice; a per-entry decode reintroduces the O(pairs × records) blowup that cache exists to fix. Refits consume at most `modelRecoveryRowCap` (10,000) rows — capture's own snapshot size, so neither side is estimated from more rows than the other.

## See

- `synthetic-data` — modes, distributions, marginals, correlations, the conflict claim order, the determinism contract, and the rest of the fidelity report.
- `synth-structural-rules` — a rule that DETERMINES a field pre-claims it ahead of the model claim.
- `docs/src/cli/synth-calibration.md` — the measurements behind every rule here, and the closed design questions.
- `regression-modeling` — the `REG_OLS` engine both the capture and the recovery refit drive.
- `docs/src/cli/profile-create.md` / `docs/src/cli/synth-from-profile.md` — the CLI surface.
