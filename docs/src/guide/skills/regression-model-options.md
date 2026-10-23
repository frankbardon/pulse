```yaml
name: regression-model-options
description: Model options beyond the textbook fit — ridge over a predictor subset (unpenalized), non-negative coefficients (NNLS), per-coefficient prior precision for an experiment-centred prior, the media-mix recipe, and a regression an embedder registered (params, outputs, what it declines). Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [unpenalized, bounds, nonnegative, NNLS, prior_precision, prior_mu, custom regression, extension regression, regression params, outputs, media mix]
```

# Regression model options

Reach for these when a plain fit shrinks or signs the wrong things. Model choice: [`regression-modeling`](regression-modeling.md); intervals: [`regression-inference`](regression-inference.md).

## Which option

| Need | Option | Model |
|---|---|---|
| Ridge, but controls or dummies must stay unshrunk | `unpenalized: [names]` | OLS, `penalty: l2` |
| Slopes that cannot be negative (media spend, prices of goods) | `bounds: {nonnegative: [names]}` | OLS |
| A prior centred on an experiment for ONE coefficient, flat or weak elsewhere | `prior_precision: [..]` per coefficient | Bayesian linear |
| A curve or fit no built-in covers | a registered regression | manifest `extensions.regressions` |

## `unpenalized` (ridge subset)

Skips the listed predictors on the ridge diagonal (glmnet `penalty.factor` 0). Each name must be a listed predictor, once. `l1` / `elasticnet`, no `penalty`, a GLM, a Bayesian fit or a registered model refuse it `PROCESSING_CONFIG` (predict too).

## `bounds.nonnegative` (NNLS)

Constrains the listed slopes ≥ 0 (Lawson–Hanson; intercept and other slopes stay free; weighted fits work).

- No bound binds: the fit is plain OLS, bit for bit.
- A bound binds: coefficients (held ones exactly 0), `R2`, `AdjR2` and `ResidualStdErr` stand, but `StdErrors`, `PValues`, intervals and `Vcov` entries are `null` and the result carries `PULSE_ADVISORY_CONSTRAINT_ACTIVE` (`details.suggested` = `resample: bootstrap`). A bootstrap refits the bounded model per replicate and gives intervals.
- Refused: any `penalty`, `selection`, another regression type, a robust `covariance` (`PROCESSING_REGRESSION_VCOV_UNSUPPORTED`).
- Predict cannot know whether a bound binds: its `p_values` count is an upper bound.

## Per-coefficient prior precision

`prior_precision` takes a number (ε·I, unchanged) or an array of predictors + 1 entries, intercept FIRST like `prior_mu`: Λ₀ = diag. `0` is flat on that coefficient, a large entry pins it to its `prior_mu`. A wrong length or negative entry is `PROCESSING_CONFIG`; an improper flat prior `PROCESSING_REGRESSION_RANK_DEFICIENT`.

## Media-mix recipe (one request)

A regression may name a same-request window label. Adstock each channel (a window, `partition_by` market, `order_by` week), saturate with `hill(x, half, shape)` in a post-window attribute, then fit OLS with `bounds.nonnegative` on the saturated channels and a `holdout` on the last weeks — one request, no materialize. Sweep the decay with a `$var` and `rank` on `regressions.<name>.holdout.rmse` ([`compose-sweeps`](compose-sweeps.md)).

- Windows-over-records shape only (no `groups` / `aggregations` / `crosstab`): a grouped shape is `PULSE_REGRESSION_WINDOW_UNAVAILABLE` (`reason: grouped_shape`); a slot `weight` naming a window label is `SERVICE_VALIDATION`. A bootstrap does not re-run windows per replicate.
- A lag's first rows are `null`: guard a formula with `lag ?? 0` or `x == nil ? nil : …`, else `PROCESSING_RUNTIME`. Null / NaN / ±Inf rows drop listwise.

## `holdout` (out-of-sample)

`"holdout": "week >= 91"` (a bool expression) fits on the other rows and reports `holdout {n, n_null, rmse, mae, r2}` beside the fit, kept by every `return` preset.

- `n` counts scored rows; `n_null` the null-predicate rows (neither train nor held out). No held-out row: that slot fails alone, `PROCESSING_REGRESSION_INSUFFICIENT_DATA` `reason: holdout_empty`.
- Penalty, `bounds`, `selection` and `resample` fit on train rows; held-out rows are scored with the point estimate. `n` equals the RMSE aggregator's `n_scored` (zero / invalid-weight rows out); a NaN / ±Inf target row is dropped.
- A registered model needs a scorer: else `PULSE_MODEL_SCORE_INVALID` `no_scorer`. A bad predicate is `PULSE_FORMULA_COMPILE_ERROR`. Predict: buffered only.

## A registered regression

An embedder may register `REG_<NS>_<NAME>`. The manifest lists it under `extensions.regressions` (`params`, `output_schema`, `streamable`); use it like a built-in: `{"type": "REG_ACME_X", "target": .., "predictors": [..], "params": {..}}`.

- `params`: only the declared keys (`PULSE_REQUEST_UNKNOWN_FIELD` otherwise); a wrong JSON type or a missing required one is `PULSE_REQUEST_PARAM_INVALID` (`details.path`, `reason`). A built-in `REG_*` takes no `params`.
- `outputs`: the model's own figures beside `coefficients` (a Hill `k`, a convergence flag), keyed by the manifest's `output_schema`, kept under every `return` preset and `--no-components`. Read them at `regressions[i].outputs.<key>`; a sweep can `rank` on one.
- A shared slot the model did not declare (`penalty`, `selection`, `vcov`, `covariance`, `save`, `family`, `prior`, ..) is `PROCESSING_CONFIG` `{regression, slot}` — never silently ignored. Predict refuses exactly as a run does.
- `resample` and `conf_level` always work: intervals are estimate ∓ q·SE (t on the model's `df`, else normal; normal on the replicate SE under `resample`), `null` where the SE is. `selection` needs the model to declare it.
- `coef("<fit>", "<name>"[, stat])` reads any coefficient the fit reports, the model's own names included, with `lower` / `upper` at `conf_level` and `se`. An unknown name is `PULSE_MODEL_TERM_UNKNOWN`: on a loaded model at predict and run, on a `fits` entry at run only (predict cannot know the names yet). `outputs` are not coefficients — read them at `regressions[i].outputs`.
- p-values only where the manifest row's `emits_type_note` says so: otherwise `p_values` is `null` throughout and a `multiplicity` method other than `none` is `PULSE_MULTIPLICITY_INVALID`.
- Scoring (`ATTR_REG_PREDICT`) and `save` need the model to carry a scorer; without one, `PULSE_MODEL_SCORE_INVALID` reason `no_scorer`. A saved model reloaded where its type is not registered is `PULSE_MODEL_REGRESSION_UNREGISTERED`.
- An undefined figure is `null`, key kept, never 0. A data failure fails that slot alone.

## See

- [`regression-modeling`](regression-modeling.md), [`regression-inference`](regression-inference.md), [`op-reg-ols`](op-reg-ols.md), [`op-reg-bayes-linear`](op-reg-bayes-linear.md), [`multiplicity-correction`](multiplicity-correction.md)
