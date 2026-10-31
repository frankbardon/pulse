package descriptor

import "github.com/frankbardon/pulse/descriptor"

// matrixInterpretations is the Interpretation registry for the MAT_*
// operators, keyed by type; builtinInterpretations merges it with the
// other category maps. Paths read a MatrixResult: primary.values (every
// cell of the primary matrix) and scalars.<key> (manifest
// output_keys.scalars). MAT_COVARIANCE's cells are in the members' own
// units, so it declares none.
var matrixInterpretations = map[string][]descriptor.Interpretation{
	"MAT_CORRELATION":         interpMatCorrelation,
	"MAT_PARTIAL_CORRELATION": interpMatPartialCorrelation,
	"MAT_RELIABILITY":         interpMatReliability,
	"MAT_PCA":                 interpMatPCA,
	"MAT_COLLINEARITY":        interpMatCollinearity,
	"MAT_FACTOR":              interpMatFactor,
	"MAT_SEM":                 interpMatSEM,
}

// interpMatSEM leaves every fit index unbanded: CFI / TLI / RMSEA
// cut-offs are rules of thumb, not verified conventions.
var interpMatSEM = []descriptor.Interpretation{
	{
		Field: "primary.values",
		Means: "Each cell is the covariance the fitted model implies between the row's and the column's measure, in their own units. Compared with the observed covariance (auxiliary.residual_covariance is the difference), it shows where the model reproduces the data and where it does not.",
		Caveats: []string{
			"Large residuals concentrated on a few pairs point at a missing path or covariance between those measures.",
		},
	},
	{
		Field: "auxiliary.estimates",
		Means: "The parameter table, one row per semopy inspect row: lval ~ rval is a loading (an indicator on its quality) or a regression path, lval ~~ rval a variance or covariance, and with a mean structure lval ~1 an intercept (a measure) or a latent mean (a quality). estimate is in the variables' own units; std_estimate rescales it so loadings and paths compare across variables; std_err, z_value and p_value test it against zero, and on a lavaan-convention fit ci_lower / ci_upper bound its Wald interval, 95% by default. params.ci_level sets another level. On a multi-group fit every row is prefixed [level]; a quality's latent mean in a level other than the reference is its mean difference from the reference level, whose latent means are fixed at 0. A name := expr row is a defined parameter, a function of labelled parameters such as an indirect effect or a path times a latent mean change, with a delta-method std_err; under resample bootstrap every free and := row's std_err is the replicates' spread and ci_lower / ci_upper the percentile interval. Under estimator dwls or wlsmv each ordered measure adds one x | tk row per threshold: the cut point, on the standard normal scale of the continuous response assumed beneath the ratings, above which category k + 1 begins; a multi-group ordinal fit adds an x ~*~ x row, the measure's scale factor in that level.",
		Sign: map[string]string{
			"+": "the left variable rises with the right one; for a latent mean, the level sits above the reference level",
			"-": "the left variable falls as the right one rises; for a latent mean, the level sits below the reference level",
		},
		Caveats: []string{
			"Each quality's first indicator is fixed to 1 to set its scale, so that row has no standard error.",
			"A variance at 0, or a standardized loading beyond 1, means an improper solution; simplify the model.",
			"Latent means compare across levels only when the loadings and intercepts are held equal (group_equal loadings, intercepts); without that the qualities need not be measured on the same scale in every level.",
			"A defined parameter, such as a contribution, differs from zero when its p_value is below alpha and its interval excludes 0; a product or ratio is skewed, so its symmetric delta-method interval can mislead where the bootstrap's percentile interval does not.",
		},
	},
	{
		Field:  "scalars.chi2_p_value",
		Shared: SharedPValue,
	},
	{
		Field: "scalars.chi2",
		Means: "The likelihood-ratio chi-square of the model against a free covariance (and free means, with a mean structure), on dof degrees of freedom: N times the minimised discrepancy. A small p says the model leaves covariance unexplained. On a multi-group fit it is the sum of each level's share, reported per level as chi2_group_<level> beside that level's n_group_<level>.",
		Caveats: []string{
			"With many rows it flags even trivial misfit; read cfi, tli and rmsea beside it.",
			"It assumes multivariate normal measures and rests on N, the rows (the summed weight under a frequency weight).",
			"Between two nested multi-group fits on the same rows (say equal loadings against none), the difference in chi2 on the difference in dof tests the added equality constraints.",
		},
	},
	{
		Field: "scalars.cfi",
		Means: "The comparative fit index: how much better the model fits than a baseline in which no measure is related to any other, from 0 to 1. Higher is better.",
		Caveats: []string{
			"No convention bands it here; published cut-offs are rules of thumb that depend on the model's size and the data.",
		},
	},
	{
		Field: "scalars.tli",
		Means: "The Tucker-Lewis index: CFI's comparison against the baseline with a penalty for every free parameter, so it can fall below 0 or rise above 1. Higher is better.",
	},
	{
		Field: "scalars.rmsea",
		Means: "The root mean square error of approximation: misfit per degree of freedom, adjusted for N, with 0 a perfect fit. Smaller is better.",
		Caveats: []string{
			"It is 0 whenever the chi-square falls below its degrees of freedom.",
		},
	},
	{
		Field: "scalars.aic",
		Means: "semopy's AIC, 2(k - F) with k the free parameters and F the minimised discrepancy: for comparing models of the same measures, lower preferred. On a lavaan-convention fit (a mean structure or a group) it is lavaan's log-likelihood AIC, -2 loglik + 2k, instead.",
	},
	{
		Field: "scalars.bic",
		Means: "semopy's BIC, ln(N)k - 2F: as aic with a heavier penalty per parameter; for comparing models of the same measures, lower preferred. On a lavaan-convention fit it is lavaan's -2 loglik + k ln(N).",
	},
	{
		Field: "scalars.loglik",
		Means: "On a lavaan-convention fit, the model's log-likelihood (lavaan's logl), the basis of its aic and bic; on a semopy fit, semopy's minimised discrepancy F. For comparing models fitted to the same rows and measures; higher fits better.",
	},
	{
		Field: "scalars.chi2_scaled",
		Means: "Under estimator wlsmv, the scaled-shifted chi-square: chi2 rescaled and shifted (a times chi2 plus shift_parameter, with 1/a the scaling_factor) so that it follows a chi-square on dof degrees of freedom when the measures are ordinal, which the unscaled chi2 does not. Its p is chi2_scaled_p_value, read like any p-value; a small one says the model leaves the polychoric correlations unexplained.",
		Caveats: []string{
			"Under wlsmv, judge fit by chi2_scaled and the _scaled indices; the unscaled chi2 has no p (null), as in lavaan.",
			"Under wlsmv, chi2_scaled is not additive: the difference between two nested fits' chi2_scaled is not itself a chi-square.",
		},
	},
	{
		Field: "scalars.scaling_factor",
		Means: "Under estimator wlsmv, lavaan's scaling factor of the scaled-shifted test: chi2_scaled is chi2 divided by it plus shift_parameter. The further scaling_factor sits from 1, the further the unscaled chi2 is from a chi-square on these ordinal measures.",
	},
	{
		Field: "scalars.shift_parameter",
		Means: "Under estimator wlsmv, the constant added after rescaling chi2 into chi2_scaled, chosen so that the scaled statistic has the mean and variance of a chi-square on dof.",
	},
	{
		Field: "scalars.chi2_baseline_scaled",
		Means: "Under estimator wlsmv, the baseline (independence) model's scaled-shifted chi-square, the reference cfi_scaled and tli_scaled compare the model against.",
	},
	{
		Field: "scalars.cfi_scaled",
		Means: "Under estimator wlsmv, the comparative fit index computed from chi2_scaled and chi2_baseline_scaled, read like cfi: from 0 to 1, higher is better.",
		Caveats: []string{
			"Under wlsmv, published cfi cut-offs were set for maximum likelihood on continuous measures and need not carry over to cfi_scaled.",
		},
	},
	{
		Field: "scalars.tli_scaled",
		Means: "Under estimator wlsmv, the Tucker-Lewis index computed from chi2_scaled and chi2_baseline_scaled, read like tli: higher is better, and it can leave the 0 to 1 range.",
	},
	{
		Field: "scalars.rmsea_scaled",
		Means: "Under estimator wlsmv, the root mean square error of approximation computed from chi2_scaled, read like rmsea: misfit per degree of freedom, 0 a perfect fit, smaller is better.",
	},
	{
		Field: "auxiliary.implied_means",
		Means: "With a mean structure, the mean of each measure the fitted model implies: its intercept plus its loadings times the latent means. Compared with auxiliary.observed_means it shows where the model reproduces the measures' means and where it does not.",
	},
	{
		Field: "auxiliary.observed_means",
		Means: "With a mean structure, each measure's sample mean over the fitted rows (the weighted mean under a frequency weight): the means the model was fitted to.",
	},
	{
		Field: "auxiliary.implied_means.*",
		Means: "On a multi-group fit, one matrix per level of the group field: the means the fitted model implies within that level, read like auxiliary.implied_means.",
	},
	{
		Field: "auxiliary.observed_means.*",
		Means: "On a multi-group fit, one matrix per level: that level's sample means of the measures, read like auxiliary.observed_means.",
	},
	{
		Field: "auxiliary.implied_covariance.*",
		Means: "On a multi-group fit, one matrix per level: the covariance the fitted model implies within that level (the primary is the reference level's).",
	},
	{
		Field: "auxiliary.residual_covariance.*",
		Means: "On a multi-group fit, one matrix per level: that level's observed covariance minus its implied covariance, so large cells locate where the model misfits that group.",
	},
}

// interpMatFactor bands KMO by Kaiser (1974), as MAT_PCA does, and
// leaves the loadings, the residual figures and the maximum-likelihood
// fit indices unbanded: their cut-offs are rules of thumb, not verified
// conventions (excluded in testdata/conventions.json).
var interpMatFactor = []descriptor.Interpretation{
	{
		Field: "primary.values",
		Means: "Each cell is a loading: how strongly the row's measure is tied to the column's factor. Without a rotation, or with an orthogonal one (varimax, quartimax, geomin_orthogonal, entropy), it is the correlation between the two; under an oblique one (promax, oblimin, geomin) it is the measure's own tie to that factor with the other factors held fixed. A factor is named by the measures with the largest loadings.",
		Sign: map[string]string{
			"+": "the measure rises with the factor",
			"-": "the measure falls as the factor rises; a factor's overall sign is a convention (each column is made to sum positive)",
		},
		Caveats: []string{
			"Unrotated loadings put most measures on the first factor; a rotation gives a cleaner pattern without changing how well the factors fit (the communalities).",
			"Under an oblique rotation the factors correlate (auxiliary.factor_correlation), so loadings on correlated factors overlap.",
			"A uniqueness at or below 0, or held at the maximum-likelihood floor (a Heywood case, flagged with a warning), means the fit is improper; try fewer factors or drop a near-duplicate measure.",
		},
	},
	bandedBy(ConventionKaiserKMO, descriptor.Interpretation{
		Field: "scalars.kmo",
		Means: "The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a factor analysis is more worthwhile.",
		Caveats: []string{
			"Null when the correlation table is singular (a measure is an exact combination of others).",
		},
	}),
	{
		Field:  "scalars.bartlett_p",
		Shared: SharedPValue,
	},
	{
		Field: "scalars.bartlett_chisq",
		Means: "Bartlett's test statistic: how far the correlation table is from one in which no measure is related to any other. Large values mean the measures are related enough to factor.",
		Caveats: []string{
			"With many rows it is almost always large; read KMO for how worthwhile the analysis is.",
		},
	},
	{
		Field: "scalars.rmsr",
		Means: "The root-mean-square residual: the typical size of the correlations the factors leave unexplained, on the correlation scale. Smaller means the factors reproduce the correlations more closely.",
		Caveats: []string{
			"No convention bands it here; it always shrinks as factors are added, so weigh it against how readable the extra factor is.",
		},
	},
	{
		Field:  "scalars.empirical_p",
		Shared: SharedPValue,
	},
	{
		Field: "scalars.empirical_chisq",
		Means: "The empirical chi-square: the sum of the squared leftover correlations times the inference size. It measures the residual size, on df degrees of freedom, and is not a likelihood test of the model.",
		Caveats: []string{
			"It rests on the inference size: the rows (listwise), the summed weight or the effective sample size under a weight, or the smallest pair under pairwise deletion.",
			"Null p when df is 0 or below: the model then has no spare correlations to test against.",
		},
	},
	{
		Field:  "scalars.likelihood_p",
		Shared: SharedPValue,
	},
	{
		Field: "scalars.likelihood_chisq",
		Means: "Maximum likelihood only: the likelihood-ratio chi-square, with Bartlett's correction, of the k-factor model against a free correlation table, on df degrees of freedom. A small p says the factors leave correlation unexplained.",
		Caveats: []string{
			"With many rows it flags even trivial misfit; read tli and rmsea beside it.",
			"It assumes the measures are multivariate normal and rests on the inference size (the rows, the summed weight or the effective sample size).",
		},
	},
	{
		Field: "scalars.tli",
		Means: "Maximum likelihood only: the Tucker-Lewis index, how much better the factor model fits per degree of freedom than a model of unrelated measures. Near 1 or above means a close fit; it is not capped at 1.",
		Caveats: []string{
			"No convention bands it here; cut-offs such as .95 come from confirmatory models under specific conditions.",
			"Null when df is 0 or below.",
		},
	},
	{
		Field: "scalars.rmsea",
		Means: "Maximum likelihood only: the root-mean-square error of approximation, the misfit per degree of freedom after allowing for sampling, as a point estimate. 0 means no misfit beyond chance; smaller is better.",
		Caveats: []string{
			"No convention bands it here; cut-offs such as .05 or .08 are rules of thumb; read rmsea_lower and rmsea_upper for its uncertainty.",
			"Null when df is 0 or below; with few rows or few df it is unstable.",
		},
	},
	{
		Field: "scalars.rmsea_lower",
		Means: "Maximum likelihood only: the lower bound of the RMSEA confidence interval at conf_level (90% by default, as psych reports it). 0 when even a perfectly fitting model would often give a chi-square this large.",
		Caveats: []string{
			"It inverts the noncentral chi-square and assumes multivariate normal measures; with few rows the interval is wide.",
			"Null when df is 0 or below, or when no bound exists on the searched range.",
		},
	},
	{
		Field: "scalars.rmsea_upper",
		Means: "Maximum likelihood only: the upper bound of the RMSEA confidence interval at conf_level (90% by default). A small upper bound says the misfit per degree of freedom is plausibly small.",
		Caveats: []string{
			"The bounds divide by the inference size minus one where the point estimate divides by the inference size itself (psych's rule), so with very few rows the point can sit outside the interval; such a bound is null.",
			"Null when df is 0 or below.",
		},
	},
	{
		Field: "scalars.bic",
		Means: "Maximum likelihood only: the Bayesian information criterion of the fit, the likelihood chi-square minus df times the log of the inference size. Compare it across factor counts on the same measures: the lower value is preferred.",
		Caveats: []string{
			"Only differences between fits of the same measures and rows mean anything; the value alone does not.",
			"Null when df is 0 or below.",
		},
	},
}

// interpMatCollinearity leaves every figure unbanded: VIF's 5 / 10 and
// Belsley's 30 are rules of thumb, not verified conventions (O'Brien
// 2007; excluded in testdata/conventions.json).
var interpMatCollinearity = []descriptor.Interpretation{
	{
		Field: "scalars.max_vif",
		Means: "The largest variance inflation factor among the predictors (each one's is also listed): how many times larger that predictor's coefficient variance is than it would be if it were unrelated to the other predictors. 1 means no overlap; tolerance, its reciprocal, is the share of the predictor's spread the others do not explain.",
		Caveats: []string{
			"No convention bands it. Cut-offs of 5 or 10 are rules of thumb only: O'Brien (2007) shows they are arbitrary, and a large sample can make a high value harmless while a small one can make a modest value matter.",
			"It depends only on the predictors, not on any outcome; it says how unstable coefficients would be, not whether a predictor matters.",
			"Under params.repair it is read off the nearest consistent correlation table, not the observed one.",
		},
	},
	{
		Field: "scalars.condition_number",
		Means: "The largest of Belsley's condition indices: how close the predictors (with the intercept, unless params.center is true) come to an exact straight-line dependency. 1 means none; large values mean some combination of predictors nearly cancels out.",
		Caveats: []string{
			"No convention bands it. Belsley, Kuh and Welsch's guideline (an index of about 30 or more where two or more variables have variance-decomposition proportions above 0.5 on that dimension) is a rule of thumb; read the variance-decomposition proportions to see which variables are involved.",
			"With the intercept included (the default) a predictor whose values sit far from zero relative to their spread raises it even when the predictors are unrelated; params.center true removes that part.",
		},
	},
	{
		Field: "primary.values",
		Means: "The predictors' correlation table that the variance inflation factors and the centered diagnostics are read from: each off-diagonal cell is the correlation of its row and column predictors, from -1 to +1.",
		Sign:  correlationSign,
		Caveats: []string{
			"A predictor can be heavily inflated with no single large pairwise correlation, when it is close to a combination of several others; read the variance inflation factors, not just this table.",
		},
	},
}

// interpMatPCA bands the overall KMO by Kaiser (1974); the loadings
// carry rules of thumb only (excluded in testdata/conventions.json),
// and Bartlett's p follows the shared p-value rule set.
var interpMatPCA = []descriptor.Interpretation{
	{
		Field: "primary.values",
		Means: "Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.",
		Sign: map[string]string{
			"+": "the measure rises with the component",
			"-": "the measure falls as the component rises; a component's overall sign is a convention (its largest loading is made positive, or under a rotation its loadings sum positive)",
		},
		Caveats: []string{
			"Unrotated, a measure loading on several components at once is common and does not mean it measures several things; params.rotation varimax redistributes the kept variance into a cleaner pattern without changing the communalities.",
			"Components summarise the measures' total spread; they do not show that an underlying cause exists.",
			"On a covariance input the loadings are in the measures' own units, so they are not comparable across measures.",
		},
	},
	bandedBy(ConventionKaiserKMO, descriptor.Interpretation{
		Field: "scalars.kmo",
		Means: "The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a component summary is more worthwhile.",
		Caveats: []string{
			"Null when the correlation table is singular (a measure is an exact combination of others).",
			"The per-measure version of this reading is also reported; a measure with a low value fits the set poorly.",
		},
	}),
	{
		Field:  "scalars.bartlett_p",
		Shared: SharedPValue,
	},
	{
		Field: "scalars.bartlett_chisq",
		Means: "Bartlett's test statistic: how far the correlation table is from one in which no measure is related to any other. Large values mean the measures are related enough to summarise.",
		Caveats: []string{
			"With many rows it is almost always large; read KMO for how worthwhile a summary is.",
			"It rests on the inference size: the rows (listwise), the summed weight or the effective sample size under a weight, or the smallest pair under pairwise deletion.",
		},
	},
}

// interpMatReliability bands alpha by George & Mallery (2003) and
// leaves omega unbanded: no omega-specific convention was verified, and
// the alpha bands are not transferred to it.
var interpMatReliability = []descriptor.Interpretation{
	bandedBy(ConventionGeorgeMalleryAlpha, descriptor.Interpretation{
		Field: "scalars.alpha",
		Means: "Cronbach's alpha: how consistently the items measure one shared quality, from the share of the summed score's spread that the items share. 1 means the items move in lockstep; it can fall below 0 when items pull against each other, which usually means a reverse-worded item was not reversed.",
		Caveats: []string{
			"Alpha rises with the number of items, so a long battery can reach a high alpha with weakly related items.",
			"A high alpha does not show the items measure only one thing.",
			"Alpha assumes every item is equally tied to the shared quality; when the ties differ it understates reliability, which omega allows for; when item errors correlate (shared wording, method effects) it overstates reliability instead (Sijtsma 2009; Green & Yang 2009).",
		},
	}),
	bandedBy(ConventionGeorgeMalleryAlpha, descriptor.Interpretation{
		Field: "scalars.alpha_standardized",
		Means: "Alpha computed on the items' correlations instead of their raw spreads: the alpha the battery would have if every item were first put on the same scale.",
		Caveats: []string{
			"It differs from alpha when the items' spreads differ; report the one that matches how the score is built (raw sums or standardised items).",
		},
	}),
	{
		Field: "scalars.omega",
		Means: "McDonald's omega: the share of the summed score's spread due to the one shared quality, from a one-factor fit that lets each item's tie to it differ. Read on the same 0 to 1 scale as alpha; it is usually at least as high.",
		Caveats: []string{
			"No published convention bands omega; it is not read against the alpha bands.",
			"Null with a warning when the battery has 2 items, when the fit puts an item's leftover spread at or below zero (a Heywood case), or when a pairwise table is inconsistent and params.repair is not set. When the fit does not converge, omega is kept as the last iterate's value and a warning says so; treat it as provisional.",
			"The fit assumes one shared quality; when the items reflect several, omega from one factor misstates reliability.",
		},
	},
	{
		Field: "scalars.mean_inter_item_r",
		Means: "The average correlation among the items, the strength of the typical pair; standardized alpha is built from it and the item count.",
		Caveats: []string{
			"An average hides spread: one item unrelated to the rest pulls it down; check the table and item_total_r.",
			"Like any correlation it shows the items move together, not that one causes another.",
		},
	},
}

var interpMatPartialCorrelation = []descriptor.Interpretation{
	bandedBy(ConventionCohenR, descriptor.Interpretation{
		Field: "primary.values",
		Means: "Each off-diagonal cell is the correlation of its row and column members once the held-fixed fields are taken out of both (every other member, or the params.control fields), from -1 to +1. 0 means no straight-line link is left after holding them fixed. The diagonal is 1.",
		Sign:  correlationSign,
		Caveats: []string{
			causationCaveatText,
			"A partial r can be much smaller than, or even opposite in sign to, the plain r when the held-fixed fields drive both members; holding fixed a field that is itself an outcome of the pair can also create a link that is not there.",
			"A null cell means the input had an undefined correlation (a member with no spread, or too few rows), so no cell can be computed.",
			"Under params.repair the cells come from the nearest consistent correlation table, not the observed one; the PULSE_MATRIX_NOT_PSD warning says how far it moved.",
			"Its p-values (params.inference) test each partial against 0 on N* − 2 − g degrees of freedom, g the held-fixed fields; read them only for the pairs you meant to test.",
		},
	}),
	matrixPInterpretation,
	matrixCIInterpretation("ci_lower", "lower", false),
	matrixCIInterpretation("ci_upper", "upper", false),
}

// matrixPInterpretation is a correlation matrix's auxiliary.p
// (params.inference): the shared p-value reading.
var matrixPInterpretation = descriptor.Interpretation{
	Field:  "auxiliary.p",
	Shared: SharedPValue,
}

// matrixCIInterpretation reads one bound of a correlation matrix's
// params.inference interval; rank adds MAT_CORRELATION's rank-method
// caveat (an approximate interval beside a possibly exact p).
func matrixCIInterpretation(key, side string, rank bool) descriptor.Interpretation {
	in := descriptor.Interpretation{
		Field: "auxiliary." + key,
		Means: "The " + side + " bound of each pair's 1 − alpha confidence interval for its correlation (Fisher's z, so the interval is lopsided toward 0): the range of correlations the data are consistent with. An interval that excludes 0 matches a p-value below alpha.",
		Caveats: []string{
			"Null on the diagonal and where the pair is too thin for an interval (PULSE_MATRIX_INSUFFICIENT_N, scope inference), never collapsed to r.",
			"Each interval covers its own pair; with many pairs some intervals miss by chance, and no multiple-comparison correction is applied.",
			"Under a row weight it rests on the effective sample size (sum of weights for frequency, Kish n_eff for probability), unequal weights only, not a survey design.",
		},
	}
	if rank {
		in.Caveats = append(in.Caveats, "Under method spearman or kendall the interval's standard error is an approximation (Bonett–Wright, Fieller) while the p-value can be exact, so near alpha the two can disagree.")
	}
	return in
}

var interpMatCorrelation = []descriptor.Interpretation{
	bandedBy(ConventionCohenR, descriptor.Interpretation{
		Field: "primary.values",
		Means: "Each off-diagonal cell is the correlation of its row and column members under params.method, from -1 to +1. pearson (default): r, how closely the two follow a straight line together; spearman: rho, r on the members' ranks, how steadily one rises or falls with the other; kendall: tau-b, the share of agreeing minus disagreeing row pairs, tie-adjusted. 0 means no such link. The diagonal is 1.",
		Sign:  correlationSign,
		Caveats: []string{
			causationCaveatText,
			"A null cell means a member had no spread (constant, or too few rows), so its correlation is undefined, not 0.",
			"A Pearson r near zero rules out only a straight-line link; a curved relationship can still be strong.",
			"The bands are Cohen's for r; Kendall's tau-b runs smaller than r or rho for the same strength (about a third smaller at moderate strength, the gap narrowing as the association strengthens; Kendall 1970; Gibbons & Chakraborti 2011), so read a kendall cell against lower cut-offs.",
			"Set params.inference for each pair's p-value and interval (every method).",
		},
	}),
	matrixPInterpretation,
	matrixCIInterpretation("ci_lower", "lower", true),
	matrixCIInterpretation("ci_upper", "upper", true),
	{
		Field: "scalars.determinant",
		Means: "The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.",
		Caveats: []string{
			"Null when the table is not positive definite, including when any cell is null.",
			"No published convention bands it; a value near 0 flags near-redundant members.",
		},
	},
}
