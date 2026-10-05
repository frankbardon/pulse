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

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
<!-- feature: capability:multiplicity -->
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p (`multiplicity-correction`).
<!-- /feature -->

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, the condition) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`. `SubjectField` (required, the within-subject grouping) — `categorical_u8`/`u16`/`u32`.

## Output

`Statistic` = F = MS_treatment / MS_error; `DF` = k-1 (`Details.df_error` = (n-1)(k-1)); `PValue` via F survival. `Details.ss_between_subjects`, `Details.ss_treatment`, `Details.ss_error`; `effect_size.partial_eta_squared` = SS_treatment / (SS_treatment + SS_error); unbanded.

## Gotchas

- Never weighted (a row weight has no meaning in the subject-by-condition table): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Buffered — requires the full wide subject × condition table.
- Subjects missing a condition are dropped (`Details.dropped_subjects`); < 2 left -> `PULSE_TEST_INSUFFICIENT_N`.
- Sphericity violations inflate type-I; no Greenhouse-Geisser yet.
- Non-normal differences: Friedman not yet shipped.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-paired-t`
