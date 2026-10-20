---
name: regression-modeling
description: Choosing and composing a regression — outcome type, priors, penalties, the resampling and stepwise modifiers, polynomial terms built upstream, and how textbook regression names map to specs. Topical design; per-model detail in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [REG, REG_OLS, REG_GLM, REG_BAYES_LINEAR, Resample, Selection, FEAT_POLY, regressions, modifiers]
---

# Regression modeling

Model operators, two spec-level modifiers and an upstream feature transform cover the textbook regression names. Per-model params, outputs, inference and errors: atomic `op-reg-*` skills.

No `Response.Components`; fits ride `Response.Regressions[i]`.

## Choosing a model

Start from the question: `pulse_skills_get intents` → `drivers` (which predictors move the outcome, and by how much) or `relationship` (do two fields move together). In `pulse_manifest`, the `regressions[]` entries carrying that intent are the models this instance offers; each states `streamable`. Each model's guidance names its alternatives — a correlation or group-mean test often answers without a model.

| Decide | Pushes toward |
|---|---|
| Outcome numeric with roughly symmetric, constant-spread residuals? | the least-squares (OLS) fit |
| Outcome yes/no (0/1) or a count? | the generalised linear (GLM) fit with a `binomial` / `poisson` family |
| Prior knowledge about the coefficients, or want credible intervals? | the Bayesian linear fit (no p-values) |
| Many correlated predictors? | a penalty (`l2` keeps all, `l1` / `elasticnet` zero some) |
| Distrust the analytical SE? | the `Resample` modifier |
| Unsure which predictors belong? | the `Selection` modifier — or a penalty, never both |
| A curved relationship? | polynomial columns built upstream, then a linear fit |

Target / predictor types follow `encoding.FieldType.IsNumericForAnalytics` (integer / float / decimal plus `u4`, `packed_bool`, `date`); a null drops the row from the observation count.

Streaming: the OLS and Bayesian linear fits share one Welford-Pébaÿ accumulator (`n, μ_x, μ_y, M2_xx, M2_xy, M2_yy`) and stream; the GLM's iteratively reweighted fit buffers. Either modifier forces buffered execution.

## Modifiers

Both compose with the OLS and GLM fits; the Bayesian fit rejects both — credible intervals already convey uncertainty, and stepwise search on a conjugate fit is out of scope.

### `Resample` (jackknife / bootstrap)

| Value | Behavior |
|---|---|
| `""` | closed-form / asymptotic SE |
| `"jackknife"` | LOO, or `jackknife_groups` / `jackknife_by` groups; SE = √((g−1)/g·Σ(β₍ₖ₎−β̄)²) |
| `"bootstrap"` | non-parametric (`bootstrap_iters` as given, `<= 0` → 1000; `rng_seed`); SE = sample std of replicates; percentile-method p; echoes `bootstrap_requested` / `bootstrap_succeeded` |

Overwrites `StdErrors` / `PValues`; the point estimate is the full-data fit's. For `l1` / `elasticnet` (null SE / p otherwise), `Resample` supplies replicate SEs and silences `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`.

### `Selection` (stepwise)

| Value | Behavior |
|---|---|
| `""` | fit on all predictors |
| `"forward"` | start intercept-only; add the predictor that lowers `Criterion` most |
| `"backward"` | start full; remove the predictor whose absence lowers `Criterion` most |
| `"stepwise"` | bidirectional sweep |

`Criterion ∈ {aic, bic}` required. BIC rejects noise predictors better at moderate `n`. `SelectedFeatures` lists chosen predictors; non-selected ones are DROPPED from `Coefficients` — absent ≠ zero.

`Selection` + `Resample` is sane. `Penalty != ""` + `Selection != ""` emits `PROCESSING_REGRESSION_REGULARIZED_SELECTION` — regularization already shrinks / selects.

## Textbook names → spec

| Name | Spec |
|---|---|
<!-- feature: REG_OLS -->
| Simple / Linear / Multiple | `REG_OLS`, one or more predictors |
| Ridge | `REG_OLS{Penalty:"l2", Alpha:λ}` |
| Lasso | `REG_OLS{Penalty:"l1", Alpha:λ}` |
| Elastic Net | `REG_OLS{Penalty:"elasticnet", Alpha, L1Ratio}` |
<!-- /feature -->
<!-- feature: REG_GLM -->
| Logistic (ridge / lasso) | `REG_GLM{Family:"binomial", Link:"logit"}` (+ `Penalty`, `Alpha`) |
<!-- /feature -->
<!-- feature: FEAT_POLY, REG_OLS -->
| Polynomial | `FEAT_POLY` upstream → `REG_OLS` |
<!-- /feature -->
<!-- feature: REG_BAYES_LINEAR -->
| Bayesian Linear | `REG_BAYES_LINEAR{Prior:"nig"}` |
<!-- /feature -->
| Jackknife | an OLS / GLM spec with `Resample:"jackknife"` |
| Stepwise | an OLS / GLM spec with `Selection:"stepwise", Criterion:"aic"\|"bic"` |
<!-- feature: AGG_AVERAGE, REG_OLS -->
| Ecological | a grouper + `AGG_AVERAGE` upstream → `REG_OLS` over per-group means (composed) |
<!-- /feature -->

Runnable JSON: `internal/examples/regression/`.

## Polynomial terms

Built in `features`, before the fit: `Degree − 1` derived columns (`<label>_2 … <label>_<Degree>`) beside the original, degree `[2, 10]`. Standardize first — `x^10` overflows `f64` past `|x|` ≈ a few hundred. Table: `feature-engineering`.

## Ecological caveat

A significant group-level slope does NOT imply individual-level association (Robinson 1950, Simpson). Use them only for group-level questions; Pulse cannot enforce this.

## Inference

Per model (atomic skill): Student-t p for unpenalized / `l2` OLS, null SE / p / CI for `l1` / `elasticnet` (advisory `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` unless `Resample`), Wald-z p for the GLM (penalized: a sandwich), credible intervals and no `p_values` for the Bayesian fit. Every SE gets `ci_lower` / `ci_upper` at `conf_level` (0.95).<!-- feature: capability:multiplicity --> A spec's own `multiplicity` block joins its coefficients' p-values to a correction family (`multiplicity-correction`).<!-- /feature -->

## Gotchas

- `n_obs` < record count: nulls drop rows.
- A fit failing on data fails ALONE: `{name, type, error}` + `PULSE_SLOT_FAILED` (`cohort-null-states`). Codes: `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM` (collinear; GLM names them) — drop one or regularize; `_SEPARATION` (binomial GLM: a predictor splits the outcome) — add `penalty: l2`, or drop or merge it; `_NO_CONVERGE` — raise `MaxIters` / `Tol` or cut `Alpha`.
- Knobs belong to their engine: a gamma GLM penalty, or `Penalty` / `Family` / `Link` on the Bayesian fit, is `PROCESSING_CONFIG`.

## See

- Recipes: `pulse_examples_search tags=["regression"]` plus atomic `op-reg-<name>`.
- `feature-engineering` — polynomial parameter table + column naming.
- `statistical-testing` — Wald-z vs Student-t.
- `request-envelope` — slot keys, streamability.
- `regression-inference` — vcov
- `pulse_errors_lookup` — `PROCESSING_REGRESSION_*` recovery.
