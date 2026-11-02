package descriptor

import "github.com/frankbardon/pulse/descriptor"

// regressionPurposes is the Purpose registry for the REG_* types, keyed
// by type. builtinPurposes assembles it with the other category maps.
//
// Each Purpose states the model as Pulse fits it (see
// internal/processing/regression): classic (not robust) OLS standard
// errors with Student-t p-values on n - p - 1 degrees of freedom; GLM
// on the canonical link only, with Wald-z p-values at a dispersion
// fixed to 1 (binomial / poisson) or Student-t p-values on the residual
// df under gamma's estimated dispersion; a conjugate Normal-Inverse-Gamma Bayesian fit with one
// scalar prior precision on every coefficient, intercept included, and
// equal-tailed Student-t credible intervals. Every Purpose says that a
// coefficient describes association holding the other predictors fixed,
// never a causal effect on its own (TestGuidanceProseLint's CORR-CAUSAL
// rule for REG_*), and that rows must be independent (ASSUME-INDEP).
var regressionPurposes = map[string]descriptor.Purpose{
	"REG_BAYES_LINEAR": purposeRegBayesLinear,
	"REG_GLM":          purposeRegGLM,
	"REG_MMM_BAYES":    purposeRegMMMBayes,
	"REG_MMM_HILL":     purposeRegMMMHill,
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
				"l1 and elasticnet fits report no standard errors or p-values (null) because the data pick the predictors.",
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
			"The link must suit the outcome (each family takes its own: logit, probit or cloglog for binomial; log, identity or sqrt for counts; inverse, log or identity for gamma); " +
				"the predictors act linearly on that link scale, and margins converts the effects to the outcome's units.",
			"Each p-value tests the null hypothesis that its coefficient is 0 given the other predictors: a Wald z with the dispersion " +
				"fixed at 1 for binomial and Poisson, where overdispersed counts (variance above the mean) make Poisson p-values too small; " +
				"a Student t on the residual df for gamma, whose dispersion is estimated from the data as R's glm does.",
			"If a predictor separates the 0s from the 1s perfectly in a logistic model, the fit cannot converge and Pulse refuses it.",
			regAssociationOnly,
			regCollinearity,
			regNumericOnly,
			regNullsDropped,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"deviance", "generalized-linear-model", "independence", "link-function",
			"listwise-deletion", "logistic-regression", "marginal-effect", "multicollinearity", "null-hypothesis",
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

	purposeRegMMMHill = descriptor.Purpose{
		Plain:   "Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls.",
		KnownAs: []string{"media mix model", "marketing mix model", "mmm"},
		Intents: []string{IntentDrivers},
		Questions: []string{
			"What does each marketing channel contribute to sales, holding price and the other channels fixed?",
			"At what weekly spend does each channel saturate?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps: "Estimate which channels earn their spend, as the curves a budget split is then optimised over.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the outcome moves in a straight line with spend, with no saturation", Use: "REG_OLS"},
		},
		Assumptions: []string{
			regIndependentRows,
			"Each channel is spend already carried over from earlier periods (adstocked upstream, for example by WIN_DISTRIBUTED_LAG): " +
				"the model does not carry spend over itself, so the carry-over decay is searched outside it.",
			"Each channel's effect follows one Hill curve, rising from 0 and flattening toward its coefficient; the controls enter in a straight line.",
			"A channel's coefficient is held at 0 or above by default, so a channel the data do not support comes back exactly 0.",
			regAssociationOnly,
			"A channel whose spend never reaches its half-saturation leaves its curve weakly pinned down even when its total contribution is right.",
			regNullsDropped,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"independence", "listwise-deletion", "regression-coefficient", "standard-error",
		},
	}

	purposeRegMMMBayes = descriptor.Purpose{
		Plain:   "Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls.",
		KnownAs: []string{"bayesian media mix model", "bayesian marketing mix model", "bayesian mmm"},
		Intents: []string{IntentDrivers},
		Questions: []string{
			"What return does each marketing channel earn, and how sure are we?",
			"How long does each channel's effect carry over?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps: "Which channels earn their spend, with intervals, before moving budget.",
		},
		NotFor: []descriptor.Alternative{
			{When: "a fast point estimate over already-adstocked channels is enough, or the fit must be saved, scored or optimised", Use: "REG_MMM_HILL"},
		},
		Assumptions: []string{
			"The rows are one market's series, one row per period with contiguous integer times: a gap or a repeated period is refused. " +
				"Each period's noise is independent of the others once the carry-over is modelled: leftover dependence over time makes the intervals too narrow.",
			"Each channel is raw spend, carried over geometrically inside the model over max_lag periods, so the decay is estimated with everything else.",
			"Each channel's effect follows one Hill curve, rising from 0 and flattening toward its coefficient; the controls enter in a straight line.",
			"The priors shape the answer where the data are thin: an experiment's ROI is the honest prior to set.",
			regAssociationOnly,
			regNullsDropped,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"listwise-deletion", "regression-coefficient",
		},
	}
)
