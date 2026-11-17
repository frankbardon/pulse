```yaml
name: regression-margins
description: Marginal effects on a regression — average marginal effects (ame) and marginal effects at the mean (mem) on least-squares, generalized linear, ordinal and multinomial fits (per level), response or link scale, delta-method standard errors that follow the fit's covariance (classical, HC, cluster, HAC), execution and the refusals. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [margins, marginal effects, average marginal effect, AME, MEM, dydx, slopes, avg_slopes, delta method, regressions, per-level effects]
```

# Regression marginal effects

A GLM coefficient is on the link scale (log-odds, log counts). `margins` reports what most readers want instead: how much the expected outcome moves per unit of a predictor, in outcome units (probability points, counts), with a standard error. Stata `margins, dydx(*)`; R `marginaleffects::avg_slopes`.

```json
{"regressions": [{"type": "REG_GLM", "family": "binomial", "target": "churned",
  "predictors": ["tenure", "spend"], "margins": {}}]}
```

`"margins": {}` = every predictor, `ame`, response scale. Absent = no block, byte-identical.

## Knobs

| Key | Values | Default | Meaning |
|---|---|---|---|
| `type` | `ame`, `mem` | `ame` | `ame`: the mean over the fitted rows of each row's effect β_j·μ′(η_i). `mem`: the effect at the predictor means, β_j·μ′(x̄ᵀβ); the point is echoed as `at`. |
| `terms` | predictors | all | Which predictors to report, each a listed predictor, once. |
| `scale` | `response`, `link` | `response` | `response`: ∂μ/∂x. `link`: ∂η/∂x, which is the coefficient itself. |

`ame` and `mem` differ under a non-identity link; `ame` describes these rows, `mem` one synthetic "average" row (Stata's `atmeans`). On an OLS fit, and on any identity link, every effect equals the coefficient with its SE, p and interval — so the block matters for a GLM on a non-identity link.

## Output

`regressions[i].margins`: `type`, `scale`, `terms` (order), then maps keyed by term — `estimates`, `std_errors`, `statistics`, `p_values`, `ci_lower`, `ci_upper` — plus `conf_level`, `df` (only for a Student t) and `at` (`mem`). A per-level fit adds `outcomes` (the levels, in order) and keys every map `"<level>:<term>"`. Rides the `standard` preset.

## Per-level effects (`REG_ORDINAL`)

One effect per level and term on P(Y = level): −β_j·[f(ζ_k − η) − f(ζ_{k−1} − η)], averaged (`ame`) or at the means (`mem`). A term's effects sum to 0 over the levels — probability moves between them, so a middle level can go either way. `scale: link` is the effect on each cumulative link P(Y ≤ level): −β_j for every level but the last, with the coefficient's SE. Delta method on the coefficient and threshold covariance (classical, `hc0`, `hc1`, `cluster`); Wald z.
A `multiplicity` block corrects all levels × terms as one family.

## Per-category effects (`REG_MULTINOMIAL`)

One effect per fitted category and term on P(Y = category): π_k·(β_kj − Σ_m π_m·β_mj), β of the reference 0, averaged (`ame`) or at the means (`mem`); they sum to 0 over the categories, and the sign can differ from the coefficient's (which compares with the reference only). `scale: link` is each non-reference category's log-odds against the reference — the coefficient, with its SE. Delta method on the fit's covariance (classical, `hc0`, `hc1`, `cluster`); Wald z. For "does x matter at all", read `term_tests` ([`op-reg-multinomial`](op-reg-multinomial.md)), not one category's effect.

## Inference

- **Delta method** on the fit's own coefficient covariance: Var = J·V·Jᵀ with J_jk = δ_jk·m̄ + β_j·mean(μ″(η_i)·x_ik) (ame). V is whatever `std_errors` used, so a robust `covariance` (`hc0`–`hc3`, `cluster`, `hac`) carries through.
- **Reference distribution** = the coefficient table's: Student t on `df` for OLS and classical gamma, Wald z otherwise (binomial, poisson, negative binomial, any robust GLM). Intervals at the fit's `conf_level`.
- **Weights**: the average is weighted by the fit's row weight (marginaleffects `wts`); `mem`'s point is the weighted means. Frequency weights equal the `rep()` expansion.
- **Multiplicity**: with the regression's own `multiplicity` block the effects form their own family per regression (`margins.p_adjusted` / `significant_adjusted`, echo `margins.multiplicity` family `margins`); the coefficients' `p_adjusted` never move.

## Execution

OLS needs no extra pass (the effect is β), so it streams with the fit. A GLM is buffered anyway: one pass over the fitted rows after IRLS converges. Results are identical across decode / shard worker counts.

## Refusals

`PROCESSING_CONFIG`, `details.requires`, the same at predict and run:

| Cause | `requires` |
|---|---|
| bad `type` / `scale` | `type ame or mem` / `scale response or link` |
| a term not a predictor, or repeated | `a predictor` |
| `REG_BAYES_LINEAR` | `type REG_OLS, REG_GLM or REG_MULTINOMIAL` |
| an extension or model regression not declaring margins | `declared_margins` |
| `penalty` / `selection` / `resample` / `bounds` | `unpenalized` / `no_selection` / `no_resample` / `unbounded` |
| a predictor derived from another predictor of the same fit (feature / attribute / window inputs, transitively) | `independent_predictors` |

A derived predictor (a log column, a one-hot dummy) is differentiated as given: its effect is that of a unit change in that column. One built from another predictor (x² beside x, an interaction beside its factors) is refused: holding the other fixed is not defined. A 0/1 dummy gets a slope, not the discrete 1 − 0 contrast `marginaleffects` reports for a binary column.

## Against R

Pinned to analytic closed forms in R 4.6.1 at 1e-9 and to `marginaleffects` 1.0.0 (`avg_slopes`, `slopes(newdata = "mean")`; on `polr` / `multinom` per level) within its numerical-derivative accuracy (~1e-8): oracle `margins.json`.

## See

- [`op-reg-glm`](op-reg-glm.md) — the GLM params.
- [`op-reg-ols`](op-reg-ols.md) — the OLS params.
- [`op-reg-ordinal`](op-reg-ordinal.md) — the ordinal params.
- [`op-reg-multinomial`](op-reg-multinomial.md) — the multinomial params, `term_tests`.
- [`regression-inference`](regression-inference.md) — standard errors, robust covariance, intervals.
- [`multiplicity-correction`](multiplicity-correction.md) — families and methods.
