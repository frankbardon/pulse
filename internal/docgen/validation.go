package docgen

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// validation.go renders the reference-validation record: which external
// implementation each inferential operator is pinned to, at what
// tolerance, and by which test file. It is the human-facing answer to
// "why should I trust this number?" and renders two ways: a
// "Validated against" list in every operator's catalog detail block, and
// the validation.md summary page.
//
// The registry is hand-maintained beside the tests it cites. Its gates
// (validation_test.go) hold it honest: every cited test file must exist,
// every key must be a built-in operator, and every built-in test,
// regression, matrix operator and inferential overlay must have an
// entry (an entry may declare no reference, which renders as a gap; it
// is never silently missing). A claim with no harness behind it is not
// published.

//go:embed validation_intro.md
var validationIntro string

// strdLRE is the NIST StRD record: a byte-identical copy of
// testdata/strd/lre.json, written by the root package's
// TestStRD_DigitsMatchRecord -update and held equal to it by
// TestStRD_DocgenCopyCurrent.
//
//go:embed strd_lre.json
var strdLRE []byte

const (
	validationPage    = "validation.md"
	validationIntroID = "validation-intro"
)

// validationRef is one external check of an operator.
type validationRef struct {
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

// validation is one operator's record.
type validation struct {
	Refs []validationRef
	// Gap is a known validation gap in plain words; "" when none.
	Gap string
}

// Shared sources and test files, named once so entries stay short.
const (
	rPin   = "R 4.6.1"
	tol10  = "1e-10 relative"
	tol12  = "1e-12 relative"
	tol9   = "1e-9 relative"
	wrVals = "internal/service/weight_reference_values_test.go"
	wrTest = "internal/service/weight_reference_tests_test.go"

	procOutput  = "internal/processing/test_output_oracle_test.go"
	procPost    = "internal/processing/test_post_oracle_test.go"
	procUndef   = "internal/processing/test_undefined_oracle_test.go"
	ovOracle    = "internal/processing/overlay_reference_oracle_test.go"
	ovPairwise  = "internal/processing/overlay_pairwise_test.go"
	vsPrior     = "internal/integration/overlay_vs_prior_test.go"
	svcCov      = "internal/service/regression_covariance_test.go"
	svcCovGLM   = "internal/service/regression_covariance_glm_test.go"
	svcVcov     = "internal/service/regression_vcov_test.go"
	regDir      = "internal/processing/regression/"
	strdTest    = "strd_reference_test.go"
	mmmSynthSrc = "synthetic data with known true parameters (internal/mmmfixture)"
)

// weighted is the weighted-reference check shared by most inferential
// operators: the stock R function on rep()-expanded rows for frequency
// weights, and a closed form on Kish-rescaled weights with scipy tails
// for probability weights where the operator accepts them.
var weighted = validationRef{
	Against:  rPin + " on `rep()`-expanded rows (frequency weights); closed form with scipy 1.16.2 tails, cross-checked with statsmodels (probability weights)",
	Tol:      tol12,
	Tests:    []string{wrVals, wrTest},
	Weighted: true,
}

// strdRef is a NIST StRD certification check; its digits achieved are
// published in the StRD table on validation.md.
func strdRef(sets string) validationRef {
	return validationRef{Against: "NIST StRD certified values (" + sets + ")", Tol: "digits achieved per dataset (see the NIST table)", Tests: []string{strdTest}}
}

func r(fn, tol string, tests ...string) validationRef {
	return validationRef{Against: rPin + " " + fn, Tol: tol, Tests: tests}
}

// validationRegistry maps a built-in operator name to its record.
var validationRegistry = map[string]validation{
	// Statistical tests.
	"TEST_ANOVA_F": {
		Refs: []validationRef{r("`aov`", tol10, procPost), strdRef("one-way ANOVA: SiRstv, SmLs01–09, AtmWtAg"), weighted},
		Gap:  "On NIST SmLs07–09 the figures agree to about 4 digits. That is the ceiling for any double-precision package: values such as 1000000000000.4 are not exactly representable, and exact arithmetic on the parsed doubles reaches only 4.0, 3.9 and 3.9 digits. Pulse reaches that ceiling.",
	},
	"TEST_ANOVA_RM":    {Refs: []validationRef{r("`aov` with `Error()` strata", tol10, procOutput)}, Gap: "No weighted reference."},
	"TEST_ANOVA_WELCH": {Refs: []validationRef{r("`oneway.test`", tol10, procPost), weighted}},
	"TEST_BARTLETT_SPHERICITY": {Refs: []validationRef{
		r("`psych::cortest.bartlett`", tol9, "internal/service/test_bartlett_sphericity_test.go"), weighted}},
	"TEST_BOX_M":          {Refs: []validationRef{r("`biotools::boxM`", tol10, "internal/processing/test_box_m_test.go"), weighted}},
	"TEST_BROWN_FORSYTHE": {Refs: []validationRef{r("`anova(lm(abs(x - median) ~ g))`", tol10, procUndef, procPost), weighted}},
	"TEST_CHISQ": {
		Refs: []validationRef{weighted, {Against: "Agresti worked example (R `chisq.test` help) and scipy for the effect size", Tol: "1e-9 to 1e-6", Tests: []string{"internal/processing/test_more_test.go", "internal/processing/test_effect_size_test.go"}}},
		Gap:  "The unweighted statistic is pinned to hand-computed values, not to an R golden file.",
	},
	"TEST_FISHER_EXACT": {Refs: []validationRef{r("`fisher.test`", tol10, "internal/processing/test_fisher_oracle_test.go"), weighted}},
	"TEST_HOTELLING_T2": {Refs: []validationRef{r("`Hotelling::hotelling.test`", tol10, "internal/processing/test_hotelling_test.go"), weighted}},
	"TEST_KENDALL_TAU":  {Refs: []validationRef{r("`cor.test(method = \"kendall\")`", tol10, procOutput), weighted}},
	"TEST_KRUSKAL_WALLIS": {
		Refs: []validationRef{weighted, {Against: rPin + " `kruskal.test` help examples (effect size)", Tol: tol9, Tests: []string{"internal/processing/test_effect_size_rank_test.go", "internal/processing/test_nonparametric_test.go"}}},
		Gap:  "The unweighted statistic and p-value are pinned to hand-computed values, not to an R golden file.",
	},
	"TEST_KS": {Refs: []validationRef{r("`ks.test` (exact `psmirnov`)", tol10, procOutput), weighted}},
	"TEST_MANN_WHITNEY_U": {
		Refs: []validationRef{weighted, {Against: rPin + " `wilcox.test` help examples (effect size)", Tol: tol9, Tests: []string{"internal/processing/test_effect_size_rank_test.go", "internal/processing/test_nonparametric_test.go"}}},
		Gap:  "The unweighted statistic and p-value are pinned to hand-computed values, not to an R golden file.",
	},
	"TEST_MANOVA":       {Refs: []validationRef{r("`manova` with `summary.manova`", tol10, "internal/processing/test_manova_test.go"), weighted}},
	"TEST_MARDIA":       {Refs: []validationRef{r("Mardia skewness and kurtosis", tol10, "internal/processing/test_mardia_test.go"), weighted}},
	"TEST_PAIRED_T":     {Refs: []validationRef{r("`t.test(paired = TRUE)`", tol10, procPost), weighted}},
	"TEST_PEARSON_R":    {Refs: []validationRef{r("`cor.test`", tol10, procOutput, procPost), weighted}},
	"TEST_PROP_Z":       {Refs: []validationRef{weighted, {Against: "textbook worked example", Tol: tol9, Tests: []string{"internal/processing/test_parametric_test.go"}}}},
	"TEST_SHAPIRO_WILK": {Refs: []validationRef{r("`nortest::sf.test` (Shapiro–Francia)", tol10, procUndef)}, Gap: "No weighted reference."},
	"TEST_SPEARMAN_R":   {Refs: []validationRef{r("`cor.test(method = \"spearman\")` (exact AS 89)", "1e-12 relative", "internal/processing/test_nonparametric_test.go", procPost), weighted}},
	"TEST_T": {
		Refs: []validationRef{weighted, {Against: "scipy `ttest_ind`", Tol: "1e-3 absolute", Tests: []string{"internal/processing/test_t_test.go"}}},
		Gap:  "The unweighted check is a 4-decimal scipy value; the tight pin is the weighted reference.",
	},
	"TEST_TREND":     {Refs: []validationRef{r("`cor.test(method = \"kendall\")`, cross-checked with `Kendall::MannKendall`", tol10, procOutput)}},
	"TEST_TUKEY_HSD": {Refs: []validationRef{r("`TukeyHSD`, `ptukey` refined by quadrature", tol10, procOutput)}},
	"TEST_WELCH": {
		Refs: []validationRef{weighted, {Against: "scipy `ttest_ind(equal_var = False)`", Tol: "1e-3 absolute", Tests: []string{"internal/processing/test_t_test.go"}}},
		Gap:  "The unweighted check is a 4-decimal scipy value; the tight pin is the weighted reference.",
	},
	"TEST_WILCOXON_SR": {Refs: []validationRef{r("`wilcox.test(paired = TRUE)`", tol10, procPost), weighted}},
	"TEST_Z_TWO_SAMPLE": {
		Refs: []validationRef{weighted},
		Gap:  "No unweighted external golden; the unweighted path is checked against Pulse's own Welch test.",
	},

	// Descriptive operators with a certified reference.
	"AGG_AVERAGE": {Refs: []validationRef{strdRef("univariate: PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4")}},
	"AGG_STDDEV":  {Refs: []validationRef{strdRef("univariate sample standard deviation, `params.ddof` 1: PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4")}},
	"AGG_WELFORD": {Refs: []validationRef{strdRef("univariate sample standard deviation (the `stddev` component): PiDigits, Lottery, Lew, Mavro, Michelso, NumAcc1–4")}},

	// Per-row regression scores with an external reference.
	"ATTR_REG_RESIDUAL": {Refs: []validationRef{{
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
	"ATTR_REG_LEVERAGE": {Refs: []validationRef{{
		Against: rPin + " `hatvalues` over a bound `lm` and binomial-logit, poisson-log and gamma-inverse `glm` (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_influence_reference_test.go"},
	}, {
		Against: rPin + " `hatvalues` over a bound probit, cloglog, poisson identity / sqrt and gamma log / identity `glm` (`params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_links_reference_test.go"},
	}, {
		Against: rPin + " + MASS 7.3-66 `hatvalues` over a bound negative binomial fit (log / sqrt / identity link, θ estimated or fixed; `params.fit`)",
		Tol:     "1e-9 relative (absolute floor 1e-12)", Tests: []string{"glm_nb_reference_test.go"},
	}}, Gap: "The OLS refit's leverage is checked against Pulse's own OLS (and the statsmodels influence figures that read it); a weighted bound fit is refused, not validated."},
	"ATTR_REG_INFLUENCE": {Refs: []validationRef{{
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
	"REG_OLS": {Refs: []validationRef{
		strdRef("linear regression: Norris, Pontius, NoInt1–2 (intercept: false), Filip, Longley, Wampler1–5"),
		r("`lm`", tol10, regDir+"ols_lm_reference_test.go"),
		r("`glmnet` (penalties), `nnls` (non-negative)", tol10, regDir+"ols_penalty_reference_test.go", regDir+"ols_nnls_reference_test.go"),
		r("`sandwich` / `lmtest` (HC0–HC3, cluster, HAC standard errors; vcov)", tol10, svcCov, svcVcov),
		r("`logLik` / `AIC` of `lm` with and without an intercept (two_log_lik, aic; σ counted)", tol10, regDir+"loglik_oracle_test.go"),
		{Against: rPin + " `logLik` / `AIC` of the `rep()`-expanded `lm` (frequency weights; `lm(weights = f)` reads them as precision weights)", Tol: tol10, Tests: []string{regDir + "loglik_oracle_test.go"}, Weighted: true},
		r("+ MASS 7.3-66 + `sandwich` 3.1-3: analytic marginal effects (`margins`, ame / mem) of `lm` with their delta-method standard errors, t statistics, p-values and intervals; cross-checked against `marginaleffects` 1.0.0 `avg_slopes` / `slopes(newdata = \"mean\")` at 1e-7", tol9, regDir+"margins_oracle_test.go"),
		weighted,
	},
		Gap: "On NIST Wampler5 the coefficients reach 5.7 digits, level with LAPACK's QR (5.6): the double-precision limit for its large residuals.",
	},
	"REG_GLM": {
		Refs: []validationRef{
			r("`glm` (binomial-logit, poisson-log, gamma-inverse)", tol10, regDir+"glm_irls_test.go", regDir+"glm_gamma_oracle_test.go"),
			r("`glm` (binomial probit / cloglog, poisson identity / sqrt, gamma log / identity: coefficients, standard errors, vcov, deviance, null deviance, AIC, dispersion, fitted means, working residuals), refitted to its fixed point", tol9, regDir+"glm_links_oracle_test.go"),
			{Against: rPin + " `glm(weights = f)` against its `rep()`-expanded fit (probit, poisson sqrt, gamma log; frequency weights)", Tol: tol9, Tests: []string{regDir + "glm_links_oracle_test.go"}, Weighted: true},
			r("`logLik` / `AIC` of binomial-logit, poisson-log and gamma-inverse `glm` (two_log_lik, aic; gamma's dispersion at deviance / n counted); the non-canonical links' `AIC` against `glm_links.json`, the negative binomial's against `glm_nb.json`", tol10, regDir+"loglik_oracle_test.go", regDir+"glm_links_oracle_test.go"),
			{Against: rPin + " `logLik` / `AIC` of `glm(weights = f)` against its `rep()`-expanded fit (binomial, poisson, gamma; frequency weights)", Tol: tol10, Tests: []string{regDir + "loglik_oracle_test.go"}, Weighted: true},
			r("`glmnet` (penalties)", tol10, regDir+"glm_penalty_test.go"),
			r("`sandwich` (robust standard errors; vcov)", "1e-11 relative", svcCovGLM, svcVcov),
			r("`sandwich` 3.1-3 `vcovHC` HC0 / HC1 / HC3 on the non-canonical links", tol9, regDir+"glm_links_oracle_test.go"),
			r("+ MASS 7.3-66 `glm.nb` (negative binomial, θ estimated on log / sqrt / identity, pushed to its fixed point) and `glm(negative.binomial(θ))` (θ fixed): coefficients, standard errors and vcov at dispersion 1, θ, SE(θ), twice the log-likelihood, AIC, deviance, null deviance, fitted means, residuals; `sandwich` 3.1-3 `vcovHC` HC0 / HC1 / HC3", tol9, regDir+"glm_nb_oracle_test.go"),
			{Against: rPin + " + MASS 7.3-66 `glm.nb(weights = f)` against its `rep()`-expanded fit (negative binomial, log link; frequency weights)", Tol: tol9, Tests: []string{regDir + "glm_nb_oracle_test.go"}, Weighted: true},
			r("+ MASS 7.3-66 `summary(glm(negative.binomial(θ)))` (θ fixed, `dispersion: estimated`, log / sqrt link): φ̂ = Pearson χ²/df, vcov and standard errors scaled by it, t p-values on df, Wald-t intervals", tol9, regDir+"glm_nb_oracle_test.go"),
			{Against: rPin + " + MASS 7.3-66 `summary(glm(negative.binomial(θ)))` on the `rep()`-expanded fit (θ fixed, `dispersion: estimated`; frequency weights)", Tol: tol9, Tests: []string{regDir + "glm_nb_oracle_test.go"}, Weighted: true},
			r("+ MASS 7.3-66 + `sandwich` 3.1-3: analytic marginal effects (`margins`: ame / mem, response / link scale) of logit, probit, cloglog, poisson, gamma inverse / log and negative binomial `glm` fits — the mean of β·μ′(η), the delta-method Jacobian on `vcov`, `vcovHC` HC3 and `vcovCL` — with statistics, p-values and intervals; cross-checked against `marginaleffects` 1.0.0 `avg_slopes` / `slopes(newdata = \"mean\")` at 1e-7", tol9, regDir+"margins_oracle_test.go"),
			{Against: rPin + " analytic marginal effects of `glm(weights = f)` (logit ame / mem) against its `rep()`-expanded fit, and `marginaleffects` `avg_slopes(wts = \"f\")`", Tol: tol9, Tests: []string{regDir + "margins_oracle_test.go"}, Weighted: true},
			weighted,
		},
		Gap: "Penalized fits on a non-canonical link are checked by their KKT conditions, not against glmnet. Off the canonical link IRLS converges linearly, so at the default tol (1e-8) the coefficients sit within about 1e-7 of the MLE; the oracle fits at tol 1e-13. The negative binomial refuses penalty, selection and resample (no oracle); its probability-weighted fit is checked for scale invariance only. Marginal effects are slopes only (no discrete factor contrasts: regressions take no factor predictors) and are refused over a derived predictor, a penalty, selection, resample or bounds.",
	},
	"REG_BAYES_LINEAR": {Refs: []validationRef{
		r("matrix algebra for the normal-inverse-gamma posterior", tol10, regDir+"bayes_reference_test.go", regDir+"bayes_precision_test.go"),
		weighted,
	}},
	"REG_MMM_HILL": {
		Refs: []validationRef{{Against: mmmSynthSrc, Tol: "0.5% to 15% relative, per parameter", Tests: []string{"internal/regmmm/hill_test.go"}}},
		Gap:  "No external reference implementation; the fit is checked by recovering known parameters only.",
	},
	"REG_MMM_BAYES": {
		Refs: []validationRef{{Against: mmmSynthSrc, Tol: "parameter recovery", Tests: []string{"internal/regmmm/bayes_test.go"}}},
		Gap:  "No external reference implementation; the fit is checked by recovering known parameters only.",
	},

	// Matrix operators.
	"MAT_COLLINEARITY": {Refs: []validationRef{r("`car::vif`, `perturb::colldiag`", tol9, "internal/service/matrix_collinearity_test.go")}},
	"MAT_CORRELATION": {Refs: []validationRef{
		{Against: "numpy / statsmodels", Tol: tol12, Tests: []string{"internal/service/matrix_correlation_test.go"}},
		r("`cor` and `cor.test` (p-values, intervals)", tol10, "internal/service/matrix_inference_test.go"),
	}},
	"MAT_COVARIANCE":          {Refs: []validationRef{{Against: "numpy `cov` / statsmodels `DescrStatsW`", Tol: tol12, Tests: []string{"internal/service/matrix_covariance_test.go"}}}},
	"MAT_DISTANCE":            {Refs: []validationRef{r("`dist`", tol9, "internal/processing/matrix_distance_test.go")}},
	"MAT_FACTOR":              {Refs: []validationRef{r("`psych::fa`, `GPArotation`", "1e-9 exact paths; 1e-4 to 2e-3 vs psych iterations", "internal/processing/matrix_factor_test.go", "internal/processing/matrix_minres_test.go", "internal/service/matrix_factor_test.go")}},
	"MAT_PARTIAL_CORRELATION": {Refs: []validationRef{r("`ppcor` / `corpcor`", tol10, "internal/service/matrix_partial_test.go")}},
	"MAT_PCA":                 {Refs: []validationRef{r("`eigen` / `prcomp`, `psych::KMO`", tol9, "internal/service/matrix_pca_test.go")}},
	"MAT_RELIABILITY":         {Refs: []validationRef{r("`psych::alpha`, `psych::omega`", "1e-10 relative; omega 1e-5", "internal/service/matrix_reliability_test.go")}},
	"MAT_SEM": {Refs: []validationRef{
		r("`lavaan` (ML, WLSMV, DWLS, bootstrap, multi-group), cross-checked with semopy", "estimates 1e-4, fit indices 1e-3", "internal/sem/lavaan_test.go"),
	}},

	// Inferential overlays.
	"OVERLAY_CHISQ_COL":         {Refs: []validationRef{weighted}, Gap: "The unweighted path has internal checks only; the R pin is the frequency-weighted reference."},
	"OVERLAY_CHISQ_MATRIX":      {Refs: []validationRef{weighted}, Gap: "The unweighted path has internal checks only; the R pin is the frequency-weighted reference."},
	"OVERLAY_CHISQ_ROW":         {Refs: []validationRef{weighted}, Gap: "The unweighted path has internal checks only; the R pin is the frequency-weighted reference."},
	"OVERLAY_CHISQ_VS_POP":      {Refs: []validationRef{r("`chisq.test`", tol10, ovOracle, "internal/processing/overlay_chisq_vs_pop_test.go")}},
	"OVERLAY_CHISQ_VS_REF":      {Refs: []validationRef{r("`chisq.test`", tol10, ovOracle), weighted}},
	"OVERLAY_FISHER_EXACT_CELL": {Refs: []validationRef{r("`fisher.test`", tol10, ovOracle, "internal/processing/overlay_fisher_exact_cell_test.go"), weighted}},
	"OVERLAY_KS_VS_POP":         {Refs: []validationRef{r("`ks.test` / `psmirnov`", tol10, ovOracle, "internal/processing/overlay_ks_vs_pop_test.go")}},
	"OVERLAY_PAIRWISE_PROBIT_T": {
		Refs: []validationRef{r("evaluation of the design formula", tol10, ovOracle)},
		Gap:  "No stock implementation exists to compare with; the reference evaluates the published formula.",
	},
	"OVERLAY_PAIRWISE_PROP_Z":               {Refs: []validationRef{r("`prop.test(correct = FALSE)`", tol10, ovPairwise), weighted}},
	"OVERLAY_PAIRWISE_TWO_MEANS_Z":          {Refs: []validationRef{r("`pnorm` on the Welch standard error", tol10, ovPairwise)}},
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": {Refs: []validationRef{weighted}},
	"OVERLAY_PAIRWISE_WELCH_T":              {Refs: []validationRef{r("`t.test`", tol10, ovPairwise), weighted}},
	"OVERLAY_PROP_Z_CELL":                   {Refs: []validationRef{r("`prop.test(correct = FALSE)`", tol10, ovPairwise), weighted}},
	"OVERLAY_PROP_Z_PANEL":                  {Refs: []validationRef{r("`prop.test(correct = FALSE)`", tol10, "internal/processing/overlay_prop_z_panel_test.go"), weighted}},
	"OVERLAY_PROP_Z_VS_PRIOR":               {Refs: []validationRef{weighted, {Against: "an independent re-implementation of the pooled two-proportion z test", Tol: tol9, Tests: []string{vsPrior}}}},
	"OVERLAY_STD_RESIDUAL":                  {Refs: []validationRef{r("`chisq.test()$stdres`", tol12, "internal/processing/overlay_std_residual_test.go"), weighted}},
	"OVERLAY_SYMMETRY":                      {Refs: []validationRef{r("`mcnemar.test` / Bowker", tol12, "internal/processing/overlay_symmetry_test.go"), weighted}},
	"OVERLAY_T_CELL":                        {Refs: []validationRef{r("`t.test` / `pt`", tol10, "internal/processing/overlay_t_cell_test.go"), weighted}},
	"OVERLAY_T_VS_PRIOR":                    {Refs: []validationRef{weighted, {Against: "an independent re-implementation of the Welch t test", Tol: tol9, Tests: []string{vsPrior}}}},
	"OVERLAY_T_VS_REF":                      {Refs: []validationRef{r("`t.test` / `pt`", tol10, "internal/integration/overlay_welford_series_oracle_test.go"), weighted}},
	"OVERLAY_Z_CELL":                        {Refs: []validationRef{r("`pnorm` on the Welch standard error", tol10, "internal/processing/overlay_z_cell_test.go"), weighted}},
	"OVERLAY_Z_VS_PRIOR":                    {Refs: []validationRef{weighted, {Against: "an independent re-implementation of the Welch z test", Tol: tol9, Tests: []string{vsPrior}}}},
	"OVERLAY_Z_VS_REF":                      {Refs: []validationRef{r("`pnorm` on the Welch standard error", tol10, "internal/integration/overlay_welford_series_oracle_test.go"), weighted}},
	"OVERLAY_RAKE":                          {Refs: []validationRef{r("`loglin`, cross-checked with `survey::rake`", tol9, "internal/processing/overlay_rake_test.go")}},
}

// validationFor returns name's record as this instance renders it:
// weighted checks are dropped when weighting is hidden. ok is false
// when the operator has no record (an extension, or a descriptive
// operator outside the registry's scope).
func (g *gen) validationFor(name string) (validation, bool) {
	v, ok := validationRegistry[name]
	if !ok {
		return validation{}, false
	}
	weightingOn := g.inst.Enabled(descx.FeatureWeighting)
	out := validation{Gap: v.Gap}
	for _, ref := range v.Refs {
		if ref.Weighted && !weightingOn {
			continue
		}
		out.Refs = append(out.Refs, ref)
	}
	return out, true
}

// renderValidationDetail writes the "Validated against" list of a
// catalog detail block; nothing for an operator with no record.
func (g *gen) renderValidationDetail(b *strings.Builder, name string) {
	v, ok := g.validationFor(name)
	if !ok {
		return
	}
	var items []string
	for _, ref := range v.Refs {
		items = append(items, refLine(ref))
	}
	if len(items) == 0 {
		items = append(items, "No external reference yet.")
	}
	writeList(b, "Validated against", items)
	if v.Gap != "" {
		b.WriteString("\n**Validation gap:** ")
		b.WriteString(v.Gap)
		b.WriteString("\n")
	}
	b.WriteString("\nSee [How Pulse is validated](../" + validationPage + ").\n")
}

func refLine(ref validationRef) string {
	tests := make([]string, len(ref.Tests))
	for i, t := range ref.Tests {
		tests[i] = "`" + t + "`"
	}
	return ref.Against + " — tolerance " + ref.Tol + "; " + strings.Join(tests, ", ")
}

// renderValidation renders validation.md: the method (preserved prose)
// and one summary row per visible operator with a record, grouped by
// catalog category.
func (g *gen) renderValidation(cats []category) string {
	var b strings.Builder
	b.WriteString("# How Pulse is validated\n\n")
	writePreserved(&b, validationIntroID, validationIntro)
	for _, c := range cats {
		var rows []entry
		for _, e := range c.ops {
			if _, ok := g.validationFor(e.name); ok {
				rows = append(rows, e)
			}
		}
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
		b.WriteString("\n## ")
		b.WriteString(c.title)
		b.WriteString("\n\n| Operator | Validated against | Tightest tolerance | Gap |\n|---|---|---|---|\n")
		for _, e := range rows {
			v, _ := g.validationFor(e.name)
			var against []string
			tightest := emptyCell
			for i, ref := range v.Refs {
				if ref.Weighted {
					against = append(against, "weighted reference (see above)")
				} else {
					against = append(against, ref.Against)
				}
				if i == 0 || tolRank(ref.Tol) < tolRank(tightest) {
					tightest = ref.Tol
				}
			}
			if len(against) == 0 {
				against = []string{"No external reference yet."}
			}
			b.WriteString("| [`")
			b.WriteString(e.name)
			b.WriteString("`](")
			b.WriteString(catalogPath(c.key))
			b.WriteString("#")
			b.WriteString(operatorAnchor(e.name))
			b.WriteString(") | ")
			b.WriteString(cell(strings.Join(against, "<br>")))
			b.WriteString(" | ")
			b.WriteString(cell(tightest))
			b.WriteString(" | ")
			b.WriteString(cell(v.Gap))
			b.WriteString(" |\n")
		}
	}
	g.renderStRD(&b)
	return b.String()
}

// strdOperator maps a StRD dataset family to the operator it checks.
var strdOperator = map[string][]string{
	"lls":   {"REG_OLS"},
	"anova": {"TEST_ANOVA_F"},
	"univ":  {"AGG_AVERAGE", "AGG_STDDEV", "AGG_WELFORD"},
}

// strdFamilyTitle names each family; the heading appends only the
// operators the instance offers, so a profile hiding one never names it.
var strdFamilyTitle = map[string]string{
	"lls":   "Linear regression",
	"anova": "One-way ANOVA",
	"univ":  "Univariate summary statistics",
}

// renderStRD writes the NIST StRD table from the embedded record: one
// row per dataset whose operator the instance offers.
func (g *gen) renderStRD(b *strings.Builder) {
	s := string(strdLRE)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Results []struct {
			Dataset    string             `json:"dataset"`
			Difficulty string             `json:"difficulty"`
			Digits     map[string]float64 `json:"digits"`
			Min        *float64           `json:"min_digits"`
			NotOffered string             `json:"not_offered"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		panic("docgen: strd_lre.json: " + err.Error())
	}
	var out strings.Builder
	family := ""
	for _, r := range doc.Results {
		fam, set, _ := strings.Cut(r.Dataset, "/")
		var ops []string
		for _, op := range strdOperator[fam] {
			if g.operatorVisible(op) {
				ops = append(ops, "`"+op+"`")
			}
		}
		if len(ops) == 0 {
			continue
		}
		if fam != family {
			family = fam
			out.WriteString("\n### " + strdFamilyTitle[fam] + " (" + strings.Join(ops, ", ") + ")\n\n| Dataset | NIST difficulty | Fewest digits | Figures (digits) | Note |\n|---|---|---|---|---|\n")
		}
		diff := r.Difficulty
		if diff == "" {
			diff = "not stated"
		}
		least := emptyCell
		if r.Min != nil {
			least = strconv.FormatFloat(*r.Min, 'f', 1, 64)
		}
		keys := make([]string, 0, len(r.Digits))
		for k := range r.Digits {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		figs := make([]string, len(keys))
		for i, k := range keys {
			figs[i] = "`" + k + "` " + strconv.FormatFloat(r.Digits[k], 'f', 1, 64)
		}
		out.WriteString("| " + set + " | " + diff + " | " + least + " | " + cell(strings.Join(figs, ", ")) + " | " + cell(r.NotOffered) + " |\n")
	}
	if out.Len() == 0 {
		return
	}
	b.WriteString("\n## NIST StRD certification\n\n")
	b.WriteString("The [NIST Statistical Reference Datasets](https://www.itl.nist.gov/div898/strd/) carry certified values to 15 significant digits and are the accuracy benchmark statistical packages are measured against. ")
	b.WriteString("Each dataset runs end to end through Pulse; a figure's *digits* are its log relative error against the certified value, LRE = −log10(|x − c| / |c|), floored to 0.1 and capped at 15. ")
	b.WriteString("Roughly, 15 is exact in double precision, 7 or more is accurate for any practical use, and under 4 means the figure is unreliable on that data. ")
	b.WriteString("Lower- and average-difficulty sets must reach 7 digits on every figure; higher-difficulty sets are published as achieved. ")
	b.WriteString("These numbers are the record the tests assert (`strd_reference_test.go`), so any change in accuracy fails the build until it is reviewed.\n")
	b.WriteString(out.String())
}

// tolRank orders tolerance strings by their leading 1e-N exponent
// (tighter first); a string without one ranks last.
func tolRank(s string) int {
	i := strings.Index(s, "1e-")
	if i < 0 {
		return 1 << 20
	}
	n := 0
	for _, ch := range s[i+3:] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return -n
}
