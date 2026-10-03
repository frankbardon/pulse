package descriptor

import "github.com/frankbardon/pulse/descriptor"

// descriptiveInterpretations is the Interpretation registry for the
// needs-reading descriptive operators (interpretation_reading.go): each
// reads the operator's primary result through the static `value` path
// (`value.*` for a multi-column result). builtinInterpretations
// assembles it with the other category maps.
//
// No descriptive reading carries bands. Spread, percentiles, ratios and
// weighted means are in the field's own units, so no unit-free cut-off
// exists; skewness, kurtosis and the aggregate z-score are scale-free,
// but no graded convention for them met the fixture bar (sources and
// reasons: testdata/conventions.json `excluded`).
//
// Estimator facts follow the code, not the textbook default: see the
// "Spread, shape and intervals" block of purposes_aggregators.go.
var descriptiveInterpretations = map[string][]descriptor.Interpretation{
	"AGG_CI_LOWER":      interpAggCILower,
	"AGG_CI_UPPER":      interpAggCIUpper,
	"AGG_KURTOSIS":      interpAggKurtosis,
	"AGG_PERCENTILE":    interpAggPercentile,
	"AGG_RATIO":         interpAggRatio,
	"AGG_SKEWNESS":      interpAggSkewness,
	"AGG_STDDEV":        interpAggStdDev,
	"AGG_VARIANCE":      interpAggVariance,
	"AGG_WEIGHTED_MEAN": interpAggWeightedMean,
	"AGG_WELFORD":       interpAggWelford,
	"AGG_ZSCORE":        interpAggZScore,
}

// Shared caveat sentences, so sibling operators say the same thing the
// same way.
const (
	descPopulationSpread = "Pulse divides by n (the population form); most tools report the sample form, dividing by n - 1, which is slightly larger. " +
		"The gap matters only on small groups; AGG_WELFORD gives the sample form."
	descSingleRowZero = "A group with a single value reads 0: no spread can be seen in one value, which is not the same as a group whose values agree."
	descShapeZero     = "It is 0 when n is 0 or 1 or the variance is zero (every value the same), so a 0 from a tiny or constant group says nothing about shape: check components n."
	descCIAboutMean   = "The interval is about the MEAN, not about individual rows: most rows can, and usually do, fall outside it."
	descCIMethod      = "The confidence level describes the method: over repeated samples, intervals built this way capture the true mean that share of the time. " +
		"Any one interval either contains it or not."
	descCINormal = "Pulse uses the normal critical value (1.96 at 95%), not Student's t, so on small groups (under about 30 rows) the interval is too narrow; " +
		"on a strongly skewed or heavy-tailed field it can stay too narrow, and lopsided, on much larger groups, so check AGG_SKEWNESS first. " +
		"The components key t_critical holds that normal value."
	descCIIndependent = "It assumes rows are independent, unweighted draws; with weighted, clustered or repeated rows it understates the uncertainty."
	descCIOverlap     = "Overlapping intervals do not show two means are equal: means can differ even when their intervals overlap, so compare groups with TEST_WELCH, not by eye."
	descCINaN         = "Empty (NaN) when fewer than two rows have a value."
)

// --- Spread ------------------------------------------------------------

var (
	interpAggStdDev = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The typical distance of a value from the group's mean, in the field's own units. 0 means every value is the same; " +
				"for a roughly bell-shaped field about two thirds of the values sit within one standard deviation of the mean.",
			Caveats: []string{
				"It is the spread of the rows, not the precision of the mean: for how precisely the average is known, read AGG_CI_LOWER / AGG_CI_UPPER.",
				descPopulationSpread,
				"Each distance is squared before averaging, so a few extreme values inflate it; for a skewed field a percentile range describes the spread better.",
				descSingleRowZero,
			},
		},
	}

	interpAggVariance = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The average squared distance of a value from the group's mean, in the field's units squared (dollars squared, minutes squared). " +
				"Its square root is AGG_STDDEV, which is in the field's own units and easier to read.",
			Caveats: []string{
				"Squared units make the size hard to judge directly. For pooling or planning a study's size use AGG_WELFORD's variance (dividing by n - 1), which those formulas expect.",
				descPopulationSpread,
				"Extreme values weigh heavily, since each distance is squared.",
				descSingleRowZero,
			},
		},
	}

	interpAggWelford = []descriptor.Interpretation{
		{
			Field: "value.*",
			Means: "Three columns per group: mean (the average), variance (the SAMPLE variance, dividing by n - 1, in the field's units squared) " +
				"and n (the rows with a value). Together they are what a t or z comparison of group means needs; " +
				"the components add stddev, the square root of variance.",
			Caveats: []string{
				"variance is 0 when n is below 2: no spread could be estimated, which is not the same as values that agree.",
				"variance here divides by n - 1, so on the same rows it is larger than AGG_VARIANCE (which divides by n).",
				"With no rows the result is empty, not zeros.",
			},
		},
	}
)

// --- Shape ---------------------------------------------------------------

var (
	interpAggSkewness = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "How lopsided the values are around their mean. Pulse computes the population moment coefficient g1: the average cubed z-score, " +
				"(m3/n) / (m2/n)^1.5, where the components m2 and m3 are SUMS of squared and cubed distances from the mean (not yet divided by n). " +
				"0 means the values balance around the mean.",
			Sign: map[string]string{
				"+": "a longer or heavier tail to the right: a few values sit far above the rest",
				"-": "a longer or heavier tail to the left: a few values sit far below the rest",
			},
			Caveats: []string{
				"The sign does not fix whether the mean sits above or below the median: the two often disagree on whole-number or many-peaked fields, " +
					"so read AGG_MEDIAN beside AGG_AVERAGE for that.",
				"There are no sourced bands for skewness: the 0.5 / 1 rules of thumb in circulation trace to secondary quotations that could not be checked " +
					"against their source, so compare values with each other rather than with a fixed cut-off.",
				"Small-n bias: g1 runs smaller in size than the adjusted G1 that Excel SKEW, SPSS and SAS print (G1 = g1 * sqrt(n(n-1)) / (n-2)), " +
					"so the two disagree on small groups and converge as n grows; on a handful of rows neither is stable.",
				descShapeZero,
				"Deviations are cubed, so one extreme value can set the sign and size on its own.",
			},
		},
	}

	interpAggKurtosis = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "How heavy the tails are next to a normal distribution. Pulse computes the population EXCESS kurtosis g2: the average fourth-power z-score " +
				"minus 3, (m4/n) / (m2/n)^2 - 3, where the components m2 and m4 are SUMS of squared and fourth-power distances from the mean " +
				"(not yet divided by n), so a normal distribution scores 0. It can never fall below -2.",
			Sign: map[string]string{
				"+": "heavier tails than a normal distribution: extreme values turn up more often than the spread suggests",
				"-": "lighter tails than a normal distribution: values far from the mean turn up less often than the spread suggests, " +
					"as in a flat (uniform) or two-humped spread; it does not mean the values are bounded",
			},
			Caveats: []string{
				"There are no sourced bands for kurtosis: the cut-offs quoted for it (such as 2 or 7) are pass/fail normality screens from particular fields, " +
					"disagree on whether 3 has been subtracted, and are not graded readings.",
				"Small-n bias: g2 differs from the adjusted G2 that Excel KURT and SPSS print, and on small groups the gap is large; neither is stable on a handful of rows.",
				descShapeZero,
				"It measures the tails, not how pointed the peak looks, and a single extreme value can dominate it.",
			},
		},
	}

	interpAggZScore = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The average of every row's z-score within the group, which is 0 by construction (up to rounding): the value carries no information about the data. " +
				"The usable numbers are in the components: pop_mean and pop_stddev (the centre and the population spread, dividing by n), " +
				"target_value (the LAST row's value) and zscore (that row's z-score).",
			Caveats: []string{
				"For a z-score on every row use ATTR_ZSCORE; for each group's value against all groups use OVERLAY_ZSCORE_VS_TOTAL.",
				"There are no sourced bands for reading a z-score of this kind: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic " +
					"built from a standard error, and this one is built from a spread of values instead, so it has no p-value behind it.",
				"Which row is last depends on row order (not on date), so the components zscore is reproducible only on a fixed order.",
				"The last row is part of the mean and spread it is scored against, which damps its zscore (never beyond sqrt(n - 1) in size), " +
					"so it understates how unusual that row is; for a reading against its own past use OVERLAY_ZSCORE_VS_ROLLING.",
				"The value and the components zscore read 0 when every value is the same (no spread); every component reads 0 when the group is empty.",
			},
		},
	}
)

// --- Intervals and positions -------------------------------------------

var (
	interpAggCILower = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The lower end of a confidence interval for the group's mean at the requested confidence (95% by default): " +
				"mean - z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).",
			Caveats: []string{descCIAboutMean, descCIMethod, descCINormal, descCIIndependent, descCIOverlap, descCINaN},
		},
	}

	interpAggCIUpper = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The upper end of a confidence interval for the group's mean at the requested confidence (95% by default): " +
				"mean + z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).",
			Caveats: []string{descCIAboutMean, descCIMethod, descCINormal, descCIIndependent, descCIOverlap, descCINaN},
		},
	}

	interpAggPercentile = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The value below which the requested share of the group's values fall, in the field's own units: at percentile 90 about 90% of values " +
				"sit at or below it. Pulse interpolates linearly between the two nearest sorted values (R's default, type 7), so it may be a value no row holds.",
			Caveats: []string{
				"Tools use different interpolation rules, so on small groups another tool can return a somewhat different value for the same percentile.",
				"A percentile near 0 or 100 on a small group rests on one or two rows and moves a lot when they change.",
				"An empty group reads 0, not empty: check components n.",
			},
		},
	}

	interpAggRatio = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The total of the numerator field divided by the total of the denominator field over the group's rows (a ratio of totals), " +
				"such as revenue per order. Its units are the numerator's per unit of the denominator's.",
			Caveats: []string{
				"It is not the average of each row's own ratio: rows with larger denominators count for more, which is usually what a rate should do.",
				"A row missing either field is left out of BOTH totals.",
				"Empty (NaN) when the denominator total is 0.",
				"Read it as a share (times 100 for a percentage) only when the numerator counts a part of what the denominator counts.",
			},
		},
	}

	interpAggWeightedMean = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The average of the field with each row counted in proportion to its weight: sum(value * weight) / sum(weight), in the field's own units. " +
				"With equal weights it equals AGG_AVERAGE.",
			Caveats: []string{
				"Rows missing the value or the weight, or with weight 0, are left out of the average yet still count in components n; read sum_weights for the base.",
				"With survey or other sampling weights, uneven weights cost precision: components n_eff (Kish's effective sample size) is the row count " +
					"the estimate is roughly worth, never more than the rows that count. It does not apply to frequency weights (a weight counting repeated units, " +
					"such as a quantity, where the base is sum_weights) or to inverse-variance weights.",
				"It reads 0 when no row has a usable weight; check components sum_weights before trusting a 0.",
				"Negative weights are not refused and can push the result outside the range of the values.",
			},
		},
	}
)
