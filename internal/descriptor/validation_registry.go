package descriptor

// validation_registry.go is the reference-validation record: which
// external implementation each inferential operator is pinned to, at
// what tolerance, and by which test file. It is DECLARATION, read by two
// surfaces that never push it: internal/docgen renders it into the
// book's "How Pulse is validated" page and each catalog entry's
// "Validated against" list, and Explain's operator mode
// (InstanceSnapshot.Validation) returns it on request. No manifest or
// default payload carries it (TestManifestGuidanceBudget).
//
// The registry is hand-maintained beside the tests it cites. Its gates
// (validation_test.go) hold it honest: every key is a built-in
// operator, every built-in test, regression, matrix operator and
// inferential overlay has an entry (one with no reference declares its
// Gap, so it is never silently missing), and every cited test file
// exists and names the operator, directly or through the golden it
// reads. A claim with no harness behind it is not published.

// ValidationRef is one external check of an operator.
type ValidationRef struct {
	// Against names the reference implementation and its pinned version.
	Against string
	// Tol is the comparison tolerance, as the tests apply it.
	Tol string
	// Tests are the repo-relative test files that hold the check.
	Tests []string
	// Weighted marks a weighted-reference check; it is dropped on an
	// instance that does not offer weighting.
	Weighted bool
}

// ValidationRecord is one operator's record.
type ValidationRecord struct {
	Refs []ValidationRef
	// Gap is a known validation gap in plain words; "" when none.
	Gap string
	// WeightRefused names the tests pinning that a weight on the operator
	// is refused (PULSE_WEIGHT_UNSUPPORTED, the permanent reason from
	// OperatorWeightRefusal) at predict and at run, never ignored: the
	// stated alternative to a weighted reference for an operator with no
	// weighted form. Dropped, like a Weighted reference, when the
	// instance does not offer weighting.
	WeightRefused []string
	// PostTestOnly marks an operator that runs only as a post-test: its
	// weight refusal is the shared post-test one
	// (PostTestWeightRefusal), not an operator reason.
	PostTestOnly bool
}

// Shared sources and test files, named once so entries stay short.
const (
	rPin   = "R 4.6.1"
	tol10  = "1e-10 relative"
	tol12  = "1e-12 relative"
	tol9   = "1e-9 relative"
	wrVals = "internal/service/weight_reference_values_test.go"
	wrTest = "internal/service/weight_reference_tests_test.go"
	// ordOracle / ordE2E pin REG_ORDINAL to ordinal.json (the design and
	// the end-to-end path).
	ordOracle = "internal/regordinal/oracle_test.go"
	ordE2E    = "internal/regordinal/ordinal_e2e_test.go"
	// wRefusal pins the permanent weight refusals (WeightRefused).
	wRefusal = "internal/integration/weight_refusal_pin_test.go"

	procOutput = "internal/processing/test_output_oracle_test.go"
	procUnw    = "internal/processing/test_unweighted_oracle_test.go"
	procPost   = "internal/processing/test_post_oracle_test.go"
	procUndef  = "internal/processing/test_undefined_oracle_test.go"
	ovOracle   = "internal/processing/overlay_reference_oracle_test.go"
	// ovChiSqAxes is the chi-square row / column / matrix overlay oracle.
	ovChiSqAxes = "internal/processing/overlay_chisq_axes_oracle_test.go"
	ovPairwise  = "internal/processing/overlay_pairwise_test.go"
	vsPrior     = "internal/integration/overlay_vs_prior_test.go"
	svcCov      = "internal/service/regression_covariance_test.go"
	svcCovGLM   = "internal/service/regression_covariance_glm_test.go"
	svcVcov     = "internal/service/regression_vcov_test.go"
	regDir      = "internal/processing/regression/"
	// svcCompareOracle is the nested-model comparison's R oracle test.
	svcCompareOracle = "internal/service/compose_compare_oracle_test.go"
	strdTest         = "strd_reference_test.go"
	mmmSynthSrc      = "synthetic data with known true parameters (internal/mmmfixture)"
)

// valWeighted is the weighted-reference check shared by most inferential
// operators: the stock R function on rep()-expanded rows for frequency
// weights, and a closed form on Kish-rescaled weights with scipy tails
// for probability weights where the operator accepts them.
var valWeighted = ValidationRef{
	Against:  rPin + " on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights)",
	Tol:      tol12,
	Tests:    []string{wrVals, wrTest},
	Weighted: true,
}

// valStrd is a NIST StRD certification check; its digits achieved are
// published in the StRD table on validation.md.
func valStrd(sets string) ValidationRef {
	return ValidationRef{Against: "NIST StRD certified values (" + sets + ")", Tol: "digits achieved per dataset (see the NIST table)", Tests: []string{strdTest}}
}

// valParity is a moment overlay's byte parity with its two-sample test
// on AGG_WELFORD cells (the shared kernel, #637): the overlay inherits
// the test's R pins, large common offsets included.
func valParity(test string) ValidationRef {
	file := "internal/processing/overlay_t_cell_parity_test.go"
	if test == "TEST_Z_TWO_SAMPLE" {
		file = "internal/processing/overlay_z_cell_parity_test.go"
	}
	return ValidationRef{Against: "byte parity with `" + test + "` on AGG_WELFORD cells, offsets to 1e12 included (so its R pins carry over)", Tol: "bit-identical", Tests: []string{file}}
}

func valR(fn, tol string, tests ...string) ValidationRef {
	return ValidationRef{Against: rPin + " " + fn, Tol: tol, Tests: tests}
}

// validationRegistry maps a built-in operator name to its record.
var validationRegistry = map[string]ValidationRecord{
	// Statistical tests.
	"TEST_ANOVA_F": {
		Refs: []ValidationRef{valR("`aov`", tol10, procPost), valStrd("one-way ANOVA: SiRstv, SmLs01–09, AtmWtAg"), valWeighted},
		Gap:  "On NIST SmLs07–09 the figures agree to about 4 digits. That is the ceiling for any double-precision package: values such as 1000000000000.4 are not exactly representable, and exact arithmetic on the parsed doubles reaches only 4.0, 3.9 and 3.9 digits. Pulse reaches that ceiling.",
	},
	"TEST_ANOVA_RM":    {Refs: []ValidationRef{valR("`aov` with `Error()` strata", tol10, procOutput)}, WeightRefused: []string{wRefusal}},
	"TEST_ANOVA_WELCH": {Refs: []ValidationRef{valR("`oneway.test`", tol10, procPost), valWeighted}},
	"TEST_BARTLETT_SPHERICITY": {Refs: []ValidationRef{
		valR("`psych::cortest.bartlett`", tol9, "internal/service/test_bartlett_sphericity_test.go"), valWeighted}},
	"TEST_BOX_M":          {Refs: []ValidationRef{valR("`biotools::boxM`", tol10, "internal/processing/test_box_m_test.go"), valWeighted}},
	"TEST_BROWN_FORSYTHE": {Refs: []ValidationRef{valR("`anova(lm(abs(x - median) ~ g))`", tol10, procUndef, procPost), valWeighted}},
	"TEST_CHISQ": {Refs: []ValidationRef{
		valR("`chisq.test(correct = FALSE)` (independence; the default applies no Yates correction) and `chisq.test(p = expected)` (goodness of fit)", tol10, procUnw),
		valR("`chisq.test(correct = TRUE)` (`params.correct`: Yates on a 2 × 2 table, inert on larger ones)", tol10, "internal/processing/test_options_oracle_test.go"),
		valWeighted,
		{Against: "Agresti worked example (R `chisq.test` help) and scipy for the effect size", Tol: "1e-9 to 1e-6", Tests: []string{"internal/processing/test_more_test.go", "internal/processing/test_effect_size_test.go"}},
	}},
	"TEST_FISHER_EXACT": {Refs: []ValidationRef{valR("`fisher.test`", tol10, "internal/processing/test_fisher_oracle_test.go"), valWeighted}},
	"TEST_HOTELLING_T2": {Refs: []ValidationRef{valR("`Hotelling::hotelling.test`", tol10, "internal/processing/test_hotelling_test.go"), valWeighted}},
	"TEST_KENDALL_TAU":  {Refs: []ValidationRef{valR("`cor.test(method = \"kendall\")`", tol10, procOutput), valWeighted}},
	"TEST_KRUSKAL_WALLIS": {Refs: []ValidationRef{
		valR("`kruskal.test` (H, df, p, rank sums, tie factor); near H = 0, where `kruskal.test`'s own form cancels, H evaluated in deviation form", tol10, procUnw),
		valWeighted,
		{Against: rPin + " `kruskal.test` help examples (effect size)", Tol: tol9, Tests: []string{"internal/processing/test_effect_size_rank_test.go", "internal/processing/test_nonparametric_test.go"}},
	}},
	"TEST_KS": {Refs: []ValidationRef{valR("`ks.test` (exact `psmirnov`)", tol10, procOutput), valWeighted}},
	"TEST_MANN_WHITNEY_U": {
		Refs: []ValidationRef{
			valR("`wilcox.test(exact = FALSE, correct = TRUE)` (U, rank sum, tie-corrected variance, z, p)", tol10, procUnw),
			valR("`wilcox.test(exact = TRUE)` (`params.exact`: the permutation p on mid-ranks, ties included)", tol10, "internal/processing/test_options_oracle_test.go"),
			valWeighted,
			{Against: rPin + " `wilcox.test` help examples (effect size)", Tol: tol9, Tests: []string{"internal/processing/test_effect_size_rank_test.go", "internal/processing/test_nonparametric_test.go"}},
		},
		Gap: "The default p is the tie- and continuity-corrected normal approximation; `wilcox.test` defaults to its exact p when both groups are under 50, so ask for `params.exact` to match it there. The exact p is computed while min(n_a, n_b)·(n_a + n_b) ≤ 5000; past that the approximation stays, with a warning.",
	},
	"TEST_MANOVA":    {Refs: []ValidationRef{valR("`manova` with `summary.manova`", tol10, "internal/processing/test_manova_test.go"), valWeighted}},
	"TEST_MARDIA":    {Refs: []ValidationRef{valR("Mardia skewness and kurtosis", tol10, "internal/processing/test_mardia_test.go"), valWeighted}},
	"TEST_PAIRED_T":  {Refs: []ValidationRef{valR("`t.test(paired = TRUE)`", tol10, procPost), valWeighted}},
	"TEST_PEARSON_R": {Refs: []ValidationRef{valR("`cor.test`", tol10, procOutput, procPost), valWeighted}},
	"TEST_PROP_Z": {Refs: []ValidationRef{
		valR("`prop.test(correct = FALSE)` (z as the signed root of X-squared, p; the two-proportion Wald interval). The default one-proportion interval is Pulse's Wald interval on p − p0, evaluated in R", tol10, procUnw),
		valR("`prop.test(correct = TRUE)` and its Wilson interval, with and without the correction (`params.correct`, `params.interval` `wilson`)", tol10, "internal/processing/test_options_oracle_test.go"),
		valWeighted,
		{Against: "textbook worked example", Tol: tol9, Tests: []string{"internal/processing/test_parametric_test.go"}},
	}},
	"TEST_SHAPIRO_WILK": {Refs: []ValidationRef{valR("`nortest::sf.test` (Shapiro–Francia)", tol10, procUndef)}, WeightRefused: []string{wRefusal}},
	"TEST_SPEARMAN_R":   {Refs: []ValidationRef{valR("`cor.test(method = \"spearman\")` (exact AS 89)", "1e-12 relative", "internal/processing/test_nonparametric_test.go", procPost), valWeighted}},
	"TEST_T": {Refs: []ValidationRef{valR("`t.test` (one-sample on `mu`; two-sample Welch, `var.equal = FALSE`, including common offsets of 1e6 to 1e12 tested on the exact deviations): t, df, p, interval, means, variances", tol10, procUnw),
		valR("`t.test(var.equal = TRUE)` (`params.variance` `pooled`): t, df, p, interval, pooled variance", tol10, "internal/processing/test_options_oracle_test.go"), valWeighted}},
	"TEST_TREND":        {Refs: []ValidationRef{valR("`cor.test(method = \"kendall\")`, cross-checked with `Kendall::MannKendall`", tol10, procOutput)}, WeightRefused: []string{wRefusal}, PostTestOnly: true},
	"TEST_TUKEY_HSD":    {Refs: []ValidationRef{valR("`TukeyHSD`, `ptukey` refined by quadrature", tol10, procOutput)}, WeightRefused: []string{wRefusal}, PostTestOnly: true},
	"TEST_WELCH":        {Refs: []ValidationRef{valR("`t.test(var.equal = FALSE)` (including common offsets of 1e6 to 1e12 tested on the exact deviations): t, Welch–Satterthwaite df, p, interval, means, variances", tol10, procUnw), valWeighted}},
	"TEST_WILCOXON_SR":  {Refs: []ValidationRef{valR("`wilcox.test(paired = TRUE)`", tol10, procPost), valWeighted}},
	"TEST_Z_TWO_SAMPLE": {Refs: []ValidationRef{valR("`pnorm` / `qnorm` on the Welch standard error (no stock R function; common offsets of 1e6 to 1e12 on the exact deviations): z, p, interval, means, variances", tol10, procUnw), valWeighted}},

	// Descriptive operators with a certified reference.
	"AGG_AVERAGE": {Refs: []ValidationRef{valStrd("univariate: PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4")}},
	"AGG_STDDEV":  {Refs: []ValidationRef{valStrd("univariate sample standard deviation, `params.ddof` 1: PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4")}},
	"AGG_WELFORD": {Refs: []ValidationRef{valStrd("univariate sample standard deviation (the `stddev` component): PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4"),
		{Against: "exact rational arithmetic on the input doubles: mean, shifted_mean and m2 of three merged partials under common offsets up to 1e12, unweighted and frequency-weighted", Tol: "1e-13 relative", Tests: []string{"internal/processing/aggregator_welford_offset_test.go"}}}},

	// Per-row regression scores with an external reference.
	"ATTR_REG_RESIDUAL": {Refs: []ValidationRef{{
		Against: "statsmodels 0.14.5 `GLMResults.resid_response` / `resid_pearson` / `resid_deviance` / `resid_working` (binomial-logit, poisson-log, gamma-inverse fits through `params.fit`, `params.kind`)",
		Tol:     "1e-8 relative", Tests: []string{"glm_residual_kinds_reference_test.go"},
	}, {
		Against: "statsmodels 0.14.5 `OLSInfluence.resid_studentized_internal` / `resid_studentized_external` (the OLS refit, `params.kind` standardized / studentized; R `rstandard` / `rstudent`)",
		Tol:     "1e-9 relative (absolute below 1)", Tests: []string{"ols_influence_reference_test.go"},
	}, {
		Against: "statsmodels 0.14.5 `OLSInfluence` on the `rep()`-expanded design, one copy's figure (frequency weight)",
		Tol:     "1e-9 relative (absolute below 1)", Tests: []string{"ols_influence_reference_test.go"}, Weighted: true,
	}, {
		Against: rPin + " `rstandard` / `rstudent` over a bound `lm` and binomial-logit, poisson-log and gamma-inverse `glm` (`params.fit`, `params.kind` standardized / studentized)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_influence_reference_test.go"},
	}, {
		Against: rPin + " `residuals(type = \"working\")` over a bound probit, cloglog, poisson identity / sqrt and gamma log / identity `glm` (`params.fit`, `params.kind` working)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_links_reference_test.go"},
	}, {
		Against: rPin + " + MASS 7.3-66 `residuals` (pearson / deviance / working), `rstandard` (both types) and `rstudent` at dispersion 1 over a bound negative binomial fit (`glm.nb`, θ estimated or fixed; `params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_nb_reference_test.go"},
	}, {
		Against: rPin + " + MASS 7.3-66 `rstandard` (both types) at `summary.glm`'s φ̂ and `rstudent` on the leave-one-out σ over a bound fixed-θ negative binomial fit with `dispersion: estimated` (`glm(negative.binomial(θ))`; `params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_nb_reference_test.go"},
	}}, Gap: "The refit's response residual is checked against Pulse's own OLS. Over a weighted bound fit the standardized / studentized kinds are refused, not validated."},
	"ATTR_REG_LEVERAGE": {Refs: []ValidationRef{{
		Against: rPin + " `hatvalues` over a bound `lm` and binomial-logit, poisson-log and gamma-inverse `glm` (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_influence_reference_test.go"},
	}, {
		Against: rPin + " `hatvalues` over a bound probit, cloglog, poisson identity / sqrt and gamma log / identity `glm` (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_links_reference_test.go"},
	}, {
		Against: rPin + " + MASS 7.3-66 `hatvalues` over a bound negative binomial fit (log / sqrt / identity link, θ estimated or fixed; `params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_nb_reference_test.go"},
	}}, Gap: "The OLS refit's leverage is checked against Pulse's own OLS (and the statsmodels influence figures that read it); a weighted bound fit is refused, not validated."},
	"ATTR_REG_INFLUENCE": {Refs: []ValidationRef{{
		Against: "statsmodels 0.14.5 `OLSInfluence.cooks_distance` / `dffits` (R `cooks.distance` / `dffits`)",
		Tol:     "1e-9 relative (absolute below 1)", Tests: []string{"ols_influence_reference_test.go"},
	}, {
		Against: "statsmodels 0.14.5 `OLSInfluence` on the `rep()`-expanded design, one copy's figure (frequency weight)",
		Tol:     "1e-9 relative (absolute below 1)", Tests: []string{"ols_influence_reference_test.go"}, Weighted: true,
	}, {
		Against: rPin + " `cooks.distance` / `dffits` over a bound `lm` and binomial-logit, poisson-log and gamma-inverse `glm` (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_influence_reference_test.go"},
	}, {
		Against: rPin + " `dffits` over a bound probit, cloglog, poisson identity / sqrt and gamma log / identity `glm` (`params.fit`; its sign is that of g′(μ))",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_links_reference_test.go"},
	}, {
		Against: rPin + " + MASS 7.3-66 `cooks.distance` and `dffits` at dispersion 1 (`family$dispersion <- 1`) over a bound negative binomial fit (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_nb_reference_test.go"},
	}, {
		Against: rPin + " + MASS 7.3-66 `cooks.distance` at `summary.glm`'s φ̂ and `dffits` on the leave-one-out σ over a bound fixed-θ negative binomial fit with `dispersion: estimated` (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_nb_reference_test.go"},
	}}, Gap: "Over a weighted bound fit the figures are refused, not validated."},

	// Regressions.
	"REG_OLS": {Refs: []ValidationRef{
		valStrd("linear regression: Norris, Pontius, NoInt1–2 (intercept: false), Filip, Longley, Wampler1–5"),
		valR("`lm`", tol10, regDir+"ols_lm_reference_test.go"),
		valR("`glmnet` (penalties), `nnls` (non-negative)", tol10, regDir+"ols_penalty_reference_test.go", regDir+"ols_nnls_reference_test.go"),
		valR("`sandwich` / `lmtest` (HC0–HC3, cluster, HAC standard errors; vcov)", tol10, svcCov, svcVcov),
		valR("`logLik` / `AIC` of `lm` with and without an intercept (two_log_lik, aic; σ counted)", tol10, regDir+"loglik_oracle_test.go"),
		{Against: rPin + " `logLik` / `AIC` of the `rep()`-expanded `lm` (frequency weights; `lm(weights = f)` reads them as precision weights)", Tol: tol10, Tests: []string{regDir + "loglik_oracle_test.go"}, Weighted: true},
		valR("+ MASS 7.3-66 + `sandwich` 3.1-3: analytic marginal effects (`margins`, ame / mem) of `lm` with their delta-method standard errors, t statistics, p-values and intervals; cross-checked against `marginaleffects` 1.0.0 `avg_slopes` / `slopes(newdata = \"mean\")` at 1e-7", tol9, regDir+"margins_oracle_test.go"),
		valR("+ `car` 3.1-5 `vif` on `lm` (`collinearity`: VIF, tolerance and, over a factor's dummies grouped as one term, Fox & Monette's generalized VIF, df and GVIF^(1/(2 df)); the closed form re-derived from the weighted design)", tol10, regDir+"vif_oracle_test.go", "collinearity_test.go"),
		{Against: rPin + " + `car` 3.1-5 `vif(lm(weights = w))`: frequency weights against the `rep()`-expanded fit, probability weights scale-free", Tol: tol10, Tests: []string{regDir + "vif_oracle_test.go"}, Weighted: true},
		valR("+ `sandwich` 3.1-3 + `lmtest` 0.9-40: the Compose nested-model comparison (`compare`) of two `lm` fits — `anova(m0, m1)` F and `test = \"Chisq\"`, `waldtest(m0, m1, vcov = vcov | vcovHC(type = \"HC3\"))`, a restricted fit through the origin; statistic, df, p-value, each fit's residual df and RSS, σ̂²", tol10, svcCompareOracle, "compare_test.go"),
		{Against: rPin + " `anova` of the `rep()`-expanded `lm` fits (frequency weights; df = Σw − rank)", Tol: tol10, Tests: []string{svcCompareOracle}, Weighted: true},
		valWeighted,
	},
		Gap: "On NIST Wampler5 the coefficients reach 5.7 digits, level with LAPACK's QR (5.6): the double-precision limit for its large residuals.",
	},
	"REG_GLM": {
		Refs: []ValidationRef{
			valR("`glm` (binomial-logit, poisson-log, gamma-inverse)", tol10, regDir+"glm_irls_test.go", regDir+"glm_gamma_oracle_test.go"),
			valR("`glm` (binomial probit / cloglog, poisson identity / sqrt, gamma log / identity: coefficients, standard errors, vcov, deviance, null deviance, AIC, dispersion, fitted means, working residuals), refitted to its fixed point", tol9, regDir+"glm_links_oracle_test.go"),
			{Against: rPin + " `glm(weights = f)` against its `rep()`-expanded fit (probit, poisson sqrt, gamma log; frequency weights)", Tol: tol9, Tests: []string{regDir + "glm_links_oracle_test.go"}, Weighted: true},
			valR("`logLik` / `AIC` of binomial-logit, poisson-log and gamma-inverse `glm` (two_log_lik, aic; gamma's dispersion at deviance / n counted); the non-canonical links' `AIC` against `glm_links.json`, the negative binomial's against `glm_nb.json`", tol10, regDir+"loglik_oracle_test.go", regDir+"glm_links_oracle_test.go"),
			{Against: rPin + " `logLik` / `AIC` of `glm(weights = f)` against its `rep()`-expanded fit (binomial, poisson, gamma; frequency weights)", Tol: tol10, Tests: []string{regDir + "loglik_oracle_test.go"}, Weighted: true},
			valR("`glmnet` (penalties)", tol10, regDir+"glm_penalty_test.go"),
			valR("`sandwich` (robust standard errors; vcov)", "1e-11 relative", svcCovGLM, svcVcov),
			valR("`sandwich` 3.1-3 `vcovHC` HC0 / HC1 / HC3 on the non-canonical links", tol9, regDir+"glm_links_oracle_test.go"),
			valR("+ MASS 7.3-66 `glm.nb` (negative binomial, θ estimated on log / sqrt / identity, pushed to its fixed point) and `glm(negative.binomial(θ))` (θ fixed): coefficients, standard errors and vcov at dispersion 1, θ, SE(θ), twice the log-likelihood, AIC, deviance, null deviance, fitted means, residuals; `sandwich` 3.1-3 `vcovHC` HC0 / HC1 / HC3", tol9, regDir+"glm_nb_oracle_test.go"),
			{Against: rPin + " + MASS 7.3-66 `glm.nb(weights = f)` against its `rep()`-expanded fit (negative binomial, log link; frequency weights)", Tol: tol9, Tests: []string{regDir + "glm_nb_oracle_test.go"}, Weighted: true},
			valR("+ MASS 7.3-66 `summary(glm(negative.binomial(θ)))` (θ fixed, `dispersion: estimated`, log / sqrt link): φ̂ = Pearson χ²/df, vcov and standard errors scaled by it, t p-values on df, Wald-t intervals", tol9, regDir+"glm_nb_oracle_test.go"),
			{Against: rPin + " + MASS 7.3-66 `summary(glm(negative.binomial(θ)))` on the `rep()`-expanded fit (θ fixed, `dispersion: estimated`; frequency weights)", Tol: tol9, Tests: []string{regDir + "glm_nb_oracle_test.go"}, Weighted: true},
			valR("+ MASS 7.3-66 + `sandwich` 3.1-3: analytic marginal effects (`margins`: ame / mem, response / link scale) of logit, probit, cloglog, poisson, gamma inverse / log and negative binomial `glm` fits — the mean of β·μ′(η), the delta-method Jacobian on `vcov`, `vcovHC` HC3 and `vcovCL` — with statistics, p-values and intervals; cross-checked against `marginaleffects` 1.0.0 `avg_slopes` / `slopes(newdata = \"mean\")` at 1e-7", tol9, regDir+"margins_oracle_test.go"),
			{Against: rPin + " analytic marginal effects of `glm(weights = f)` (logit ame / mem) against its `rep()`-expanded fit, and `marginaleffects` `avg_slopes(wts = \"f\")`", Tol: tol9, Tests: []string{regDir + "margins_oracle_test.go"}, Weighted: true},
			valR("+ MASS 7.3-66 + `car` 3.1-5 `vif` (`collinearity`: VIF, tolerance, generalized VIF of a factor's dummies, df, GVIF^(1/(2 df))) on binomial-logit, poisson-log, fixed-θ negative binomial and gamma-log `glm` fits refitted to their fixed point (basis model: the IRLS working weights), and on `lm` over the same predictors and prior weights (basis predictors); end to end through `FEAT_ONE_HOT`", tol10, regDir+"vif_oracle_test.go", "collinearity_test.go"),
			{Against: rPin + " + `car` 3.1-5 `vif(glm(weights = w))`: frequency weights against the `rep()`-expanded fit (logit, poisson basis predictors), probability weights (gamma log) scale-free", Tol: tol10, Tests: []string{regDir + "vif_oracle_test.go"}, Weighted: true},
			valR("+ MASS 7.3-66 + `sandwich` 3.1-3 + `lmtest` 0.9-40: the Compose nested-model comparison (`compare`) of two `glm` fits refitted to their fixed point — `anova(m0, m1, test = \"LRT\" | \"F\")` on logit, probit, poisson, gamma inverse / log and fixed-θ negative binomial fits (the full fit's Pearson dispersion), MASS `anova.negbin` (θ estimated, 2 loglik change; cross-checked against `lrtest`), `waldtest(m0, m1, vcov = vcov | vcovHC(type = \"HC3\"), test = \"Chisq\" | \"F\")`", tol10, svcCompareOracle, "compare_test.go"),
			{Against: rPin + " `anova` of the `rep()`-expanded `glm` fits (poisson LRT, gamma log F; frequency weights)", Tol: tol10, Tests: []string{svcCompareOracle}, Weighted: true},
			valWeighted,
		},
		Gap: "Penalized fits on a non-canonical link are checked by their KKT conditions, not against glmnet. Off the canonical link IRLS converges linearly, so at the default tol (1e-8) the coefficients sit within about 1e-7 of the MLE; the oracle fits at tol 1e-13. The negative binomial refuses penalty, selection and resample (no oracle); its probability-weighted fit is checked for scale invariance only. Marginal effects are slopes only (no discrete factor contrasts: regressions take no factor predictors; a 0/1 dummy gets its slope, where marginaleffects reports a 1 − 0 contrast, so that cross-check skips it) and are refused over a predictor derived from another predictor, a penalty, selection, resample or bounds. The collinearity block's penalized basis-predictors figure is checked equal to the unpenalized one (a design figure), not against an external penalized VIF. A nested-model comparison under a probability weight runs the Wald test only (F / LRT refused), checked against its own covariance rather than an external survey test.",
	},
	"REG_BAYES_LINEAR": {Refs: []ValidationRef{
		valR("matrix algebra for the normal-inverse-gamma posterior", tol10, regDir+"bayes_reference_test.go", regDir+"bayes_precision_test.go"),
		valWeighted,
	}},
	"REG_MMM_HILL": {Refs: []ValidationRef{
		{Against: "`minpack.lm::nlsLM` 1.2-4 on the same objective, start and bounds (coefficients, standard errors, R², residual SE)", Tol: "1e-6 relative (the solver's stop rule)", Tests: []string{"internal/regmmm/hill_oracle_test.go"}},
		{Against: mmmSynthSrc, Tol: "0.5% to 15% relative, per parameter", Tests: []string{"internal/regmmm/hill_test.go"}},
	}},
	"REG_ORDINAL": {
		Refs: []ValidationRef{
			valR("+ MASS 7.3-66 `polr` (logit / probit / cloglog), polished by Newton to its MLE: coefficients, thresholds, standard errors and vcov from the analytic observed information (polr's numerical Hessian agrees to 1e-4), deviance, null deviance, two_log_lik, AIC, the mean fitted probabilities (`colMeans(fitted())`); `sandwich` 3.1-3 `sandwich` / `vcovCL` (HC0, HC1, CR1); `brant` 0.3.0 (omnibus and per-predictor χ², the binary logits at their fixed point); `params.levels` dropping an unlisted response as `factor(y, levels =)` does", tol10, ordOracle, ordE2E),
			{Against: rPin + " + MASS 7.3-66 `polr(weights = f)` against its `rep()`-expanded fit, and the Brant test on the expansion (frequency weights)", Tol: tol10, Tests: []string{ordOracle, ordE2E}, Weighted: true},
			{Against: rPin + " + MASS 7.3-66 `polr(weights = w*)`, w* = w·n_eff/Σw (probability weights: the frequency formulas on w*, no likelihood figures)", Tol: tol10, Tests: []string{ordOracle, ordE2E}, Weighted: true},
		},
		Gap: "The Brant test's omnibus statistic reads the symmetric covariance of the binary logits' slopes (Brant 1990); brant 0.3.0 stores the (m, l) cross block untransposed at (l, m), so its omnibus figure differs (per-predictor statistics agree) — the oracle script reproduces the package's figure exactly and pins the symmetric one. Resample, selection, penalty, margins and collinearity are refused (no oracle), and covariance hc2 / hc3 / hac (no row-wise hat matrix, no series).",
	},
	"REG_MMM_BAYES": {
		Refs: []ValidationRef{
			{Against: "`REG_BAYES_LINEAR`'s closed-form conjugate posterior (" + rPin + " matrix algebra), in the conjugate limit: adstock and curve pinned, flat priors (posterior means, SDs and σ)",
				Tol: "within 4 Monte Carlo standard errors, seeded", Tests: []string{"internal/regmmm/bayes_conjugate_heavy_test.go"}},
			{Against: "simulation-based calibration (Talts et al. 2018) on the default priors: 200 seeded replicates, rank of the truth among 99 thinned draws",
				Tol: "uniform rank histogram per parameter (χ² test, p ≥ 0.001 / 7)", Tests: []string{"internal/regmmm/bayes_sbc_heavy_test.go"}},
			{Against: mmmSynthSrc, Tol: "parameter recovery", Tests: []string{"internal/regmmm/bayes_test.go"}},
		},
		Gap: "No external MCMC implementation (Stan, PyMC-Marketing) is compared on the full nonlinear model. The calibration runs with the outcome scale fixed, because the default priors are stated on the max-scaled outcome and so move with the data.",
	},

	// Matrix operators.
	"MAT_COLLINEARITY": {Refs: []ValidationRef{valR("`car::vif`, `perturb::colldiag`", tol9, "internal/service/matrix_collinearity_test.go")}},
	"MAT_CORRELATION": {Refs: []ValidationRef{
		{Against: "numpy / statsmodels", Tol: tol12, Tests: []string{"internal/service/matrix_correlation_test.go"}},
		valR("`cor` and `cor.test` (p-values, intervals)", tol10, "internal/service/matrix_inference_test.go"),
	}},
	"MAT_COVARIANCE":          {Refs: []ValidationRef{{Against: "numpy `cov` / statsmodels `DescrStatsW`", Tol: tol12, Tests: []string{"internal/service/matrix_covariance_test.go"}}}},
	"MAT_DISTANCE":            {Refs: []ValidationRef{valR("`dist`", tol9, "internal/processing/matrix_distance_test.go")}},
	"MAT_FACTOR":              {Refs: []ValidationRef{valR("`psych::fa`, `GPArotation`", "1e-9 exact paths; 1e-4 to 2e-3 vs psych iterations", "internal/processing/matrix_factor_test.go", "internal/service/matrix_factor_test.go")}},
	"MAT_PARTIAL_CORRELATION": {Refs: []ValidationRef{valR("`ppcor` / `corpcor`", tol10, "internal/service/matrix_partial_test.go")}},
	"MAT_PCA":                 {Refs: []ValidationRef{valR("`eigen` / `prcomp`, `psych::KMO`", tol9, "internal/service/matrix_pca_test.go")}},
	"MAT_RELIABILITY":         {Refs: []ValidationRef{valR("`psych::alpha`, `psych::omega`", "1e-10 relative; omega 1e-5", "internal/service/matrix_reliability_test.go")}},
	"MAT_SEM": {Refs: []ValidationRef{
		valR("`lavaan` (ML, WLSMV, DWLS, bootstrap, multi-group), cross-checked with semopy", "estimates 1e-4, fit indices 1e-3",
			"internal/service/matrix_sem_means_test.go", "internal/service/matrix_sem_ordinal_test.go", "internal/service/matrix_sem_test.go",
			"internal/integration/sem_group_test.go", "internal/integration/sem_bootstrap_test.go"),
	}},

	// Inferential overlays.
	"OVERLAY_CHISQ_COL": {Refs: []ValidationRef{valR("`chisq.test(table[, j], p = rowSums / N)` per column", tol10, ovChiSqAxes), valWeighted}},
	"OVERLAY_CHISQ_MATRIX": {Refs: []ValidationRef{valR("`chisq.test(correct = FALSE)` (the default applies no Yates correction)", tol10, ovChiSqAxes),
		valR("`chisq.test(correct = TRUE)` (`params.correct` on a 2 × 2 host)", tol10, "internal/processing/test_options_oracle_test.go"), valWeighted}},
	"OVERLAY_CHISQ_ROW":         {Refs: []ValidationRef{valR("`chisq.test(table[i, ], p = colSums / N)` per row", tol10, ovChiSqAxes), valWeighted}},
	"OVERLAY_CHISQ_VS_POP":      {Refs: []ValidationRef{valR("`chisq.test`", tol10, ovOracle, "internal/processing/overlay_chisq_vs_pop_test.go")}},
	"OVERLAY_CHISQ_VS_REF":      {Refs: []ValidationRef{valR("`chisq.test`", tol10, ovOracle), valWeighted}},
	"OVERLAY_FISHER_EXACT_CELL": {Refs: []ValidationRef{valR("`fisher.test`", tol10, ovOracle, "internal/processing/overlay_fisher_exact_cell_test.go"), valWeighted}},
	"OVERLAY_KS_VS_POP":         {Refs: []ValidationRef{valR("`ks.test` / `psmirnov`", tol10, ovOracle, "internal/processing/overlay_ks_vs_pop_test.go")}},
	"OVERLAY_PAIRWISE_PROBIT_T": {
		Refs: []ValidationRef{valR("evaluation of the design formula", tol10, ovOracle)},
		Gap:  "No stock implementation exists to compare with; the reference evaluates the published formula.",
	},
	"OVERLAY_PAIRWISE_PROP_Z":               {Refs: []ValidationRef{valR("`prop.test(correct = FALSE)`", tol10, ovPairwise), valWeighted}},
	"OVERLAY_PAIRWISE_TWO_MEANS_Z":          {Refs: []ValidationRef{valR("`pnorm` on the Welch standard error", tol10, ovPairwise)}},
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": {Refs: []ValidationRef{valWeighted}},
	"OVERLAY_PAIRWISE_WELCH_T":              {Refs: []ValidationRef{valR("`t.test`", tol10, ovPairwise), valWeighted}},
	"OVERLAY_PROP_Z_CELL":                   {Refs: []ValidationRef{valR("`prop.test(correct = FALSE)` on the shared pooled two-proportion kernel, which the overlay runs on the legs each cell reads", tol10, ovOracle, "internal/processing/overlay_weighted_contingency_test.go"), valWeighted}},
	"OVERLAY_PROP_Z_PANEL":                  {Refs: []ValidationRef{valR("`prop.test(correct = FALSE)`", tol10, "internal/processing/overlay_prop_z_panel_test.go"), valWeighted}},
	"OVERLAY_PROP_Z_VS_PRIOR":               {Refs: []ValidationRef{valWeighted, {Against: "an independent re-implementation of the pooled two-proportion z test", Tol: tol9, Tests: []string{vsPrior}}}},
	"OVERLAY_STD_RESIDUAL":                  {Refs: []ValidationRef{valR("`chisq.test()$stdres`", tol12, "internal/processing/overlay_std_residual_test.go"), valWeighted}},
	"OVERLAY_SYMMETRY":                      {Refs: []ValidationRef{valR("`mcnemar.test` / Bowker", tol12, "internal/processing/overlay_symmetry_test.go"), valWeighted}},
	"OVERLAY_T_CELL":                        {Refs: []ValidationRef{valR("`t.test` / `pt`", tol10, "internal/processing/overlay_t_cell_test.go"), valParity("TEST_WELCH"), valWeighted}},
	"OVERLAY_T_VS_PRIOR":                    {Refs: []ValidationRef{valWeighted, {Against: "an independent re-implementation of the Welch t test", Tol: tol9, Tests: []string{vsPrior}}}},
	"OVERLAY_T_VS_REF":                      {Refs: []ValidationRef{valR("`t.test` / `pt`", tol10, "internal/integration/overlay_welford_series_oracle_test.go"), valParity("TEST_WELCH"), valWeighted}},
	"OVERLAY_Z_CELL":                        {Refs: []ValidationRef{valR("`pnorm` on the Welch standard error", tol10, "internal/processing/overlay_z_cell_test.go"), valParity("TEST_Z_TWO_SAMPLE"), valWeighted}},
	"OVERLAY_Z_VS_PRIOR":                    {Refs: []ValidationRef{valWeighted, {Against: "an independent re-implementation of the Welch z test", Tol: tol9, Tests: []string{vsPrior}}}},
	"OVERLAY_Z_VS_REF":                      {Refs: []ValidationRef{valR("`pnorm` on the Welch standard error", tol10, "internal/integration/overlay_welford_series_oracle_test.go"), valParity("TEST_Z_TWO_SAMPLE"), valWeighted}},
	"OVERLAY_RAKE":                          {Refs: []ValidationRef{valR("`loglin`, cross-checked with `survey::rake`", tol9, "internal/processing/overlay_rake_test.go")}},
}
