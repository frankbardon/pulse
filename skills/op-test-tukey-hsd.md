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

## Params

- `alpha` — float, default `0.05`, family-wise, in `(0, 1)`.
- `ms_within` / `df_within` — float, both required. Within-group mean square + df from a preceding `TEST_ANOVA_F`.

`Field` + `SplitBy` both required. **Tier-2 only** — list in `Request.PostTests`.

## Inputs

`Field` — per-group mean column from `AGG_AVERAGE` / `AGG_WELFORD`. `SplitBy` — the grouper column the upstream ANOVA partitioned by.

## Output

`Statistic` = q (studentized range, worst pair); `PValue` via studentized-range CDF. `Details.pairs` = per-pair `{label_a, label_b, mean_diff, q, p_value, reject}`; α family-wise.

## Gotchas

- Runs against materialized per-group result rows. Buffered (`Streamable=false`).
- Consumes tier-1 `TEST_ANOVA_F` outputs: run as a follow-up Request, or compose inside a ProcessChain that exposes them.
- Assumes equal variances (as ANOVA); Games-Howell for unequal is not yet shipped.
- Headline tracks the worst pair — inspect `Details.pairs` for the full matrix.

## See

- `pulse_examples_search tags=[post-hoc]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
