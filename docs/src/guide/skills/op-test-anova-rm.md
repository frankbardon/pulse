```yaml
name: op-test-anova-rm
description: Repeated-measures one-way ANOVA; rows pivoted on SubjectField × SplitBy condition.
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
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, the condition) — categorical (`packed_bool` levels `"0"`/`"1"`). `SubjectField` (required, the within-subject grouping) — `categorical_u8`/`u16`/`u32` ONLY: a `packed_bool` subject (two ids) is refused, same code.

## Output

`Statistic` = F = MS_treatment / MS_error; `DF` = k-1 (`Details.df_error` = (n-1)(k-1)); `PValue` via F survival. `Details.ss_between_subjects`, `Details.ss_treatment`, `Details.ss_error`; `effect_size.partial_eta_squared` = SS_treatment / (SS_treatment + SS_error); unbanded.

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
- Subjects missing a condition are dropped (`Details.dropped_subjects`); < 2 left -> `PULSE_TEST_INSUFFICIENT_N`.
- Sphericity violations inflate type-I; no Greenhouse-Geisser yet.
- Non-normal differences: Friedman not yet shipped.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-paired-t`](op-test-paired-t.md)
