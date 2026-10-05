---
name: statistical-testing
description: Tier-1 (`tests`) vs tier-2 (`post_tests`) pairing, assumption gates, ANOVA + post-hoc composition, p-value conventions. Topical design; per-TEST detail in atomic op-test-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [TEST, tier-1, tier-2, tests, post_tests, ANOVA, Tukey, assumption-gates, p-value]
---

# Statistical testing

Pulse runs hypothesis tests through two independent slots. This file teaches choosing and chaining; per-test detail lives in atomic `op-test-<name>` skills.

Tests do not emit `Response.Components`. Results ride `Response.Tests` / `Response.PostTests`.

## Finding the right test

Start from the question, not a test name:

1. `pulse_skills_get intents` — pick the question kind: `compare_groups` (do averages / rates / mixes differ), `relationship` (do two fields move together), `distribution_shape` (is it normal, do two shapes differ), `change_over_time` (is an ordered series trending), `benchmark` (one group against a fixed target).
2. In `pulse_manifest`, keep the `tests[]` entries whose `intents` carry that ID. Each entry also states its `tier` and `streamable`.
3. Read the chosen `op-test-<name>` skill; each test's guidance names its alternatives, so narrow by data shape (below), not by catalogue.

## Two tiers, one shape

| Slot | Stage | Consumes | Cost |
|---|---|---|---|
| `tests` (tier 1) | during row scan | raw records | near-zero when reusing aggregator Welford state |
| `post_tests` (tier 2) | after `windows` | materialized result rows | one pass over `Response.Data` |

Both share `Test` shape (`{type, field, field2?, split_by?, params?}`) and `TestResult` output. The split is *what they read*, not configuration shape. Either slot may be empty.

## Picking the tier

Tier 1 — raw distribution questions (A/B revenue, churn × region independence, column normality). Tier 2 — per-group summaries / windowed columns (do per-region averages differ, trend on `moving_avg`, correlate two per-group aggregates).

Ecological caveat: raw-row and per-group-aggregate correlations can disagree (Simpson's paradox); the `Variant` (`pearson` vs `pearson_post`) says which you got.

## Decision criteria

Four questions narrow a comparison:

| Question | Pushes toward |
|---|---|
| How many groups? | two → a two-sample test; three or more → an ANOVA-family test |
| Same subjects measured more than once? | yes → a paired / repeated-measures test |
| Roughly normal, or skewed / ranked / extreme? | normal → mean-based tests; otherwise rank-based tests |
| Similar spread across groups? | no → the Welch forms (heteroscedasticity-robust) |

Outcome type routes first: a numeric measure → mean or rank tests; a yes/no rate → the two-proportion z or a contingency test; two categorical fields → the χ² test (2×2 with any expected cell < 5 → Fisher's exact, `PULSE_TEST_EXPECTED_COUNT_TOO_LOW`); two numeric fields → a correlation (linear, monotonic or concordance — Kendall's cost grows with n²).

## Assumption gates before an ANOVA

Before trusting a one-way ANOVA:

1. Per-group normality — the Shapiro-Wilk test with `split_by` (n ≤ 5000 per group; larger → `PULSE_TEST_SHAPIRO_N_BOUND`).
2. Equal spread — the Brown-Forsythe test (median-based, robust under non-normality).

A failed spread gate moves you to Welch's ANOVA (same Welford state, Welch-Satterthwaite df); badly failed normality moves you to the rank-based Kruskal-Wallis. Gates are evidence, not proof: at small n they miss real departures, at huge n they flag trivial ones.

## ANOVA alone vs ANOVA + post-hoc

The F test rejects the global null but says nothing about *which pairs* differ. ANOVA alone is correct for a yes/no global question. For pairwise localization chain two requests:

1. Tier-1 one-way ANOVA; read `ms_within` / `df_within` from its `Details`.
2. Tier-2 Tukey HSD with those two values as `params` — Tukey-Kramer studentized-range p, family-wise α controlled.

After Welch's ANOVA, Tukey's pooled `ms_within` is wrong: run pairwise Welch tests with a `multiplicity` block (`holm`).

## P-value conventions

- `alpha` defaults to 0.05; range `(0, 1)`; out-of-range → `PULSE_TEST_INVALID_ALPHA`.
- Two-sided by default; one-sided needs caller-side post-processing on the statistic.
- Regression inference uses Wald-z, not Student-t — the `TEST_*` families never mix the two.
- Multi-group headline tracks worst group; per-group detail in `Details.per_group`.
<!-- feature: capability:multiplicity -->
- Many tests: `multiplicity` adds `p_adjusted` beside raw p (`multiplicity-correction`).
<!-- /feature -->

## Streamability

A tier-1 test streams when it can run on online state (mean / variance / n, or contingency counts); rank-, sort- and permutation-based tests are forced buffered. Read the per-test answer from the manifest `tests[].streamable` (declared in `types/streamability.go`) or `pulse_predict` — never from memory. Tier 2 is always buffered, and some families exist on tier 2 only (the manifest lists one entry per tier).

## Composition with aggregators

Cheapest pattern — declare the Welford aggregator on the same `(field, split_by)`; a mean-based tier-1 test reads its running `(mean, variance, n)` free, byte-equal to the standalone Welch numerators (likewise the crosstab cell t / z overlays). See `aggregation-design`.

## Gotchas

- Tier-2 `field` names match the aggregator's projected column (`AGG_<TYPE>_<field>`).
- Tier-2 Welch's ANOVA needs `params.n_col` + `params.variance_col` upstream.
- Tier-2 Tukey HSD requires `params.ms_within` + `params.df_within` from a preceding tier-1 ANOVA.
- Tiny groups → unstable p; gate with a row count and the `PULSE_TEST_INSUFFICIENT_N` floor.
- `PULSE_TEST_VARIANCE_ZERO`: constant field within a split group breaks correlation and t-tests.

## See

- Recipes: `pulse_examples_search tags=["ab-test"]`, `tags=["anova"]`, `tags=["correlation"]`, `tags=["nonparametric"]` plus atomic `op-test-<name>`.
- `aggregation-design` — Welford-triple reuse + `MetaAggregator` contract.
- `regression-modeling` — Wald-z vs Student-t inference inside regressions.
- `overlay-system` — crosstab cell-level stat overlays.
- `request-envelope` — slot keys, streamability rules.
- `pulse_errors_lookup` — `PULSE_TEST_*` recovery steps.
