```yaml
name: op-reg-ols
description: Ordinary least squares with optional l1/l2/elasticnet penalty; covers simple, multiple, ridge, lasso, and elastic-net regression over streaming sufficient statistics.
kind: operator
category: REG
operator: REG_OLS
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, streaming-friendly]
```

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

## Use when

Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares.

Questions it answers:

- Which of price, discount and season are associated with weekly sales, holding the others fixed?
- How much does predicted order value change per extra visit, with the other predictors held fixed?

Use something else:

- `REG_GLM` when the outcome is yes/no (0/1) or a count.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | `0` | Strength; >0 when `penalty` set. |
| `l1_ratio` | float | `0` | Elastic-net mix in `[0,1]`. |
| `max_iters` / `tol` | int / float | engine | Coordinate-descent caps for regularized fits. |
| `weight` | slot weight | inherited | `null` opts out; both kinds. WLS β (penalty × Σw: kind-free); SE, df = N*−p−1 (fractional), `AdjR2` on N* = Σw (frequency) / Kish n_eff (probability). |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation` (`MatrixValues`, keys `(intercept)` + predictors); √diag = `StdErrors`. Unpenalized σ̂²(XᵀX)⁻¹ / ridge sandwich, N* basis. `l1` / `elasticnet` / modifiers → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. |

`resample` / `selection` are top-level modifiers — see [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md).

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, plus nullable variants. Nullable rows drop from `n_obs` via the per-record bitmap.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `R2`, `AdjR2`, `ResidualStdErr`, `NObs` (raw rows); weighted adds `SumWeights` (+ `NEff`, probability). Penalized shrunk-to-zero βs drop from `StdErrors`. Streams Welford-Pébaÿ sufficient stats; the regularized solve runs once at finalize over the p×p Gram.

## Reading the output

- `coefficients.*`: The difference in the outcome's expected value between rows one unit apart on this predictor, holding the other predictors fixed, in outcome units per predictor unit (an association, not the effect of changing it).
- `std_errors.*`: How much the coefficient would vary from sample to sample; smaller means a more precise estimate. Pulse reports the classic standard error, which assumes residuals of constant spread.
- `r2`: R-squared: the share of the outcome's variation around its mean that the fitted model accounts for in these rows, from 0 to 1.

## Gotchas

- Weighted: `resample` / `selection` → `PULSE_WEIGHT_UNSUPPORTED`; n_eff ≤ p+1 → `PULSE_WEIGHT_LOW_NEFF`; residual df ≤ 0 makes SEs, p, `AdjR2`, `ResidualStdErr` null.
- `l1` / `elasticnet` SE is plug-in over the active set — pair with [`op-reg-mod-resample`](op-reg-mod-resample.md); otherwise `PROCESSING_REGRESSION_APPROXIMATE_SE` warns.
- Collinearity → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`; drop a predictor or add `l2`.
- `penalty != ""` + `selection != ""` → `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Polynomial: stage `FEAT_POLY` polynomial columns upstream; degree gate `[2,10]`; standardize first.
- Per-row residual / fitted / leverage live in `ATTR_REG_*` — separate slot, separate prepass.

## See

- `pulse_examples_search tags=[ols]`
- Skills: [`regression-modeling`](regression-modeling.md), [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md), [`op-reg-glm`](op-reg-glm.md), [`op-reg-bayes-linear`](op-reg-bayes-linear.md)
