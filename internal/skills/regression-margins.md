---
name: regression-margins
description: Marginal effects on a regression — average marginal effects (ame) and marginal effects at the mean (mem) on least-squares, generalized linear, ordinal and multinomial fits (per level), response or link scale, delta-method standard errors that follow the fit's covariance (classical, HC, cluster, HAC), execution and the refusals. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [margins, marginal effects, average marginal effect, AME, MEM, dydx, slopes, avg_slopes, delta method, regressions, per-level effects]
---

# Regression marginal effects

A GLM coefficient is on the link scale (log-odds, log counts). `margins` reports how much the expected outcome moves per unit of a predictor, in outcome units (probability points, counts), with a standard error (Stata `margins, dydx(*)`; R `marginaleffects::avg_slopes`).

<!-- feature: REG_GLM -->
```json cohort=subscribers
{"cohort": {"filename": "subscribers.pulse"},
 "regressions": [{"type": "REG_GLM", "family": "binomial", "target": "churned",
  "predictors": ["late_payment"], "margins": {}}]}
```
<!-- /feature -->

`"margins": {}` = every predictor, `ame`, response scale. Absent = no block, byte-identical.

## Knobs

| Key | Values | Default | Meaning |
|---|---|---|---|
| `type` | `ame`, `mem` | `ame` | `ame`: the mean over the fitted rows of each row's effect β_j·μ′(η_i). `mem`: the effect at the predictor means, β_j·μ′(x̄ᵀβ); the point is echoed as `at`. |
| `terms` | predictors | all | Which predictors to report, each a listed predictor, once. |
| `scale` | `response`, `link` | `response` | `response`: ∂μ/∂x. `link`: ∂η/∂x, which is the coefficient itself. |
| `binary` | `slope`, `contrast` | `slope` | `contrast`: a predictor that is 0/1 on every fitted row reports μ(x=1) − μ(x=0) instead, averaged like `type`. |

`ame` describes these rows, `mem` one synthetic "average" row (Stata's `atmeans`). On OLS and any identity link every effect is the coefficient, with its SE, p and interval.

## Output

`regressions[i].margins`: `type`, `scale`, `terms` (order), then maps keyed by term — `estimates`, `std_errors`, `statistics`, `p_values`, `ci_lower`, `ci_upper` — plus `conf_level`, `df` (only for a Student t), `at` (`mem`), `inference` (`resample` only), and under `binary: contrast` the echo `binary` and `contrasts` (0/1 terms). A per-level fit adds `outcomes` (the levels, in order) and keys every map `"<level>:<term>"`. Rides the `standard` preset.

<!-- feature: REG_ORDINAL -->
## Per-level effects (`REG_ORDINAL`)

One effect per level and term on P(Y = level): −β_j·[f(ζ_k − η) − f(ζ_{k−1} − η)], averaged (`ame`) or at the means (`mem`). A term's effects sum to 0 over the levels — probability moves between them, so a middle level can go either way. `scale: link` is the effect on each cumulative link P(Y ≤ level): −β_j for every level but the last, with the coefficient's SE. Delta method on the coefficient and threshold covariance (classical, `hc0`, `hc1`, `cluster`); Wald z.
<!-- /feature -->
<!-- feature: REG_ORDINAL, capability:multiplicity -->
A `multiplicity` block corrects all levels × terms as one family.
<!-- /feature -->

<!-- feature: REG_MULTINOMIAL -->
## Per-category effects (`REG_MULTINOMIAL`)

One effect per fitted category and term on P(Y = category): π_k·(β_kj − Σ_m π_m·β_mj), β of the reference 0, averaged (`ame`) or at the means (`mem`); they sum to 0 over the categories, and the sign can differ from the coefficient's (which compares with the reference only). `scale: link` is each non-reference category's log-odds against the reference — the coefficient, with its SE. Delta method on the fit's covariance (classical, `hc0`, `hc1`, `cluster`); Wald z. For "does x matter at all", read `term_tests` (`op-reg-multinomial`), not one category's effect.
<!-- /feature -->

## Inference

- **Delta method** on the fit's own coefficient covariance: Var = J·V·Jᵀ with J_jk = δ_jk·m̄ + β_j·mean(μ″(η_i)·x_ik) (ame). V is whatever `std_errors` used, so a robust `covariance` (`hc0`–`hc3`, `cluster`, `hac`) carries through.
- **Reference distribution** = the coefficient table's: Student t on `df` for OLS and classical gamma, Wald z otherwise (binomial, poisson, negative binomial, any robust GLM). Intervals at the fit's `conf_level`.
- **Resample**: every replicate refit re-estimates the effects (over its own rows and means; the full fit's `contrasts`). Their replicate spread is the SE, p the Wald z (jackknife) or percentile (bootstrap), intervals normal, no `df`, `inference: "resample"` — the resampled coefficient table's rules; estimates stay the full fit's.
<!-- feature: capability:weighting -->
- **Weights**: the average is weighted by the fit's row weight (marginaleffects `wts`); `mem`'s point is the weighted means. Frequency weights equal the `rep()` expansion.
<!-- /feature -->
<!-- feature: capability:multiplicity -->
- **Multiplicity**: with the regression's own `multiplicity` block the effects form their own family per regression (`margins.p_adjusted` / `significant_adjusted`, echo `margins.multiplicity` family `margins`); the coefficients' `p_adjusted` never move.
<!-- /feature -->

## Execution

OLS streams with the fit (the effect is β); a GLM adds one pass over the fitted rows after IRLS.

## Refusals

`PROCESSING_CONFIG`, `details.requires`, the same at predict and run:

| Cause | `requires` |
|---|---|
| bad `type` / `scale` / `binary` | `type ame or mem` / `scale response or link` / `binary slope or contrast` |
| a term not a predictor, or repeated | `a predictor` |
<!-- feature: REG_OLS, REG_GLM, REG_BAYES_LINEAR, REG_MULTINOMIAL -->
| `REG_BAYES_LINEAR` | `type REG_OLS, REG_GLM or REG_MULTINOMIAL` |
<!-- /feature -->
| an extension or model regression not declaring margins | `declared_margins` |
| `penalty` / `selection` / `bounds` | `unpenalized` / `no_selection` / `unbounded` |
| a predictor derived from another predictor of the same fit (feature / attribute / window inputs, transitively) | `independent_predictors` |

A derived predictor (a log column, a one-hot dummy) is differentiated as given. One built from another predictor (x² beside x, an interaction beside its factors) is refused: holding the other fixed is not defined.

## Against R

Pinned to analytic closed forms in R 4.6.1 at 1e-9 and to `marginaleffects` 1.0.0 (`avg_slopes`, `slopes(newdata = "mean")`; on `polr` / `multinom` per level) within its numerical-derivative accuracy (~1e-8): oracle `margins.json`; resampled, base-R replicate loops at 1e-7 (`margins_resample.json`).

## See

<!-- feature: REG_GLM -->
- `op-reg-glm` — the GLM params.
<!-- /feature -->
<!-- feature: REG_OLS -->
- `op-reg-ols` — the OLS params.
<!-- /feature -->
<!-- feature: REG_ORDINAL -->
- `op-reg-ordinal` — the ordinal params.
<!-- /feature -->
<!-- feature: REG_MULTINOMIAL -->
- `op-reg-multinomial` — the multinomial params, `term_tests`.
<!-- /feature -->
- `regression-inference` — standard errors, robust covariance, intervals.
<!-- feature: capability:multiplicity -->
- `multiplicity-correction` — families and methods.
<!-- /feature -->
