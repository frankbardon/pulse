---
name: regression-comparison-batteries
description: Nested comparison of two Compose vector fits (batteries) member by member — each restricted member regressed on the other restricted members against the other full members, one F / likelihood-ratio / Wald row per member read exactly from the co-moment, R's per-member anova(lm); the nesting and same-rows rules, weights, the rows and the refusals. Topical design; regression-fit comparisons live in regression-comparison.
type: guide
kind: design
applies_to: compose, predict
covers: [compare, comparisons, battery, vector fit, members, member-wise, nested model, anova, F test, likelihood ratio test, Wald test, co-moment, FitComparisonMember, FitComparisonResult]
requires: [capability:compose_compare, capability:matrices]
---

# Comparing two batteries

A vector fit holds a battery's listwise mean and co-moment — no response, no predictors. Comparing a smaller battery with a larger one that contains it asks, for each member of the smaller one: once the extra members are known, does it become more predictable? Each restricted member j is the response of two Gaussian linear models over the same rows:

- restricted: `lm(j ~ the other restricted members)` (`lm(j ~ 1)` when the restricted battery is j alone);
- full: `lm(j ~ the other full members)`.

One test row per restricted member, in the restricted battery's order.

<!-- feature: MAT_COVARIANCE -->
```json
{"fits": [
   {"name": "core", "request": {"cohort": {"filename": "s.pulse"},
     "matrices": [{"type": "MAT_COVARIANCE", "fields": ["q1", "q2"]}]}},
   {"name": "core_plus", "request": {"cohort": {"filename": "s.pulse"},
     "matrices": [{"type": "MAT_COVARIANCE", "fields": ["q1", "q2", "q3", "q4"]}]}}],
 "compare": [{"name": "q3_q4_add", "restricted": "core", "full": "core_plus"}],
 "requests": [...]}
```
<!-- /feature -->

## Nested and on the same rows

Judged on the specs, at predict and at run alike, when `restricted` names a vector fit:

- `full` is a vector fit too (anything else is `fit_kind`, the vector fits under `available`);
- both batteries LIST their members: a matrix slot's or fitted attribute's `fields`, or a request `vectors` entry it names that lists `fields` (its `labels` name the members). A `pattern` or a vector field resolves its members against the cohort only at run, so nesting cannot be judged: `members_unlisted` (`field`, `value`, `slot`);
- every restricted member — label AND field — is a full member (`not_nested`, `missing`; swapped sides say so), and the full battery adds at least one: the tested members, `terms`, in the full battery's order (`no_extra_terms`);
- the requests are equal but for the member lists (the slot's `fields`, the named `vectors` entry's `fields` / `labels`), the slot's reporting knobs (a matrix slot's `name`, `encoding`<!-- feature: capability:multiplicity -->, `multiplicity`<!-- /feature -->; an attribute's `label`) and the request's `label`, `return`, `disable_components`<!-- feature: capability:multiplicity -->, `multiplicity`<!-- /feature --> (`request_mismatch`, `keys`). So one operator type, params, filters, features<!-- feature: capability:weighting -->, weight<!-- /feature -->. Two different operators over the same rows (a covariance and a correlation) are refused — keep the battery slot identical.

The full battery's listwise rows are a subset of the restricted one's; unequal rows (N or Σw) fail the comparison alone, `PULSE_COMPARISON_ROWS_DIFFER`. A failed vector fit passes its own code (`details.fit`).

## Tests

Both models' residual sums of squares are Schur complements of the FULL fit's co-moment M2 (the intercept is the centring): RSS(j | S) = M2_jj − M2_jS M2_SS⁻¹ M2_Sj. With q tested members, N* the fit's (Σw), df₁ = N* − p_full, df₀ = N* − p_restricted, σ̂² = RSS₁ / df₁:

| `test` | Statistic | R, per member |
|---|---|---|
| `f` (default) | ((RSS₀ − RSS₁)/q)/σ̂² on (q, df₁) | `anova(m0, m1)` |
| `lrt` | (RSS₀ − RSS₁)/σ̂² on χ²(q) | `anova(m0, m1, test = "Chisq")` |
| `wald` | W/q on (q, df₁), the classical covariance | `lmtest::waldtest(m0, m1)` |

On a linear model the classical Wald W/q IS the F statistic. Gaussian only: a battery has no family, so no glm comparison exists.

<!-- feature: capability:weighting -->
## Weights

The battery's weight (slot, else request, else instance default; equal on both). Frequency = the `rep()` expansion (N* = Σw). A probability weight is refused (`requires` `no_probability_weight`, no alternative): its co-moment carries no likelihood or coefficient covariance a nested test reads. Opt a slot out with `"weight": null`.
<!-- /feature -->

## Result

`comparisons[i]` keeps the comparison's shape: `name`, `restricted`, `full`, `test`, `distribution`, `terms` (the tested members), `df` (q), `df_residual` (df₁, F only), `n_obs` (the listwise rows), `restricted_df_residual` / `full_df_residual`. The single-pair `statistic`, `p_value` and `*_deviance` are `null` (undefined for a battery); the tests are `members`:

`{member, statistic, p_value, restricted_deviance, full_deviance, dispersion}` — the RSS of each model (weighted under a frequency weight) and σ̂² (absent on `wald`). Predict's `compare` summary lists `members` (the row order) beside `test`, `distribution` and `terms`.

<!-- feature: capability:multiplicity -->
## Multiplicity

Every member row is ONE hypothesis of the comparison's family (`compose`, the default): a battery of k restricted members adds k tests to the pool, and each row carries `p_adjusted` / `significant_adjusted` beside its raw `p_value` (the comparison itself carries none). A `none` block is byte-identical to no block.
<!-- /feature -->

## Against R

R 4.6.1 per-member `anova(lm)`, `anova(test = "Chisq")` and `lmtest::waldtest` at 1e-10 — one and two tested members, the full battery in another order, an intercept-only restricted model, a frequency weight (row expansion); batteries on a matrix slot, a fitted attribute and a `vectors` entry: `compare_vector.json`.

## See

- `regression-comparison` — comparing two regression fits.
- `compose-fits` — the vector fits a comparison reads.
- `multivariate-design` — choosing the battery's operator.
