---
name: regression-modeling
description: Choosing and composing a regression — outcome type, priors, penalties, the resampling and stepwise modifiers, polynomial terms built upstream, and how textbook regression names map to specs. Topical design; per-model detail in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [REG, REG_OLS, REG_GLM, REG_BAYES_LINEAR, REG_MULTINOMIAL, REG_ORDINAL, Resample, Selection, FEAT_POLY, regressions, modifiers, custom regression]
---

# Regression modeling

Model operators, two spec-level modifiers and an upstream feature transform cover the textbook names.

No `Response.Components`; fits ride `Response.Regressions[i]`.

## Choosing a model

Start from the question: `pulse_skills_get intents` → `drivers` (which predictors move the outcome, how much) or `relationship` (do two fields move together). In `pulse_manifest`, the `regressions[]` entries carrying that intent are this instance's models, each stating `streamable`. Each model's guidance names its alternatives (a correlation or group-mean test may suffice).

| Decide | Pushes toward |
|---|---|
| Outcome numeric with roughly symmetric, constant-spread residuals? | the least-squares (OLS) fit |
| Outcome yes/no (0/1) or a count? | the generalised linear (GLM) fit with a `binomial` / `poisson` family (over-dispersed: `negative_binomial`) |
| A category of 3+ levels? | ordered: the ordinal fit; unordered: the multinomial logit |
| Prior knowledge about the coefficients, or want credible intervals? | the Bayesian linear fit (no p-values) |
| Many correlated predictors? | a penalty (`l2` keeps all, `l1` / `elasticnet` zero some) |
| Distrust the analytical SE? | the `resample` modifier |
| Unsure which predictors belong? | the `selection` modifier — or a penalty, never both |
| A curved relationship? | polynomial columns built upstream, then a linear fit |
| Non-negative slopes, a centred prior, a partial ridge, an embedder's model? | `regression-model-options` |

Target / predictor types: `IsNumericForAnalytics` (each model's `## Contract`); a null, NaN or ±Inf drops the row from `n_obs`.

Streaming: OLS and Bayesian linear stream; the others and either modifier buffer.

## Modifiers

Both compose with the OLS and GLM fits; the Bayesian fit rejects both (its credible intervals already convey uncertainty).

### `resample` (jackknife / bootstrap)

| Value | Behavior |
|---|---|
| `""` | closed-form / asymptotic SE |
| `"jackknife"` | LOO, or `jackknife_groups` / `jackknife_by` groups; SE = √((g−1)/g·Σ(β₍ₖ₎−β̄)²) |
| `"bootstrap"` | non-parametric (`bootstrap_iters`, `rng_seed`); SE = sample std of replicates; percentile-method p |

Overwrites `std_errors` / `p_values`; the point estimate is the full-data fit's. For `l1` / `elasticnet` or a binding `bounds`, `bootstrap` supplies the missing SEs.

### `selection` (stepwise)

| Value | Behavior |
|---|---|
| `""` | fit on all predictors |
| `"forward"` | start intercept-only; add the predictor that lowers `criterion` most |
| `"backward"` | start full; remove the predictor whose absence lowers `criterion` most |
| `"stepwise"` | bidirectional sweep |

`criterion ∈ {aic, bic}` required. `selected_features` lists chosen predictors; non-selected ones are DROPPED from `coefficients` — absent ≠ zero.

`selection` + `resample` is fine. `penalty` + `selection` emits `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.

## Textbook names → spec

| Name | Spec |
|---|---|
<!-- feature: REG_OLS -->
| Simple / Linear / Multiple | `REG_OLS`, one or more predictors |
| Ridge | `REG_OLS{penalty:"l2", alpha:λ}` |
| Lasso | `REG_OLS{penalty:"l1", alpha:λ}` |
| Elastic Net | `REG_OLS{penalty:"elasticnet", alpha, l1_ratio}` |
| Non-negative (NNLS) | `REG_OLS{bounds:{nonnegative:[…]}}` |
<!-- /feature -->
<!-- feature: REG_GLM -->
| Logistic / probit (ridge / lasso) | `REG_GLM{family:"binomial", link:"logit"\|"probit"}` (+ `penalty`, `alpha`) |
| Negative binomial | `REG_GLM{family:"negative_binomial"}` |
<!-- /feature -->
<!-- feature: REG_MULTINOMIAL -->
| Multinomial logistic | `REG_MULTINOMIAL` (categorical target) |
<!-- /feature -->
<!-- feature: FEAT_POLY, REG_OLS -->
| Polynomial | `FEAT_POLY` upstream → `REG_OLS` |
<!-- /feature -->
<!-- feature: REG_BAYES_LINEAR -->
| Bayesian Linear | `REG_BAYES_LINEAR{prior:"nig"}` |
<!-- /feature -->
<!-- feature: REG_ORDINAL -->
| Ordinal logit (polr) | `REG_ORDINAL{params:{link, levels}}` |
<!-- /feature -->
| Jackknife | an OLS / GLM spec with `resample:"jackknife"` |
| Stepwise | an OLS / GLM spec with `selection:"stepwise", criterion:"aic"\|"bic"` |
<!-- feature: AGG_AVERAGE, REG_OLS -->
| Ecological | a grouper + `AGG_AVERAGE` upstream → `REG_OLS` over per-group means (composed) |
<!-- /feature -->

Runnable JSON: `internal/examples/regression/`.

## Polynomial terms

Built in `features`, before the fit: `degree − 1` derived columns (`<label>_2 … <label>_<degree>`), `degree` in `[2, 10]`. Standardize first — `x^10` overflows `f64`. Table: `feature-engineering`.

## Ecological caveat

A significant group-level slope does NOT imply an individual-level association (Robinson 1950): use it for group-level questions only.

## Inference

Per model (atomic skill): Student-t p for unpenalized / `l2` OLS, null SE / p / CI for `l1` / `elasticnet` (advisory `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` unless `resample`), Wald-z p for the GLM, credible intervals and no `p_values` for the Bayesian fit. Every SE gets `ci_lower` / `ci_upper` at `conf_level` (0.95).<!-- feature: capability:multiplicity --> A spec's own `multiplicity` block joins its coefficients' p-values to a correction family (`multiplicity-correction`).<!-- /feature -->

## Gotchas

- `n_obs` < record count: missing values dropped rows.
- A fit failing on data fails ALONE: `{name, type, error}` + `PULSE_SLOT_FAILED` (`cohort-null-states`). Codes: `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM` (collinear; GLM names them) — drop one or regularize; `_SEPARATION` (binomial GLM: a predictor splits the outcome) — add `penalty: l2`; `_NO_CONVERGE` — raise `max_iters` / `tol` or cut `alpha`.
- Knobs belong to their engine: another engine's knob is `PROCESSING_CONFIG` (`regression-spec`).

## See

- Recipes: `pulse_examples_search tags=["regression"]`, atomic `op-reg-<name>`.
- `feature-engineering` — polynomial parameter table + column naming.
- `statistical-testing` — Wald z vs t.
- `regression-spec` — flat slot keys, engine knobs, penalty, weighted fits, GLM dispersion.
- `request-envelope` — streaming.
- `regression-row-scores` — per-row fitted, residual and scored columns.
- `regression-inference` — vcov
- `regression-model-options` — ridge subset, NNLS, per-coefficient prior, registered regressions
- `pulse_errors_lookup` — `PROCESSING_REGRESSION_*` recovery.
