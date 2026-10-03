package descriptor

import "github.com/frankbardon/pulse/descriptor"

// aggregatorPurposes is the Purpose registry for the AGG_* operators.
// builtinPurposes assembles it with the other category maps.
//
// The self-reading aggregators (interpretation_reading.go) return a count,
// a total, an extreme or a list of labels that reads as itself, so each
// carries a Purpose and no Interpretation. The needs-reading ones (spread,
// shape, intervals, percentiles, ratios, weighted means — the "Spread,
// shape and intervals" block below) also carry a `value` / `value.*`
// Interpretation in interpretations_descriptive.go. Each Purpose states
// the operator as Pulse implements it (see the
// internal/skills/op-agg-*.md atomic skills): nulls are skipped unless the
// operator counts them, AGG_MODE breaks ties by the smallest value, and
// AGG_DISTINCT_SUM keeps the first value seen per key.
var aggregatorPurposes = map[string]descriptor.Purpose{
	"AGG_AVERAGE":             purposeAggAverage,
	"AGG_CI_LOWER":            purposeAggCILower,
	"AGG_CI_UPPER":            purposeAggCIUpper,
	"AGG_COUNT":               purposeAggCount,
	"AGG_DISTINCT_COUNT":      purposeAggDistinctCount,
	"AGG_DISTINCT_SUM":        purposeAggDistinctSum,
	"AGG_FREQUENCY":           purposeAggFrequency,
	"AGG_KURTOSIS":            purposeAggKurtosis,
	"AGG_MAX":                 purposeAggMax,
	"AGG_MEDIAN":              purposeAggMedian,
	"AGG_MIN":                 purposeAggMin,
	"AGG_MODE":                purposeAggMode,
	"AGG_NULL_COUNT":          purposeAggNullCount,
	"AGG_PERCENTILE":          purposeAggPercentile,
	"AGG_RANGE":               purposeAggRange,
	"AGG_RATIO":               purposeAggRatio,
	"AGG_SET_CARDINALITY_AVG": purposeAggSetCardinalityAvg,
	"AGG_SET_CARDINALITY_SUM": purposeAggSetCardinalitySum,
	"AGG_SET_DISTINCT_VALUES": purposeAggSetDistinctValues,
	"AGG_SET_FREQUENCY":       purposeAggSetFrequency,
	"AGG_SET_INTERSECTION":    purposeAggSetIntersection,
	"AGG_SET_UNION":           purposeAggSetUnion,
	"AGG_SKEWNESS":            purposeAggSkewness,
	"AGG_STDDEV":              purposeAggStdDev,
	"AGG_SUM":                 purposeAggSum,
	"AGG_VARIANCE":            purposeAggVariance,
	"AGG_WEIGHTED_MEAN":       purposeAggWeightedMean,
	"AGG_WELFORD":             purposeAggWelford,
	"AGG_ZSCORE":              purposeAggZScore,
}

// --- Totals and counts -----------------------------------------------

var (
	purposeAggAverage = descriptor.Purpose{
		Plain:   "Average of a numeric field, over all rows or per group.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"What is the average order value?",
			"What is the typical rating in each region?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Mean satisfaction score per segment.",
			descriptor.DomainOps:     "Average order value by sales channel.",
			descriptor.DomainScience: "Mean measurement per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is skewed or has extreme values and you want the typical row", Use: "AGG_MEDIAN"},
			{When: "rows carry weights", Use: "AGG_WEIGHTED_MEAN"},
			{When: "you also need the spread around the average", Use: "AGG_WELFORD"},
		},
		Assumptions: []string{
			"Missing values are skipped: the average is over rows that have a value.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"mean", "median", "outlier", "skew"},
	}

	purposeAggCount = descriptor.Purpose{
		Plain:   "Number of rows that have a value in a field, over all rows or per group.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"How many responses did each region return?",
			"How many orders were placed through each channel?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Number of answered questionnaires per segment.",
			descriptor.DomainOps:     "Order count per store per day.",
			descriptor.DomainScience: "Number of recorded measurements per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the rows where the field is empty", Use: "AGG_NULL_COUNT"},
			{When: "you want how many different values appear, not how many rows", Use: "AGG_DISTINCT_COUNT"},
			{When: "you want a count for every value of the field: group by it, then count", Use: "GROUP_CATEGORY"},
		},
		Assumptions: []string{
			"Rows with a missing value in the field are not counted.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value", "sample-size"},
	}

	purposeAggSum = descriptor.Purpose{
		Plain:   "Total of a numeric field, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentComposition},
		Questions: []string{
			"What is total revenue by region?",
			"How many units did each product line sell in total?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Total of a per-respondent weight to get the weighted base of each segment.",
			descriptor.DomainOps:     "Revenue per sales channel per month.",
			descriptor.DomainScience: "Total dose delivered per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the typical value per row rather than the total", Use: "AGG_AVERAGE"},
			{When: "rows carry weights and you want a weighted average", Use: "AGG_WEIGHTED_MEAN"},
			{When: "a value repeats on several rows of the same key and must be added once per key", Use: "AGG_DISTINCT_SUM"},
			{When: "you want the total so far, row by row", Use: "WIN_RUNNING_SUM"},
		},
		Assumptions: []string{
			"Missing values are skipped: they add nothing to the total.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeAggDistinctSum = descriptor.Purpose{
		Plain:   "Total of a numeric field counting each key once, such as a respondent's weight summed once per respondent.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"What is the weighted base when each respondent appears on several rows?",
			"What is the total contract value when each contract repeats on every line item?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Weighted base of a segment in a cohort with one row per answer.",
			descriptor.DomainOps:    "Total customer credit limit when each customer has many orders.",
		},
		NotFor: []descriptor.Alternative{
			{When: "every row is its own unit and should be added", Use: "AGG_SUM"},
			{When: "you want how many keys there are, not a total", Use: "AGG_DISTINCT_COUNT"},
		},
		Assumptions: []string{
			"The value is the same on every row of a key: when it differs, the first value seen is kept.",
			"A row missing either the value or the key adds nothing.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value", "weighting"},
	}

	purposeAggNullCount = descriptor.Purpose{
		Plain:   "Number of rows where a field has no value, over all rows or per group.",
		Intents: []string{IntentDataQuality},
		Questions: []string{
			"How many respondents skipped the income question?",
			"Which regions have the most orders missing a delivery date?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Item non-response per question and per segment.",
			descriptor.DomainOps:     "Records missing a required field, per data source.",
			descriptor.DomainScience: "Missing measurements per site before an analysis.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the rows that do have a value", Use: "AGG_COUNT"},
			{When: "you want to keep or drop the rows with no value", Use: "FILTER_NULL"},
		},
		Assumptions: []string{
			"Only a stored null counts: an empty selection in a multi-select field is a value, not a null.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeAggDistinctCount = descriptor.Purpose{
		Plain:   "Number of different values a field takes, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentDataQuality},
		Questions: []string{
			"How many different customers ordered this month?",
			"Does the respondent ID appear once per row, or are there duplicates?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Number of distinct respondents per wave.",
			descriptor.DomainOps:     "Unique customers per store.",
			descriptor.DomainScience: "Number of distinct subjects per site.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how many rows there are, not how many different values", Use: "AGG_COUNT"},
			{When: "you want how many rows hold each value: group by the field, then count", Use: "GROUP_CATEGORY"},
			{When: "the field is a multi-select and each combination is the unit", Use: "AGG_SET_DISTINCT_VALUES"},
		},
		Assumptions: []string{
			"Missing values are not counted as a value.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Extremes and typical values -------------------------------------

var (
	purposeAggMin = descriptor.Purpose{
		Plain:   "Smallest value of a numeric or date field, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentDataQuality},
		Questions: []string{
			"What is the earliest order date in each region?",
			"Are there negative or impossible values in the age field?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Lowest age reported, to catch out-of-range answers.",
			descriptor.DomainOps:     "First order date per customer segment.",
			descriptor.DomainScience: "Lowest reading per sensor, to spot faulty sensors.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the largest value", Use: "AGG_MAX"},
			{When: "one extreme row would mislead and you want a low value that ignores it", Use: "AGG_PERCENTILE"},
			{When: "you want the distance between smallest and largest", Use: "AGG_RANGE"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"It rests on one row, so a single bad record sets it.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"outlier", "percentile"},
	}

	purposeAggMax = descriptor.Purpose{
		Plain:   "Largest value of a numeric or date field, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentDataQuality},
		Questions: []string{
			"What is the latest order date in each region?",
			"Is any order value implausibly large?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Highest household size reported, to catch typing errors.",
			descriptor.DomainOps:     "Largest single order per sales channel.",
			descriptor.DomainScience: "Peak reading per run.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the smallest value", Use: "AGG_MIN"},
			{When: "one extreme row would mislead and you want a high value that ignores it", Use: "AGG_PERCENTILE"},
			{When: "you want the distance between smallest and largest", Use: "AGG_RANGE"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"It rests on one row, so a single bad record sets it.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"outlier", "percentile"},
	}

	purposeAggRange = descriptor.Purpose{
		Plain:   "Distance between the smallest and largest value of a numeric field, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentDistributionShape},
		Questions: []string{
			"How far apart are the cheapest and most expensive orders?",
			"How widely do delivery times span in each depot?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Span of ages within each segment.",
			descriptor.DomainOps:     "Spread of daily sales between the slowest and busiest store.",
			descriptor.DomainScience: "Span of readings across repeat measurements.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a spread measure that uses every row, not only the two extremes", Use: "AGG_STDDEV"},
			{When: "you want the spread of the middle of the data, ignoring extremes", Use: "AGG_PERCENTILE"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"It rests on the two most extreme rows, so one outlier can stretch it a lot.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"outlier", "standard-deviation"},
	}

	purposeAggMedian = descriptor.Purpose{
		Plain:   "Middle value of a numeric field once sorted: the typical row, not pulled by a few extreme values.",
		Intents: []string{IntentDescribe, IntentDistributionShape},
		Questions: []string{
			"What is the typical household income in each region?",
			"What is the typical delivery time when a few deliveries take weeks?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Typical income per segment when a few incomes are very large.",
			descriptor.DomainOps:     "Typical order value when a few bulk orders inflate the average.",
			descriptor.DomainScience: "Typical response time per condition.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a value other than the middle, such as the 90th percentile", Use: "AGG_PERCENTILE"},
			{When: "the field is roughly symmetric and you want a figure that combines across groups", Use: "AGG_AVERAGE"},
			{When: "the field holds categories rather than numbers", Use: "AGG_MODE"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"With an even number of values it is the halfway point between the two middle values.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"median", "mean", "outlier", "percentile", "skew"},
	}

	purposeAggMode = descriptor.Purpose{
		Plain:   "Most common value of a field, over all rows or per group.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"What is the most common answer to the preferred-brand question?",
			"Which product is ordered most often in each store?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Most chosen option per question and segment.",
			descriptor.DomainOps:     "Most frequent payment method per channel.",
			descriptor.DomainScience: "Most common category of outcome per group.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the count of every value, not only the top one: group by the field, then count", Use: "GROUP_CATEGORY"},
			{When: "the field is numeric and you want its middle value", Use: "AGG_MEDIAN"},
			{When: "the field is a multi-select", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"When several values share the top count, the smallest wins (for categories, the first in the dictionary); tie_count in the components shows it happened.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"median"},
	}

	purposeAggFrequency = descriptor.Purpose{
		Plain:   "How many rows share a field's most common value, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentComposition},
		Questions: []string{
			"How many respondents gave the most common answer?",
			"How many orders used the most popular payment method?",
			"Grouped by the same field, how many rows fall in each category?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Size of the largest answer group per question and segment.",
			descriptor.DomainOps:     "Orders carrying the most common status per day.",
			descriptor.DomainScience: "Size of the most common outcome category per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a count for every value of the field: group by it, then count rows", Use: "GROUP_CATEGORY"},
			{When: "you want per-value counts for several fields in one call", Use: "capability:facet"},
			{When: "you want which value is most common, not how many rows hold it", Use: "AGG_MODE"},
			{When: "you want how many different values there are", Use: "AGG_DISTINCT_COUNT"},
			{When: "the field is a multi-select", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Missing values are skipped, so only rows that have a value are counted.",
			"Grouped by the same field, each group holds one value, so the result is that group's row count.",
			"When several values tie for most common the count is the same; the components name the smallest tied value as mode_value.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Multi-select fields ---------------------------------------------

var (
	purposeAggSetFrequency = descriptor.Purpose{
		Plain:   "For a multi-select field, how many rows chose each option, over all rows or per group.",
		Intents: []string{IntentComposition, IntentDescribe},
		Questions: []string{
			"How many respondents selected each brand they have heard of?",
			"How many customers use each payment method?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Counts per option of a select-all-that-apply question.",
			descriptor.DomainOps:    "Customers per enabled feature.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field holds one value per row: group by it, then count", Use: "GROUP_CATEGORY"},
			{When: "you want how many different combinations were chosen", Use: "AGG_SET_DISTINCT_VALUES"},
		},
		Assumptions: []string{
			"A row with several options is counted once under each, so the counts can add up to more than the rows.",
		},
		Level: descriptor.LevelBasic,
	}

	purposeAggSetCardinalityAvg = descriptor.Purpose{
		Plain:   "For a multi-select field, the average number of options chosen per row.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"How many brands does a respondent recognise on average?",
			"How many features does a typical customer have turned on?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Average number of answers to a select-all-that-apply question, per segment.",
			descriptor.DomainOps:    "Average number of products per customer bundle.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the total number of selections", Use: "AGG_SET_CARDINALITY_SUM"},
			{When: "you want which options were chosen", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Rows with an empty selection count as zero and pull the average down; null rows are skipped.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"mean", "missing-value"},
	}

	purposeAggSetCardinalitySum = descriptor.Purpose{
		Plain:   "For a multi-select field, the total number of options chosen across all rows.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"How many brand mentions did the survey collect in total?",
			"How many add-ons were sold across all orders?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Total mentions for a select-all-that-apply question, as a base for share of mentions.",
			descriptor.DomainOps:    "Total add-ons attached to orders per channel.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the number per row", Use: "AGG_SET_CARDINALITY_AVG"},
			{When: "you want the count for each option", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Null rows are skipped; an empty selection adds zero.",
		},
		Level: descriptor.LevelBasic,
	}

	purposeAggSetDistinctValues = descriptor.Purpose{
		Plain:   "For a multi-select field, how many different combinations of options appear.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"How many different combinations of channels do customers use?",
			"Do respondents mostly pick the same few sets of brands?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Variety of answer patterns to a select-all-that-apply question.",
			descriptor.DomainOps:    "Number of distinct product bundles sold.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the count of each single option", Use: "AGG_SET_FREQUENCY"},
			{When: "you want every option chosen by anyone", Use: "AGG_SET_UNION"},
		},
		Assumptions: []string{
			"Each combination is one value: choosing A and B differs from choosing A alone.",
		},
		Level: descriptor.LevelBasic,
	}

	purposeAggSetUnion = descriptor.Purpose{
		Plain:   "For a multi-select field, every option chosen by at least one row, over all rows or per group.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"Which brands were mentioned by anyone in this segment?",
			"Which features does at least one customer in each plan use?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Options that received any mention per segment.",
			descriptor.DomainOps:    "Features used by anyone on each plan.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the options every row chose", Use: "AGG_SET_INTERSECTION"},
			{When: "you want how many rows chose each option", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Null rows are skipped.",
		},
		Level: descriptor.LevelBasic,
	}

	purposeAggSetIntersection = descriptor.Purpose{
		Plain:   "For a multi-select field, the options chosen by every row, over all rows or per group.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"Which brands did every respondent in this segment recognise?",
			"Which features does every customer on a plan use?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Options recognised by the whole segment.",
			descriptor.DomainOps:    "Features common to every account on a plan.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want every option chosen by anyone", Use: "AGG_SET_UNION"},
			{When: "you want how many rows chose each option", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Null rows are skipped, but one row with an empty selection empties the result.",
		},
		Level: descriptor.LevelBasic,
	}
)

// --- Spread, shape and intervals (needs reading) ----------------------
//
// Estimator facts are stated as the code computes them
// (internal/processing/aggregator.go, aggregator_online.go,
// aggregator_cohort.go, aggregator_welford.go): AGG_STDDEV / AGG_VARIANCE
// divide by n, AGG_WELFORD and the CI bounds by n - 1; AGG_SKEWNESS is the
// population moment coefficient g1 and AGG_KURTOSIS the population excess
// kurtosis g2, both 0 for n <= 1 or zero variance; the CI bounds use the
// normal critical value, not Student's t; AGG_ZSCORE's value is the mean
// z-score, 0 by construction.

var (
	purposeAggStdDev = descriptor.Purpose{
		Plain:   "Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentDistributionShape},
		Questions: []string{
			"How much do delivery times vary around the average in each region?",
			"Which product line's order values vary the most in absolute terms (divide by each line's average to compare relative consistency)?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "How widely satisfaction scores spread within each segment.",
			descriptor.DomainOps:     "Day-to-day variability of order volume per store.",
			descriptor.DomainScience: "Spread of a measurement within each treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how precisely the average itself is known", Use: "AGG_CI_LOWER"},
			{When: "you want the sample form (dividing by n - 1) together with the mean and n", Use: "AGG_WELFORD"},
			{When: "the field has extreme values and you want a spread they cannot drag", Use: "AGG_PERCENTILE"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"Population form: the squared distances are averaged over n, not n - 1.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"standard-deviation", "variance", "mean", "outlier"},
	}

	purposeAggVariance = descriptor.Purpose{
		Plain:   "Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group.",
		Intents: []string{IntentDescribe, IntentDistributionShape},
		Questions: []string{
			"How much does the order value vary within each channel, in squared units?",
			"Which sites show the most variable readings?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Variability of daily demand per product, for a safety-stock formula.",
			descriptor.DomainScience: "Variance of a measurement across every unit of a fully measured batch.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the spread in the field's own units, which is easier to read", Use: "AGG_STDDEV"},
			{When: "you want the sample variance (dividing by n - 1) with the mean and n, as pooling and study-size planning expect", Use: "AGG_WELFORD"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"Population form: the squared distances are averaged over n, not n - 1.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"variance", "standard-deviation", "mean"},
	}

	purposeAggWelford = descriptor.Purpose{
		Plain:   "Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs.",
		Intents: []string{IntentDescribe, IntentCompareGroups},
		Questions: []string{
			"What are the mean, variance and size of each cell, so cells can be tested against each other?",
			"How do the average and spread of scores compare across segments?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Per-cell mean score, variance and base feeding a t-test overlay on a crosstab.",
			descriptor.DomainScience: "Per-arm summary statistics for a two-sample comparison.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want only the average", Use: "AGG_AVERAGE"},
			{When: "you want the population spread (dividing by n) on its own", Use: "AGG_STDDEV"},
			{When: "you want the comparison itself on raw rows", Use: "TEST_WELCH"},
		},
		Assumptions: []string{
			"Missing values are skipped; only plain integer and float fields are accepted.",
			"The variance divides by n - 1 and is 0 when fewer than two rows have a value.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"mean", "variance", "sample-size", "t-statistic"},
	}

	purposeAggSkewness = descriptor.Purpose{
		Plain:   "How lopsided a numeric field is: positive when a long tail runs to high values, negative when it runs to low ones.",
		Intents: []string{IntentDistributionShape},
		Questions: []string{
			"Are incomes in each region bunched low with a few very high ones?",
			"Is the delivery-time distribution symmetric or dragged out by slow orders?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Whether a rating scale piles up at one end per segment.",
			descriptor.DomainOps:     "Whether order values have a long tail of large orders.",
			descriptor.DomainScience: "Checking a measurement's shape before choosing a test.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to know whether the tails are heavy rather than lopsided", Use: "AGG_KURTOSIS"},
			{When: "you want a test of whether the field follows a normal shape", Use: "TEST_SHAPIRO_WILK"},
			{When: "you want the typical value of a skewed field", Use: "AGG_MEDIAN"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"Population moment coefficient g1 (dividing by n): at small n it runs smaller in size than the adjusted G1 most packages print.",
			"It is 0 when n is 0 or 1 or every value is the same.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"skew", "outlier", "median", "normal-distribution"},
	}

	purposeAggKurtosis = descriptor.Purpose{
		Plain:   "How heavy a numeric field's tails are next to a normal distribution: positive when extreme values are more common.",
		Intents: []string{IntentDistributionShape},
		Questions: []string{
			"Do transaction amounts have more extreme values than a bell curve would give?",
			"Are response times prone to rare, very long waits?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Spotting fields where rare extreme values dominate risk.",
			descriptor.DomainScience: "Checking a measurement's tails before trusting a mean-based test.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to know whether the field is lopsided", Use: "AGG_SKEWNESS"},
			{When: "you want a test of whether the field follows a normal shape", Use: "TEST_SHAPIRO_WILK"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"Population excess kurtosis g2 (dividing by n, minus 3): a normal shape scores 0, and at small n it differs from the adjusted G2 most packages print.",
			"It is 0 when n is 0 or 1 or every value is the same.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"kurtosis", "normal-distribution", "outlier"},
	}

	purposeAggZScore = descriptor.Purpose{
		Plain:   "Population mean and standard deviation of a numeric field, for standardizing it; the value itself is always 0.",
		Intents: []string{IntentDescribe, IntentPrepare},
		Questions: []string{
			"What centre and scale standardize this field in each group?",
			"How many standard deviations does the group's last row (in row order) sit from the group's average, that row included?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "How far the group's last row, in a fixed row order, sits from the group's centre.",
			descriptor.DomainScience: "Group centre and scale for standardizing a measurement by hand.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a z-score on every row", Use: "ATTR_ZSCORE"},
			{When: "you want each group's value against all groups", Use: "OVERLAY_ZSCORE_VS_TOTAL"},
			{When: "you want only the spread", Use: "AGG_STDDEV"},
		},
		Assumptions: []string{
			"Missing values are skipped; the spread is the population form (dividing by n).",
			"The last row is the last in row order, not the latest by date, and it is part of the mean and spread it is compared with, which damps its own score; " +
				"for a reading against its past use OVERLAY_ZSCORE_VS_ROLLING.",
			"Not streamable: the whole group is read before the result is ready.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"z-score", "mean", "standard-deviation"},
	}

	purposeAggCILower = descriptor.Purpose{
		Plain:   "Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down.",
		Intents: []string{IntentDescribe, IntentCompareGroups},
		Questions: []string{
			"How precisely do we know the average satisfaction in each segment?",
			"What is the lowest average order value the data are consistent with at 95% confidence?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Error bars on a mean score per segment.",
			descriptor.DomainOps:     "Range for average handling time per team.",
			descriptor.DomainScience: "Interval for a mean measurement per arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the upper end of the same interval", Use: "AGG_CI_UPPER"},
			{When: "you want to test whether two group means differ", Use: "TEST_WELCH"},
			{When: "you want the range individual rows fall in, not the mean", Use: "AGG_PERCENTILE"},
		},
		Assumptions: []string{
			"Rows are independent draws, unweighted.",
			"Normal critical value (1.96 at 95%), not Student's t, so small groups, and strongly skewed fields even well past 30 rows, get an interval that is too narrow.",
			"Empty (NaN) when fewer than two rows have a value.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"confidence-interval", "mean", "standard-error", "sample-size", "independence"},
	}

	purposeAggCIUpper = descriptor.Purpose{
		Plain:   "Upper end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down.",
		Intents: []string{IntentDescribe, IntentCompareGroups},
		Questions: []string{
			"What is the highest average wait time the data are consistent with at 95% confidence?",
			"How wide is the uncertainty around each region's mean score?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Error bars on a mean score per segment.",
			descriptor.DomainOps:     "Upper end of the 95% interval for average delivery time per carrier: the true average can still lie above it.",
			descriptor.DomainScience: "Interval for a mean measurement per arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the lower end of the same interval", Use: "AGG_CI_LOWER"},
			{When: "you want to test whether two group means differ", Use: "TEST_WELCH"},
		},
		Assumptions: []string{
			"Rows are independent draws, unweighted.",
			"Normal critical value (1.96 at 95%), not Student's t, so small groups, and strongly skewed fields even well past 30 rows, get an interval that is too narrow.",
			"Empty (NaN) when fewer than two rows have a value.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"confidence-interval", "mean", "standard-error", "sample-size", "independence"},
	}

	purposeAggPercentile = descriptor.Purpose{
		Plain:   "Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time.",
		Intents: []string{IntentDescribe, IntentDistributionShape},
		Questions: []string{
			"How long do the slowest 10% of deliveries take in each region?",
			"What income marks the top quarter of respondents?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Quartiles of household income per segment.",
			descriptor.DomainOps:     "95th-percentile response time per service.",
			descriptor.DomainScience: "Reference range (2.5th to 97.5th percentile) of a measurement.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the 50th percentile", Use: "AGG_MEDIAN"},
			{When: "you want each row's own percentile position", Use: "ATTR_PERCENTILE"},
			{When: "you want rows split into equal-sized bands", Use: "GROUP_QUANTILE"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"Linear interpolation between the two nearest sorted values, so the result may be a value no row holds.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"percentile", "median", "outlier"},
	}

	purposeAggRatio = descriptor.Purpose{
		Plain:   "Total of one field divided by the total of another, over all rows or per group, such as revenue per order.",
		Intents: []string{IntentDescribe, IntentComposition},
		Questions: []string{
			"What is revenue per visit in each channel?",
			"What share of budgeted hours were actually worked per team?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Completes per invitation sent, per wave.",
			descriptor.DomainOps:     "Revenue per order, or cost per unit, by region.",
			descriptor.DomainScience: "Events per person-year of follow-up per arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the average of each row's own ratio", Use: "ATTR_FORMULA"},
			{When: "you want an average where some rows count more than others", Use: "AGG_WEIGHTED_MEAN"},
		},
		Assumptions: []string{
			"A row missing either field is left out of both totals.",
			"Empty (NaN) when the denominator total is 0.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"mean"},
	}

	purposeAggWeightedMean = descriptor.Purpose{
		Plain:   "Average of a numeric field where each row counts in proportion to a weight field, such as a survey weight.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"What is the weighted average satisfaction per region?",
			"What is the average price per unit when each order counts by its quantity?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Population-weighted mean score per segment.",
			descriptor.DomainOps:     "Volume-weighted average price per product.",
			descriptor.DomainScience: "Precision-weighted mean of repeated measurements.",
		},
		NotFor: []descriptor.Alternative{
			{When: "every row should count equally", Use: "AGG_AVERAGE"},
			{When: "you want one total divided by another", Use: "AGG_RATIO"},
		},
		Assumptions: []string{
			"Rows missing the value or the weight, or with weight 0, are left out of the average.",
			"Negative weights are not refused.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"weighting", "mean", "effective-sample-size"},
	}
)
