package descriptor

import "github.com/frankbardon/pulse/descriptor"

// aggregatorPurposes is the Purpose registry for the AGG_* operators.
// builtinPurposes assembles it with the other category maps.
//
// The entries here are the self-reading aggregators (interpretation_reading.go):
// their result is a count, a total, an extreme or a list of labels that
// reads as itself, so each carries a Purpose and no Interpretation. Each
// Purpose states the operator as Pulse implements it (see the
// internal/skills/op-agg-*.md atomic skills): nulls are skipped unless the
// operator counts them, AGG_MODE breaks ties by first-seen value, and
// AGG_DISTINCT_SUM keeps the first value seen per key.
var aggregatorPurposes = map[string]descriptor.Purpose{
	"AGG_AVERAGE":             purposeAggAverage,
	"AGG_COUNT":               purposeAggCount,
	"AGG_DISTINCT_COUNT":      purposeAggDistinctCount,
	"AGG_DISTINCT_SUM":        purposeAggDistinctSum,
	"AGG_FREQUENCY":           purposeAggFrequency,
	"AGG_MAX":                 purposeAggMax,
	"AGG_MEDIAN":              purposeAggMedian,
	"AGG_MIN":                 purposeAggMin,
	"AGG_MODE":                purposeAggMode,
	"AGG_NULL_COUNT":          purposeAggNullCount,
	"AGG_RANGE":               purposeAggRange,
	"AGG_SET_CARDINALITY_AVG": purposeAggSetCardinalityAvg,
	"AGG_SET_CARDINALITY_SUM": purposeAggSetCardinalitySum,
	"AGG_SET_DISTINCT_VALUES": purposeAggSetDistinctValues,
	"AGG_SET_FREQUENCY":       purposeAggSetFrequency,
	"AGG_SET_INTERSECTION":    purposeAggSetIntersection,
	"AGG_SET_UNION":           purposeAggSetUnion,
	"AGG_SUM":                 purposeAggSum,
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
			{When: "you want a count for every value of the field", Use: "AGG_FREQUENCY"},
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
			{When: "you want how many rows hold each value", Use: "AGG_FREQUENCY"},
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
			{When: "you want the count of every value, not only the top one", Use: "AGG_FREQUENCY"},
			{When: "the field is numeric and you want its middle value", Use: "AGG_MEDIAN"},
			{When: "the field is a multi-select", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Missing values are skipped.",
			"When several values share the top count, the first one seen wins; tie_count in the components shows it happened.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"median"},
	}

	purposeAggFrequency = descriptor.Purpose{
		Plain:   "Count of rows for each value of a field, over all rows or per group.",
		Intents: []string{IntentComposition, IntentDescribe},
		Questions: []string{
			"How many respondents gave each answer?",
			"How are orders split across payment methods?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Answer counts for a single-choice question, per segment.",
			descriptor.DomainOps:     "Orders per status per day.",
			descriptor.DomainScience: "Count of each outcome category per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want only the most common value", Use: "AGG_MODE"},
			{When: "you want how many different values there are", Use: "AGG_DISTINCT_COUNT"},
			{When: "the field is a multi-select", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"Missing values are skipped, so the counts add up to the rows that have a value.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"cross-tabulation", "missing-value"},
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
			{When: "the field holds one value per row", Use: "AGG_FREQUENCY"},
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
