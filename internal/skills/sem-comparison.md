---
name: sem-comparison
description: Nested comparison of two Compose SEM fits — the chi-square difference test of lavaan's lavTestLRT, plain under ml / dwls and the Satorra-Bentler (2001) scaled difference under wlsmv; invariance steps (configural, metric, scalar) and dropped paths, how nesting is declared and checked, the result, the refusals and the failures. Topical design; the regression comparisons in regression-comparison.
kind: design
type: guide
applies_to: compose, predict
covers: [compare, comparisons, SEM, SEM fit, chi-square difference, lavTestLRT, Satorra-Bentler, scaled difference, measurement invariance, configural, metric, scalar, MAT_SEM, FitComparisonResult]
requires: [MAT_SEM, capability:compose_compare]
---

# Comparing two SEM fits

A `compare` entry over two SEM fits (`sem-fits`: a `fits` entry holding one `MAT_SEM` matrix) asks whether the restricted model's extra constraints fit significantly worse: a dropped path, a fixed covariance, an invariance step across groups (configural → metric → scalar). R `lavaan::lavTestLRT(m0, m1)`. It reads the two finished fits, no row.

```jsonc
{"fits": [
   {"name": "metric", "request": {"cohort": {"filename": "survey.pulse"},
     "matrices": [{"type": "MAT_SEM", "vector": "ratings",
       "params": {"model": "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3", "group": "wave", "group_equal": ["loadings"]}}]}},
   {"name": "configural", "request": {"cohort": {"filename": "survey.pulse"},
     "matrices": [{"type": "MAT_SEM", "vector": "ratings",
       "params": {"model": "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3", "group": "wave"}}]}}],
 "compare": [{"name": "loadings_hold", "restricted": "metric", "full": "configural"}],
 "requests": [...]}
```

## Nesting is declared

Pulse trusts the constraints you write; it never proves nesting (no NET check). It does check, at predict and at run alike:

- both names are SEM fits (`kind_mismatch` beside a regression fit), two different ones;
- the two fit requests are equal but for the model knobs — `params.model`, `group_equal`, `group_partial` — and the reporting and solver params (`ci_level`, `resample`, `bootstrap_iters`, `rng_seed`, `solver`, `information`, `ftol`, `max_iter`, `grad_tol`) and the request's `label`, `return`, `disable_components`<!-- feature: capability:multiplicity -->, `multiplicity`<!-- /feature -->: same cohort, filters, members<!-- feature: capability:weighting -->, weight<!-- /feature -->, `group`, `estimator`, `ordered`, `meanstructure` (`request_mismatch`, `details.keys`);
- the model knobs differ (`same_model`; `group_equal` compares as a set);
- two written models name the same observed variables (`observed_differ`, `restricted_only` / `full_only`).

Once fitted, the restricted fit must have MORE degrees of freedom than the full one (`df_not_positive`; swapped sides say so) and both fits must follow the same conventions and both model the means or neither (`conventions_differ` — a `~ 1` or `:=` line in one model only). These fail the comparison alone (predict cannot count thresholds or group levels), as do unequal row or group counts (`PULSE_COMPARISON_ROWS_DIFFER`, `rows_differ`).

## The test

`test` is `lrt`, the default (`f` and `wald` read regression coefficients: `test_unsupported`). With T each fit's model χ² and d its df:

| `estimator` | Statistic on χ²(d₀ − d₁) | R |
|---|---|---|
| `ml`, `dwls` | T₀ − T₁ | `lavTestLRT(m0, m1)` |
| `wlsmv` | (T₀ − T₁)/c_d, c_d = (d₀c₀ − d₁c₁)/(d₀ − d₁), c = tr(UΓ)/d | `lavTestLRT(m0, m1, method = "satorra.bentler.2001")` on `test = "satorra.bentler"` |

T is the fit's `chi2` scalar — lavaan's standard (unscaled) statistic on a lavaan-convention fit, semopy's on a covariance-only one; compare fits of one convention. Under `wlsmv` the scaled difference uses each fit's mean-adjusted factor, not the scaled-shifted `chi2_scaled` (a difference of scaled statistics is not χ²). A negative c_d (possible on small samples or a misspecified pair) leaves the statistic, p and `scaling_factor` `null`. lavaan's default for a WLSMV pair is `satorra.2000`, which Pulse does not compute; MLR is no Pulse estimator.

## Result

`comparisons[i]`: `test` `lrt`, `distribution` `chisq`, `terms` `[]`, `statistic`, `df` (d₀ − d₁), `p_value`, `n_obs`, `restricted_df_residual` / `full_df_residual` (each model df), `restricted_deviance` / `full_deviance` (each model χ²), `scaling_factor` (`wlsmv` only). Multiplicity and the fails-alone shape are a regression comparison's (`regression-comparison`). Predict lists the entry with `test` `lrt`, `distribution` `chisq`, empty `terms`.

## Against R

lavaan 0.7.3 on R 4.6.1 (`sem_compare.json`): a dropped structural path, the metric and scalar invariance steps across groups (ML), a fixed factor covariance (DWLS, WLSMV), threshold + loading invariance against the configural fit (WLSMV) — within 1e-8 (ML) and 1e-6 (ordinal, the two-step fit's stop).

## See

- `sem-fits` — the SEM fit entry.
- `sem-groups` — `group_equal` / `group_partial` invariance.
- `sem-ordinal` — `dwls` / `wlsmv` and the scaled test.
- `regression-comparison` — comparisons of regression fits.
