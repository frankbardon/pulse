---
name: regression-influence
description: Leverage, Cook's distance, DFFITS and studentized residuals over a BOUND fit (params.fit) — read off the fit's vcov and dispersion, the least-squares / GLM hat matrix and a nonlinear media-mix fit's tangent plane, which fits qualify, their refusals and why a posterior has none. Topical design; the refit and the per-attribute params live in regression-row-scores and the atomic op-attr-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [leverage, influence, hat matrix, tangent plane, Cook's distance, DFFITS, studentized residual, GLM leverage, dispersion, params.fit, bound fit, Gauss-Newton, nonlinear least squares]
---

# Influence over a bound fit

A leverage, influence or standardized / studentized residual slot with `params.fit` reads the bound fit's design — its `vcov`, `dispersion` and residual df — never its rows, so one fit serves any scoring frame. The fit's own rows reproduce R 4.6.1's `hatvalues` / `rstandard` / `rstudent` / `cooks.distance` / `dffits`; any other row takes the same formulas.

## Least squares and GLM

x̃ = [1, x], μ the fitted mean, w = (dμ/dη)²/V(μ) (1 least-squares), rP the Pearson residual, φ the dispersion (1 binomial / poisson, else the fit's `dispersion`), p′ coefficients, df = n_obs − p′:

- h = w·x̃ᵀ(vcov/φ)x̃; standardized rP/(√φ·√(1 − h)) (`residual_type: deviance`: the deviance residual in place of rP, R `rstandard(type = "deviance")`; equal on a Gaussian fit, refused beside any other kind); cooks (rP/(1 − h))²·h/(φ·p′).
- studentized rP/(σ₍ᵢ₎·√(1 − h)), σ₍ᵢ₎² = (φ·df − rP²/(1 − h))/(df − 1) where φ is estimated; binomial / poisson: the standardized.
- dffits sign(g′(μ))·rP·√h/(σ·(1 − h)), σ = σ₍ᵢ₎ or √φ — the change on the link scale, so under the gamma inverse link it is NEGATIVE where rP is positive.

<!-- feature: REG_MMM_HILL -->
## A nonlinear media-mix fit

`REG_MMM_HILL` is a nonlinear least squares: its design row is the tangent g = ∂ŷ/∂θ at the optimum over every FREE parameter (β, the curve's k and s, an in-model adstock's kernel parameters; one held on a bound joins nothing), so h = gᵀ(vcov/σ̂²)g — the Gauss–Newton hat value R reads off an `nls` gradient. σ̂² = SSR/df is the fit's `dispersion` (with `vcov: true`), p′ the free count, df the fit's residual df; then the least-squares formulas above (Gaussian: both residual types are equal). On its own rows Σ h = p′; with every curve held it is the linear model's leverage.

- `params.prior` is a penalty (vcov σ²·(JᵀJ + P)⁻¹): `requires: unpenalized`.
- Over an in-model adstock the slot carries it like the fitted value (`carryover` / `order`, `regression-order`): the kernel parameters' partials read the carried lags. `carryover: false` reads the channel as given (its kernel partials are 0).
<!-- /feature -->

## Which fits qualify

- `vcov: true`, the classical covariance, no penalty, selection, resample, bounds or weight, else `PULSE_MODEL_SCORE_INVALID` `params_invalid` (`details.requires` names the missing face). Weighted fits are not supported yet: refit with `weight: null`.
- A model file without `dispersion` (an earlier build's) needs a re-save.
- A Bayesian-linear or registered fit is `type_unsupported`.<!-- feature: REG_MMM_BAYES --> So is `REG_MMM_BAYES`, by design: hat-matrix leverage and Cook's distance belong to a point estimate's (XᵀWX)⁻¹ and a posterior has none — its influence analogue, PSIS-LOO's Pareto k, is a different statistic. Read its rows with the fitted value and residual instead.<!-- /feature -->

## See

- `regression-row-scores`, `regression-order`, `regression-modeling`
