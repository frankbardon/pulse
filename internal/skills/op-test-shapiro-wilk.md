---
name: op-test-shapiro-wilk
description: Shapiro-Francia (W′) normality test on field (= nortest::sf.test); runs per-group when split_by is set; needs n ≥ 5.
kind: operator
category: TEST
operator: TEST_SHAPIRO_WILK
type: reference
applies_to: process, compose, predict
examples_tags: [hypothesis-test, tier-1-test, normality-test, distribution-shape, parametric, buffered-pipeline]
---

Tests emit statistic / p-value / effect size; no `Response.Components`.

<!-- generated: use-when -->

## Params

`alpha` is a top-level key; the rest go in `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | `0.05` | Significance level in `(0, 1)`; `reject_null` when p < alpha. |
| `partition_by` | list | — | Post-test over 2+ groupers: one run per partition, the rows sharing these leading group keys (default all but the last; `[]` = one run). |

## Inputs

`field` (required) — numeric (`decimal128` as its float64 echo, `packed_bool` as 0/1). `split_by` (optional — when set, runs per-group) — categorical (`packed_bool` levels `"0"`/`"1"`).

## Output

`statistic` = W' (Shapiro-Francia, approximates Shapiro-Wilk W; `(0, 1]`); `p_value` (small p ⇒ reject normality). With `split_by`: `details.per_group` carries per-arm W and p; headline `statistic` / `p_value` track the worst-rejecting group.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Buffered — requires the ordered values.
- Matches R `nortest::sf.test` (not `shapiro.test`, a different W). p calibrated for 5..5000 rows: above 5000 `per_group[].warning` marks it advisory.
- Not a gate: few rows miss real departures, many flag trivial ones.
- Any group with `n < 5` → `PULSE_TEST_INSUFFICIENT_N`; a constant group (W′ undefined; `sf.test` NA) → `PULSE_TEST_VARIANCE_ZERO`. Either way the slot fails alone (`error`, `PULSE_SLOT_FAILED`; fatal under strict), both tiers.
- Tier-2 variant `TEST_SHAPIRO_WILK/shapiro_francia_post` runs on a result column. A row with a null in a used column drops listwise (R's NA default); each `per_group[].n` is the n used.

## See

- `pulse_examples_search tags=[normality-test]`
- Skills: `statistical-testing`, `op-test-anova-f`, `op-test-kruskal-wallis`, `op-test-ks`
