---
id: U69
slug: regression-depth-2
title: "Regression refusals that remain on weighted, penalized, resampled and panel fits are lifted, each against a reference oracle"
track: Statistical integrity
size: L
status: done
depends_on: [U77, U78]
soft_depends_on: [U65]
blocks: [U32]
todo_items: [638, 639, 640, 641, 642, 643, 401, 487, 677, 678]
branch: regression-depth-2
---

# U69 — regression-depth-2

**Outcome:** Regression refusals that remain on weighted, penalized, resampled and panel fits are lifted, each against a reference oracle.

**Track:** Statistical integrity · **Size:** L · **Depends on:** U77, U78 (CI and test hygiene land first) · **Soft:** U65

## Summary

The remaining regression-depth gaps: negative-binomial penalty / selection / resample, resampled marginal effects and Wald comparisons, influence over weighted fits, the binary-predictor contrast mode, comparisons over sweep / vector / SEM fits, panel-robust covariance, lasso / elastic-net penalty factors, leverage on penalized and GLM fits, and Type II / III sums of squares for unbalanced repeated measures.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [x] **#638** `penalty` / `selection` / `resample` on the negative binomial: θ re-estimated inside each refit driver (penalized fit, every selection candidate, every jackknife / bootstrap replicate), pinned to a `MASS::stepAIC` and a jackknife oracle; the refusals (`requires: unpenalized` / `no_selection` / `no_resample`) stay until then; done in U69 E1-S1 / E3-S1 / E3-S2: penalty, selection and resample on the negative binomial re-estimate θ in every refit (`mpath::glmregNB`, `MASS::stepAIC`, jackknife / bootstrap oracles); NB + penalty + `dispersion: "estimated"` stays refused · [U69](units/U69-regression-depth-2.md)
- [x] **#639** A bootstrap of the marginal effects under `resample`: replicate the AME / MEM itself (not the coefficient covariance), so `margins` stops refusing `resample` (`requires: no_resample`); done in U69 E2-S2: `margins` runs beside `resample` with replicate-based SE / p (`inference: "resample"`) · [U69](units/U69-regression-depth-2.md)
- [x] **#640** Influence over a weighted bound fit: the prior-weighted working weight w, Pearson residual rP and dispersion φ for `ATTR_REG_LEVERAGE` / `ATTR_REG_INFLUENCE` / `ATTR_REG_RESIDUAL` kind `standardized` / `studentized` with `params.fit` (frequency against the `rep()` expansion), lifting `requires: unweighted`; done in U69 E1-S3: influence attributes read a frequency-weighted bound fit (against the `rep()` expansion); a probability weight stays refused, permanently · [U69](units/U69-regression-depth-2.md)
- [x] **#641** An opt-in `contrast` mode in `RegressionMargins` that reports a 0/1 predictor as its discrete 1 − 0 contrast (μ at 1 minus μ at 0, the other predictors as observed or at their means, with a delta-method SE), as `marginaleffects` does for a binary column; today the dummy gets its slope, and the oracle's `me_binary_terms` cross-check skips it. The default (slope) stays; done in U69 E2-S1: `margins.binary: "contrast"` reports a 0/1 predictor as its discrete 1 - 0 contrast · [U69](units/U69-regression-depth-2.md)
- [x] **#642** A Wald nested-model comparison (`compare` `test: wald`) over a resampled fit: the full fit's jackknife / bootstrap replicate covariance of the tested coefficients (χ² on q, as the resampled coefficient table is Wald z), pinned to an R replicate-covariance oracle; today a Wald comparison refuses `resample` (`requires: no_resample`) while F / LRT run on the base fit's deviances; done in U69 E2-S3: a `test: wald` comparison over a resampled fit reads the replicate covariance (χ² on q) · [U69](units/U69-regression-depth-2.md)
- [x] **#643** Nested-model comparisons (`compare`) over `from_sweep`, vector and SEM fits: a nesting rule for a battery (a vector fit's members, a SEM model's free parameters) and for a sweep-bound fit's winning request, with an R oracle (`lavaan` `anova` for SEM); today they are refused (`reason: from_sweep` / `fit_kind`); done in U69 E5-S1 / E5-S2 / E5-S3: `compare` over `from_sweep`, vector (member-wise) and SEM fits · [U69](units/U69-regression-depth-2.md)
- [x] **#401** Panel-robust `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` covariance: HAC across a panel (per-entity Newey–West or Driscoll–Kraay) and two-way clustering; done in U69 E4-S1 / E4-S2: `covariance.entity` (per-entity `hac`), type `driscoll_kraay`, and `covariance.cluster2` (Cameron–Gelbach–Miller two-way), against `sandwich::vcovPL` / `vcovCL` and `plm`; the MMM example (`docs/roadmap/fixtures/mmm-harbor-pine`) moves from clustering on the week to Driscoll–Kraay over region × week · [U69](units/U69-regression-depth-2.md)
- [x] **#487** Lasso / elastic-net `unpenalized`: per-predictor penalty factors for `l1` / `elasticnet` (glmnet `penalty.factor` with a coordinate-descent step per factor), beside the ridge form U53 shipped. Found in U53 (E3-S1); done in U69 E1-S1: lasso / elastic-net (and GLM) `unpenalized` per-predictor penalty factors, pinned to glmnet `penalty.factor` · [U69](units/U69-regression-depth-2.md)
- [x] **#677** `ATTR_REG_LEVERAGE` on penalized and GLM fits: `internal/processing/regression/coeffs.go:114` and `internal/model/refit.go:83-86` support only unpenalized OLS ("penalized / GLM leverage not implemented"); #235 covers only skill wording and #487 / #640 do not cover leverage; done in U69 E1-S2 / E1-S3 / E1-S4: `ATTR_REG_LEVERAGE` on penalized refits, GLM refits (`params.family` / `link`) and penalized bound fits · [U69](units/U69-regression-depth-2.md)
- [x] **#678** Repeated-measures `TEST_ANOVA_RM` on UNBALANCED data: a repeated (subject, condition) cell is refused (`PULSE_TEST_BALANCED_DESIGN_REQUIRED`, the slot fails alone; before, the last value won silently); a subject missing a condition is dropped (`params.incomplete` `complete_case`, default) or kept in a mixed model (`params.incomplete: "mixed"`: REML random-intercept fit, Satterthwaite denominator df, pinned to `lmerTest`). Done in U69 E6-S1; no Type II / III sums of squares are implemented · [U69](units/U69-regression-depth-2.md)
