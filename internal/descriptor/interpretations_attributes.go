package descriptor

import "github.com/frankbardon/pulse/descriptor"

// attributeInterpretations is the Interpretation registry for the
// needs-reading ATTR_* operators (interpretation_reading.go): each reads
// the per-row column through the static `value` path. builtinInterpretations
// assembles it with the other category maps.
//
// No attribute reading carries bands. The z- and T-scores are descriptive
// (divided by the spread of the values, not a standard error), raw
// residuals are in the target's units, and leverage has a screening rule
// of thumb (2p/n, Hoaglin & Welsch 1978), not a graded convention: the
// reasons are in testdata/conventions.json `excluded`.
//
// Every fact follows internal/processing/attribute*.go and
// internal/processing/regression/coeffs.go: the population standard
// deviation (dividing by n), a missing input that scores null (left out
// of the pre-pass, U50 E6-S1), a zero spread that scores every row null
// (0/0, U50 E7-S2), rank / n * 100 with distinct ranks for ties,
// and the hat-matrix identity h = 1/n + (x - mean)' M^-1 (x - mean).
var attributeInterpretations = map[string][]descriptor.Interpretation{
	"ATTR_MAHALANOBIS":   interpAttrMahalanobis,
	"ATTR_PC_SCORE":      interpAttrPCScore,
	"ATTR_FACTOR_SCORE":  interpAttrFactorScore,
	"ATTR_NORMALIZED":    interpAttrNormalized,
	"ATTR_PERCENTILE":    interpAttrPercentile,
	"ATTR_REG_FITTED":    interpAttrRegFitted,
	"ATTR_REG_LEVERAGE":  interpAttrRegLeverage,
	"ATTR_REG_INFLUENCE": interpAttrRegInfluence,
	"ATTR_REG_PREDICT":   interpAttrRegPredict,
	"ATTR_REG_RESIDUAL":  interpAttrRegResidual,
	"ATTR_SEM_SCORE":     interpAttrSEMScore,
	"ATTR_TSCORE":        interpAttrTScore,
	"ATTR_ZSCORE":        interpAttrZScore,
}

// Shared caveat sentences, so sibling attributes say the same thing the
// same way.
const (
	attrGlobalScale = "The scale comes from every row that passed the filters, not from the row's group: " +
		"averaging it per group shows how each group sits against the whole, and a different filter gives different values."
	attrZWeighted = "Under a row weight the mean and standard deviation are weighted (dividing by the sum of weights), the same for frequency and probability weights; " +
		"a row with no usable weight adds nothing to them but is still scored."
	attrZNoBands = "There are no sourced bands for reading a z-score of this kind: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic " +
		"built from a standard error, and this one is built from the spread of the values instead, so it has no p-value behind it."
	attrZNormalOnly = "Rules such as 'about 95% of rows lie within 2 standard deviations' hold only for a roughly bell-shaped (normal) field; " +
		"on a skewed field far more rows can sit beyond 2 on the long side."
	attrZOutlierPull = "One extreme row inflates the standard deviation and pulls every other score toward the centre; " +
		"on n rows no z-score can exceed sqrt(n - 1) in size (Shiffler 1988), so on a small set a modest score may be the most extreme possible."
	attrRegAssociation = "The model describes association in these rows: a fitted value is what rows with these predictor values look like here, " +
		"not what would happen to a row if a predictor were changed; a causal reading needs a design that supports it."
	attrRegSameRows = "The model is fitted and read on the same rows, so it matches them more closely than it will match new rows (overfitting), " +
		"most of all with many predictors and few rows."
	attrRegPenalty = "With a penalty (l1, l2, elasticnet) the coefficients are deliberately shrunk toward 0, so fitted values sit closer to the target's mean " +
		"and the residuals are larger overall (their sum of squares rises) than an unpenalized fit's, though a single row's can go either way; " +
		"use no penalty for diagnostics."
)

// --- Standardised scores --------------------------------------------------

var (
	interpAttrZScore = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "How many standard deviations the row's value sits from the mean: (value - mean) / standard deviation, " +
				"with the population standard deviation (dividing by n). 0 is exactly average, 1 is one standard deviation above it.",
			Sign: map[string]string{
				"+": "the row's value is above the mean",
				"-": "the row's value is below the mean",
			},
			Caveats: []string{
				attrZNoBands,
				attrZNormalOnly,
				attrZOutlierPull,
				attrGlobalScale,
				attrZWeighted,
				"A row with a missing value gets no score (null) and is left out of the mean and standard deviation; " +
					"every row reads null when all values are equal or only one row has a value (no spread: the score is 0/0, undefined).",
			},
		},
	}

	interpAttrTScore = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The z-score rescaled to a mean of 50 and a standard deviation of 10: 50 + 10 * z, with the population standard deviation (dividing by n). " +
				"60 is one standard deviation above the mean, 40 one below.",
			Caveats: []string{
				"Unrelated to Student's t or a t test: the name comes from educational testing, and the value carries no p-value.",
				attrZNoBands,
				"It is not a percentile: 70 does not mean the 70th percentile. Only on a roughly normal field does 60 sit near the 84th percentile.",
				attrZOutlierPull,
				attrGlobalScale,
				attrZWeighted,
				"A row with a missing value gets no score (null) and is left out of the mean and standard deviation; " +
					"every row reads null when all values are equal (no spread: 0/0, undefined), and an extreme row can fall below 0 or above 100.",
			},
		},
	}
)

// --- Positions --------------------------------------------------------------

var (
	interpAttrPercentile = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The row's position among the sorted values, as a percentage: rank / n * 100, where the smallest value has rank 1 and n counts the rows with a value. " +
				"The largest value reads 100 and the smallest 100 / n, so for a value no other row shares it is the share of rows at or below this one; " +
				"tied values break that (see below).",
			Caveats: []string{
				"Tied values do not share a percentile: each tied row takes its own rank in an arbitrary order, so equal values can read different percentiles, " +
					"far apart on a field with few distinct values (a 1-5 rating); use WIN_RANK when ties must share a rank.",
				"A row with a missing value gets no percentile (null) and is not counted in n.",
				"Other tools define it differently (smallest at 0, or ties at their midpoint), so the same row can read a few points apart elsewhere.",
				"Equal steps in percentile are not equal steps in the value: near the middle of a bell-shaped field a small change in value moves the percentile a lot.",
				attrGlobalScale,
			},
		},
	}

	interpAttrNormalized = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "Where the row's value sits between the smallest and largest value, on a 0-to-1 scale: (value - min) / (max - min). " +
				"0 is the minimum, 1 the maximum and 0.5 halfway between them, which is not the median.",
			Caveats: []string{
				"One extreme value sets the minimum or maximum and squeezes every other row into a narrow part of the scale; ATTR_PERCENTILE is not affected that way.",
				"A row with a missing value gets no value (null) and is left out of the minimum and maximum; every row reads null when all values are equal (max = min: 0/0, undefined).",
				attrGlobalScale,
			},
		},
	}
)

// --- Regression diagnostics -------------------------------------------------

var (
	interpAttrRegFitted = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The target value the fitted straight-line model predicts for the row from its predictors: intercept + sum(coefficient * predictor), " +
				"in the target's units. The model is fitted by least squares to the rows that passed the filters and have the target and every predictor.",
			Caveats: []string{
				attrRegAssociation,
				attrRegSameRows,
				"A straight-line model: where the target bends with a predictor, fitted values miss in a pattern over parts of its range; read ATTR_REG_RESIDUAL to see it.",
				"A row missing the target still gets a fitted value when its predictors are present; a row missing any predictor gets null, never 0.",
				attrRegPenalty,
			},
		},
	}

	interpAttrRegPredict = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The target value the named fitted model predicts for the row from the row's own predictor values: intercept + sum(coefficient * predictor) " +
				"at the fit's full precision, in the target's units — for a GLM on the response scale (the inverse link applied: a probability, a count) unless scale is link. " +
				"With terms, only the listed terms are summed: that part of the prediction, a contribution in the target's units (link units for a GLM).",
			Caveats: []string{
				"On rows the model was fitted to it equals ATTR_REG_FITTED exactly and matches them more closely than it will match new rows (overfitting): " +
					"judge a model on rows it was not fitted to.",
				"A coefficient's confidence bound gives the range of that one term, not a prediction interval for a row.",
				attrRegAssociation,
				"A row missing any predictor the score reads gets null, never 0.",
			},
		},
	}

	interpAttrRegResidual = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "Actual minus predicted: the row's target value minus the value the straight-line model predicts for it, in the target's units. " +
				"Over the rows used in an unpenalized fit the residuals average 0.",
			Sign: map[string]string{
				"+": "the row's target is above what the model predicts from its predictors",
				"-": "the row's target is below what the model predicts from its predictors",
			},
			Caveats: []string{
				"A residual is what this model leaves unexplained, not a measurement error and not the effect of any one thing left out: " +
					"another set of predictors gives other residuals.",
				"It is in the target's units, so there are no sourced cut-offs for a large one; compare it with the model's typical miss (REG_OLS residual_std_err) " +
					"times sqrt(1 - leverage) from ATTR_REG_LEVERAGE, since a high-leverage row's residual varies less, for a row-fair comparison.",
				"A row with high leverage (ATTR_REG_LEVERAGE) pulls the line toward itself, so its residual can be small even when the row is unusual.",
				"Over a bound fit, params.kind changes the scale: pearson divides by the family's standard deviation at the fitted mean, deviance is the row's signed share of the model deviance, " +
					"and working is on the linear-predictor scale. Pearson and deviance residuals of a count or 0/1 model are comparable across rows; response residuals are not.",
				"On the refit, params.kind standardized divides the residual by its own standard error, s times sqrt(1 - leverage), and studentized by the one from a fit leaving the row out, " +
					"so both are unit-free and comparable across rows. Screens of |2| or |3| circulate as rules of thumb, not a sourced convention, and over many rows some exceed them by chance alone. " +
					"Both need an unpenalized fit; a frequency weight reads one copy of the row, and a probability weight is refused. " +
					"Over a bound fit (params.fit) both read that fit's leverage and dispersion and start from the Pearson residual, as R does for a generalized-linear model.",
				"Patterns across rows (a curve, or spread that grows with the predicted value) point to a missing term or heteroscedasticity, not to rows that are wrong.",
				attrRegAssociation,
				"A row missing the target or any predictor gets null, never 0.",
				attrRegPenalty,
			},
		},
	}

	interpAttrRegLeverage = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "How far the row's predictor values sit from the other rows' (from the predictors' means, scaled by their spread and correlation), " +
				"and so how hard the row can pull the fitted line toward itself: the hat-matrix diagonal 1/n + (x - mean)' M^-1 (x - mean), " +
				"where M is the predictors' centred sum-of-squares-and-cross-products matrix. " +
				"Over the n rows in the fit it runs from 1/n to 1 and averages p / n, where p counts the coefficients including the intercept.",
			Caveats: []string{
				"A common rule of thumb flags leverage above 2p / n, twice the average (Hoaglin & Welsch 1978); it is a screening threshold, " +
					"not a graded convention, and with few rows per coefficient it flags many rows.",
				"It reads the predictors only, never the target: a high-leverage row can sit right on the line. Whether it moved the fit depends on its residual too " +
					"(ATTR_REG_RESIDUAL); ATTR_REG_INFLUENCE combines the two (Cook's distance, DFFITS).",
				"A row outside the fit (missing the target) still gets a leverage from its predictors, which can exceed 1 when it lies beyond the fitted rows; " +
					"a row missing any predictor gets null; a row a weighted fit leaves out (no usable weight) reads 0.",
				"Unpenalized least squares only; a request with a penalty is refused. Over a bound fit (params.fit) it is that fit's leverage: for a generalized-linear fit it also weighs the row's fitted value " +
					"(its working weight), so the same predictor values carry less leverage where the fitted mean leaves little room to move.",
			},
		},
	}

	interpAttrRegInfluence = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "How much the row moves the fitted line. With measure cooks (the default): Cook's distance, the shift of every fitted value when the row is left out, " +
				"scaled by p times the model's residual variance, where p counts the coefficients including the intercept; it is never negative and grows with both the row's residual and its leverage. " +
				"With measure dffits: the shift of the row's own fitted value when it is left out, in standard errors of that fit, signed like the residual.",
			Sign: map[string]string{
				"+": "dffits: leaving the row out lowers its fitted value (the row pulls the line up toward itself)",
				"-": "dffits: leaving the row out raises its fitted value (the row pulls the line down toward itself)",
			},
			Caveats: []string{
				"A Cook's distance above 1 (Cook & Weisberg 1982) and the 4 / n screen are rules of thumb, not a graded convention; " +
					"for DFFITS, |value| above 2 sqrt(p / n) (Belsley, Kuh & Welsch 1980) scales with p and n. Read them as a ranking of rows to look at, not a test.",
				"An influential row is not a wrong row: check it, and report the fit with and without it rather than dropping it silently.",
				"Several unusual rows close together can mask each other, since leaving out one leaves the others to hold the line.",
				attrRegAssociation,
				"A row missing the target or any predictor, or left out of a weighted fit (no usable weight), gets null, never 0; so does every row when the fit has too few rows to estimate its spread.",
				"Unpenalized least squares only; a request with a penalty is refused. A frequency weight reads one copy of the row; a probability weight is refused.",
				"Over a bound generalized-linear fit (params.fit) dffits is the shift on the link scale, so under a decreasing link (gamma's inverse) its sign is the reverse of the residual's; Cook's distance is unaffected.",
			},
		},
	}
)

// --- Fitted vector scores -------------------------------------------------

var (
	interpAttrMahalanobis = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "With output d2 (the default): the row's squared Mahalanobis distance D² = (x - mean)' S^-1 (x - mean) across the p fields, " +
				"the distance from the fitted average row in units of the fields' own spread and correlation; 0 is exactly average and it is never negative. " +
				"With output p: the chance of a D² at least this large under a chi-square curve on p degrees of freedom, so a small value marks an unusual row.",
			Caveats: []string{
				"D² grows with the number of fields: over the fitted rows it averages about p, so compare it with p (or read output p), never with a fixed cut-off across batteries.",
				"The p output assumes the fields are jointly normal; on skewed or bounded fields (rating scales) it is only a ranking, and screening many rows flags some by chance.",
				"Extreme rows in the fit pull the mean and inflate the spread, masking themselves and each other; fit_on can fit on a clean subset while still scoring every row.",
				"The fit is over every filtered row (or the fit_on subset), not the row's group. Under a row weight the mean and covariance are weighted; a row with no usable weight is left out of the fit but still scored.",
				"A row missing any field reads null, and every row reads null, with a warning, when the fit has no more complete rows than fields or a field is constant or a combination of others.",
			},
		},
	}

	interpAttrPCScore = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The row's score on the chosen principal component of the fields, standardized so the fitted rows average 0 with a standard deviation of 1: " +
				"0 is an average row, +1 is one standard deviation toward the end where the fields with positive loadings are high.",
			Caveats: []string{
				"The sign is a convention (MAT_PCA's: each component's largest loading is positive), not a meaning: check MAT_PCA's loadings on the same fields before calling a high score good or bad.",
				"A component summarises the spread the fields share; it does not say why they move together, and a later component carries less of their variance and is noisier.",
				"On the correlation every field counts equally; on the covariance a field with a large spread dominates the score.",
				"The fit is over every filtered row (or the fit_on subset), not the row's group, so scores are comparable across groups. Under a row weight the fit is weighted; a row with no usable weight is left out of the fit but still scored.",
				"A row missing any field reads null, and every row reads null, with a warning, when the fit has no more complete rows than fields or a field is constant or a combination of others.",
			},
		},
	}

	interpAttrSEMScore = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The row's estimated score on the chosen latent quality, in the units of its first (marker) measure: " +
				"higher is toward the end where that measure is high; with a mean structure 0 is the reference group's average.",
			Caveats: []string{
				"A score is an estimate, not the quality itself: regression scores are shrunk toward the group's mean, Bartlett scores are unbiased but noisier.",
				"Group averages of the scores track the model's latent means only as far as the equality constraints you set make the measures comparable across groups.",
				"The fit is over every filtered row (or the fit_on subset), never the request's groups. A frequency weight counts each row that many times in the fit; every row is still scored once.",
				"A row missing any measure or its group reads null, and every row reads null, with a warning, when the fit cannot score.",
				"On a dwls or wlsmv fit each score is an empirical Bayes modal score (the latent's most likely value given the row's rating categories) on the scale of the marker's underlying continuous response, and a row holding a rating category that dwls or wlsmv fit never saw reads null, with a warning.",
			},
		},
	}
	interpAttrFactorScore = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The row's estimated score on the chosen factor, on the scale of the fields standardized over the fitted rows: " +
				"0 is an average row, positive is toward the end where the fields loading positively on the factor are high.",
			Caveats: []string{
				"The direction is MAT_FACTOR's convention (each factor's loadings sum positive), not a meaning: check its loadings on the same fields before calling a high score good or bad.",
				"A factor score is an estimate, not the factor itself: regression scores are shrunk toward 0 (their spread is below 1), Bartlett scores are unbiased but noisier.",
				"After an oblique rotation (promax, oblimin, geomin) the factors correlate, so scores on two factors move together.",
				"The fit is over every filtered row (or the fit_on subset), not the row's group, so scores are comparable across groups. Under a row weight the fit is weighted; a row with no usable weight is left out of the fit but still scored.",
				"A row missing any field reads null, and every row reads null, with a warning, when the fit cannot score (too few rows, a constant or redundant field, a factor count the fields cannot identify).",
			},
		},
	}
)
