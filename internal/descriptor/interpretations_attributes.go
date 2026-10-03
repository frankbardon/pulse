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
// deviation (dividing by n), a missing input that reads 0 (50 for the
// T-score) rather than null, rank / n * 100 with distinct ranks for ties,
// and the hat-matrix identity h = 1/n + (x - mean)' M^-1 (x - mean).
var attributeInterpretations = map[string][]descriptor.Interpretation{
	"ATTR_NORMALIZED":   interpAttrNormalized,
	"ATTR_PERCENTILE":   interpAttrPercentile,
	"ATTR_REG_FITTED":   interpAttrRegFitted,
	"ATTR_REG_LEVERAGE": interpAttrRegLeverage,
	"ATTR_REG_RESIDUAL": interpAttrRegResidual,
	"ATTR_TSCORE":       interpAttrTScore,
	"ATTR_ZSCORE":       interpAttrZScore,
}

// Shared caveat sentences, so sibling attributes say the same thing the
// same way.
const (
	attrGlobalScale = "The scale comes from every row that passed the filters, not from the row's group: " +
		"averaging it per group shows how each group sits against the whole, and a different filter gives different values."
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
				"A row with a missing value reads 0, the same as an exactly average row, and every row reads 0 when all values are equal (no spread); " +
					"check the source field before reading a 0 as average.",
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
				"A row with a missing value reads 50, the same as an exactly average row, and every row reads 50 when all values are equal; " +
					"an extreme row can fall below 0 or above 100.",
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
				"A row with a missing value reads 0, which no row with a value can: treat 0 as missing.",
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
				"A row with a missing value reads 0, the same as the minimum, and every row reads 0 when all values are equal (max = min).",
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
				"A row missing the target still gets a fitted value when its predictors are present; a row missing any predictor reads 0, which is not a prediction.",
				attrRegPenalty,
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
				"Patterns across rows (a curve, or spread that grows with the predicted value) point to a missing term or heteroscedasticity, not to rows that are wrong.",
				attrRegAssociation,
				"A row missing the target or any predictor reads 0, the same as a row the model fits exactly.",
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
					"(ATTR_REG_RESIDUAL); influence measures that combine the two, such as Cook's distance, are not computed.",
				"A row outside the fit (missing the target) still gets a leverage from its predictors, which can exceed 1 when it lies beyond the fitted rows; " +
					"a row missing any predictor reads 0, below the 1/n floor, so 0 marks a missing row.",
				"Unpenalized least squares only; a request with a penalty is refused.",
			},
		},
	}
)
