```yaml
name: sem-ordinal
description: Ordinal structural equation models on MAT_SEM — lavaan's DWLS and WLSMV estimators over polychoric and polyserial correlations, for rating-scale, Likert and binary items. When to prefer them over maximum likelihood, how to declare the ordered members, how the threshold rows and the scaled test read, and what is refused.
kind: design
type: guide
applies_to: process, compose, predict
covers: [SEM, ordinal, WLSMV, DWLS, polychoric correlation, polyserial correlation, threshold, Likert, scaled chi-square, robust standard error]
requires: [MAT_SEM, capability:sem_ordinal]
```

# Ordinal SEM

`estimator: "wlsmv"` (or `"dwls"`) fits the model as lavaan does with `ordered = ...`: each ordered member is a cut-up latent normal response.

## ML vs WLSMV — when to prefer which

- Prefer `wlsmv` for items with 5 or fewer categories, binary items, or answers piled on one end. ML reads them as continuous and normal, which biases loadings down and inflates the chi-square.
- With 7+ roughly symmetric categories ML is usually close. Prefer ML then, and whenever you need `loglik`, `aic` or `bic`, a streamed or merged fit (`wlsmv` buffers), or Bartlett scores.
- `wlsmv` needs more rows: polychorics from sparse tables are unstable below a few hundred rows. Merge rare categories first.
- Unsure? Fit both in two slots: close `std_estimate` loadings mean ML's continuous reading does little harm.
- Prefer `wlsmv` to `dwls`: the same `estimate` and `std_err`, plus the scaled-shifted test that corrects the chi-square.

## Declaring the members

- `ordered` lists the ordered members by label (a list or one comma-separated string). Every other member is continuous; a continuous member gets polyserial correlations with the ordered ones.
- Each ordered member's categories are its distinct observed values in ascending order. A value no row holds is no category. Store ratings as integers or floats; a `packed_bool` member needs a vector with `coerce: "binary"`.
- At most 32 categories per ordered member. More is a continuous measure: the slot fails with `PULSE_MATRIX_TOO_MANY_CATEGORIES`. Fit it as numeric, or recode it.
- An ordered member must be an indicator or an outcome, never a predictor of something else.
- Frequency weights are supported. A probability weight is `PULSE_WEIGHT_UNSUPPORTED`.

## How it fits

- Stage 1: each ordered member's thresholds, then every pair's polychoric (ordered × ordered), polyserial (ordered × continuous) or Pearson correlation.
- Stage 2: the model is fitted to those statistics, weighted by the inverse diagonal of their asymptotic covariance (Gamma, built from per-row scores). It uses Fisher scoring under lavaan's conventions, with lavaan's delta parameterization.
- Gamma needs every row, so the slot keeps its rows and the request runs the buffered path. Predict reports this.
- Only lavaan's default delta parameterization is fitted. `parameterization: "theta"` is refused (`parameterization_unsupported`).

## Reading the outputs

- An ordered member's intercept is fixed at 0. Its residual variance is `1 −` its explained part and is reported as a fixed row (no SE).
- Each threshold is a row `x | t1`, `x | t2`, … (one fewer than the member's categories), with an SE, z, p and interval.
- `std_err` is lavaan's robust sandwich SE. `:=` rows ([`sem-defined-parameters`](sem-defined-parameters.md)) use the same robust covariance.
- `implied_covariance` has 1 on each ordered member's diagonal. `residual_covariance` is the polychoric / polyserial matrix minus it.
- Scalars: `chi2` and `dof` are the unscaled test, and its `chi2_p_value` is `null` (lavaan reports none). `loglik`, `aic` and `bic` are `null`: there is no likelihood. `cfi`, `tli`, `rmsea`, `gfi` and `agfi` are lavaan's.
- Under `wlsmv` the scaled test is added: `chi2_scaled`, `chi2_scaled_p_value`, `scaling_factor`, `shift_parameter`, `chi2_baseline_scaled` and `cfi_scaled`, `tli_scaled`, `rmsea_scaled`. Judge fit by the scaled figures.
- A threshold's p joins the matrix's own `multiplicity` family like every free row. Predict counts one threshold per ordered member as a lower bound, or the exact count for a member with a known category count (`packed_bool`).

## Across groups

- With `group`, each level gets its own thresholds, polychorics and Gamma. The categories are the pooled rows', so a category one level lacks fails the slot (`empty_category`, `details.level`).
- `group_equal: "thresholds"` holds every threshold equal. As in lavaan it also frees, outside the reference level, each ordered member's intercept and its scale factor (row `x ~*~ x`; a binary member's stays 1). Add `"loadings"` for the usual threshold + loading invariance. Add `"intercepts"` to free the latent means instead.
- `group_partial` frees one threshold: `"x|t2"`. Naming a threshold the data lack fails the slot (`partial_threshold_absent`).
- Every level's table carries an `x ~*~ x` row per ordered member.
## Bootstrap

- `resample: "bootstrap"` refits each replicate's own thresholds and polychorics at the full fit's categories. A replicate that loses a category is skipped. Thresholds get bootstrap SEs too. lavaan bootstraps DWLS only; a `wlsmv` bootstrap has the same estimates.

## Scores

- `ATTR_SEM_SCORE` scores an ordinal fit (inline, a Compose `fits` entry, a `.pulse-sem.json`) by lavaan's empirical Bayes modal scores: each row's posterior mode, with its own group's thresholds and parameters. `method: "regression"` means them, as in lavaan; `"bartlett"` is `bad_params`.
- A row is coded by the fit's categories: a value the fit never saw scores null (`PULSE_SEM_SCORE_LEVEL_UNSEEN`, reason `category_unseen`). A null member scores null (lavaan skips it instead). An observed mediator is scored as lavaan does.

## Refusals

- `bad_params`: an unknown estimator; `ordered` without an ordinal estimator; an ordinal estimator without `ordered`; a duplicate member; a latent, a predictor or a member the model does not read in `ordered`; `solver: "slsqp"`, `information`, `singular` or `meanstructure: false` with an ordinal estimator; a standardizing vector.
- On the data, the slot fails alone: a constant ordered member is `PULSE_MATRIX_SINGULAR`; no rows or an empty category is `PULSE_MATRIX_INSUFFICIENT_N`. `details.reason` and `details.variable` say which.

## See

- [`op-mat-sem`](op-mat-sem.md)
- [`sem-modeling`](sem-modeling.md) — the lavaan conventions an ordinal fit runs under.
- [`multivariate-design`](multivariate-design.md) — choosing the operator.
