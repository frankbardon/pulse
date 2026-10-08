```yaml
name: op-test-mann-whitney-u
description: Nonparametric two-sample location test; buffered tie-corrected rank sum (Mann-Whitney U).
kind: operator
category: TEST
operator: TEST_MANN_WHITNEY_U
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, nonparametric, two-sample, comparison, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

Rank-based check of whether values in one of two groups tend to be larger than in the other.

Questions it answers:

- Do customers on the new plan tend to rate the service higher?
- Do response times at one site tend to run longer than at the other?

Use something else:

- `TEST_WILCOXON_SR` when the same subjects are measured twice.
- `TEST_KRUSKAL_WALLIS` when there are three or more groups.

## Params

- `alpha` — float, default `0.05`, in `(0, 1)`.
- `weight` — slot weight (`null` opts out), kind `frequency` only (a probability weight is `PULSE_WEIGHT_UNSUPPORTED` naming the kind): a row of weight w ranks as w identical rows — equals the test on the expanded rows. `Details` add `sum_weights` shaped like `n` (raw rows).
- `multiplicity` — optional `{method, family}`; adds `p_adjusted` beside the raw p ([`multiplicity-correction`](multiplicity-correction.md)).

## Inputs

`Field` (required) — numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `SplitBy` (required, exactly 2 groups) — categorical: `categorical_u8`/`u16`/`u32`, `packed_bool`.

## Output

`Statistic` = U_min; `PValue` two-sided normal approx, tie-corrected. `Details`: `groups` (sorted), `n`, `u_a`, `u_b`, `u_min`, `r_a`, `r_b`, `mu_u`, `var_u`, `z`; `effect_size.rank_biserial` = (U_A − U_B)/(n_A·n_B) — > 0 ⇒ `groups[0]` tends larger (sign of `z`).

## Reading the output

- `statistic`: U counts, over every pair of rows from the two groups, how often one group's value is larger (ties count half). Pulse reports the smaller of the two counts, from 0 to half of n_A times n_B: the smaller U, the more the groups separate.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `details.z`: The normal score the p-value is read from, with a continuity correction of one half.

## Gotchas

- Buffered — combined values mid-ranked under tie correction.
- Robust alternative to a t-test when normality fails.
- Tests stochastic equality, not mean difference — disagreeing with a t-test is signal, not a bug.
- Asymptotic only (no exact p); n < 2 per group is `PULSE_TEST_INSUFFICIENT_N`.

## See

- `pulse_examples_search tags=[nonparametric]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-kruskal-wallis`](op-test-kruskal-wallis.md), [`op-test-wilcoxon-sr`](op-test-wilcoxon-sr.md), [`op-test-welch`](op-test-welch.md)
