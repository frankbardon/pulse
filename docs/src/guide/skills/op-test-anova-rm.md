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

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, the condition) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `SubjectField` (required, the within-subject grouping) — `categorical_u8`/`u16`/`u32`.

## Output

`Statistic` = F = MS_treatment / MS_error; `DF` = k-1 (`Details.df_error` = (n-1)(k-1)); `PValue` via F survival. `Details.ss_between_subjects`, `Details.ss_treatment`, `Details.ss_error`; `effect_size.partial_eta_squared` = SS_treatment / (SS_treatment + SS_error); unbanded.

## Reading the output

- `statistic`: F compares how far apart the condition averages are with the leftover noise once each subject's own level is removed; larger values are stronger evidence that conditions differ by more than that noise; the p-value says whether this F is large enough to be surprising.
  - Caveat: It says whether some conditions differ, not which ones.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Gotchas

- Never weighted (a row weight has no meaning in the subject-by-condition table): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Buffered — requires the full wide subject × condition table.
- Subjects missing a condition are dropped (`Details.dropped_subjects`); < 2 left -> `PULSE_TEST_INSUFFICIENT_N`.
- Sphericity violations inflate type-I; no Greenhouse-Geisser yet.
- Non-normal differences: Friedman not yet shipped.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-paired-t`](op-test-paired-t.md)
