---
id: U68
slug: regression-ordinal-multinomial
title: "Ordinal and multinomial fits can be scored, compared, resampled and read as marginal effects like the other regressions"
track: Statistical integrity
size: L
status: done
depends_on: [U77, U78]
soft_depends_on: [U65]
blocks: [U32]
todo_items: [650, 651, 652, 653, 654, 655, 656, 657, 658, 659, 734]
branch: regression-ordinal-multinomial
---

# U68 — regression-ordinal-multinomial

**Outcome:** Ordinal and multinomial fits can be scored, compared, resampled and read as marginal effects like the other regressions.

**Track:** Statistical integrity · **Size:** L · **Depends on:** U77, U78 (CI and test hygiene land first) · **Soft:** U65

## Summary

`REG_ORDINAL` and `REG_MULTINOMIAL` shipped with a fit and a coefficient table but refuse most of the surrounding regression machinery. This unit adds row scorers (lifting the `save` / `holdout` / `models` refusals), resample and selection drivers, marginal effects, nested comparisons, robust covariance, relative-risk ratios, and the proportional-odds check for probit / cloglog links, each pinned to an R oracle.

Shipped in seven epics (scoring, resample / selection / penalty, robust covariance and exponentiated reporting, per-level margins, nested comparisons, the proportional-odds check, close-out). Every item landed in-unit.

## Owned TODO items

- [x] **#650** A row scorer for `REG_ORDINAL`: per-row category probabilities (and the expected level) over a bound ordinal fit — `ATTR_REG_PREDICT` gains a `params.level` (or a per-level output) — which also lifts the `save` and `holdout` refusals (no scorer today), pinned to `predict(polr, type = "probs")` · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#651** `resample` (jackknife / bootstrap) and `selection` on `REG_ORDINAL` through the engine's drivers, pinned to an R manual-deletion jackknife and `MASS::stepAIC` on `polr`; today both are refused by the shared-slot table · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#652** Marginal effects (`margins`) on `REG_ORDINAL`: the effect of each predictor on each level's probability (AME / MEM, delta method on the fit's covariance), pinned to `marginaleffects` on `polr`; today `margins` is refused (`requires: type REG_OLS or REG_GLM`) · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#653** Nested-model comparisons (`compare`) of two `REG_ORDINAL` fits: the likelihood-ratio test (`anova.polr`) and the Wald test on the full fit's covariance; today `compare` reads `REG_OLS` / `REG_GLM` fits only · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#654** A proportional-odds check for the probit / cloglog links (the Brant test is the logit model's): a likelihood-ratio test against the partial proportional-odds model, as `ordinal::nominal_test`; today `params.brant` with another link is refused (`requires: link logit`) · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#655** Per-row fitted category probabilities of a `REG_MULTINOMIAL` fit: `ATTR_REG_PREDICT` with a `params.category` over a bound multinomial fit (a scorer, so a Compose `fits` entry, `models`, `save` and `holdout` stop refusing it — `PULSE_FIT_INVALID` `regression_type`, `requires: no_save`, `no_scorer`), pinned to `fitted(multinom)` / `predict(type = "probs")` · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#656** A robust (sandwich) covariance on `REG_MULTINOMIAL` — HC0 / HC1 and cluster over the per-row scores (y − π) ⊗ x, so a probability weight gets a design-consistent SE — lifting `requires: classical_covariance`, pinned to an R closed form (`sandwich` has no `multinom` method) · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#657** Relative-risk ratios on `REG_MULTINOMIAL` as a reporting knob (Stata `mlogit, rrr`): exp(β) with its delta-method SE and the exponentiated interval, absent byte-identical · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#658** `penalty`, `selection` and `resample` on `REG_MULTINOMIAL` (glmnet `family = "multinomial"`, `MASS::stepAIC` on `multinom`, jackknife / bootstrap replicates), lifting `requires: unpenalized` / `no_selection` / `no_resample` · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#659** Marginal effects (`margins`, each category's probability: ∂π_k/∂x) and a joint test of a predictor across every category (Wald / LR on its K coefficients, `car::Anova(multinom)`) on `REG_MULTINOMIAL`, lifting `requires: no_margins` · [U68](units/U68-regression-ordinal-multinomial.md)
- [x] **#734** Nested-model comparisons (`compare`) of two `REG_MULTINOMIAL` fits: the likelihood ratio (`anova(multinom0, multinom1)`) and the Wald test over every `"<category>:<term>"` coefficient of the added predictors on the full fit's classical or robust covariance, df (K − 1)·(added predictors), the same category set and reference required (`reference_differs`), lifting `type_unsupported` · [U68](units/U68-regression-ordinal-multinomial.md)

## Outcome / decisions

Owner-delegated design calls (2026-11-17, "err on the side of flexibility and comprehensiveness"):

- **D1** each parallel unit (U75 / U76) cuts its own alpha at finalize, in merge order.
- **D2** scorer shape: one slot is one column. Ordinal `params.output` is `probability` / `cumulative` (both need `params.level`), `expected` (1-based, the default without a level) or `class`; multinomial is `probability` (needs `params.category`) or `class`; `params.scale: link` gives the ordinal xᵀβ and the multinomial log-odds against the reference. All levels at once through the item `for_each`.
- **D3** holdout over a categorical target reports `log_loss`, `brier`, `accuracy` (+ `rps` for ordinal); `rmse` / `mae` / `r2` are ABSENT.
- **D4** margins reuse the flat `"<level>:<term>"` keys plus an additive `outcomes` list; AME and MEM, response and link scale.
- **D5** replicates keep the base fit's levels / categories: a bootstrap replicate that loses one is skipped (`bootstrap_requested` − `bootstrap_succeeded`), a jackknife replicate that loses one fails the slot alone (`replicate_empty_level`).
- **D6** penalised multinomial is glmnet's symmetric K-category form (no reference, intercepts unpenalised); inference is `null` unless resampled. Unpenalised fits stay baseline-category and byte-identical.
- **D7** multinomial `compare` (LR + Wald, df (K − 1)·added predictors) is in scope: TODO #734.
- **D8** the partial proportional-odds LR test (`params.nominal_test`) is available for every link, logit included, beside Brant.
- **D9** `exponentiate: true` is one reporting knob: multinomial relative-risk ratios, ordinal cumulative odds ratios on predictors only; refused on other types.
- **D10** ordinal margins and compare run through new optional public `extend` hooks, `MarginsRegression` and `CompareRegression` (declared on `RegressionRegistration`, probed at `pulse.New`), so any extension regression can supply them; ordinal is the first implementer.
- **D11** multinomial selection is term-level (a predictor enters or leaves across all K − 1 categories at once), as `stepAIC` on `multinom`.

Notable implementation calls:

- **API break:** `types.RegressionHoldout` figures became `*float64` `omitempty` (wire bytes unchanged for numeric fits).
- The `bootstrap_skipped` field was dropped as redundant with the existing requested / succeeded counts.
- Multinomial robust covariance is `hc0` / `hc1` / `cluster`; `hc2` / `hc3` / `hac` and robust beside penalty / resample / selection are `PROCESSING_REGRESSION_VCOV_UNSUPPORTED` (the old `requires: classical_covariance` is gone).
- Weighted bootstrap and selection are `PULSE_WEIGHT_UNSUPPORTED`; a weighted jackknife runs. `nominal_test` is absent under a probability weight.
- Pre-existing parity gaps fixed in-unit: `REG_OLS` / `REG_GLM` selection without a criterion is `SERVICE_VALIDATION` at run time too; predict's `p_values` now count `params.brant`.
- `TestManifestForIntent_SizeGate` drivers exception rose 0.160 → 0.1692 cumulatively. The cause is structural: each lifted refusal added required Params to the REG_ORDINAL / REG_MULTINOMIAL manifest rows (`covariance`, `exponentiate`, `term_tests`, the penalty and resample knobs, `nominal_test`), and the slim manifest carries no prose left to trim.
- New R oracles: `ordinal`, `ucminf`, `car`, `nnet`, `glmnet`, `marginaleffects` (pins in `docs/src/internals/regenerating-goldens.md`).

## Handed-on

None: every owned item shipped in-unit.
