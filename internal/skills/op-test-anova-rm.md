---
name: op-test-anova-rm
description: Repeated-measures one-way ANOVA; rows pivoted on subject_field × split_by condition.
kind: operator
category: TEST
operator: TEST_ANOVA_RM
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, parametric, repeated-measures, k-sample, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `incomplete` | enum | `complete_case` | `complete_case` drops a subject missing a condition; `mixed` keeps every observed cell (below). |

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (required, the condition) — categorical (`packed_bool` levels `"0"`/`"1"`). `subject_field` (required, the within-subject grouping) — categorical only (not `packed_bool`).

## Output

`statistic` = F = MS_treatment / MS_error; `df` = k-1 (`details.df_error` = (n-1)(k-1)); `p_value` via F survival. `details.ss_between_subjects`, `details.ss_treatment`, `details.ss_error`; `effect_size.partial_eta_squared` = SS_treatment / (SS_treatment + SS_error); unbanded.

`incomplete: "mixed"` (`variant` `mixed`): REML `y ~ cond + (1 | subject)` on every cell, F with Satterthwaite `df_error` (fractional; lmerTest `anova`). `details` `var_subject`, `var_residual`, `incomplete_subjects`, `n_obs`, model `condition_means`; no SS or effect size. Balanced data reproduce the classic F.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — requires the full wide subject × condition table.
- One observation per subject per condition: a repeat -> `PULSE_TEST_BALANCED_DESIGN_REQUIRED` (details `subject`, `condition`); the slot fails alone. Average repeats first.
- Subjects missing a condition are dropped (`details.dropped_subjects`, warns `PULSE_TEST_SUBJECT_MISSING`); < 2 left -> `PULSE_TEST_INSUFFICIENT_N`. Keep them with `incomplete: "mixed"`.
- Sphericity violations inflate type-I error; there is no Greenhouse-Geisser correction and no Friedman test.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-paired-t`
