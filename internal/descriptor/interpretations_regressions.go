package descriptor

import "github.com/frankbardon/pulse/descriptor"

// regressionInterpretations is the Interpretation registry for the REG_*
// types: how to read each RegressionResult key the type emits on its
// plain fit path. Fields respect the per-type applicability table
// regressionOutputs (no r2 on REG_GLM, no p_values on REG_BAYES_LINEAR,
// pseudo_r2 on REG_GLM only); map-valued outputs are read through their
// "<key>.*" pattern, one reading for every predictor and "(intercept)".
//
// Only REG_OLS r2 is banded, by the registered Cohen R-squared
// convention; adjusted R-squared, the Bayesian posterior-mean R-squared
// and the GLM pseudo-R-squared are listed under the convention
// fixture's exclusions with their reasons.
var regressionInterpretations = map[string][]descriptor.Interpretation{
	"REG_BAYES_LINEAR": interpRegBayesLinear,
	"REG_GLM":          interpRegGLM,
	"REG_OLS":          interpRegOLS,
}

// Shared readings, so the three types say the same thing the same way.
var (
	regCoefCausal = "Association, not causation: the coefficient shows how the outcome moves with this predictor " +
		"in these rows, holding the other predictors fixed; a causal reading needs a design that supports it, " +
		"and a predictor left out of the model can drive both."
	regCoefUnits = "Its size depends on the predictor's units, so do not rank predictors by raw coefficients " +
		"unless they share units; standardize the predictors first to compare them."
	regCoefCollinear = "With strongly related predictors (multicollinearity) a coefficient can swing, or even flip sign, " +
		"between fits on similar data."
	regInterceptAtZero = "(intercept) is the prediction when every predictor is 0, which may lie outside the data and mean nothing."
	regLinearSign      = map[string]string{
		"+": "higher values of the predictor are associated with a higher outcome, other predictors held fixed",
		"-": "higher values of the predictor are associated with a lower outcome, other predictors held fixed",
	}

	regNObs = descriptor.Interpretation{
		Field: "n_obs",
		Means: "Rows used in the fit, after rows with a null target or predictor were dropped.",
		Caveats: []string{
			"Few rows per predictor make every estimate unstable and invite overfitting.",
		},
	}
	regAdjR2Caveats = []string{
		"It can fall below 0 when the predictors fit no better than chance would.",
		"There are no published bands for adjusted R-squared, so none are attached: Cohen's R-squared benchmarks are stated for R-squared itself.",
	}
	regRSE = "Typical size of a residual, in the outcome's units: how far actual values sit from the fitted line on a typical row."
)

var interpRegOLS = []descriptor.Interpretation{
	{
		Field: "coefficients.*",
		Means: "The difference in the outcome's expected value between rows one unit apart on this predictor, holding the other " +
			"predictors fixed, in outcome units per predictor unit (an association, not the effect of changing it).",
		Sign: regLinearSign,
		Caveats: []string{
			regCoefCausal,
			regCoefUnits,
			regCoefCollinear,
			regInterceptAtZero,
			"Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 on purpose; an l1 coefficient shrunk to 0 " +
				"means the penalty dropped it, not that the predictor is unrelated to the outcome.",
		},
	},
	{
		Field: "std_errors.*",
		Means: "How much the coefficient would vary from sample to sample; smaller means a more precise estimate. " +
			"Pulse reports the classic standard error, which assumes residuals of constant spread.",
		Caveats: []string{
			"With heteroscedasticity (residual spread that changes) it can be too small or too large; with dependent rows it is usually too small.",
			"For l1 and elasticnet fits it is a plug-in approximation over the kept predictors (Pulse warns); request resample for an empirical one.",
		},
	},
	{
		Field:  "p_values.*",
		Shared: SharedPValue,
		Caveats: []string{
			"The request's alpha param is the penalty strength for l1 / l2 / elasticnet fits, not a significance level; Pulse applies no significance threshold to these p-values.",
			"On a penalized fit (l1, l2, elasticnet) the coefficients are shrunk on purpose, so these p-values do not have the usual reading; l1 and elasticnet ones are approximate.",
		},
	},
	bandedBy(ConventionCohenR2, descriptor.Interpretation{
		Field: "r2",
		Means: "R-squared: the share of the outcome's variation around its mean that the fitted model accounts for in these rows, from 0 to 1.",
		Caveats: []string{
			"It never falls when a predictor is added, even a useless one; compare models of different sizes with adj_r2.",
			"It is measured on the rows the model was fitted to, so it overstates how well the model will predict new rows.",
			"The bands are Cohen's benchmarks for a population R-squared, set for behavioural-science research as a last resort; " +
				"what counts as a good fit depends on the field and the outcome.",
			"A high value does not mean the model is right or that the predictors cause the outcome.",
		},
	}),
	{
		Field:   "adj_r2",
		Means:   "R-squared corrected for the number of predictors: 1 - (1 - r2) * (n - 1) / (n - p - 1), for p predictors.",
		Caveats: regAdjR2Caveats,
	},
	{
		Field: "residual_std_err",
		Means: regRSE + " It divides the residual sum of squares by n - p - 1.",
	},
	regNObs,
	{
		Field: "converged_iters",
		Means: "Coordinate-descent iterations an l1 or elasticnet fit took to converge; absent for fits solved in closed form.",
		Caveats: []string{
			"A value at max_iters suggests the fit stopped before settling; raise max_iters or tol.",
		},
	},
}

var interpRegGLM = []descriptor.Interpretation{
	{
		Field: "coefficients.*",
		Means: "The change in the linear predictor, on the link scale, for a one-unit increase in this predictor, holding " +
			"the other predictors fixed: log-odds for binomial (logit link), the log of the expected count for Poisson (log link), " +
			"the inverse of the expected value for gamma (inverse link).",
		Sign: map[string]string{
			"+": "higher predictor values are associated with a higher probability (binomial) or expected count (Poisson); " +
				"under gamma's inverse link, with a LOWER expected value",
			"-": "higher predictor values are associated with a lower probability (binomial) or expected count (Poisson); " +
				"under gamma's inverse link, with a HIGHER expected value",
		},
		Caveats: []string{
			"Raised to the power e, a binomial coefficient is an odds ratio and a Poisson one a rate ratio per one-unit increase: " +
				"multiplicative, not additive. An odds ratio is not a ratio of probabilities.",
			regCoefCausal,
			regCoefUnits,
			regCoefCollinear,
			"A very large coefficient with a very large standard error in a logistic model is a sign of near-separation; do not read it as a strong association.",
		},
	},
	{
		Field: "std_errors.*",
		Means: "How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix, " +
			"with the dispersion fixed at 1.",
		Caveats: []string{
			"With overdispersion (counts varying more than their mean) the Poisson standard errors are too small.",
			"Gamma data rarely have dispersion 1, so gamma standard errors are unreliable.",
		},
	},
	{
		Field:  "p_values.*",
		Shared: SharedPValue,
		Caveats: []string{
			"Wald z at dispersion fixed to 1: with overdispersed counts the Poisson p-values come out too small, and gamma p-values are unreliable.",
		},
	},
	{
		Field: "deviance",
		Means: "How far the fitted predictions sit from the observed outcomes, as twice the log-likelihood gap to a model " +
			"that fits every row exactly; lower is closer.",
		Caveats: []string{
			"It has no fixed scale: compare it only with null_deviance or with another model fitted to the same rows and outcome.",
			"For a 0/1 outcome the deviance alone does not show whether the model fits.",
		},
	},
	{
		Field: "null_deviance",
		Means: "The deviance of the intercept-only model, which predicts the overall mean for every row: the baseline the predictors are judged against.",
	},
	{
		Field: "pseudo_r2",
		Means: "1 - deviance / null_deviance: the share of the intercept-only model's deviance the predictors remove, from 0 to 1. " +
			"For a 0/1 binomial outcome this equals McFadden's pseudo-R-squared.",
		Caveats: []string{
			"It is not a share of variance explained and runs much lower than an OLS R-squared for an equally useful model, " +
				"so OLS rules of thumb do not apply; there are no published bands for it, so none are attached.",
			"Compare it only across models of the same outcome and family.",
		},
	},
	regNObs,
	{
		Field: "converged_iters",
		Means: "Iteratively reweighted least squares steps taken before the coefficients stopped changing (relative change below tol).",
		Caveats: []string{
			"A fit that cannot converge within max_iters is refused rather than reported.",
		},
	},
}

var interpRegBayesLinear = []descriptor.Interpretation{
	{
		Field: "coefficients.*",
		Means: "Posterior mean of the coefficient: the expected difference in the outcome between rows one unit apart on this predictor, " +
			"holding the other predictors fixed, after combining the data with the prior.",
		Sign: regLinearSign,
		Caveats: []string{
			regCoefCausal,
			"With the default weak prior and enough rows it is close to the REG_OLS coefficient; a stronger prior pulls it toward prior_mu (0 by default).",
			regCoefUnits,
			regInterceptAtZero,
		},
	},
	{
		Field: "std_errors.*",
		Means: "Scale of the coefficient's Student-t posterior: how uncertain the coefficient is given the data and the prior. " +
			"With many rows it is close to the posterior standard deviation.",
		Caveats: []string{
			"It is a posterior spread, not a frequentist standard error, and no p-value is built from it.",
		},
	},
	{
		Field: "credible_intervals.*",
		Means: "[lower, upper] equal-tailed posterior interval at credible_level (95% by default): given the prior and the model, " +
			"the coefficient lies in this range with that probability.",
		Caveats: []string{
			"It is a probability statement about the coefficient conditional on the prior and the model, not a statement about repeated samples; " +
				"if either is wrong, so is the probability.",
			"With the default weak prior and many rows per predictor it is close to the OLS confidence interval. With few rows it is narrower, " +
				"because the default variance prior divides by about n rather than n - p - 1 (about 19% narrower at 20 rows and 5 predictors). " +
				"With an informative prior it also reflects that prior.",
			"An interval that excludes 0 is not a significance test; report the interval itself.",
		},
	},
	{
		Field: "r2",
		Means: "R-squared of the posterior-mean fit: the share of the outcome's variation the posterior-mean coefficients account for in these rows.",
		Caveats: []string{
			"It is computed from the posterior-mean coefficients, not averaged over the posterior, so it is not a Bayesian R-squared.",
			"A strong prior pulls the fit away from least squares, so it can sit below the REG_OLS value and even below 0; " +
				"there are no published bands for it, so none are attached.",
		},
	},
	{
		Field:   "adj_r2",
		Means:   "The posterior-mean fit's R-squared corrected for the number of predictors: 1 - (1 - r2) * (n - 1) / (n - p - 1).",
		Caveats: regAdjR2Caveats,
	},
	{
		Field: "residual_std_err",
		Means: regRSE + " Here it is sqrt(b_n / a_n) from the posterior on the variance, combining the data with the prior on it.",
		Caveats: []string{
			"Under the default prior that is about sqrt(RSS / n), so it runs below REG_OLS's value (which divides by n - p - 1), most visibly " +
				"with few rows per predictor; do not read the gap as a better fit.",
		},
	},
	regNObs,
}
