---
name: statistical-testing
description: Tier-1 (`tests`) vs tier-2 (`post_tests`) pairing, assumption gates, ANOVA + post-hoc composition, p-value conventions. Topical design; per-TEST detail in atomic op-test-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [TEST, tier-1, tier-2, tests, post_tests, ANOVA, Tukey, assumption-gates, p-value]
---

# Statistical testing

Pulse runs hypothesis tests through two independent slots. Per-TEST math lives in atomic `op-test-*` skills; this file covers pairing.

Tests do not emit `Response.Components`. Results ride `Response.Tests` / `Response.PostTests`.

## Two tiers, one shape

| Slot | Stage | Consumes | Cost |
|---|---|---|---|
| `tests` (tier 1) | during row scan | raw records | near-zero when reusing aggregator Welford state |
| `post_tests` (tier 2) | after `windows` | materialized result rows | one pass over `Response.Data` |

Both share `Test` shape (`{type, field, field2?, split_by?, params?}`) and `TestResult` output. The split is *what they read*, not configuration shape. Either slot may be empty.

## Picking the tier

Tier 1 — raw distribution questions (A/B revenue, churn × region independence, column normality). Tier 2 — per-group summaries / windowed columns (do per-region averages differ, trend on `moving_avg`, correlate two per-group aggregates).

Ecological caveat: `TEST_PEARSON_R` raw-row vs per-group aggregate disagree under Simpson's paradox. `Variant` (`pearson` vs `pearson_post`) disambiguates.

## Pairing rules

**ANOVA alone vs ANOVA + Tukey.** `TEST_ANOVA_F` rejects the global null but tells you nothing about *which pairs* differ. For pairwise localization, follow with `TEST_TUKEY_HSD` (tier-2 only) — Tukey-Kramer studentized-range p, family-wise α controlled. Workflow: tier-1 `TEST_ANOVA_F`, read `ms_within` / `df_within`, second request with `TEST_TUKEY_HSD` passing those as `params`. ANOVA alone is correct for a yes/no global question.

**Pre-ANOVA gates.** Before `TEST_ANOVA_F`: (1) per-group normality via `TEST_SHAPIRO_WILK` w/ `split_by` (n ≤ 5000 per group; larger → `PULSE_TEST_SHAPIRO_N_BOUND`); (2) equal variance via `TEST_BROWN_FORSYTHE` (median-based, robust under non-normality).

If Brown-Forsythe rejects → `TEST_ANOVA_WELCH` (heteroscedasticity-robust; same Welford state, Welch-Satterthwaite df). Normality fails badly → `TEST_KRUSKAL_WALLIS`. Repeated measures (one observation per subject per condition) → `TEST_ANOVA_RM`.

**Two-sample.** `TEST_T` / `TEST_WELCH` assume approximate normality; `TEST_WELCH` relaxes equal-variance. `TEST_Z_TWO_SAMPLE` only when n is large per group AND survey conventions demand normal-CDF p (small-n divergence from `TEST_WELCH` non-trivial). Normality suspect → `TEST_MANN_WHITNEY_U` (independent), `TEST_WILCOXON_SR` (paired). Paired parametric → `TEST_PAIRED_T`.

**Contingency.** `TEST_CHISQ` on `rows × cols`. Fall back to `TEST_FISHER_EXACT` when any expected cell < 5 (`PULSE_TEST_EXPECTED_COUNT_TOO_LOW`) — Fisher restricted to 2×2. Two-proportion shortcut: `TEST_PROP_Z`.

**Correlation.** `TEST_PEARSON_R` linear (parametric). `TEST_SPEARMAN_R` monotonic (rank-based, robust to outliers). `TEST_KENDALL_TAU` concordance (small-sample preferred; O(n²) cost). All three on both tiers.

**Distribution / trend.** `TEST_KS` two-sample CDF (both tiers; buffered). `TEST_TREND` Mann-Kendall on ordered series (tier 2 only — needs `order_by`).

## P-value conventions

- `alpha` defaults to 0.05; range `(0, 1)`; out-of-range → `PULSE_TEST_INVALID_ALPHA`.
- Two-sided by default. Symmetric distributions emit two-sided p; one-sided needs caller-side post-processing on the statistic.
- REG_* inference uses Wald-z, not Student-t — `TEST_*` never mixes the two.
- Multi-group headline tracks worst group; per-group detail in `Details.per_group`.

## Streamability

Tier-1 streaming reuses online state from aggregators on the same fields — `TEST_T`, `TEST_WELCH`, `TEST_Z_TWO_SAMPLE`, `TEST_CHISQ`, `TEST_ANOVA_F`, `TEST_ANOVA_WELCH`, `TEST_PEARSON_R`, `TEST_PAIRED_T`, `TEST_PROP_Z`. Forced-buffered tier-1: `TEST_KS`, `TEST_MANN_WHITNEY_U`, `TEST_WILCOXON_SR`, `TEST_KRUSKAL_WALLIS`, `TEST_SPEARMAN_R`, `TEST_KENDALL_TAU`, `TEST_BROWN_FORSYTHE`, `TEST_FISHER_EXACT`, `TEST_SHAPIRO_WILK`, `TEST_ANOVA_RM`. Declared in `types/streamability.go`; surfaces via `pulse_predict`.

Tier 2 always buffered. `TEST_TUKEY_HSD` and `TEST_TREND` are tier-2 only.

## Composition with aggregators

Cheapest pattern — declare `AGG_WELFORD` on the same `(field, split_by)` and the tier-1 t-test reads its running `(mean, variance, n)` for free. `Response.Components.Aggregations[i].Operator` mean / variance triples are byte-equal to the standalone `TEST_WELCH` numerators on the same inputs; same goes for `OVERLAY_T_CELL` / `OVERLAY_Z_CELL` on crosstab cells. See `aggregation-design`.

## Gotchas

- Tier-2 `field` names match the aggregator's projected column (`AGG_<TYPE>_<field>`); aliases not honored in output schema today.
- Tier-2 `TEST_ANOVA_WELCH` needs `params.n_col` + `params.variance_col` upstream.
- Tier-2 `TEST_TUKEY_HSD` requires `params.ms_within` + `params.df_within` from a preceding tier-1 ANOVA.
- Tiny groups → unstable p; gate with `AGG_COUNT` and the `PULSE_TEST_INSUFFICIENT_N` floor.
- `PULSE_TEST_VARIANCE_ZERO`: constant field within a split group breaks correlation and t-tests.

## See

- Recipes: `pulse_examples_search tags=["ab-test"]`, `tags=["anova"]`, `tags=["correlation"]`, `tags=["nonparametric"]` plus atomic `op-test-<name>`.
- `aggregation-design` — Welford-triple reuse + `MetaAggregator` contract.
- `regression-modeling` — Wald-z vs Student-t inference inside REG_*.
- `overlay-system` — crosstab cell-level stat overlays.
- `request-envelope` — slot keys, streamability rules.
- `error-code-reference` — `PULSE_TEST_*` recovery steps.
