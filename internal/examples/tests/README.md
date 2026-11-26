# Statistical Testing Examples

One JSON request per supported `TEST_*` operator. Every example resolves
its cohort from the shared `.data` directory and assumes
`./internal/examples/fixtures/build.sh` has been run.

| File | Tier | Operator | Demonstrates |
|---|---|---|---|
| `01_t_test_one_sample.json` | 1 | `TEST_T` (one-sample) | mean of `revenue` against a hypothesized μ=100 |
| `02_t_test_two_sample.json` | 1 | `TEST_T` (Welch two-sample) | revenue lift from a planted treatment effect |
| `03_welch_alias.json` | 1 | `TEST_WELCH` | the explicit two-sample alias, used here on `session_minutes` |
| `04_chi_square_independence.json` | 1 | `TEST_CHISQ` | a *rejected* test (segment × converted, dependent by construction) and an *accepted* test (region × converted, independent) in one request |
| `05_anova_f_streaming.json` | 1 | `TEST_ANOVA_F` | regional revenue differences across 4 groups |
| `06_ks_two_sample.json` | 1 | `TEST_KS` | distribution-shape change on `session_minutes` even when mean tests stay ambiguous |
| `07_combined_battery.json` | 1 | three tests | t-test + ANOVA + chi-square in a single Process call |
| `08_post_anova_on_grouped_means.json` | 2 | `TEST_ANOVA_F` (post) | re-running ANOVA over per-region summary rows produced by aggregators |
| `09_post_trend_on_dates.json` | 2 | `TEST_TREND` | Mann-Kendall over the weekly revenue-to-baseline ratio (`AGG_RATIO`) — picks up the planted upward drift |
| `10_post_trend_on_window.json` | 2 | `TEST_TREND` | trend test on a 7-day moving average; smoothing makes the points dependent, so its p-value is too small (test the raw series, as 09 does) |
| `11_pearson_correlation.json` | 1 | `TEST_PEARSON_R` | linear correlation between revenue and revenue_before (paired by construction) |
| `12_paired_t_before_after.json` | 1 | `TEST_PAIRED_T` | paired-sample t on per-unit revenue lift |
| `13_two_proportion_z.json` | 1 | `TEST_PROP_Z` | conversion-rate lift between control and variant |
| `14_parametric_battery.json` | 1 | four tests | Welch t + paired t + two-proportion z + Pearson r in one Process call |
| `15_mann_whitney_revenue.json` | 1 | `TEST_MANN_WHITNEY_U` | nonparametric two-sample on revenue split by treatment |
| `16_kruskal_wallis_regions.json` | 1 | `TEST_KRUSKAL_WALLIS` | nonparametric k-group on revenue across regions |
| `17_wilcoxon_paired.json` | 1 | `TEST_WILCOXON_SR` | nonparametric paired test on revenue vs revenue_before |
| `18_spearman_correlation.json` | 1 | `TEST_SPEARMAN_R` | rank Pearson on the pre/post pair |
| `19_kendall_tau.json` | 1 | `TEST_KENDALL_TAU` | concordance-based correlation on the same pair |
| `20_nonparametric_battery.json` | 1 | five tests | Mann-Whitney + Kruskal-Wallis + Wilcoxon + Spearman + Kendall in one Process call |
| `21_welch_anova_unequal_var.json` | 1 | `TEST_ANOVA_WELCH` | heteroscedasticity-robust one-way ANOVA on revenue × region |
| `22_brown_forsythe_homogeneity.json` | 1 | `TEST_BROWN_FORSYTHE` | variance-homogeneity gate pre-flight for ANOVA |
| `23_rm_anova_three_conditions.json` | 1 | `TEST_ANOVA_RM` | repeated-measures ANOVA on a 40-subject × 3-condition cohort |
| `24_anova_robustness_battery.json` | 1 | three ANOVA variants | classical F + Welch F + Brown-Forsythe side by side on revenue × region |
| `25_tukey_hsd_after_anova.json` | 2 | `TEST_TUKEY_HSD` | pairwise Tukey HSD over per-region means, `ms_within` / `df_within` copied from the ungrouped `TEST_ANOVA_F` of 05 (a grouped request runs tier-1 tests once per group) |
| `26_fisher_exact_small_sample.json` | 1 | `TEST_CHISQ` + `TEST_FISHER_EXACT` | a 31-row subgroup (segment_a, north) with an expected count below 5; chi-square and Fisher exact side by side |
| `28_z_two_sample.json` | 1 | `TEST_Z_TWO_SAMPLE` + `TEST_WELCH` | the large-sample z beside Welch's t on revenue by treatment |
| `27_shapiro_wilk_normality.json` | 1 | `TEST_SHAPIRO_WILK` | per-treatment normality check on the log-normal revenue field |
| `29_hotelling_t2_profile.json` | 1 | `TEST_HOTELLING_T2` | revenue and session_minutes as one profile: two-sample by treatment on a named vector, one-sample against a benchmark `mu` |
| `30_hotelling_t2_paired_unequal.json` | 1 | `TEST_HOTELLING_T2` | paired form (`fields2`, the row-wise difference vector against `mu`) on revenue vs its baseline, and two-sample `variance: unequal` (Krishnamoorthy–Yu) on the engagement profile by treatment |
| `31_manova_profile_by_region.json` | 1 | `TEST_MANOVA` | one-way MANOVA of the revenue_before / revenue / session_minutes profile across the four regions; Pillai headline, Wilks / Hotelling–Lawley / Roy in details |
| `32_bartlett_sphericity_factorable.json` | 1 | `TEST_BARTLETT_SPHERICITY` | is the revenue_before / revenue / session_minutes set correlated enough to factor; bit-equal to `MAT_PCA`'s Bartlett scalars |
| `33_box_m_covariance_by_region.json` | 1 | `TEST_BOX_M` | do the four regions share one covariance across the engagement profile (the MANOVA assumption check); chi-square headline, F form in details |
| `34_mardia_joint_normality.json` | 1 | `TEST_MARDIA` | Mardia's joint-normality check on the revenue_before / revenue / session_minutes profile; skewness χ² headline, kurtosis z in details; buffered |
| `35_prop_z_on_flag.json` | 1 | `TEST_PROP_Z` | conversion stored as a `packed_bool` flag on `subscribers.pulse`: `success: "1"` names the converted level, `split_by` the arm — no yes/no twin column |
| `36_chisq_on_flag.json` | 1 | `TEST_CHISQ` | plan × a `packed_bool` churn flag on `subscribers.pulse`, the flag read directly as levels `"0"` / `"1"` |
| `37_ab_readout.json` | 1 | `TEST_CHISQ` + `TEST_PROP_Z` + `TEST_WELCH` | the A/B readout in one streaming request with no aggregation: goodness-of-fit sample-ratio check (`params.expected`, kept raw), conversion and CUPED revenue lifts read variant − control (`params.reference`, `params.covariate`), Holm-corrected together |
| `38_union_source_vs_twin.json` | 1 | `TEST_KS` + `TEST_CHISQ` | a real cohort and its synthetic twin (`customers_twin.pulse`, written by `build.sh` with `synth from-profile --synthetic-only`) read as ONE union; both tests split by the virtual `_source` field |

## Cohort

Most examples read `experiment.pulse`, a 400-row A/B test cohort
generated by `internal/examples/fixtures/gen.go`; 23 reads
`repeated_measures.pulse`, 35 and 36 `subscribers.pulse`, and 38
`customers.pulse` with its twin (see [`../README.md`](../README.md)).
`experiment.pulse`'s fields:

| Field | Type | Notes |
|---|---|---|
| `id` | u32 | row id |
| `treatment` | categorical_u8 | `control`, `variant` — variant has a planted ~22% revenue lift and an asymmetric right-tail shift on `session_minutes` |
| `region` | categorical_u8 | `north`, `south`, `east`, `west` — planted ordered revenue lifts |
| `segment` | categorical_u8 | `segment_a`, `segment_b`, `segment_c` — planted ordered conversion rates |
| `converted` | categorical_u8 | `yes`, `no` — depends on segment (and slightly on treatment); independent of region |
| `revenue_before` | f64 | pre-treatment baseline revenue per unit (log-normal × region lift only); pairs row-wise with `revenue` for paired tests |
| `revenue` | f64 | log-normal base × region lift × treatment lift × time drift |
| `session_minutes` | f64 | half-normal; variant stretches the right tail |
| `period` | date | 90 consecutive days starting 2026-01-01 |

The generator is portable and deterministic: re-running
`go run ./internal/examples/fixtures/cmd/genfixtures` produces
byte-identical CSV on every architecture
(`TestFixtures_RegenerateByteIdentical` pins it).

## Running

```
# one-time setup
make build
./internal/examples/fixtures/build.sh

# every example
./internal/examples/tests/run-all.sh

# one example
bin/pulse api process --request internal/examples/tests/02_t_test_two_sample.json --json
bin/pulse api predict --request internal/examples/tests/02_t_test_two_sample.json --json
```
