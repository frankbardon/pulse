package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/descriptor"
)

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
	"REG_MMM_BAYES":    interpRegMMMBayes,
	"REG_MMM_HILL":     interpRegMMMHill,
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
			"Under a row weight it stays the row count: the fit's sample size is sum_weights (frequency weights) or n_eff, Kish's effective sample size (probability weights), and the standard errors and residual degrees of freedom read that, not n_obs.",
		},
	}
	regAdjR2Caveats = []string{
		"It can fall below 0 when the predictors fit no better than chance would.",
		"There are no published bands for adjusted R-squared, so none are attached: Cohen's R-squared benchmarks are stated for R-squared itself.",
	}
	regRSE = "Typical size of a residual, in the outcome's units: how far actual values sit from the fitted line on a typical row."

	regCIRepeated = "It is a statement about the method, not this one range: intervals built this way cover the true coefficient " +
		"in about conf_level of repeated samples, if the model's assumptions hold."
	regCIOneAtATime = "Each interval covers one coefficient on its own; several intervals read together do not jointly cover at conf_level."
	regConfLevel    = descriptor.Interpretation{
		Field: "conf_level",
		Means: "The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).",
	}
)

// regCI is the ci_lower.* / ci_upper.* pair for one type: means names
// how the bound is built, caveats are the type's own.
func regCI(means string, caveats ...string) []descriptor.Interpretation {
	return []descriptor.Interpretation{
		{Field: "ci_lower.*", Means: "Lower bound of the coefficient's two-sided confidence interval at conf_level: " + means, Caveats: caveats},
		{Field: "ci_upper.*", Means: "Upper bound of the coefficient's two-sided confidence interval at conf_level: " + means, Caveats: caveats},
	}
}

var interpRegOLS = slices.Concat([]descriptor.Interpretation{
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
			"Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 on purpose, on the predictors' own units (a predictor on a large scale is barely shrunk); " +
				"an l1 coefficient shrunk to 0 means the penalty dropped it, not that the predictor is unrelated to the outcome.",
		},
	},
	{
		Field: "std_errors.*",
		Means: "How much the coefficient would vary from sample to sample; smaller means a more precise estimate. " +
			"It is the classic standard error, which assumes residuals of constant spread, unless the request's covariance names a robust one.",
		Caveats: []string{
			"Classic: with heteroscedasticity (residual spread that changes) it can be too small or too large, and with dependent rows it is usually too small; " +
				"covariance hc3 guards the first, cluster (rows grouped by a field) or hac (a series in time) the second, and p_values, the intervals and vcov follow it.",
			"Null on every coefficient of an l1 or elasticnet fit: the penalty picks the predictors from the data, so no analytical standard error is valid " +
				"(advisory PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED); request resample for replicate ones. An l2 fit's is the closed-form ridge sandwich.",
			"Null on every coefficient of a bounds fit whose non-negativity constraint binds (a coefficient held at 0): the constrained estimator has no classical standard error " +
				"(advisory PULSE_ADVISORY_CONSTRAINT_ACTIVE); resample bootstrap refits the bounded model for replicate ones. When no bound binds the fit is plain OLS.",
		},
	},
	{
		Field:  "p_values.*",
		Shared: SharedPValue,
		Caveats: []string{
			"The request's alpha param is the penalty strength for l1 / l2 / elasticnet fits, not a significance level; Pulse applies no significance threshold to these p-values.",
			"On an l2 fit the coefficients are shrunk on purpose, so these p-values do not have the usual reading; on an l1 or elasticnet fit they are null (no valid analytical form).",
		},
	},
}, append(regCI("the coefficient minus (lower) or plus (upper) a Student-t critical value on df times its standard error.",
	regCIRepeated,
	regCIOneAtATime,
	"Under resample the critical value is the normal one and the standard error the resampled one; df is then absent.",
	"On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.",
), regConfLevel, descriptor.Interpretation{
	Field: "df",
	Means: "Residual degrees of freedom the intervals' critical value reads: n - p - 1, with sum_weights or n_eff in place of n on a weighted fit, so it can be fractional; " +
		"under covariance cluster, the number of clusters minus 1.",
	Caveats: []string{
		"Null when it is 0 or below: too few rows (or too small an effective sample) for the predictors, and every bound is then null too.",
	},
}), []descriptor.Interpretation{
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
})

var interpRegGLM = slices.Concat([]descriptor.Interpretation{
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
			"Penalized fits (l1, l2, elasticnet) shrink coefficients toward 0 on purpose, on the predictors' own units; an l1 coefficient shrunk to 0 " +
				"means the penalty dropped it, not that the predictor is unrelated to the outcome.",
		},
	},
	{
		Field: "std_errors.*",
		Means: "How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix: " +
			"with the dispersion fixed at 1 for binomial and Poisson, and scaled by the estimated dispersion for gamma.",
		Caveats: []string{
			"With overdispersion (counts varying more than their mean) the Poisson standard errors are too small.",
			"The gamma dispersion is estimated as the Pearson chi-square over the residual df, as R's summary.glm does, so gamma standard errors " +
				"need no dispersion assumption; with no residual df (as many rows as coefficients) they are null.",
			"Unless the request's covariance names a robust one: hc0-hc3 (sandwich over the fit's scores, which also absorbs overdispersion), " +
				"cluster (rows grouped by a field) or hac (a series in time); p_values, the intervals and vcov follow it, still Wald z.",
			"On an l2 fit it is the penalized sandwich, which treats the shrinkage as fixed; on an l1 or elasticnet fit it is null on every coefficient " +
				"(the data pick the predictors, so no analytical standard error is valid); request resample for replicate ones.",
		},
	},
	{
		Field:  "p_values.*",
		Shared: SharedPValue,
		Caveats: []string{
			"Binomial and Poisson: Wald z at dispersion fixed to 1, so with overdispersed counts the Poisson p-values come out too small. " +
				"Gamma: a Student t on df, because its dispersion is estimated (R's summary.glm); a robust covariance makes it Wald z again.",
			"The request's alpha param is the penalty strength, not a significance level; on an l2 fit the coefficients are shrunk on purpose, " +
				"so these p-values do not have the usual reading, and on an l1 or elasticnet fit they are null.",
		},
	},
}, append(regCI("the coefficient minus (lower) or plus (upper) a critical value times its standard error, on the link scale: "+
	"standard normal (a Wald interval) for binomial and Poisson, Student t on df for gamma.",
	regCIRepeated,
	regCIOneAtATime,
	"Binomial and Poisson read dispersion fixed to 1: too narrow with overdispersed counts, and poor near separation in a logistic model.",
	"Raised to the power e, the bounds of a binomial or Poisson coefficient bound its odds ratio or rate ratio.",
	"On an l2 fit the interval is centred on a shrunken coefficient, so it does not have the usual coverage; on an l1 or elasticnet fit both bounds are null.",
), regConfLevel, descriptor.Interpretation{
	Field: "df",
	Means: "Gamma only: the residual degrees of freedom n - p - 1 (sum_weights or n_eff in place of n on a weighted fit, so it can be fractional) " +
		"that the estimated dispersion divides by and the p-values' and intervals' Student t reads.",
	Caveats: []string{
		"Absent on binomial and Poisson fits, whose dispersion is fixed at 1, and on any fit under a robust covariance or resample, which use the normal.",
		"Null when it is 0 or below: no residual df to estimate the dispersion from, so the standard errors, p-values and bounds are null too.",
	},
}), []descriptor.Interpretation{
	{
		Field: "deviance",
		Means: "How far the fitted predictions sit from the observed outcomes, as twice the log-likelihood gap to a model " +
			"that fits every row exactly; lower is closer.",
		Caveats: []string{
			"On a penalized fit it is the unpenalized deviance at the shrunken coefficients, so it sits above the unpenalized fit's.",
			"It has no fixed scale: compare it only with null_deviance or with another model fitted to the same rows and outcome.",
			"For a 0/1 outcome the deviance alone does not show whether the model fits.",
			"Under selection, binomial and Poisson candidates are scored by deviance plus the parameter penalty; a gamma candidate by R's AIC / BIC " +
				"(its likelihood at dispersion deviance / n, the dispersion counted as a parameter), so a gamma deviance gap weighs 1 / dispersion times more.",
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
		Means: "Iteratively reweighted least squares steps taken before the coefficients stopped changing (relative change below tol); a penalized fit counts the same outer steps.",
		Caveats: []string{
			"A fit that cannot converge within max_iters is refused rather than reported.",
		},
	},
})

var interpRegBayesLinear = slices.Concat([]descriptor.Interpretation{
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
			"Under a robust covariance (hc0-hc3, cluster, hac) it is instead the scale of Muller's sandwich posterior, a normal centred on the same " +
				"posterior mean whose spread is the sandwich, not the model's: the posterior covariance is REPLACED, so the model's own uncertainty no longer enters.",
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
			"Under a robust covariance it is the sandwich posterior's normal interval (no df), a substitute for the posterior: honest about " +
				"heteroskedastic or dependent rows, but no longer the conjugate model's probability statement.",
		},
	},
}, append(regCI("the equal-tailed posterior interval on the coefficient's Student-t posterior with df degrees of freedom, as in credible_intervals.",
	"With conf_level unset it is built at credible_level, so it equals credible_intervals; it reads like them, as a probability given the prior and the model.",
), regConfLevel, descriptor.Interpretation{
	Field: "df",
	Means: "Degrees of freedom of the coefficients' Student-t posterior, 2 * a_n, which the interval's critical value reads.",
}), []descriptor.Interpretation{
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
})

var interpRegMMMHill = slices.Concat([]descriptor.Interpretation{
	{
		Field: "coefficients.*",
		Means: "A channel's coefficient is the most its curve can add to the outcome (the effect at full saturation), in outcome units, " +
			"holding the other predictors fixed; a control's is the change in the outcome per unit of the control, holding the other predictors fixed; " +
			"k:<channel> is the spend at which the channel delivers half its maximum and s:<channel> how sharply its curve bends there.",
		Sign: regLinearSign,
		Caveats: []string{
			regCoefCausal,
			"A channel's coefficient is held at 0 or above by default: exactly 0 means the data do not support an effect, not a proven absence of one.",
			"A channel whose spend never reaches its k sits on a ridge where only the ratio of its coefficient to k^s is pinned down: " +
				"judge it by its contribution, not by k or s.",
			regInterceptAtZero,
		},
	},
	{
		Field: "std_errors.*",
		Means: "How much the parameter would vary from sample to sample, from the curvature of the least-squares fit at its optimum " +
			"(Gauss-Newton), mapped onto k and s.",
		Caveats: []string{
			"Null for a parameter that ends on its bound, and for the k and s of a channel whose coefficient is held at 0: the data cannot inform the shape of a curve that adds nothing.",
			"It treats the carry-over decay as known; a decay chosen by a search makes the true uncertainty larger.",
			"With rows ordered in time it is usually too small (see the independence assumption); resample bootstrap gives replicate ones.",
		},
	},
	{
		Field:  "p_values.*",
		Shared: SharedPValue,
		Caveats: []string{
			"Null on a plain fit: the model reports none itself; under resample each comes from the replicate fits.",
		},
	},
	{
		Field: "statistics.*",
		Means: "The Wald statistic: the parameter divided by its standard error, null where the standard error is.",
		Caveats: []string{
			"It is not converted to a p-value on a plain fit; a k or s statistic tests a value of 0 the curve never takes, so read it only as precision.",
		},
	},
	{
		Field: "outputs.*",
		Means: "outputs.k and outputs.s repeat each channel's k:<channel> and s:<channel> coefficients as objects keyed by channel.",
	},
}, append(regCI("the parameter minus (lower) or plus (upper) a Student-t critical value on df times its standard error.",
	regCIRepeated,
	regCIOneAtATime,
	"Null where the standard error is; under resample the critical value is the normal one on the replicate standard error.",
), regConfLevel, descriptor.Interpretation{
	Field: "df",
	Means: "Residual degrees of freedom n minus every fitted parameter (intercept, channel coefficients, controls, and each channel's k and s).",
}), []descriptor.Interpretation{
	{
		Field: "r2",
		Means: "R-squared: the share of the outcome's variation around its mean that the fitted curves and controls account for in these rows.",
		Caveats: []string{
			"It is measured on the rows the model was fitted to; a holdout on the last periods shows how well the curves predict.",
			"There are no published bands for a nonlinear model's R-squared, so none are attached.",
		},
	},
	{
		Field: "residual_std_err",
		Means: regRSE + " It divides the residual sum of squares by df.",
	},
	{
		Field: "deviance",
		Means: "The residual sum of squares at the fit: lower is closer; compare it only across fits of the same rows and outcome.",
	},
	regNObs,
	{
		Field: "converged_iters",
		Means: "Least-squares solver iterations taken before the fit settled.",
		Caveats: []string{
			"A fit that cannot converge within the iteration cap fails its slot alone rather than being reported.",
		},
	},
})

var interpRegMMMBayes = slices.Concat([]descriptor.Interpretation{
	{
		Field: "coefficients.*",
		Means: "Each coefficient's posterior mean in original units: a channel's coefficient is the most its curve can add to the outcome (the effect at full saturation) " +
			"and a control's the change in the outcome per unit of the control, each holding the other predictors fixed; " +
			"decay:<channel>, k:<channel> and s:<channel> are each channel's carry-over, half-saturation spend and curve shape, and sigma the noise SD.",
		Sign: regLinearSign,
		Caveats: []string{
			regCoefCausal,
			"An experiment-measured prior on a channel's ROI anchors its effect.",
			"k and s trade off against each other on a channel whose spend never reaches its k: judge it by its ROI and contribution, not by k or s.",
			regInterceptAtZero,
		},
	},
	{
		Field: "std_errors.*",
		Means: "The posterior standard deviation of each coefficient over every retained draw of every chain: the spread of what the data and the priors allow.",
		Caveats: []string{
			"It includes the carry-over decay's uncertainty, which a fit over already-adstocked channels cannot.",
		},
	},
	{
		Field:  "p_values.*",
		Shared: SharedPValue,
		Caveats: []string{
			"Always null: the model reports no p-value; read the credible intervals in outputs instead.",
		},
	},
	{
		Field: "statistics.*",
		Means: "Always null: the model reports no test statistic.",
	},
	{
		Field: "outputs.*",
		Means: "The posterior's own answers, each summary {mean, median, sd, lower, upper} with [lower, upper] the credible interval at credible_level: " +
			"roi per channel, the outcome each unit of spend returned over the fitted period against spending nothing on it; " +
			"mroi per channel, the return on the next unit of spend at the fitted level (below the ROI when the channel saturates); " +
			"contribution per channel, the outcome it produced over the fitted period; params per coefficient; " +
			"diagnostics, whether the chains agree and explored the posterior (R-hat at most 1.01, effective sample sizes of at least 400, no divergences or tree-depth hits); " +
			"holdout, with params.holdout_from, the held-out periods' rmse and the share inside the predictive interval (near credible_level when the intervals are honest); " +
			"and seed, the seed the sampler ran with.",
		Caveats: []string{
			"A credible interval is conditional on the model and the priors: a wrong adstock window, a missing control or a seasonal confounder moves it without widening it.",
			"An ROI below 1 means the channel returned less outcome than it cost only when the outcome and the spend are in the same currency.",
			"mroi is a local slope: it holds the fitted response curve fixed and is not the return of a large budget shift.",
			"Contributions are attributed by the model; they are not observed.",
			"A fit that fails a diagnostics check carries a PROCESSING_REGRESSION_DIAGNOSTIC warning naming it; its intervals may be too narrow or off-centre.",
			"A short holdout gives a noisy coverage: with ten periods each one moves it by a tenth.",
		},
	},
}, append(regCI("the posterior mean minus (lower) or plus (upper) a normal critical value times the posterior SD, a normal approximation to the posterior.",
	"For a skewed posterior (a decay near 0 or 1, a k on its floor) it can leave the parameter's range: read the credible intervals in outputs.params instead.",
	regCIOneAtATime,
), regConfLevel, descriptor.Interpretation{
	Field: "n_obs",
	Means: "Rows used in the fit, after rows with a null target or predictor were dropped; with params.holdout_from, the held-out rows are counted too.",
	Caveats: []string{
		"A dropped row leaves a gap in the series, which the model refuses: the series must stay contiguous.",
	},
}))
