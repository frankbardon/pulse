---
id: U68
slug: regression-ordinal-multinomial
title: "Ordinal and multinomial fits can be scored, compared, resampled and read as marginal effects like the other regressions"
track: Statistical integrity
size: L
status: not-started
depends_on: [U77, U78]
soft_depends_on: [U65]
blocks: [U32]
todo_items: [650, 651, 652, 653, 654, 655, 656, 657, 658, 659]
branch: regression-ordinal-multinomial
---

# U68 — regression-ordinal-multinomial

**Outcome:** Ordinal and multinomial fits can be scored, compared, resampled and read as marginal effects like the other regressions.

**Track:** Statistical integrity · **Size:** L · **Depends on:** U77, U78 (CI and test hygiene land first) · **Soft:** U65

## Summary

`REG_ORDINAL` and `REG_MULTINOMIAL` shipped with a fit and a coefficient table but refuse most of the surrounding regression machinery. This unit adds row scorers (lifting the `save` / `holdout` / `models` refusals), resample and selection drivers, marginal effects, nested comparisons, robust covariance, relative-risk ratios, and the proportional-odds check for probit / cloglog links, each pinned to an R oracle.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#650** A row scorer for `REG_ORDINAL`: per-row category probabilities (and the expected level) over a bound ordinal fit — `ATTR_REG_PREDICT` gains a `params.level` (or a per-level output) — which also lifts the `save` and `holdout` refusals (no scorer today), pinned to `predict(polr, type = "probs")` · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#651** `resample` (jackknife / bootstrap) and `selection` on `REG_ORDINAL` through the engine's drivers, pinned to an R manual-deletion jackknife and `MASS::stepAIC` on `polr`; today both are refused by the shared-slot table · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#652** Marginal effects (`margins`) on `REG_ORDINAL`: the effect of each predictor on each level's probability (AME / MEM, delta method on the fit's covariance), pinned to `marginaleffects` on `polr`; today `margins` is refused (`requires: type REG_OLS or REG_GLM`) · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#653** Nested-model comparisons (`compare`) of two `REG_ORDINAL` fits: the likelihood-ratio test (`anova.polr`) and the Wald test on the full fit's covariance; today `compare` reads `REG_OLS` / `REG_GLM` fits only · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#654** A proportional-odds check for the probit / cloglog links (the Brant test is the logit model's): a likelihood-ratio test against the partial proportional-odds model, as `ordinal::nominal_test`; today `params.brant` with another link is refused (`requires: link logit`) · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#655** Per-row fitted category probabilities of a `REG_MULTINOMIAL` fit: `ATTR_REG_PREDICT` with a `params.category` over a bound multinomial fit (a scorer, so a Compose `fits` entry, `models`, `save` and `holdout` stop refusing it — `PULSE_FIT_INVALID` `regression_type`, `requires: no_save`, `no_scorer`), pinned to `fitted(multinom)` / `predict(type = "probs")` · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#656** A robust (sandwich) covariance on `REG_MULTINOMIAL` — HC0 / HC1 and cluster over the per-row scores (y − π) ⊗ x, so a probability weight gets a design-consistent SE — lifting `requires: classical_covariance`, pinned to an R closed form (`sandwich` has no `multinom` method) · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#657** Relative-risk ratios on `REG_MULTINOMIAL` as a reporting knob (Stata `mlogit, rrr`): exp(β) with its delta-method SE and the exponentiated interval, absent byte-identical · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#658** `penalty`, `selection` and `resample` on `REG_MULTINOMIAL` (glmnet `family = "multinomial"`, `MASS::stepAIC` on `multinom`, jackknife / bootstrap replicates), lifting `requires: unpenalized` / `no_selection` / `no_resample` · [U68](units/U68-regression-ordinal-multinomial.md)
- [ ] **#659** Marginal effects (`margins`, each category's probability: ∂π_k/∂x) and a joint test of a predictor across every category (Wald / LR on its K coefficients, `car::Anova(multinom)`) on `REG_MULTINOMIAL`, lifting `requires: no_margins` · [U68](units/U68-regression-ordinal-multinomial.md)
