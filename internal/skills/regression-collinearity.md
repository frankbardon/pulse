---
name: regression-collinearity
description: Variance inflation factors on a fitted regression — VIF and tolerance per term, the generalized VIF (GVIF and GVIF^(1/(2 df))) for a multi-column term such as a one-hot block, read off the fit's own weighted design or the predictors alone, execution and the refusals. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [collinearity, multicollinearity, VIF, GVIF, variance inflation factor, tolerance, car vif, regressions]
---

# Regression collinearity (VIF)

`collinearity` reports how much each term's coefficient variance is inflated by its overlap with the other predictors, on the fit itself — R `car::vif(model)`, Stata `estat vif`.

<!-- feature: MAT_COLLINEARITY -->
For a candidate predictor set with no model yet, use `MAT_COLLINEARITY` instead (same VIF, plus Belsley's diagnostics).
<!-- /feature -->

<!-- feature: REG_GLM -->
```json
{"regressions": [{"type": "REG_GLM", "family": "binomial", "target": "churned",
  "predictors": ["tenure", "plan_basic", "plan_pro", "spend"],
  "collinearity": {"groups": [{"name": "plan", "predictors": ["plan_basic", "plan_pro"]}]}}]}
```
<!-- /feature -->

`"collinearity": {}` = every predictor its own term, basis `model`. Absent = no block, byte-identical.

## Knobs

| Key | Values | Default | Meaning |
|---|---|---|---|
| `basis` | `model`, `predictors` | `model` | `model`: the fit's centered XᵀWX at the fitted weights (a GLM's IRLS working weights) — `car::vif` on the fit. `predictors`: the prior-weighted predictor correlation alone, as if the model were linear. On OLS the two are one matrix. |
| `groups` | `[{name, predictors}]` | none | Columns judged together as one term (a one-hot block's dummies, a spline basis). Every other predictor is its own term. |

## Output

`regressions[i].collinearity`: `basis`, `terms` (order: a group at its first predictor), then maps keyed by term — `vif`, `tolerance` (1/vif), `df` (columns), and `gvif_scaled` = vif^(1/(2·df)) when `groups` is set. A group's `vif` is Fox & Monette's generalized VIF det(R_SS)·det(R_−S)/det(R); compare groups of different `df` through `gvif_scaled` (√vif for one column). Unbanded: 5 / 10 are rules of thumb. Rides the `standard` preset.

## Design, not estimator

The VIF reads the design only: a `penalty` (OLS), `bounds`, a robust `covariance`, `resample` or `holdout` leave the figures as the plain fit's on the same rows. Dispersion and the sandwich never enter.
<!-- feature: capability:weighting -->
Weights enter as the fit's prior weights; frequency = the `rep()` expansion, and both kinds give the same figures (scale-free).
<!-- /feature -->

## Execution

OLS reads its streaming moments, so it streams and merges with the fit. A GLM is buffered anyway: one pass over the fitted rows after IRLS. Identical across decode / shard worker counts.

## Refusals

`PROCESSING_CONFIG`, `details.requires`, the same at predict and run:

| Cause | `requires` |
|---|---|
| bad `basis` | `basis model or predictors` |
| a group with no name / a non-predictor or repeated member / a clashing name | `a group name` / `a predictor` / `a unique term name` |
| `intercept: false` | `intercept` |
| `selection` | `no_selection` |
| a GLM `penalty` on basis `model` | `unpenalized` (or set basis `predictors`) |
| fewer than two terms | `two_or_more_terms` |
<!-- feature: REG_OLS, REG_GLM, REG_BAYES_LINEAR -->
| `REG_BAYES_LINEAR`, an extension or model regression | `type REG_OLS or REG_GLM` |
<!-- /feature -->

## Against R

Pinned to `car::vif` 3.1-5 on R 4.6.1 at 1e-10 (OLS, logit, poisson, negative binomial, gamma; grouped terms; both bases; frequency and probability weights): oracle `vif.json`.

## See

<!-- feature: REG_GLM -->
- `op-reg-glm` — the GLM params.
<!-- /feature -->
<!-- feature: REG_OLS -->
- `op-reg-ols` — the OLS params.
<!-- /feature -->
<!-- feature: MAT_COLLINEARITY -->
- `op-mat-collinearity` — VIF and Belsley's diagnostics before a model.
<!-- /feature -->
- `regression-inference` — standard errors and intervals.
