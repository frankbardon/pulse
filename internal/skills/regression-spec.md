---
name: regression-spec
description: The regression entry's shared contract — flat top-level slot keys, knobs another engine owns, the penalty objective and its missing inference, weighted fits per engine, conf_level, save and name, and the GLM's dispersion, IRLS and pseudo-R² details. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [RegressionSpec, slot keys, penalty, alpha, l1_ratio, conf_level, weight, save, name, dispersion, IRLS, PseudoR2, ConvergedIters]
---

# Regression spec

What every built-in fit shares. Per-model params and outputs: atomic `op-reg-*`; choosing a model: `regression-modeling`; intervals, `vcov`, robust `covariance`: `regression-inference`; `unpenalized`, `bounds`, per-coefficient priors: `regression-model-options`; the resample knobs: the resample modifier's atomic skill.

## Slot keys

Every param is a FLAT top-level key of the regression entry (manifest `slot: true`); a `params` key on a built-in `REG_*` is `PULSE_REQUEST_UNKNOWN_FIELD` (only a registered regression takes `params`).

Knobs belong to their engine; another engine's knob is `PROCESSING_CONFIG`, at predict and at runtime alike:

- a `penalty` on a gamma GLM, or `alpha` / `l1_ratio` without a `penalty`;
- `penalty` / `alpha` / `l1_ratio` / `unpenalized` / `family` / `link` on the Bayesian fit;
- `resample` / `selection` on the Bayesian fit (not advertised; credible intervals already convey uncertainty).

Shared keys:

- `conf_level` — `CILower` / `CIUpper` coverage, `0<c<1`, default 0.95 (`regression-inference`). The Bayesian fit's `credible_level` sets `CredibleIntervals`; the two intervals are equal when `conf_level` is unset.
- `name` — the result alias, and the saved model's name (default: the `save` file's stem).
<!-- feature: capability:model_save -->
- `save` — writes the finished fit as a model file (`.pulse-model.json`, `compose-fits`).
<!-- /feature -->
<!-- feature: capability:multiplicity -->
- `multiplicity` `{method, family, alpha, coefficients}` (OLS, GLM) — the member coefficients' p-values join a `request` / `compose` family; adds `PAdjusted` / `SignificantAdjusted` + an echo beside the raw `PValues`, which never move (`multiplicity-correction`). The Bayesian fit has no p-values: any method but `none` is `PULSE_MULTIPLICITY_INVALID`, predict too.
<!-- /feature -->

## Penalty

`penalty` `l2` / `l1` / `elasticnet` (OLS; GLM binomial / poisson) uses glmnet's `standardize=FALSE` objective: raw predictor units, intercept unpenalized. The GLM minimizes −loglik/N* + `alpha`·[(1−`l1_ratio`)/2‖β‖² + `l1_ratio`‖β‖₁], N* = Σw.

- `alpha` — strength, `> 0` (glmnet `lambda`), required with a `penalty`. `l1_ratio` — the mix, `0<r<1`, `elasticnet` only.
- Raw units: divide each predictor by its SD upstream to penalize them all alike.
- `l2` SEs are the ridge sandwich (GLM: H = XᵀWX + N*·α·D).
- `l1` / `elasticnet` have no analytical inference (the data pick the predictors): `StdErrors` / `PValues` / CIs are null on EVERY key and `Advisories` carries `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED` — an advisory, not a warning, never Strict-escalated. `vcov` is `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. Want uncertainty → `resample` (it refits every replicate penalized and silences the advisory); want p-values → `l2` or no penalty.
- A penalty with `selection`: predict warns `PROCESSING_REGRESSION_REGULARIZED_SELECTION` — the penalty already shrinks or selects.

## OLS solver and intercept

- `solver`: `qr` (default) solves a streaming QR of the design at κ(X); `normal` solves the normal equations at κ(X)² (pre-alpha.35 bits). Unpenalized, unbounded fits only.
- `intercept: false` fits through the origin: `(intercept)` stays 0 with null SE / p / CI, R² is uncentred Σŷ²/Σy², `DF` = N* − p (R `lm(y ~ 0 + x)`). Refused with a penalty, bounds, a modifier, a robust `covariance` or `normal`.

## Weighted fits

`weight: null` opts a slot out<!-- feature: capability:weighting --> (`weighting`)<!-- /feature -->. `NObs` stays the raw row count; a weighted fit adds `SumWeights` (+ `NEff` under a probability weight).

| Fit | Kinds | What the weight does |
|---|---|---|
| OLS | frequency, probability | WLS β; df = N* − p − 1, N* = Σw (frequency) or Kish n_eff (probability) |
| GLM | frequency, probability | IRLS prior weights w* (R `glm(weights = w*)`); SEs and deviances on w*; a robust `covariance` follows w* |
| Bayesian linear | frequency only | the expanded rows: XᵀWX, XᵀWy and Σw enter the posterior |

- `resample: bootstrap` or `selection` under a weight → `PULSE_WEIGHT_UNSUPPORTED`; a jackknife is weighted (JK1, each replicate keeps its rows' weights).
- n_eff ≤ p + 1 (OLS, gamma GLM) or < p + 1 (binomial / poisson GLM) → `PULSE_WEIGHT_LOW_NEFF`.
- OLS residual df ≤ 0 makes `StdErrors`, `PValues`, `CILower` / `CIUpper`, `DF`, `AdjR2` and `ResidualStdErr` null.

## GLM details

- Dispersion φ = 1 for binomial / poisson: Wald z, `CILower` / `CIUpper` = β ∓ z·SE (R `confint.default`, not the profile `confint`), no `DF`. Overdispersed counts therefore get too-small SEs — use a robust `covariance` (dispersion-free) or a resample.
- Gamma estimates φ̂ = Pearson χ²/`DF`, `DF` = N* − p − 1 (N* = n, Σw or n_eff): t p-values and β ∓ t·SE (R `summary.glm`); φ̂ itself is not emitted; `DF` ≤ 0 nulls SE / p / CI / `DF`. Gamma `selection` scores R's `AIC(glm)` / `BIC(glm)` (φ = D/n counted), not D + 2k.
- `PseudoR2` = 1 − D/D₀ (McFadden only for a 0/1 binomial target); `Deviance`, `NullDeviance` beside it.
- `ConvergedIters` counts IRLS steps (R's per-row start, step-halving). Non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE` (`details.reason`): raise `max_iters`, loosen `tol` or rescale.
- `link` per family, canonical default first: `binomial` `logit` / `probit` / `cloglog`; `poisson` `log` / `identity` / `sqrt`; `gamma` `inverse` / `log` / `identity`. Another family's link → `PROCESSING_REGRESSION_INVALID_LINK`. Every link's SEs read the expected information φ·(XᵀWX)⁻¹, W = μ′(η)²/V(μ) (R `summary.glm`); off the canonical link IRLS converges linearly (β ~1e-7 relative of the MLE at the default `tol`).
- Binomial separation: the MLE is infinite, so no coefficients. Check the separating predictor is not recorded after the outcome; a strong but unseparated predictor converges.
- Always buffered: IRLS needs several passes.

## See

- `regression-modeling`, `regression-inference`, `regression-model-options`, `op-reg-mod-resample`, `op-reg-mod-selection`<!-- feature: capability:weighting -->, `weighting`<!-- /feature --><!-- feature: capability:multiplicity -->, `multiplicity-correction`<!-- /feature -->
