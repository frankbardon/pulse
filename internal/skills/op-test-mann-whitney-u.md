---
name: op-test-mann-whitney-u
description: Nonparametric two-sample location test; buffered tie-corrected rank sum (Mann-Whitney U).
kind: operator
category: TEST
operator: TEST_MANN_WHITNEY_U
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, two-sample, comparison, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | In `(0, 1)`. Significance level in (0, 1); RejectNull is true when PValue < alpha. |
| `reference` | string | — | The `SplitBy` level read second (`groups[1]`), so `u_a`/`r_a`, `z` and `rank_biserial` belong to the other group (default: alphabetical). Not a level, or no `SplitBy` → `PULSE_TEST_REFERENCE_INVALID` (predict too); neither observed group → `PULSE_TEST_REFERENCE_MISSING` (grouped: that group's figures `null`). |

- `weight` — frequency only: a row of weight w ranks as w identical rows — equals the test on the expanded rows. `Details` add `sum_weights` shaped like `n` (raw rows).

## Inputs

`Field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `SplitBy` (required, exactly 2 groups) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`Statistic` = U_min; `PValue` two-sided normal approx, tie-corrected. `Details`: `groups` (sorted; `reference` second), `n`, `u_a`, `u_b`, `u_min`, `r_a`, `r_b`, `mu_u`, `var_u`, `z`; `effect_size.rank_biserial` = (U_A − U_B)/(n_A·n_B) — > 0 ⇒ `groups[0]` tends larger (sign of `z`).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — combined values mid-ranked under tie correction.
- ≥ 50% tied → warning `PULSE_TEST_TIES_DOMINATE`: the test then mostly compares the share of tied values, not their order; the asymptotic p is unreliable.
- Robust alternative to a t-test when normality fails.
- Tests stochastic equality, not mean difference — disagreeing with a t-test is signal, not a bug.
- Asymptotic only (no exact p); n < 2 per group is `PULSE_TEST_INSUFFICIENT_N`.
- With `groups`: one result per group, each with `group_key`; `split_by` a grouper → `PULSE_TEST_SPLIT_GROUPS_LT_2`; a too-sparse group → `null` figures + a warning (`statistical-testing`).

## See

- `pulse_examples_search tags=[nonparametric]`
- Skills: `statistical-testing`, `op-test-kruskal-wallis`, `op-test-wilcoxon-sr`, `op-test-welch`
