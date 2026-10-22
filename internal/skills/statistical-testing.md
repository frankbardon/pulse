---
name: statistical-testing
description: Tier-1 (`tests`) vs tier-2 (`post_tests`) pairing, assumption gates, ANOVA + post-hoc composition, p-value conventions. Topical design; per-TEST detail in atomic op-test-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [TEST, tier-1, tier-2, tests, post_tests, ANOVA, Tukey, assumption-gates, p-value, grouped-tests, group_key]
---

# Statistical testing

Two test slots; this file teaches choosing and chaining. No `Response.Components`; results ride `Response.Tests` / `PostTests`.

## Which skill answers what

| Question | Skill |
|---|---|
| CUPED `covariate`, `reference`, one-proportion `p0`, chi-square `expected` | `test-variant-options` |
| Reusing the Welford state of the same `(field, split_by)` | `aggregation-design` |
| Wald-z vs Student-t in regressions | `regression-modeling` |
| One test's params and output | atomic `op-test-<name>` |

## Finding the right test

1. `pulse_skills_get intents` — question kind: `compare_groups` (do averages / rates / mixes differ), `relationship` (do two fields move together), `distribution_shape` (is it normal, do two shapes differ), `change_over_time` (is an ordered series trending), `benchmark` (one group against a fixed target).
2. In `pulse_manifest`, keep the `tests[]` entries whose `intents` carry that ID (each states its `tier` and `streamable`).
3. Read the `op-test-<name>` skill; narrow by data shape (below).

## Two tiers, one shape

| Slot | Stage | Consumes | Cost |
|---|---|---|---|
| `tests` (tier 1) | during row scan | raw records | near-zero reusing Welford state |
| `post_tests` (tier 2) | after `windows` | materialized result rows | one pass over `Response.Data` |

Same `Test` / `TestResult`. A post-test drops rows with a null in a used column (listwise; `details.n` = rows used); a NaN cell stays and nulls the result.

## Picking the tier

Tier 1 — raw distribution questions (A/B revenue, normality). Tier 2 — per-group summaries / windowed columns (trend on `moving_avg`).

Raw-row and per-group correlations can disagree (Simpson's paradox); `Variant` (`pearson` / `pearson_post`) says which.

## Decision criteria

| Question | Pushes toward |
|---|---|
| How many groups? | two → a two-sample test; three or more → an ANOVA-family test |
| Same subjects measured more than once? | yes → a paired / repeated-measures test |
| Roughly normal, or skewed / ranked / extreme? | normal → mean-based; otherwise rank-based |
| Similar spread across groups? | no → the Welch forms |

Outcome type routes first: a numeric measure → mean or rank tests; a yes/no rate (categorical or `packed_bool`) → the two-proportion z or a contingency test; two categorical fields → χ² (2×2 with any expected cell < 5 → Fisher's exact, `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`); two numeric fields → a correlation.

<!-- feature: capability:matrices -->
A whole battery of numeric fields (mean profiles, covariance, sphericity, joint normality) takes a `vector` test: `multivariate-design`.
<!-- /feature -->

## Assumption gates before an ANOVA

1. Per-group normality — the Shapiro-Wilk test with `split_by` (n ≤ 5000 per group; larger → `PULSE_TEST_SHAPIRO_N_BOUND`).
2. Equal spread — the Brown-Forsythe test (median-based, robust under non-normality).

A failed spread gate → Welch's ANOVA (same Welford state); badly failed normality → rank-based Kruskal-Wallis.

## ANOVA alone vs ANOVA + post-hoc

The F test rejects the global null but not *which pairs* differ. For pairwise localization chain two requests:

1. Tier-1 one-way ANOVA; read `ms_within` / `df_within` from `Details`.
2. Tier-2 Tukey HSD with those two as `params` (Tukey-Kramer p, family-wise α controlled).

After Welch's ANOVA, Tukey's pooled `ms_within` is wrong: run pairwise Welch tests<!-- feature: capability:multiplicity --> with a `multiplicity` block (`holm`)<!-- /feature -->.

## P-value conventions

- `alpha` defaults to 0.05, range `(0, 1)` (else `PULSE_TEST_INVALID_ALPHA`).
- Two-sided, fixed-horizon: one look at a planned n. Limits: no sequential / always-valid p-values (peeking inflates false positives), no Bayesian readout (probability to beat control); one-sided is caller-side.
- Two-group signs read first − second (alphabetical); `params.reference` is second.
- Multi-group headline tracks the worst group; detail in `Details.per_group`.
<!-- feature: capability:multiplicity -->
- Many tests: `multiplicity` adds `p_adjusted` beside raw p (`multiplicity-correction`).
<!-- /feature -->

## Grouped requests

With `groups` set, every `tests[]` entry runs once per group (composite key): `Response.Tests` holds tests × groups results, test-major, groups in `data` row order, each with `group_key` — equal to an ungrouped run filtered to that group. Buffered. Over 2+ groupers each `post_tests[]` entry likewise runs once per partition — rows sharing every key but the last, or the leading keys `params.partition_by` names (`[]` = all rows; same on every post-test) — `group_key` the partition key.

- A `split_by`, `rows` or `cols` naming a grouper (post-tests: a partition key) is refused (`PULSE_TEST_SPLIT_GROUPS_LT_2` / `PULSE_TEST_CONTINGENCY_DEGENERATE`): one level each.
- A sparse or singular group / partition gets `null` `statistic` / `p_value` plus a warning naming its `group_key`; the rest proceed.
- ACROSS groups (ANOVA by region): ungrouped, or `partition_by: []`.

## Streamability

A tier-1 test streams when it can run on online state (mean / variance / n, or contingency counts); rank-, sort- and permutation-based tests are forced buffered<!-- feature: TEST_MARDIA -->, as is `TEST_MARDIA` (needs the fitted covariance first)<!-- /feature -->. Read manifest `tests[].streamable` or `pulse_predict`. Tier 2 is always buffered; some families exist on tier 2 only (one manifest entry per tier).

## Gotchas

- Tier-2 `field` names match the aggregator's projected column (`AGG_<TYPE>_<field>`).
- Tier-2 Welch's ANOVA needs `params.n_col` + `params.variance_col`.
- A test failing on its data (`PULSE_TEST_VARIANCE_ZERO`: a constant field) fails alone: `{label, type, error}` + `PULSE_SLOT_FAILED` (`cohort-null-states`).

## See

- Recipes: `pulse_examples_search tags=["experiment-analysis"]`, `tags=["k-sample"]`, `tags=["correlation-analysis"]`, `tags=["nonparametric"]`.
- `test-variant-options`, `aggregation-design` (`MetaAggregator` contract), `regression-modeling`, `overlay-system` (crosstab cell stat overlays), `request-envelope` (slot keys).
- `pulse_errors_lookup` — `PULSE_TEST_*` recovery.
