---
name: regression-comparison
description: Nested-model comparison of two Compose regression fits — the F test, the likelihood-ratio (deviance) test and the Wald test of the full model's extra terms, R's anova(m0, m1) / lmtest::waldtest; how nesting and same rows are verified, the defaults per family, weights, the result and the refusals. Topical design; the fits themselves in compose-fits.
type: guide
kind: design
applies_to: compose, predict
covers: [compare, comparisons, nested model, model comparison, anova, anova.polr, anova.multinom, F test, likelihood ratio test, LRT, Wald test, waldtest, lrtest, deviance, ComposedRequest, FitComparisonSpec, FitComparisonResult]
requires: [capability:compose_compare]
---

# Nested-model comparison

`compare` asks whether a larger model fits better than a smaller one it contains: do the full model's extra terms add anything? R `anova(m0, m1)`, `lmtest::waldtest`; Stata `lrtest` / `test`. It reads two finished `fits`, no row.

<!-- feature: REG_GLM -->
```json
{"fits": [
   {"name": "base", "request": {"cohort": {"filename": "c.pulse"},
     "regressions": [{"type": "REG_GLM", "family": "binomial", "target": "churned", "predictors": ["tenure"]}]}},
   {"name": "plus_spend", "request": {"cohort": {"filename": "c.pulse"},
     "regressions": [{"type": "REG_GLM", "family": "binomial", "target": "churned", "predictors": ["tenure", "spend", "visits"]}]}}],
 "compare": [{"name": "spend_adds", "restricted": "base", "full": "plus_spend"}],
 "requests": [...]}
```
<!-- /feature -->

## Nested and on the same rows

Verified on the specs, at predict and at run alike:

- both names are regression fits (not vector / SEM; `from_sweep`: per slot, `compose-sweep-bind`), two different ones, one regression type;
- every restricted term (its predictors, plus the intercept when it fits one) is a full-fit term, and the full fit adds at least one — the tested `terms`;
- the requests are equal but for the regression's `predictors`, `intercept` and reporting knobs (`name`, `vcov`, `conf_level`, `margins`, `collinearity`, `save`<!-- feature: capability:multiplicity -->, `multiplicity`<!-- /feature -->) and the request's `label`, `return`, `disable_components`<!-- feature: capability:multiplicity -->, `multiplicity`<!-- /feature -->: same cohort, filters, features<!-- feature: capability:weighting -->, weight<!-- /feature -->, family, link, θ, dispersion, covariance. Put a feature only the full model uses in both requests.

The full fit's rows (listwise over more columns) are then a subset of the restricted fit's; unequal row counts fail the comparison alone, `PULSE_COMPARISON_ROWS_DIFFER` (as R's anova stops). Filter both to rows where the extra predictors are non-null.

## Tests

| `test` | Statistic | R |
|---|---|---|
| `f` | ((D₀ − D₁)/q)/φ̂₁ on (q, df₁) | `anova(m0, m1)` (lm), `anova(..., test = "F")` |
| `lrt` | (D₀ − D₁)/φ₁ on q; a θ-estimated negative binomial 2ℓ₁ − 2ℓ₀ | `anova(..., test = "Chisq" / "LRT")`, MASS `anova.negbin` |
| `wald` | b_Sᵀ V_SS⁻¹ b_S, V the full fit's own covariance | `lmtest::waldtest(m0, m1, vcov = V)` |

D is the residual deviance (OLS: the weighted RSS), φ̂₁ the full fit's estimated dispersion (OLS σ̂², the Pearson χ²/df), φ₁ = 1 where the family fixes it. Default (omitted `test`): R's anova default — `f` where the dispersion is estimated (least squares, gamma, a fixed-θ negative binomial under `dispersion: estimated`), `lrt` otherwise. `wald` reads the classical or robust `covariance` the fit chose (HC, cluster, HAC) and the coefficient table's distribution: F = W/q on its df where the p-values are Student t (least squares, classical gamma), χ² otherwise. A classical least-squares Wald equals the F test. Under `resample` V is the full fit's replicate covariance (its `vcov`, asked or not), χ² on every type; `f` / `lrt` keep the base deviances.

## Categorical-response fits

Ordinal and multinomial fits (and an embedder model declaring comparisons, `extend.CompareRegression`) test their predictors: `lrt` (default) = D₀ − D₁ on χ², `dispersion` 1; `wald` = χ² on the full fit's own covariance; `f` refused (`estimated_dispersion`). Untested terms differing once fitted fail alone (`PULSE_COMPARISON_ROWS_DIFFER`, `terms_differ`).
<!-- feature: REG_ORDINAL -->
- `REG_ORDINAL`: `anova.polr`, df = added predictors; same `params.levels` (or none) — thresholds are never tested.
<!-- /feature -->
<!-- feature: REG_MULTINOMIAL -->
- `REG_MULTINOMIAL`: `anova.multinom`; each term tests its K − 1 `"<level>:<term>"` keys, df = (K − 1)·added; same `reference` (or none); residual df (K − 1)·N* − edf.
<!-- /feature -->

<!-- feature: capability:weighting -->
## Weights

The fits' own weight (equal on both). Frequency = the `rep()` expansion (df = Σw − rank). A probability weight refuses `f` and `lrt` (pseudo-likelihoods, as `two_log_lik` is absent there); use `wald`, ideally on a robust covariance.
<!-- /feature -->

## Result

`comparisons[i]` (declaration order; outside `return`, never rounded): `name`, `restricted`, `full`, `test`, `distribution` (`f` / `chisq`), `terms`, `statistic`, `df` (q), `df_residual` (F only), `p_value`, `n_obs`, per fit `*_df_residual` and `*_deviance`, `dispersion` (the φ divided by; not on Wald), and `*_two_log_lik` where the LR reads them. Undefined figures are `null`. A failed comparison keeps `{name, restricted, full, test, error}` with `PULSE_SLOT_FAILED` (`slot_kind: comparisons`); a failed fit passes its own code (`details.fit`); Strict fails the batch.

<!-- feature: capability:multiplicity -->
## Multiplicity

A comparison is one hypothesis: like a test it inherits the batch `multiplicity` (own block → batch → instance default). Only family `compose` (the default): the comparisons pool with every compose-family member, `p_adjusted` / `significant_adjusted` beside the raw `p_value`.
<!-- /feature -->

## Refusals

`PULSE_COMPARISON_INVALID`, `details.reason` (`compare` = entry index), identical at predict and run: `name_missing` / `name_duplicate`, `fit_missing` / `fit_unknown` (`available`) / `fit_kind` / `same_fit`, `type_unsupported` / `type_mismatch`, `test_unknown`, `not_nested` (`missing`; swapped sides say so), `no_extra_terms`, `request_mismatch` (`keys`), and `requires`: `unpenalized`, `no_selection`, `unbounded`, `estimated_dispersion` (`f` on a fixed dispersion)<!-- feature: capability:weighting -->, `no_probability_weight` (`alternative: wald`)<!-- /feature -->. Predict lists each valid entry's resolved `test`, `distribution` and `terms` under `compare`.<!-- feature: REG_ORDINAL --> Ordinal `params.levels` differ: `levels_differ`.<!-- /feature --><!-- feature: REG_MULTINOMIAL --> Multinomial `reference` differs: `reference_differs`.<!-- /feature -->

## Against R

R 4.6.1 (`anova`, MASS, `sandwich`, `lmtest`) at 1e-10 — least squares, logit, probit, poisson, gamma, negative binomial; HC3; frequency weights; a restricted fit through the origin<!-- feature: REG_ORDINAL -->; `anova.polr`<!-- /feature --><!-- feature: REG_MULTINOMIAL -->; `anova.multinom`<!-- /feature -->: `nested.json`. Resampled Wald: `car::linearHypothesis(vcov. = V)`, `compare_resample.json`.

## See

- `compose-fits` — the fits a comparison reads.
- `regression-inference` — the coefficient table and covariance.
