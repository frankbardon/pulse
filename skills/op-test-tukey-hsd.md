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

Statistical tests emit summary statistics (statistic, p-value, effect size); they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Family-wise significance level in `(0, 1)`. |
| `ms_within` | float | required | Within-group mean square from a preceding `TEST_ANOVA_F`. |
| `df_within` | float | required | Within-group degrees of freedom from the same ANOVA. |

Slot params: `Field` (required), `SplitBy` (required). **Tier-2 only** — list in `Request.PostTests`.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | per-group mean column from upstream `AGG_AVERAGE` / `AGG_WELFORD` |
| `SplitBy` | grouper column the upstream ANOVA partitioned by |

## Output

`Statistic` = q (studentized range, worst pair); `PValue` via studentized-range CDF. `Details.pairs` = per-pair `{label_a, label_b, mean_diff, q, p_value, reject}`; α controlled family-wise.

## Gotchas

- Tier-2 only — runs against the materialized per-group result rows.
- Pairing semantics: consumes tier-1 `TEST_ANOVA_F` outputs (`ms_within`, `df_within`) — run as a follow-up Request, or compose inside a ProcessChain that exposes them.
- Buffered (`Streamable=false`).
- Assumes equal variances (same as ANOVA); for unequal use Games-Howell — not yet shipped.
- Headline tracks the worst pair; inspect `Details.pairs` for the full matrix.

## See

- `pulse_examples_search tags=[post-hoc]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
