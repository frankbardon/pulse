---
name: op-test-anova-rm
description: Repeated-measures one-way ANOVA; rows pivoted on SubjectField × SplitBy condition.
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

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `incomplete` | enum | `complete_case` | `complete_case` drops a subject missing a condition; `mixed` keeps every observed cell (below). |

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, the condition) — categorical (`packed_bool` levels `"0"`/`"1"`). `SubjectField` (required, the within-subject grouping) — `categorical_u8`/`u16`/`u32` ONLY: a `packed_bool` subject (two ids) is refused, same code.

## Output

`Statistic` = F = MS_treatment / MS_error; `DF` = k-1 (`Details.df_error` = (n-1)(k-1)); `PValue` via F survival. `Details.ss_between_subjects`, `Details.ss_treatment`, `Details.ss_error`; `effect_size.partial_eta_squared` = SS_treatment / (SS_treatment + SS_error); unbanded.

`incomplete: "mixed"` (`Variant` `mixed`): REML `y ~ cond + (1 | subject)` on every cell, F with Satterthwaite `df_error` (fractional; lmerTest `anova`). Details `var_subject`, `var_residual`, `incomplete_subjects`, `n_obs`, model `condition_means`; no SS or effect size. Balanced data reproduce the classic F.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — requires the full wide subject × condition table.
- One observation per subject per condition: a repeat -> `PULSE_TEST_BALANCED_DESIGN_REQUIRED` (details `subject`, `condition`); the slot fails alone. Average repeats first.
- Subjects missing a condition are dropped (`Details.dropped_subjects`); < 2 left -> `PULSE_TEST_INSUFFICIENT_N`. Keep them with `incomplete: "mixed"`.
- Sphericity violations inflate type-I; no Greenhouse-Geisser yet.
- Non-normal differences: Friedman not yet shipped.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-paired-t`
