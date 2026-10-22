---
name: op-test-tukey-hsd
description: Tier-2 post-hoc pairwise comparison of group means using Tukey's Honestly Significant Difference (Tukey-Kramer).
kind: operator
category: TEST
operator: TEST_TUKEY_HSD
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-2-test, post-hoc, k-sample, parametric, comparison, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Family-wise, in `(0, 1)`. |
| `ms_within` | float | (required) | Both required. Within-group mean square + df from a preceding one-way ANOVA. |
| `df_within` | float | (required) | Both required. Within-group mean square + df from a preceding one-way ANOVA. |
| `n_column` | string | `n` | Result-row column holding each group's size (Tukey-Kramer weighs a pair by it). |
| `partition_by` | list | — | Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). |

`Field` + `SplitBy` both required. **Tier-2 only** — list in `Request.PostTests`.

## Inputs

`Field` — per-group mean column (an average aggregate). `SplitBy` — the grouper column the upstream ANOVA partitioned by; a `packed_bool`<!-- feature: GROUP_CATEGORY --> `GROUP_CATEGORY`<!-- /feature --> keys `"0"`/`"1"`, but the test needs ≥ 3 groups (two alone is `PULSE_TEST_SPLIT_GROUPS_LT_2`).

## Output

`Statistic` unused (0); `PValue` = smallest `p_adj`. `Details`: `k_groups`, `family_alpha`, `ms_within`, `df_within`, `q_critical`, `comparisons` = per pair (a < b by label) `{a, b, diff, se, q, p_adj, reject_null, ci_low, ci_high}`; α family-wise.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- <!-- feature: capability:multiplicity -->Already family-wise: never joins a `multiplicity` family; an explicit non-`none` block is refused, an inherited one skipped.<!-- /feature -->
- Runs against materialized per-group result rows; a group row with a null drops (listwise); a non-numeric mean or `n_column` value is `PULSE_TEST_FIELD_NOT_NUMERIC`, a missing column `SERVICE_VALIDATION`. Buffered (`Streamable=false`).
- Consumes tier-1 one-way ANOVA outputs: run as a follow-up Request, or compose inside a ProcessChain that exposes them.
- Assumes equal variances (as ANOVA); Games-Howell for unequal is not yet shipped.
- Headline tracks the worst pair — inspect `Details.comparisons` for every pair.

## See

- `pulse_examples_search tags=[post-hoc]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
