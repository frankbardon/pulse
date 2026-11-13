---
name: mmm-bayes-model
description: REG_MMM_BAYES model choices — the adstock kernels (params.adstock) and saturation curves (params.saturation) with their formulas, coefficient names, prior keys and default priors, and how response() and hill() read a Bayesian fit.
kind: design
type: guide
applies_to: process, compose, predict
requires: [REG_MMM_BAYES]
covers: [adstock kernel, saturation curve, Hill curve, logistic saturation, Michaelis-Menten, default priors, prior keys, response]
---

# Bayesian MMM: kernels, curves, priors

`REG_MMM_BAYES` (`op-reg-mmm-bayes`) adstocks raw spend with one kernel and saturates it with one curve, the same for every channel. Every parameter below is sampled per channel, so each interval carries its uncertainty.

## Adstock kernel (`params.adstock`)

| Kernel | Weight at lag l (unnormalised) | Coefficients | Default priors |
|---|---|---|---|
| `geometric` (default) | decay^l | `decay:<ch>` | decay Beta(1, 1) |
| `delayed` | decay^((l − peak)²) | `decay:`, `peak:<ch>` | decay Beta(1, 1), peak TruncNormal(1, 2, 0, max_lag) |
| `weibull_cdf` | exp(−(l/scale)^shape) | `shape:`, `scale:<ch>` | shape LogNormal(0, 0.5), scale LogNormal(1, 0.75) in lags |
| `weibull_pdf` | the Weibull density at l + 1 | `shape:`, `scale:<ch>` | as `weibull_cdf` |

Every kernel but `geometric` requires `params.max_lag`. A peak prior lies inside `[0, max_lag]` (a beta, or a truncnormal bounded there).

## Saturation curve (`params.saturation`)

Each curve runs on the channel's adstock `a`, is 0 at zero spend and rises to 1, so the channel's coefficient is its effect at full saturation.

| Curve | h(a) | Coefficients | Default priors |
|---|---|---|---|
| `hill` (default) | 1 / (1 + (k/a)^s) | `k:<ch>`, `s:<ch>` | k TruncNormal(1, 1, 0.02, 10), s LogNormal(0.3, 0.5) |
| `logistic` | (1 − e^(−λa)) / (1 + e^(−λa)) | `lam:<ch>` | λ LogNormal(0, 1) |
| `michaelis_menten` | a / (k + a) | `k:<ch>` | k as Hill's |

`k` is a half-saturation; the logistic is half-saturated at log 3 / λ. Coefficients are in raw adstock units (`lam:` per raw unit); priors are stated on the adstock scaled by the channel's peak spend (1 = one peak week). Pick `hill` when the response may be S-shaped (s > 1); `logistic` or `michaelis_menten` for a concave curve with one parameter, so fewer trade-offs and no S-shape. Michaelis–Menten is Hill at s = 1 and saturates slowly; the logistic saturates within a few half-saturations. A curve the data never bend (spend far below half-saturation) leaves its parameter weakly identified: read the ROI, not k or λ.

## Priors

`priors.<channel>` takes `roi` or `beta` (β mode) plus the kernel's and the curve's own keys (`decay`, `peak`, `shape`, `scale`; `k`, `s`, `lam`). Another kernel's or curve's key is `PROCESSING_CONFIG` reason `prior_not_in_kernel` / `prior_not_in_curve`. Each prior is `{family, …}`: normal (mu, sigma), halfnormal (sigma), lognormal (mu, sigma), beta (alpha, beta), truncnormal (mu, sigma, lower, upper). Other defaults: ROI LogNormal(0.2, 0.9), `beta` HalfNormal(1); intercept Normal(0, 2), controls Normal(0, 1) and σ HalfNormal(1), all on the max-scaled outcome.

A predictor starting with the chosen kernel's or curve's prefix (or named `(intercept)` / `sigma`) collides with a coefficient and is refused.

## Reading a fit downstream

- `response(fit, channel, spend)` evaluates the fit's own curve at its posterior means, on the adstock record's steady state (`mmm-workflow`). The fit carries its curve; an inline model declares a non-Hill one in a `saturation` section.
- `hill(x, k, s)` is the Hill formula only: a logistic or Michaelis–Menten fit is read through `response()`.
- mROI is the analytic derivative of the contribution under a uniform scaling of spend, holding the curve fixed.

## See

`op-reg-mmm-bayes` · `mmm-workflow` · `compose-optimize` · `regression-model-options`
