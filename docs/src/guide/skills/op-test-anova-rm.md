```yaml
name: op-test-anova-rm
description: Repeated-measures one-way ANOVA; rows pivoted on subject_field × split_by condition.
kind: operator
category: TEST
operator: TEST_ANOVA_RM
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, repeated-measures, k-sample, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Checks whether the average differs across conditions when every subject is measured under each condition.

Questions it answers:

- Do the same panel members rate the three ad concepts differently on average?
- Does each patient's score change across the baseline, mid-point and final visits?

Use something else:

- `TEST_ANOVA_F` when each group holds different, unrelated subjects.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `incomplete` | enum | `complete_case` | `complete_case` drops a subject missing a condition; `mixed` keeps every observed cell (below). |

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (required, the condition) — categorical (`packed_bool` levels `"0"`/`"1"`). `subject_field` (required, the within-subject grouping) — categorical only (not `packed_bool`).

## Output

`statistic` = F = MS_treatment / MS_error; `df` = k-1 (`details.df_error` = (n-1)(k-1)); `p_value` via F survival. `details.ss_between_subjects`, `details.ss_treatment`, `details.ss_error`; `effect_size.partial_eta_squared` = SS_treatment / (SS_treatment + SS_error); unbanded.

`incomplete: "mixed"` (`variant` `mixed`): REML `y ~ cond + (1 | subject)` on every cell, F with Satterthwaite `df_error` (fractional; lmerTest `anova`). `details` `var_subject`, `var_residual`, `incomplete_subjects`, `n_obs`, model `condition_means`; no SS or effect size. Balanced data reproduce the classic F.

## Reading the output

- `statistic`: F compares how far apart the condition averages are with the leftover noise once each subject's own level is removed; larger values are stronger evidence that conditions differ by more than that noise; the p-value says whether this F is large enough to be surprising.
  - Caveat: It says whether some conditions differ, not which ones.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`.
- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`subject_field`): `categorical_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`: a per-row weight has no defined meaning in the subject-by-condition table repeated-measures ANOVA reads); `weight: null` on the slot runs it unweighted.
- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only.

## Gotchas

- Buffered — requires the full wide subject × condition table.
- One observation per subject per condition: a repeat -> `PULSE_TEST_BALANCED_DESIGN_REQUIRED` (details `subject`, `condition`); the slot fails alone. Average repeats first.
- Subjects missing a condition are dropped (`details.dropped_subjects`, warns `PULSE_TEST_SUBJECT_MISSING`); < 2 left -> `PULSE_TEST_INSUFFICIENT_N`. Keep them with `incomplete: "mixed"`.
- Sphericity violations inflate type-I error; there is no Greenhouse-Geisser correction and no Friedman test.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-paired-t`](op-test-paired-t.md)
