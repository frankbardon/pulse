---
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-specific link functions. Always buffered.
kind: operator
category: REG
operator: REG_GLM
type: reference
applies_to: process, compose, predict
examples_tags: [regression, glm, logistic, buffered-pipeline]
---

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `family` | enum | required | `binomial`, `poisson`, `gamma`. |
| `link` | enum | family default | `binomial→logit`, `poisson→log`, `gamma→inverse`. `identity`, `probit`, `cloglog`, `sqrt` reserved. |
| `max_iters` / `tol` | int / float | engine | IRLS caps. |
| `weight` | slot weight | inherited | `null` opts out; both kinds. IRLS prior weights w* (R `glm(weights = w*)`); SEs, deviances on w*. |
| `vcov` | bool | `false` | Adds `Vcov` = (XᵀWX)⁻¹ at the converged weights (dispersion 1) + `Correlation`; √diag = `StdErrors`. Modifiers → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. |

`resample` / `selection` are top-level modifiers — see `op-reg-mod-resample`, `op-reg-mod-selection`.

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, plus nullable variants. Binomial targets accept `{0,1}` or `packed_bool`. Nullable rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs (link scale); `StdErrors`, `PValues` (Wald-z from `Cov(β) = (XᵀWX)⁻¹`); `Deviance`, `NullDeviance`, `PseudoR2` = 1 − D/D₀ (McFadden only for 0/1 binomial); `Family`, `Link` echoed; `ConvergedIters` = IRLS steps; `NObs` raw rows, weighted adds `SumWeights` (+ `NEff`, probability). Always buffered — IRLS needs multiple passes.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted: `resample` / `selection` → `PULSE_WEIGHT_UNSUPPORTED`; n_eff < p+1 → `PULSE_WEIGHT_LOW_NEFF`.
- `penalty` / `alpha` / `l1_ratio` rejected → `PROCESSING_CONFIG`. Regularized GLM is a later phase.
- Binomial separation diverges → `PROCESSING_REGRESSION_NO_CONVERGE`. Raise `max_iters`, drop the offender, or pre-bin.
- Unsupported link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Dispersion fixed at 1 for binomial / poisson, and gamma (weighted too) inherits it — be sceptical of gamma SEs and overdispersed counts.
- Collinearity → `PROCESSING_REGRESSION_RANK_DEFICIENT`; drop a predictor.

## See

- `pulse_examples_search tags=[glm]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-bayes-linear`, `op-reg-mod-resample`, `op-reg-mod-selection`
