---
name: regression-model-options
description: Model options beyond the textbook fit — ridge over a predictor subset (unpenalized), non-negative coefficients (NNLS), per-coefficient prior precision for an experiment-centred prior, the media-mix recipe, and a regression an embedder registered (params, outputs, what it declines). Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [unpenalized, bounds, nonnegative, NNLS, prior_precision, prior_mu, custom regression, extension regression, regression params, outputs, media mix]
---

# Regression model options

Reach for these when a plain fit shrinks or signs the wrong things. Choosing the model: `regression-modeling`; intervals and `vcov`: `regression-inference`.

## Which option

| Need | Option | Model |
|---|---|---|
| Ridge, but controls or dummies must stay unshrunk | `unpenalized: [names]` | OLS, `penalty: l2` |
| Slopes that cannot be negative (media spend, prices of goods) | `bounds: {nonnegative: [names]}` | OLS |
| A prior centred on an experiment for ONE coefficient, flat or weak elsewhere | `prior_precision: [..]` per coefficient | Bayesian linear |
| A curve or fit no built-in covers | a registered regression | `manifest` → `extensions.regressions` |

## `unpenalized` (ridge subset)

Skips the listed predictors on the ridge diagonal (glmnet `penalty.factor` 0); the ridge SEs read the same system. Each name must be a listed predictor, once. `l1` / `elasticnet`, no `penalty`, a GLM, a Bayesian fit or a registered model refuse it `PROCESSING_CONFIG` (predict too). Lasso and elastic-net penalty factors are not offered.

## `bounds.nonnegative` (NNLS)

Constrains the listed slopes ≥ 0 (Lawson–Hanson, R `nnls`; the intercept and other slopes stay free; weighted fits work).

- No bound binds: the fit is plain OLS, bit for bit.
- A bound binds: coefficients (held ones exactly 0), `R2`, `AdjR2` and `ResidualStdErr` stand, but `StdErrors`, `PValues`, intervals and `Vcov` entries are `null` and the result carries `PULSE_ADVISORY_CONSTRAINT_ACTIVE` (`details.suggested` = `resample: bootstrap`). A bootstrap refits the bounded model per replicate and gives intervals.
- Refused: any `penalty`, `selection`, another regression type, a robust `covariance` (`PROCESSING_REGRESSION_VCOV_UNSUPPORTED`, `reason: bounds`).
- Predict cannot know whether a bound binds: its `p_values` count is an upper bound.

## Per-coefficient prior precision

`prior_precision` takes a number (ε·I, unchanged) or an array of predictors + 1 entries, intercept FIRST like `prior_mu`: Λ₀ = diag. `0` is flat on that coefficient, a large entry pins it to its `prior_mu`. Wrong length or a negative entry: `PROCESSING_CONFIG`. A flat prior the data leave improper: `PROCESSING_REGRESSION_RANK_DEFICIENT`. An experiment-centred prior: `prior_mu` the experiment's lift on that slot, a high precision there, `0` or a small value elsewhere.

## Media-mix recipe

Adstock each channel upstream (a window that carries the lagged spend), saturate with `hill(x, half_saturation, shape)` in an attribute, then fit OLS with `bounds.nonnegative` on the channels — or a registered nonlinear model when the Hill parameters must be estimated jointly. A regression cannot read a window label from its own request: materialize the adstock first<!-- feature: capability:materialize --> (`derived-cohorts`)<!-- /feature -->.

## A registered regression

An embedder may register `REG_<NS>_<NAME>`. It appears in `pulse_manifest` under `extensions.regressions` with `params`, `output_schema` and `streamable`, and is used like a built-in: `{"type": "REG_ACME_X", "target": .., "predictors": [..], "params": {..}}`.

- `params`: only the declared keys (`PULSE_REQUEST_UNKNOWN_FIELD` otherwise); a wrong JSON type or a missing required one is `PULSE_REQUEST_PARAM_INVALID` (`details.path`, `reason`). A built-in `REG_*` takes no `params`.
- `outputs`: the model's own figures beside `coefficients` (a Hill `k`, a convergence flag), keyed by the manifest's `output_schema`, kept under every `return` preset and `--no-components`. Read them at `regressions[i].outputs.<key>`; a sweep can `rank` on one.
- A shared slot the model did not declare (`penalty`, `selection`, `vcov`, `covariance`, `save`, `conf_level`, `family`, `prior`, ..) is `PROCESSING_CONFIG` `{regression, slot}` — never silently ignored. Check `pulse_predict` first; it refuses exactly as a run does.
- `resample` always works (replicate SE, Wald z, p; `conf_level` stays refused: no interval). `selection` needs the model to declare it.
- p-values only where the manifest row's `emits_type_note` says so: otherwise `p_values` is `null` throughout<!-- feature: capability:multiplicity --> and a `multiplicity` method other than `none` is `PULSE_MULTIPLICITY_INVALID`<!-- /feature -->.
<!-- feature: ATTR_REG_PREDICT -->
- Scoring (`ATTR_REG_PREDICT`) and `save` need the model to carry a scorer; without one, `PULSE_MODEL_SCORE_INVALID` reason `no_scorer`. A saved model reloaded where its type is not registered is `PULSE_MODEL_REGRESSION_UNREGISTERED`.
<!-- /feature -->
- An undefined figure is `null`, key kept, never 0. A data failure (`PROCESSING_REGRESSION_NO_CONVERGE`, ..) fails that slot alone.

## See

- `regression-modeling`, `regression-inference`<!-- feature: REG_OLS -->, `op-reg-ols`<!-- /feature --><!-- feature: REG_BAYES_LINEAR -->, `op-reg-bayes-linear`<!-- /feature --><!-- feature: capability:multiplicity -->, `multiplicity-correction`<!-- /feature -->
