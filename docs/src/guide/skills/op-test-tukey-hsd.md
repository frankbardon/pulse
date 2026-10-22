```yaml
name: op-test-tukey-hsd
description: Tier-2 post-hoc pairwise comparison of group means using Tukey's Honestly Significant Difference (Tukey-Kramer).
kind: operator
category: TEST
operator: TEST_TUKEY_HSD
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-2-test, post-hoc, k-sample, parametric, comparison, buffered-pipeline]
```

Tests emit statistic / p-value / effect size; no `Response.Components`.

## Use when

After an ANOVA, compares every pair of group averages to find which ones differ, keeping the overall false-alarm rate in check.

Questions it answers:

- Which regions differ from each other in average spend?
- Which treatment arms have different average outcomes?

Use something else:

- `TEST_ANOVA_F` when you only need to know whether any group differs.

## Params

- `alpha` — float, default `0.05`, family-wise, in `(0, 1)`.
- `ms_within` / `df_within` — float, both required. Within-group mean square + df from a preceding one-way ANOVA.
- `n_column` — string, default `"n"`: result-row column holding each group's size (Tukey-Kramer weighs a pair by it).

`Field` + `SplitBy` both required. **Tier-2 only** — list in `Request.PostTests`.

## Inputs

`Field` — per-group mean column (an average aggregate). `SplitBy` — the grouper column the upstream ANOVA partitioned by; a `packed_bool` `GROUP_CATEGORY` keys `"0"`/`"1"`, but the test needs ≥ 3 groups (two alone is `PULSE_TEST_SPLIT_GROUPS_LT_2`).

## Output

`Statistic` unused (0); `PValue` = smallest `p_adj`. `Details`: `k_groups`, `family_alpha`, `ms_within`, `df_within`, `q_critical`, `comparisons` = per pair (a < b by label) `{a, b, diff, se, q, p_adj, reject_null, ci_low, ci_high}`; α family-wise.

## Reading the output

- `statistic`: Not used: TEST_TUKEY_HSD reports no single statistic and leaves this slot at 0. Each pair's studentized range q is in details.comparisons.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: This is the smallest Tukey-adjusted p-value across all pairs, already corrected for multiple comparisons; read details.comparisons for which pairs it belongs to.

## Contract

- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`: it reads the aggregated per-group result rows, which carry no row weights, and weighted Tukey-Kramer has no reference form); `weight: null` on the slot runs it unweighted.
- Multiplicity: already corrected for its own comparisons; a method on its own block is `PULSE_MULTIPLICITY_INVALID`, an inherited one skips it.
- Streaming: buffered only.

## Gotchas

- Already family-wise: never joins a `multiplicity` family; an explicit non-`none` block is refused, an inherited one skipped.
- Runs against materialized per-group result rows; a group row with a null drops (listwise); a non-numeric mean or `n_column` value is `PULSE_TEST_FIELD_NOT_NUMERIC`, a missing column `SERVICE_VALIDATION`. Buffered (`Streamable=false`).
- Consumes tier-1 one-way ANOVA outputs: run as a follow-up Request, or compose inside a ProcessChain that exposes them.
- Assumes equal variances (as ANOVA); Games-Howell for unequal is not yet shipped.
- Headline tracks the worst pair — inspect `Details.comparisons` for every pair.

## See

- `pulse_examples_search tags=[post-hoc]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-anova-welch`](op-test-anova-welch.md)
