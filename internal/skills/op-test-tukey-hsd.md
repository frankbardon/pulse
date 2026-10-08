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

- `alpha` — float, default `0.05`, family-wise, in `(0, 1)`.
- `ms_within` / `df_within` — float, both required. Within-group mean square + df from a preceding one-way ANOVA.

`Field` + `SplitBy` both required. **Tier-2 only** — list in `Request.PostTests`.

## Inputs

`Field` — per-group mean column (an average aggregate). `SplitBy` — the grouper column the upstream ANOVA partitioned by.

## Output

`Statistic` = q (studentized range, worst pair); `PValue` via studentized-range CDF. `Details.pairs` = per-pair `{label_a, label_b, mean_diff, q, p_value, reject}`; α family-wise.

<!-- generated: reading-the-output -->

## Gotchas

- Already family-wise: never joins a `multiplicity` family; an explicit non-`none` block is refused, an inherited one skipped.
- Never weighted (it reads aggregated result rows, which carry no row weights): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- Runs against materialized per-group result rows. Buffered (`Streamable=false`).
- Consumes tier-1 one-way ANOVA outputs: run as a follow-up Request, or compose inside a ProcessChain that exposes them.
- Assumes equal variances (as ANOVA); Games-Howell for unequal is not yet shipped.
- Headline tracks the worst pair — inspect `Details.pairs` for the full matrix.

## See

- `pulse_examples_search tags=[post-hoc]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-anova-welch`
