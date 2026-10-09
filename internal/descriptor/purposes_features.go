package descriptor

import "github.com/frankbardon/pulse/descriptor"

// featurePurposes is the Purpose registry for the FEAT_* operators.
// builtinPurposes assembles it with the other category maps.
//
// A feature adds derived columns to every RECORD before anything else in
// the request runs: features → filterers → attributes → groups →
// aggregations → windows. So a filter can select on a feature column, but
// a feature never sees a filter: the whole-cohort features
// (FEAT_FREQUENCY_ENCODE, FEAT_TARGET_ENCODE, FEAT_TRAIN_TEST_SPLIT and
// FEAT_BUCKETIZE in quantile mode) compute over every record of the cohort.
// The self-reading features (interpretation_reading.go) return a bucket
// index, 0/1 indicator columns, date parts or a split code; the
// needs-reading ones (transformed values and encodings) also carry a
// `value` / `value.*` Interpretation in interpretations_features.go.
//
// Each Purpose states the operator as Pulse implements it
// (internal/processing/feature/*.go), not as the textbook default:
// FEAT_LOG is ln(1 + x), FEAT_FREQUENCY_ENCODE is a share of the
// non-missing rows, and FEAT_TARGET_ENCODE reads no split column, so it
// encodes from every record — test rows and the row's own target
// included — whatever FEAT_TRAIN_TEST_SPLIT runs before it. The pinning
// tests are in internal/processing/feature/reading_semantics_test.go.
var featurePurposes = map[string]descriptor.Purpose{
	"FEAT_BUCKETIZE":        purposeFeatBucketize,
	"FEAT_DATE_FEATURES":    purposeFeatDateFeatures,
	"FEAT_FREQUENCY_ENCODE": purposeFeatFrequencyEncode,
	"FEAT_LOG":              purposeFeatLog,
	"FEAT_ONE_HOT":          purposeFeatOneHot,
	"FEAT_POLY":             purposeFeatPoly,
	"FEAT_SQRT":             purposeFeatSqrt,
	"FEAT_TARGET_ENCODE":    purposeFeatTargetEncode,
	"FEAT_TRAIN_TEST_SPLIT": purposeFeatTrainTestSplit,
}

// Shared assumption sentences, so sibling features say the same thing the
// same way.
const (
	featBeforeFilters = "Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees."
	featWholeCohort   = "Its figures come from every record of the cohort, before any filter: filtering afterwards does not recompute them."
	featNullOut       = "A missing input value gives a missing output."
)

// --- Transforming a number -------------------------------------------------

var (
	purposeFeatLog = descriptor.Purpose{
		Plain:   "Adds a column holding the natural log of 1 + the value, which pulls in a long tail of large values so a skewed field is easier to model.",
		Intents: []string{IntentPrepare, IntentDistributionShape},
		Questions: []string{
			"Can I tame the long tail of customer spend before fitting a model on it?",
			"How does income look on a log-like scale, where for large values doubling counts about the same everywhere?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Log of reported household income before using it as a predictor.",
			descriptor.DomainOps:     "Log of order value, where a few huge orders dwarf the rest.",
			descriptor.DomainScience: "Log of a concentration that spans several orders of magnitude.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the tail is only moderate and a gentler squeeze is enough", Use: "FEAT_SQRT"},
			{When: "you want each value in standard units from the mean, not a squeezed scale", Use: "ATTR_ZSCORE"},
			{When: "you want ordered bins instead of a continuous transform", Use: "FEAT_BUCKETIZE"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"Computes ln(1 + x), not ln(x): 0 maps to 0, and values between -1 and 0 map below 0.",
			"A value of -1 or less has no log and reads missing, with no error; " + featNullOut,
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"skew", "missing-value"},
	}

	purposeFeatSqrt = descriptor.Purpose{
		Plain:   "Adds a column holding the square root of the value, a gentler squeeze than a log for counts with a moderate long tail.",
		Intents: []string{IntentPrepare, IntentDistributionShape},
		Questions: []string{
			"Can I soften the few very large complaint counts without a full log?",
			"What does the field look like with its big values pulled in a little?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Square root of the number of open-text mentions per respondent.",
			descriptor.DomainOps:     "Square root of daily ticket counts before modelling them.",
			descriptor.DomainScience: "Square root of event counts, a common step for count data.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the tail is very long and needs a stronger squeeze", Use: "FEAT_LOG"},
			{When: "you want each value in standard units from the mean", Use: "ATTR_ZSCORE"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"A negative value has no real square root and reads missing, with no error; " + featNullOut,
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value"},
	}

	purposeFeatPoly = descriptor.Purpose{
		Plain:   "Adds columns holding the value squared, cubed and so on up to a chosen power, so a straight-line model can bend into a curve.",
		Intents: []string{IntentPrepare, IntentRelationship},
		Questions: []string{
			"Does satisfaction rise with tenure and then level off?",
			"Is the effect of temperature on yield curved rather than straight?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Age and age squared as predictors, for an effect that peaks in mid-life.",
			descriptor.DomainOps:     "Order size and its square, for a cost curve that steepens.",
			descriptor.DomainScience: "Dose, dose squared and dose cubed for a curved dose-response fit.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to fit the curve itself, not just build its columns", Use: "REG_OLS"},
			{When: "you want one custom transform of the value", Use: "ATTR_FORMULA"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"Emits powers 2 up to the degree (at most 10); the power-1 term is the original column, which you add yourself.",
			"Raw powers grow fast and move together; centre or standardise the field first. " + featNullOut,
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"regression-coefficient", "multicollinearity", "missing-value"},
	}
)

// --- Recoding a category ---------------------------------------------------

var (
	purposeFeatOneHot = descriptor.Purpose{
		Plain:   "Turns a category field into one 0/1 column per category, so a model or a sum can treat each category as its own yes/no.",
		KnownAs: []string{"dummy coding", "dummy variables"},
		Intents: []string{IntentPrepare},
		Questions: []string{
			"How do I feed region into a regression as separate yes/no predictors?",
			"Which rows are in each plan tier, as columns I can sum?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "One yes/no column per answer to a single-choice question.",
			descriptor.DomainOps:     "One column per warehouse for a model of delivery time.",
			descriptor.DomainScience: "One column per treatment arm in a design table.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field has hundreds of categories and one column each is too many", Use: "FEAT_FREQUENCY_ENCODE"},
			{When: "you want how many rows fall in each category: group by the field, then count", Use: "GROUP_CATEGORY"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"Columns come from the field's dictionary, so a category with no rows still gets an all-zero column.",
			"A missing category reads 0 in every column; there is no separate unknown column.",
			"In a model with an intercept, leave one category's column out as the reference: all of them together are perfectly collinear and the fit is refused.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value", "multicollinearity"},
	}

	purposeFeatFrequencyEncode = descriptor.Purpose{
		Plain:   "Replaces each category with the share of records that carry it, one number that says how common the category is.",
		Intents: []string{IntentPrepare, IntentComposition},
		Questions: []string{
			"How common is each customer's city, as a single number a model can use?",
			"Which records belong to rare product codes?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "How common each respondent's free-text employer code is.",
			descriptor.DomainOps:     "How common each SKU is, to flag rare items.",
			descriptor.DomainScience: "How common each species label is in the sample.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the count table itself, one row per category: group by the field, then count", Use: "GROUP_CATEGORY"},
			{When: "the field has few categories and each should be its own column", Use: "FEAT_ONE_HOT"},
		},
		Assumptions: []string{
			featBeforeFilters,
			featWholeCohort,
			"The share is out of records with a category; records missing it read missing and are left out of the total.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value"},
	}

	purposeFeatTargetEncode = descriptor.Purpose{
		Plain:   "Replaces each category with the average outcome of the records in that category, optionally pulled toward the overall average.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"How do I give a model a single number for each of a thousand postcodes?",
			"What is the average sale price of each product category, on every record?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Average satisfaction of each respondent's employer, as a predictor.",
			descriptor.DomainOps:     "Average delivery delay of each carrier route, on every shipment.",
			descriptor.DomainScience: "Average response of each batch from earlier or held-out runs, as a covariate (an average that includes a row's own response leaks it).",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to report each category's average outcome", Use: "AGG_AVERAGE"},
			{When: "the field has few categories and each should be its own column", Use: "FEAT_ONE_HOT"},
			{When: "you want how common each category is, without using the outcome", Use: "FEAT_FREQUENCY_ENCODE"},
		},
		Assumptions: []string{
			featBeforeFilters,
			featWholeCohort,
			"It reads no split column: placing FEAT_TRAIN_TEST_SPLIT first changes nothing, and test rows and each row's own outcome still feed every average.",
			"Smoothing s gives (n * category average + s * overall average) / (n + s); 0 means no pull.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"mean", "overfitting", "missing-value"},
	}
)

// --- Calendar parts, bins and splits (self-reading) -------------------------

var (
	purposeFeatDateFeatures = descriptor.Purpose{
		Plain:   "Splits a date into year, month, day, day of week and quarter columns, so each part can be filtered, grouped or modelled.",
		Intents: []string{IntentPrepare, IntentChangeOverTime},
		Questions: []string{
			"Do orders differ by day of the week?",
			"Is there a seasonal pattern by month across several years?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Quarter and weekday of each interview, to check fieldwork timing.",
			descriptor.DomainOps:     "Weekday and month of each order, as model inputs for demand.",
			descriptor.DomainScience: "Month of each sample, to look for seasonal effects.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you need just one part of the date", Use: "ATTR_DATE_PART"},
			{When: "you want results grouped by day, month or year", Use: "GROUP_DATE"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"Accepts a date or datetime field; a datetime is read on the local clock of its time zone and adds an hour column. Day of week is 0 for Sunday through 6 for Saturday.",
			"A missing date gives missing values in every column.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFeatBucketize = descriptor.Purpose{
		Plain:   "Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number.",
		Intents: []string{IntentPrepare, IntentSegment},
		Questions: []string{
			"Which income band is each customer in?",
			"Which spend decile (tenth of orders ranked by spend) does each order fall in?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Age bands for each respondent, from fixed cut points.",
			descriptor.DomainOps:     "Order value tenths, as a column later steps can filter on.",
			descriptor.DomainScience: "Exposure bands for each subject, for a banded analysis.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want results grouped by fixed-width bands", Use: "GROUP_RANGE"},
			{When: "you want results grouped by equal-count bins", Use: "GROUP_QUANTILE"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"Bin 0 is the lowest; a value equal to a cut point goes in the bin below it.",
			"Equal-count bins use cut points from every record of the cohort, and repeated values can leave bins uneven. " + featNullOut,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"percentile", "missing-value"},
	}

	purposeFeatTrainTestSplit = descriptor.Purpose{
		Plain:   "Labels every record train (0), validation (1) or test (2) by a seeded shuffle, so a model can be checked on records it never learned from.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"How do I hold back 20% of records to test a model on?",
			"Can I split records so each class keeps its share in train and test?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Hold out a share of respondents to check a scoring model.",
			descriptor.DomainOps:     "Train, validation and test sets for a churn model.",
			descriptor.DomainHarness: "A repeatable split so two runs compare models on the same rows.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you just want a random subset of rows to look at", Use: "capability:sample"},
			{When: "you want to compare groups that already exist", Use: "GROUP_CATEGORY"},
		},
		Assumptions: []string{
			featBeforeFilters,
			"Each share is the ratio times the record count, rounded; with stratify it is applied within each category.",
			"The same seed on the same records in the same order gives the same labels; adding or reordering records reshuffles them.",
			"The split does not isolate other features: frequency encoding, target encoding and equal-count bucketing in the same request still learn from test rows, " +
				"so build those from the train rows in a separate request.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"overfitting"},
	}
)
