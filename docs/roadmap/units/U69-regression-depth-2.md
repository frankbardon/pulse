---
id: U69
slug: regression-depth-2
title: "Regression refusals that remain on weighted, penalized, resampled and panel fits are lifted, each against a reference oracle"
track: Statistical integrity
size: L
status: not-started
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

- [ ] **#638** `penalty` / `selection` / `resample` on the negative binomial: θ re-estimated inside each refit driver (penalized fit, every selection candidate, every jackknife / bootstrap replicate), pinned to a `MASS::stepAIC` and a jackknife oracle; the refusals (`requires: unpenalized` / `no_selection` / `no_resample`) stay until then · [U69](units/U69-regression-depth-2.md)
- [ ] **#639** A bootstrap of the marginal effects under `resample`: replicate the AME / MEM itself (not the coefficient covariance), so `margins` stops refusing `resample` (`requires: no_resample`) · [U69](units/U69-regression-depth-2.md)
- [ ] **#640** Influence over a weighted bound fit: the prior-weighted working weight w, Pearson residual rP and dispersion φ for `ATTR_REG_LEVERAGE` / `ATTR_REG_INFLUENCE` / `ATTR_REG_RESIDUAL` kind `standardized` / `studentized` with `params.fit` (frequency against the `rep()` expansion), lifting `requires: unweighted` · [U69](units/U69-regression-depth-2.md)
- [ ] **#641** An opt-in `contrast` mode in `RegressionMargins` that reports a 0/1 predictor as its discrete 1 − 0 contrast (μ at 1 minus μ at 0, the other predictors as observed or at their means, with a delta-method SE), as `marginaleffects` does for a binary column; today the dummy gets its slope, and the oracle's `me_binary_terms` cross-check skips it. The default (slope) stays · [U69](units/U69-regression-depth-2.md)
- [ ] **#642** A Wald nested-model comparison (`compare` `test: wald`) over a resampled fit: the full fit's jackknife / bootstrap replicate covariance of the tested coefficients (χ² on q, as the resampled coefficient table is Wald z), pinned to an R replicate-covariance oracle; today a Wald comparison refuses `resample` (`requires: no_resample`) while F / LRT run on the base fit's deviances · [U69](units/U69-regression-depth-2.md)
- [ ] **#643** Nested-model comparisons (`compare`) over `from_sweep`, vector and SEM fits: a nesting rule for a battery (a vector fit's members, a SEM model's free parameters) and for a sweep-bound fit's winning request, with an R oracle (`lavaan` `anova` for SEM); today they are refused (`reason: from_sweep` / `fit_kind`) · [U69](units/U69-regression-depth-2.md)
- [ ] **#401** Panel-robust `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` covariance: HAC across a panel (per-entity Newey–West or Driscoll–Kraay) and two-way clustering. `covariance.type: hac` reads one series and refuses a repeated `order_by` (`order_by_ties`), so the MMM example clusters on the week · [U69](units/U69-regression-depth-2.md)
- [ ] **#487** Lasso / elastic-net `unpenalized`: per-predictor penalty factors for `l1` / `elasticnet` (glmnet `penalty.factor` with a coordinate-descent step per factor), beside the ridge form U53 shipped. Found in U53 (E3-S1) · [U69](units/U69-regression-depth-2.md)
- [ ] **#677** `ATTR_REG_LEVERAGE` on penalized and GLM fits: `internal/processing/regression/coeffs.go:114` and `internal/model/refit.go:83-86` support only unpenalized OLS ("penalized / GLM leverage not implemented"); #235 covers only skill wording and #487 / #640 do not cover leverage · [U69](units/U69-regression-depth-2.md)
- [ ] **#678** Type II / III sums-of-squares for the UNBALANCED repeated-measures case: `errors/codes.go:1191` (`PULSE_TEST_BALANCED_DESIGN_REQUIRED`) says it is "not implemented yet" and the test fails instead; implement it, or reword the refusal as permanent · [U69](units/U69-regression-depth-2.md)
