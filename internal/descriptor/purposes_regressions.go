package descriptor

import "github.com/frankbardon/pulse/descriptor"

// regressionPurposes is the Purpose registry for the REG_* types, keyed
// by type. builtinPurposes assembles it with the other category maps.
//
// Each Purpose states the model as Pulse fits it (see
// internal/processing/regression): classic (not robust) OLS standard
// errors with Student-t p-values on n - p - 1 degrees of freedom; GLM
// on the canonical link only, with Wald-z p-values at a dispersion
// fixed to 1; a conjugate Normal-Inverse-Gamma Bayesian fit with one
// scalar prior precision on every coefficient, intercept included, and
// equal-tailed Student-t credible intervals. Every Purpose says that a
// coefficient describes association holding the other predictors fixed,
// never a causal effect on its own (TestGuidanceProseLint's CORR-CAUSAL
// rule for REG_*), and that rows must be independent (ASSUME-INDEP).
var regressionPurposes = map[string]descriptor.Purpose{
	"REG_BAYES_LINEAR": purposeRegBayesLinear,
	"REG_GLM":          purposeRegGLM,
	"REG_OLS":          purposeRegOLS,
}

// Shared assumption sentences, so the three types say the same thing
// the same way.
const (
	regIndependentRows = "Rows are independent of each other: repeated rows for the same customer, or values over time, " +
		"make the reported uncertainty too small."
	regAssociationOnly = "A coefficient describes association with the outcome, holding the other predictors fixed; " +
		"it shows a causal effect only when the design supports one (for example random assignment), never from the fit alone, " +
		"and a predictor left out of the model can drive both."
	regCollinearity = "Strongly related predictors (multicollinearity) make coefficients unstable and their uncertainty large; " +
		"Pulse refuses an exactly collinear set."
	regNullsDropped = "Rows with a null target or predictor are dropped before fitting (listwise deletion); n_obs counts the rows used."
	regNumericOnly  = "Predictors must be numeric: encode a category as 0/1 columns first (FEAT_ONE_HOT) and leave one level out, " +
		"or the columns are collinear with the intercept."
)

var (
	purposeRegOLS = descriptor.Purpose{
		Plain:   "Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares.",
		KnownAs: []string{"linear regression", "ordinary least squares", "lm"},
		Intents: []string{IntentDrivers, IntentRelationship},
		Questions: []string{
			"Which of price, discount and season are associated with weekly sales, holding the others fixed?",
			"How much does predicted order value change per extra visit, with the other predictors held fixed?",
			"How much of the variation in satisfaction do these attribute ratings account for together?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Relate overall satisfaction to several attribute ratings at once (often called key-driver analysis, though it shows which ratings go with satisfaction, not which ones drive it).",
			descriptor.DomainOps:     "Model ticket handling time from queue length and agent tenure.",
			descriptor.DomainScience: "Estimate how a measured response changes with dose while adjusting for body weight.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the outcome is yes/no (0/1) or a count", Use: "REG_GLM"},
			{When: "you want intervals that combine the data with prior knowledge about the coefficients", Use: "REG_BAYES_LINEAR"},
			{When: "you only want how closely two numeric fields follow a straight line together", Use: "TEST_PEARSON_R"},
			{When: "the predictor is a set of groups and you want to know whether the group averages differ", Use: "TEST_ANOVA_F"},
			{When: "you want each row's residual, to check the residual assumptions or find rows the model fits badly", Use: "ATTR_REG_RESIDUAL"},
		},
		Assumptions: []string{
			regIndependentRows,
			"The outcome changes linearly with each predictor; model a curve by adding transformed predictors (FEAT_POLY, FEAT_LOG).",
			"Residuals have roughly constant spread: Pulse reports classic standard errors, not heteroscedasticity-robust ones. " +
				"With few rows the residuals should also be roughly normal for the p-values to hold.",
			"Each p-value tests the null hypothesis that its coefficient is 0 given the other predictors, " +
				"on a t distribution with n - p - 1 degrees of freedom (p predictors).",
			regAssociationOnly,
			regCollinearity,
			"Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 by design, so their p-values lose the usual reading; " +
				"l1 and elasticnet standard errors are approximate and Pulse warns.",
			regNumericOnly,
			regNullsDropped,
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"adjusted-r-squared", "degrees-of-freedom", "heteroscedasticity", "independence",
			"listwise-deletion", "multicollinearity", "normal-distribution", "null-hypothesis",
			"outlier", "overfitting", "p-value", "r-squared", "regression-coefficient", "residual",
			"standard-error",
		},
	}

	purposeRegGLM = descriptor.Purpose{
		Plain:   "Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts.",
		KnownAs: []string{"generalized linear model", "logistic regression", "poisson regression", "glm"},
		Intents: []string{IntentDrivers, IntentRelationship},
		Questions: []string{
			"Which customer traits are associated with the chance of churning, holding the others fixed?",
			"How does the expected number of support tickets change with account size?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Relate a yes/no answer (would recommend) to respondent attributes.",
			descriptor.DomainOps:     "Model the count of incidents per site from traffic and staffing.",
			descriptor.DomainScience: "Relate the probability of a response to dose and age.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the outcome is a numeric measure with roughly symmetric, constant-spread residuals", Use: "REG_OLS"},
			{When: "you only want to know whether a yes/no rate differs between two groups", Use: "TEST_PROP_Z"},
			{When: "the outcome and the single predictor are both categories", Use: "TEST_CHISQ"},
		},
		Assumptions: []string{
			regIndependentRows,
			"The family must match the outcome: binomial needs a 0/1 (or packed_bool) target, Poisson non-negative counts, gamma positive values.",
			"Only the canonical link runs for each family (logit for binomial, log for Poisson, inverse for gamma); " +
				"the predictors act linearly on that link scale.",
			"Each p-value is a Wald z test of the null hypothesis that its coefficient is 0 given the other predictors, " +
				"with the dispersion fixed at 1. Overdispersed counts (variance above the mean) make Poisson p-values too small; " +
				"gamma data rarely have dispersion 1, so gamma standard errors and p-values are unreliable, and the gamma path " +
				"is not yet checked against a reference implementation.",
			"If a predictor separates the 0s from the 1s perfectly in a logistic model, the fit cannot converge and Pulse refuses it.",
			regAssociationOnly,
			regCollinearity,
			regNumericOnly,
			regNullsDropped,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"deviance", "generalized-linear-model", "independence", "link-function",
			"listwise-deletion", "logistic-regression", "multicollinearity", "null-hypothesis",
			"odds-ratio", "overdispersion", "p-value", "pseudo-r-squared", "regression-coefficient",
			"skew", "standard-error",
		},
	}

	purposeRegBayesLinear = descriptor.Purpose{
		Plain:   "Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval.",
		KnownAs: []string{"bayesian linear regression"},
		Intents: []string{IntentDrivers, IntentRelationship},
		Questions: []string{
			"Given the data and our prior belief, what range of values is plausible for the price coefficient?",
			"How do the coefficients shift when last year's estimates are used as the prior?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Fit a key-driver model on a small wave, using the previous wave's coefficients as the prior mean.",
			descriptor.DomainOps:     "Relate cost per order to volume when only a few weeks of data exist.",
			descriptor.DomainScience: "Combine a small new study with earlier estimates of a dose-response slope.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want p-values, resampled standard errors or stepwise predictor selection (none run on this type)", Use: "REG_OLS"},
			{When: "the outcome is yes/no (0/1) or a count", Use: "REG_GLM"},
			{When: "you only want how closely two numeric fields follow a straight line together", Use: "TEST_PEARSON_R"},
		},
		Assumptions: []string{
			regIndependentRows,
			"The outcome changes linearly with each predictor, with normal residuals of constant spread (the model's likelihood).",
			"The prior is conjugate Normal-Inverse-Gamma: each coefficient is normal around prior_mu (zeros by default, intercept first) " +
				"with one shared precision for every coefficient, intercept included, measured relative to the residual variance: each coefficient's prior " +
				"standard deviation is σ / sqrt(prior_precision), so the prior's strength changes with the noise level. The residual variance is inverse-gamma.",
			"The default prior is weak (precision 0.001), so with enough rows the coefficients sit close to REG_OLS. " +
				"Its pull depends on each predictor's units: a coefficient that is large because its predictor's units are small " +
				"is pulled harder toward prior_mu.",
			"Credible intervals are probability statements about each coefficient given the prior and the model; " +
				"a wrong prior or a wrong model makes them wrong too.",
			regAssociationOnly,
			"Strongly related predictors (multicollinearity) make coefficients unstable; the prior keeps even an exactly " +
				"collinear set solvable, but then the prior, not the data, decides how the shared association is split between them.",
			regNumericOnly,
			regNullsDropped,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"credible-interval", "heteroscedasticity", "independence", "listwise-deletion",
			"multicollinearity", "normal-distribution", "posterior", "prior", "r-squared",
			"regression-coefficient", "residual",
		},
	}
)
