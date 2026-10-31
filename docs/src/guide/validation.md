# How Pulse is validated

<!-- docgen:preserve begin validation-intro -->
Pulse checks its inferential figures (test statistics, p-values, intervals, regression coefficients and standard errors, matrix results and significance overlays) against established external implementations wherever one exists. This page lists, operator by operator, what each is checked against, how tightly, and where the check lives, so you can see why to trust a number and where the evidence is thinner.

**How the checks work.**

- **R goldens.** `make reference` runs pinned R scripts (`scripts/reference/`) on R 4.6.1, with versions pinned for psych, car, ppcor, corpcor, GPArotation, sandwich, lmtest, biotools, nortest, survey, glmnet, lavaan and others. Each number is written at full precision (`%.17g`) together with the exact input data. Every golden file ends in a SHA-256 footer that the tests check, so a value cannot be edited by hand without the test failing. CI never runs R: the goldens are committed, and the Go tests compare against them on every `go test`, typically at a relative tolerance of 1e-10.
- **Weighted reference.** For weighted analysis, a separate harness (`internal/service/testdata/weight_reference/`) checks the weight-aware tests, regressions and overlays. For frequency weights it runs the stock R function on rows repeated by their weight (`rep()`). For probability weights it uses a closed form on Kish-rescaled weights with scipy tails, cross-checked with statsmodels. The tolerance is 1e-12 relative.
- **Multivariate and SEM.** Matrix operators are checked against numpy, statsmodels and the R packages named in each row. Structural equation models are checked against lavaan and cross-checked with semopy.
- **Undefined figures.** Edge cases where R returns `NaN` or `Inf` are pinned too: Pulse reports those as `null`, never as a made-up number.

**Reading a row.**

- *Validated against* names the reference and its version.
- *Tightest tolerance* is the strictest comparison the tests apply.
- *Gap* says plainly where the evidence is weaker: a loose or hand-computed check, no weighted check, or no external implementation at all.

The registry behind this page is gated: every test file it cites must exist, and every built-in test, regression, matrix operator and inferential overlay must have a row. An operator is never silently missing from this page.

To regenerate the goldens, see *Regenerating goldens* in the contributor guide.
<!-- docgen:preserve end validation-intro -->

## Aggregators

| Operator | Validated against | Tightest tolerance | Gap |
|---|---|---|---|
| [`AGG_AVERAGE`](catalog/aggregator.md#op-agg_average) | NIST StRD certified values (univariate: PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4) | digits achieved per dataset (see the NIST table) | — |
| [`AGG_WELFORD`](catalog/aggregator.md#op-agg_welford) | NIST StRD certified values (univariate sample standard deviation: PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4) | digits achieved per dataset (see the NIST table) | — |

## Statistical tests

| Operator | Validated against | Tightest tolerance | Gap |
|---|---|---|---|
| [`TEST_ANOVA_F`](catalog/test.md#op-test_anova_f) | R 4.6.1 `aov`<br>NIST StRD certified values (one-way ANOVA: SiRstv, SmLs01–09, AtmWtAg)<br>weighted reference (see above) | 1e-12 relative | On NIST SmLs07–09 the figures agree to about 4 digits. That is the ceiling for any double-precision package: values such as 1000000000000.4 are not exactly representable, and exact arithmetic on the parsed doubles reaches only 4.0, 3.9 and 3.9 digits. Pulse reaches that ceiling. |
| [`TEST_ANOVA_RM`](catalog/test.md#op-test_anova_rm) | R 4.6.1 `aov` with `Error()` strata | 1e-10 relative | No weighted reference. |
| [`TEST_ANOVA_WELCH`](catalog/test.md#op-test_anova_welch) | R 4.6.1 `oneway.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_BARTLETT_SPHERICITY`](catalog/test.md#op-test_bartlett_sphericity) | R 4.6.1 `psych::cortest.bartlett`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_BOX_M`](catalog/test.md#op-test_box_m) | R 4.6.1 `biotools::boxM`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_BROWN_FORSYTHE`](catalog/test.md#op-test_brown_forsythe) | R 4.6.1 `anova(lm(abs(x - median) ~ g))`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_CHISQ`](catalog/test.md#op-test_chisq) | weighted reference (see above)<br>Agresti worked example (R `chisq.test` help) and scipy for the effect size | 1e-12 relative | The unweighted statistic is pinned to hand-computed values, not to an R golden file. |
| [`TEST_FISHER_EXACT`](catalog/test.md#op-test_fisher_exact) | R 4.6.1 `fisher.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_HOTELLING_T2`](catalog/test.md#op-test_hotelling_t2) | R 4.6.1 `Hotelling::hotelling.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_KENDALL_TAU`](catalog/test.md#op-test_kendall_tau) | R 4.6.1 `cor.test(method = "kendall")`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_KRUSKAL_WALLIS`](catalog/test.md#op-test_kruskal_wallis) | weighted reference (see above)<br>R 4.6.1 `kruskal.test` help examples (effect size) | 1e-12 relative | The unweighted statistic and p-value are pinned to hand-computed values, not to an R golden file. |
| [`TEST_KS`](catalog/test.md#op-test_ks) | R 4.6.1 `ks.test` (exact `psmirnov`)<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_MANN_WHITNEY_U`](catalog/test.md#op-test_mann_whitney_u) | weighted reference (see above)<br>R 4.6.1 `wilcox.test` help examples (effect size) | 1e-12 relative | The unweighted statistic and p-value are pinned to hand-computed values, not to an R golden file. |
| [`TEST_MANOVA`](catalog/test.md#op-test_manova) | R 4.6.1 `manova` with `summary.manova`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_MARDIA`](catalog/test.md#op-test_mardia) | R 4.6.1 Mardia skewness and kurtosis<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_PAIRED_T`](catalog/test.md#op-test_paired_t) | R 4.6.1 `t.test(paired = TRUE)`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_PEARSON_R`](catalog/test.md#op-test_pearson_r) | R 4.6.1 `cor.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_PROP_Z`](catalog/test.md#op-test_prop_z) | weighted reference (see above)<br>textbook worked example | 1e-12 relative | — |
| [`TEST_SHAPIRO_WILK`](catalog/test.md#op-test_shapiro_wilk) | R 4.6.1 `nortest::sf.test` (Shapiro–Francia) | 1e-10 relative | No weighted reference. |
| [`TEST_SPEARMAN_R`](catalog/test.md#op-test_spearman_r) | R 4.6.1 `cor.test(method = "spearman")` (exact AS 89)<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_T`](catalog/test.md#op-test_t) | weighted reference (see above)<br>scipy `ttest_ind` | 1e-12 relative | The unweighted check is a 4-decimal scipy value; the tight pin is the weighted reference. |
| [`TEST_TREND`](catalog/test.md#op-test_trend) | R 4.6.1 `cor.test(method = "kendall")`, cross-checked with `Kendall::MannKendall` | 1e-10 relative | — |
| [`TEST_TUKEY_HSD`](catalog/test.md#op-test_tukey_hsd) | R 4.6.1 `TukeyHSD`, `ptukey` refined by quadrature | 1e-10 relative | — |
| [`TEST_WELCH`](catalog/test.md#op-test_welch) | weighted reference (see above)<br>scipy `ttest_ind(equal_var = False)` | 1e-12 relative | The unweighted check is a 4-decimal scipy value; the tight pin is the weighted reference. |
| [`TEST_WILCOXON_SR`](catalog/test.md#op-test_wilcoxon_sr) | R 4.6.1 `wilcox.test(paired = TRUE)`<br>weighted reference (see above) | 1e-12 relative | — |
| [`TEST_Z_TWO_SAMPLE`](catalog/test.md#op-test_z_two_sample) | weighted reference (see above) | 1e-12 relative | No unweighted external golden; the unweighted path is checked against Pulse's own Welch test. |

## Regressions

| Operator | Validated against | Tightest tolerance | Gap |
|---|---|---|---|
| [`REG_BAYES_LINEAR`](catalog/regression.md#op-reg_bayes_linear) | R 4.6.1 matrix algebra for the normal-inverse-gamma posterior<br>weighted reference (see above) | 1e-12 relative | — |
| [`REG_GLM`](catalog/regression.md#op-reg_glm) | R 4.6.1 `glm` (binomial-logit, poisson-log, gamma-inverse)<br>R 4.6.1 `glmnet` (penalties)<br>R 4.6.1 `sandwich` (robust standard errors; vcov)<br>weighted reference (see above) | 1e-12 relative | Only the canonical link of each family is implemented, so only those are validated. |
| [`REG_MMM_BAYES`](catalog/regression.md#op-reg_mmm_bayes) | synthetic data with known true parameters (internal/mmmfixture) | parameter recovery | No external reference implementation; the fit is checked by recovering known parameters only. |
| [`REG_MMM_HILL`](catalog/regression.md#op-reg_mmm_hill) | synthetic data with known true parameters (internal/mmmfixture) | 0.5% to 15% relative, per parameter | No external reference implementation; the fit is checked by recovering known parameters only. |
| [`REG_OLS`](catalog/regression.md#op-reg_ols) | NIST StRD certified values (linear regression: Norris, Pontius, Longley, Wampler1–5)<br>R 4.6.1 `lm`<br>R 4.6.1 `glmnet` (penalties), `nnls` (non-negative)<br>R 4.6.1 `sandwich` / `lmtest` (HC0–HC3, cluster, HAC standard errors; vcov)<br>weighted reference (see above) | 1e-12 relative | NIST Filip (degree-10 polynomial) is refused as rank-deficient, and NoInt1 / NoInt2 cannot run: REG_OLS has no regression through the origin. |

## Matrix operators

| Operator | Validated against | Tightest tolerance | Gap |
|---|---|---|---|
| [`MAT_COLLINEARITY`](catalog/matrix.md#op-mat_collinearity) | R 4.6.1 `car::vif`, `perturb::colldiag` | 1e-9 relative | — |
| [`MAT_CORRELATION`](catalog/matrix.md#op-mat_correlation) | numpy / statsmodels<br>R 4.6.1 `cor` and `cor.test` (p-values, intervals) | 1e-12 relative | — |
| [`MAT_COVARIANCE`](catalog/matrix.md#op-mat_covariance) | numpy `cov` / statsmodels `DescrStatsW` | 1e-12 relative | — |
| [`MAT_DISTANCE`](catalog/matrix.md#op-mat_distance) | R 4.6.1 `dist` | 1e-9 relative | — |
| [`MAT_FACTOR`](catalog/matrix.md#op-mat_factor) | R 4.6.1 `psych::fa`, `GPArotation` | 1e-9 exact paths; 1e-4 to 2e-3 vs psych iterations | — |
| [`MAT_PARTIAL_CORRELATION`](catalog/matrix.md#op-mat_partial_correlation) | R 4.6.1 `ppcor` / `corpcor` | 1e-10 relative | — |
| [`MAT_PCA`](catalog/matrix.md#op-mat_pca) | R 4.6.1 `eigen` / `prcomp`, `psych::KMO` | 1e-9 relative | — |
| [`MAT_RELIABILITY`](catalog/matrix.md#op-mat_reliability) | R 4.6.1 `psych::alpha`, `psych::omega` | 1e-10 relative; omega 1e-5 | — |
| [`MAT_SEM`](catalog/matrix.md#op-mat_sem) | R 4.6.1 `lavaan` (ML, WLSMV, DWLS, bootstrap, multi-group), cross-checked with semopy | estimates 1e-4, fit indices 1e-3 | — |

## Overlays

| Operator | Validated against | Tightest tolerance | Gap |
|---|---|---|---|
| [`OVERLAY_CHISQ_COL`](catalog/overlay.md#op-overlay_chisq_col) | weighted reference (see above) | 1e-12 relative | The unweighted path has internal checks only; the R pin is the frequency-weighted reference. |
| [`OVERLAY_CHISQ_MATRIX`](catalog/overlay.md#op-overlay_chisq_matrix) | weighted reference (see above) | 1e-12 relative | The unweighted path has internal checks only; the R pin is the frequency-weighted reference. |
| [`OVERLAY_CHISQ_ROW`](catalog/overlay.md#op-overlay_chisq_row) | weighted reference (see above) | 1e-12 relative | The unweighted path has internal checks only; the R pin is the frequency-weighted reference. |
| [`OVERLAY_CHISQ_VS_POP`](catalog/overlay.md#op-overlay_chisq_vs_pop) | R 4.6.1 `chisq.test` | 1e-10 relative | — |
| [`OVERLAY_CHISQ_VS_REF`](catalog/overlay.md#op-overlay_chisq_vs_ref) | R 4.6.1 `chisq.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_FISHER_EXACT_CELL`](catalog/overlay.md#op-overlay_fisher_exact_cell) | R 4.6.1 `fisher.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_KS_VS_POP`](catalog/overlay.md#op-overlay_ks_vs_pop) | R 4.6.1 `ks.test` / `psmirnov` | 1e-10 relative | — |
| [`OVERLAY_PAIRWISE_PROBIT_T`](catalog/overlay.md#op-overlay_pairwise_probit_t) | R 4.6.1 evaluation of the design formula | 1e-10 relative | No stock implementation exists to compare with; the reference evaluates the published formula. |
| [`OVERLAY_PAIRWISE_PROP_Z`](catalog/overlay.md#op-overlay_pairwise_prop_z) | R 4.6.1 `prop.test(correct = FALSE)`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_PAIRWISE_TWO_MEANS_Z`](catalog/overlay.md#op-overlay_pairwise_two_means_z) | R 4.6.1 `pnorm` on the Welch standard error | 1e-10 relative | — |
| [`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](catalog/overlay.md#op-overlay_pairwise_weighted_two_means_z) | weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_PAIRWISE_WELCH_T`](catalog/overlay.md#op-overlay_pairwise_welch_t) | R 4.6.1 `t.test`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_PROP_Z_CELL`](catalog/overlay.md#op-overlay_prop_z_cell) | R 4.6.1 `prop.test(correct = FALSE)`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_PROP_Z_PANEL`](catalog/overlay.md#op-overlay_prop_z_panel) | R 4.6.1 `prop.test(correct = FALSE)`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_PROP_Z_VS_PRIOR`](catalog/overlay.md#op-overlay_prop_z_vs_prior) | weighted reference (see above)<br>an independent re-implementation of the pooled two-proportion z test | 1e-12 relative | — |
| [`OVERLAY_RAKE`](catalog/overlay.md#op-overlay_rake) | R 4.6.1 `loglin`, cross-checked with `survey::rake` | 1e-9 relative | — |
| [`OVERLAY_STD_RESIDUAL`](catalog/overlay.md#op-overlay_std_residual) | R 4.6.1 `chisq.test()$stdres`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_SYMMETRY`](catalog/overlay.md#op-overlay_symmetry) | R 4.6.1 `mcnemar.test` / Bowker<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_T_CELL`](catalog/overlay.md#op-overlay_t_cell) | R 4.6.1 `t.test` / `pt`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_T_VS_PRIOR`](catalog/overlay.md#op-overlay_t_vs_prior) | weighted reference (see above)<br>an independent re-implementation of the Welch t test | 1e-12 relative | — |
| [`OVERLAY_T_VS_REF`](catalog/overlay.md#op-overlay_t_vs_ref) | R 4.6.1 `t.test` / `pt`<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_Z_CELL`](catalog/overlay.md#op-overlay_z_cell) | R 4.6.1 `pnorm` on the Welch standard error<br>weighted reference (see above) | 1e-12 relative | — |
| [`OVERLAY_Z_VS_PRIOR`](catalog/overlay.md#op-overlay_z_vs_prior) | weighted reference (see above)<br>an independent re-implementation of the Welch z test | 1e-12 relative | — |
| [`OVERLAY_Z_VS_REF`](catalog/overlay.md#op-overlay_z_vs_ref) | R 4.6.1 `pnorm` on the Welch standard error<br>weighted reference (see above) | 1e-12 relative | — |

## NIST StRD certification

The [NIST Statistical Reference Datasets](https://www.itl.nist.gov/div898/strd/) carry certified values to 15 significant digits and are the accuracy benchmark statistical packages are measured against. Each dataset runs end to end through Pulse; a figure's *digits* are its log relative error against the certified value, LRE = −log10(|x − c| / |c|), floored to 0.1 and capped at 15. Roughly, 15 is exact in double precision, 7 or more is accurate for any practical use, and under 4 means the figure is unreliable on that data. Lower- and average-difficulty sets must reach 7 digits on every figure; higher-difficulty sets are published as achieved. These numbers are the record the tests assert (`strd_reference_test.go`), so any change in accuracy fails the build until it is reviewed.

### Linear regression (`REG_OLS`)

| Dataset | NIST difficulty | Fewest digits | Figures (digits) | Note |
|---|---|---|---|---|
| Norris | lower | 10.7 | `B0` 12.4, `B1` 14.3, `SE(B0)` 10.7, `SE(B1)` 10.7, `r2` 15.0, `residual_sd` 10.7 | — |
| Pontius | lower | 8.5 | `B0` 11.2, `B1` 14.1, `B2` 12.2, `SE(B0)` 8.5, `SE(B1)` 8.5, `SE(B2)` 8.5, `r2` 15.0, `residual_sd` 8.5 | — |
| NoInt1 | average | — | — | regression through the origin (no intercept) is not offered by REG_OLS |
| NoInt2 | average | — | — | regression through the origin (no intercept) is not offered by REG_OLS |
| Filip | higher | — | — | REG_OLS refused the fit: PROCESSING_REGRESSION_RANK_DEFICIENT |
| Longley | higher | 11.5 | `B0` 12.6, `B1` 11.5, `B2` 12.0, `B3` 12.6, `B4` 12.9, `B5` 11.5, `B6` 12.6, `SE(B0)` 13.2, `SE(B1)` 14.1, `SE(B2)` 12.8, `SE(B3)` 12.9, `SE(B4)` 13.1, `SE(B5)` 13.2, `SE(B6)` 13.3, `r2` 15.0, `residual_sd` 12.8 | — |
| Wampler1 | higher | 6.7 | `B0` 6.9, `B1` 6.7, `B2` 7.1, `B3` 8.0, `B4` 9.3, `B5` 11.0, `SE(B0)` 15.0, `SE(B1)` 15.0, `SE(B2)` 15.0, `SE(B3)` 15.0, `SE(B4)` 15.0, `SE(B5)` 15.0, `r2` 15.0, `residual_sd` 15.0 | — |
| Wampler2 | higher | 6.4 | `B0` 12.1, `B1` 10.8, `B2` 10.2, `B3` 10.0, `B4` 10.3, `B5` 11.0, `SE(B0)` 6.4, `SE(B1)` 6.4, `SE(B2)` 6.9, `SE(B3)` 7.8, `SE(B4)` 9.0, `SE(B5)` 10.7, `r2` 15.0, `residual_sd` 6.4 | — |
| Wampler3 | higher | 6.3 | `B0` 6.6, `B1` 6.3, `B2` 6.7, `B3` 7.6, `B4` 8.9, `B5` 10.6, `SE(B0)` 10.2, `SE(B1)` 10.1, `SE(B2)` 10.1, `SE(B3)` 10.1, `SE(B4)` 10.1, `SE(B5)` 10.1, `r2` 15.0, `residual_sd` 10.3 | — |
| Wampler4 | higher | 6.7 | `B0` 6.9, `B1` 6.7, `B2` 7.1, `B3` 8.0, `B4` 9.3, `B5` 11.0, `SE(B0)` 11.3, `SE(B1)` 10.7, `SE(B2)` 10.6, `SE(B3)` 10.5, `SE(B4)` 10.5, `SE(B5)` 10.6, `r2` 15.0, `residual_sd` 14.8 | — |
| Wampler5 | higher | 6.0 | `B0` 6.2, `B1` 6.0, `B2` 6.4, `B3` 7.3, `B4` 8.5, `B5` 10.2, `SE(B0)` 11.3, `SE(B1)` 10.7, `SE(B2)` 10.6, `SE(B3)` 10.5, `SE(B4)` 10.5, `SE(B5)` 10.6, `r2` 13.7, `residual_sd` 14.8 | — |

### One-way ANOVA (`TEST_ANOVA_F`)

| Dataset | NIST difficulty | Fewest digits | Figures (digits) | Note |
|---|---|---|---|---|
| SiRstv | lower | 13.0 | `f` 13.0, `ms_between` 14.0, `ms_within` 13.1, `r2` 13.1, `residual_sd` 13.4, `ss_between` 14.0, `ss_within` 13.1 | — |
| SmLs01 | lower | 15.0 | `f` 15.0, `ms_between` 15.0, `ms_within` 15.0, `r2` 15.0, `residual_sd` 15.0, `ss_between` 15.0, `ss_within` 15.0 | — |
| SmLs02 | lower | 15.0 | `f` 15.0, `ms_between` 15.0, `ms_within` 15.0, `r2` 15.0, `residual_sd` 15.0, `ss_between` 15.0, `ss_within` 15.0 | — |
| SmLs03 | lower | 14.9 | `f` 14.9, `ms_between` 15.0, `ms_within` 15.0, `r2` 15.0, `residual_sd` 15.0, `ss_between` 15.0, `ss_within` 15.0 | — |
| AtmWtAg | average | 10.1 | `f` 10.1, `ms_between` 10.2, `ms_within` 10.9, `r2` 10.2, `residual_sd` 11.2, `ss_between` 10.2, `ss_within` 10.9 | — |
| SmLs04 | average | 10.0 | `f` 10.4, `ms_between` 10.0, `ms_within` 10.2, `r2` 10.7, `residual_sd` 10.5, `ss_between` 10.0, `ss_within` 10.2 | — |
| SmLs05 | average | 9.9 | `f` 10.2, `ms_between` 9.9, `ms_within` 10.2, `r2` 10.4, `residual_sd` 10.5, `ss_between` 9.9, `ss_within` 10.2 | — |
| SmLs06 | average | 9.9 | `f` 10.1, `ms_between` 9.9, `ms_within` 10.2, `r2` 10.4, `residual_sd` 10.5, `ss_between` 9.9, `ss_within` 10.2 | — |
| SmLs07 | higher | 4.0 | `f` 4.4, `ms_between` 4.0, `ms_within` 4.2, `r2` 4.6, `residual_sd` 4.5, `ss_between` 4.0, `ss_within` 4.2 | — |
| SmLs08 | higher | 3.9 | `f` 4.1, `ms_between` 3.9, `ms_within` 4.2, `r2` 4.4, `residual_sd` 4.5, `ss_between` 3.9, `ss_within` 4.2 | — |
| SmLs09 | higher | 3.9 | `f` 4.1, `ms_between` 3.9, `ms_within` 4.2, `r2` 4.4, `residual_sd` 4.5, `ss_between` 3.9, `ss_within` 4.2 | — |

### Univariate summary statistics (`AGG_AVERAGE`, `AGG_WELFORD`)

| Dataset | NIST difficulty | Fewest digits | Figures (digits) | Note |
|---|---|---|---|---|
| PiDigits | lower | 14.9 | `mean` 15.0, `sd` 14.9 | lag-1 autocorrelation: no built-in aggregator computes it |
| Lottery | lower | 15.0 | `mean` 15.0, `sd` 15.0 | lag-1 autocorrelation: no built-in aggregator computes it |
| Lew | lower | 15.0 | `mean` 15.0, `sd` 15.0 | lag-1 autocorrelation: no built-in aggregator computes it |
| Mavro | lower | 12.0 | `mean` 15.0, `sd` 12.0 | lag-1 autocorrelation: no built-in aggregator computes it |
| Michelso | not stated | 12.3 | `mean` 15.0, `sd` 12.3 | lag-1 autocorrelation: no built-in aggregator computes it |
| NumAcc1 | lower | 15.0 | `mean` 15.0, `sd` 15.0 | lag-1 autocorrelation: no built-in aggregator computes it |
| NumAcc2 | average | 15.0 | `mean` 15.0, `sd` 15.0 | lag-1 autocorrelation: no built-in aggregator computes it |
| NumAcc3 | average | 9.4 | `mean` 15.0, `sd` 9.4 | lag-1 autocorrelation: no built-in aggregator computes it |
| NumAcc4 | higher | 8.2 | `mean` 15.0, `sd` 8.2 | lag-1 autocorrelation: no built-in aggregator computes it |
