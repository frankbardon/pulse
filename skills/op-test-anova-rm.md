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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`. |

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical — the condition), `SubjectField` (required, categorical — the within-subject grouping).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` |
| `SplitBy` | categorical: `categorical_u8`/`u16`/`u32`, `packed_bool` |
| `SubjectField` | categorical: `categorical_u8`/`u16`/`u32` |

## Output

`Statistic` = F = MS_treatment / MS_error; `DF` = (k − 1, (n − 1)(k − 1)); `PValue` via F-distribution survival. `Details.ss_between_subjects`, `Details.ss_treatment`, `Details.ss_error`; effect size partial η².

## Gotchas

- Buffered — requires the full wide subject × condition table.
- Each subject must contribute one observation per condition; missing → `PULSE_TEST_RM_UNBALANCED`.
- Sphericity assumption inflates type-I when violated; Greenhouse-Geisser not yet shipped.
- Non-normal differences: Friedman not yet shipped.
- Independent groups → `TEST_ANOVA_F`.

## See

- `pulse_examples_search tags=[repeated-measures]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-paired-t`
