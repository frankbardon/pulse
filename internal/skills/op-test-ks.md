---
name: op-test-ks
description: Kolmogorov-Smirnov two-sample distribution test on Field partitioned by SplitBy.
kind: operator
category: TEST
operator: TEST_KS
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, two-sample, distribution-shape, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Always two-sided; there is no one-sided form. |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

- `weight` — frequency only: ECDFs step by Σw, p on the Σw sizes — equals the expanded rows. `Details` add `sum_weights` beside `n` (raw rows).

Slot params: `Field` (required, numeric), `SplitBy` (required, categorical, exactly 2 groups).

## Inputs

`Field` — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = D (sup |F₁(x) − F₂(x)|); `PValue` two-sided = R `ks.test(x, y)$p.value`: exact when n₁·n₂ < 10000 (Σw under weights; conditional on ties), else the limiting Kolmogorov distribution. `Details.groups` + per-arm `Details.n`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — both ECDFs must materialize and sort before comparison.
- Sensitive to distribution shape, not just mean.
- Only n < 2 per group is refused (`PULSE_TEST_INSUFFICIENT_N`); the large-sample (asymptotic) p is approximate with ties.
- Tier-2 variant `TEST_KS/two_sample_post` runs between two output columns of the result set. A row with a null in a used column drops listwise (R's NA default); `details.n` counts the rows used.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `statistical-testing`, `op-test-mann-whitney-u`, `op-test-shapiro-wilk`
