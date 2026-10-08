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

`Field` + `SplitBy` both required. **Tier-2 only** — list in `Request.PostTests`.

## Inputs

`Field` — per-group mean column (an average aggregate). `SplitBy` — the grouper column the upstream ANOVA partitioned by.

## Output

`Statistic` = q (studentized range, worst pair); `PValue` via studentized-range CDF. `Details.pairs` = per-pair `{label_a, label_b, mean_diff, q, p_value, reject}`; α family-wise.

## Reading the output

- `statistic`: Not used: TEST_TUKEY_HSD reports no single statistic and leaves this slot at 0. Each pair's studentized range q is in details.comparisons.
- `p_value`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
  - Caveat: This is the smallest Tukey-adjusted p-value across all pairs, already corrected for multiple comparisons; read details.comparisons for which pairs it belongs to.

## Gotchas

- Already family-wise: never joins a `multiplicity` family; an explicit non-`none` block is refused, an inherited one skipped.
- Never weighted (it reads aggregated result rows, which carry no row weights): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Runs against materialized per-group result rows. Buffered (`Streamable=false`).
- Consumes tier-1 one-way ANOVA outputs: run as a follow-up Request, or compose inside a ProcessChain that exposes them.
- Assumes equal variances (as ANOVA); Games-Howell for unequal is not yet shipped.
- Headline tracks the worst pair — inspect `Details.pairs` for the full matrix.

## See

- `pulse_examples_search tags=[post-hoc]`
- Skills: [`statistical-testing`](statistical-testing.md), [`op-test-anova-f`](op-test-anova-f.md), [`op-test-anova-welch`](op-test-anova-welch.md)
