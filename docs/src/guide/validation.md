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

## Statistical tests

| Operator | Validated against | Tightest tolerance | Gap |
|---|---|---|---|
| [`TEST_ANOVA_F`](catalog/test.md#op-test_anova_f) | R 4.6.1 `aov`<br>weighted reference (see above) | 1e-12 relative | — |
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
| [`REG_OLS`](catalog/regression.md#op-reg_ols) | R 4.6.1 `lm`<br>R 4.6.1 `glmnet` (penalties), `nnls` (non-negative)<br>R 4.6.1 `sandwich` / `lmtest` (HC0–HC3, cluster, HAC standard errors; vcov)<br>weighted reference (see above) | 1e-12 relative | — |

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
