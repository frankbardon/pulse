package descriptor

import "github.com/frankbardon/pulse/descriptor"

// attributePurposes is the Purpose registry for the ATTR_* operators.
// builtinPurposes assembles it with the other category maps.
//
// An attribute adds one number to every row, computed over every row that
// passed the filters (before any grouping, so a group never re-centres
// it). The self-reading attributes (interpretation_reading.go) return a
// calendar part, a formula result, a 0/1 membership flag or a selection
// count; the needs-reading ones (standardised scores, ranks, min-max
// scaling and the regression diagnostics) also carry a `value`
// Interpretation in interpretations_attributes.go.
//
// Each Purpose states the operator as Pulse implements it
// (internal/processing/attribute*.go), not as the textbook default: the
// z- and T-scores use the population standard deviation (dividing by n),
// a missing input reads 0 (50 for the T-score), never null, and
// ATTR_PERCENTILE gives tied values distinct ranks.
var attributePurposes = map[string]descriptor.Purpose{
	"ATTR_CODE_IN":      purposeAttrCodeIn,
	"ATTR_DATE_PART":    purposeAttrDatePart,
	"ATTR_FORMULA":      purposeAttrFormula,
	"ATTR_NORMALIZED":   purposeAttrNormalized,
	"ATTR_PERCENTILE":   purposeAttrPercentile,
	"ATTR_REG_FITTED":   purposeAttrRegFitted,
	"ATTR_REG_LEVERAGE": purposeAttrRegLeverage,
	"ATTR_REG_RESIDUAL": purposeAttrRegResidual,
	"ATTR_SET_HAS":      purposeAttrSetHas,
	"ATTR_SET_POPCOUNT": purposeAttrSetPopcount,
	"ATTR_TSCORE":       purposeAttrTScore,
	"ATTR_ZSCORE":       purposeAttrZScore,
}

// Shared assumption sentences, so sibling attributes say the same thing
// the same way.
const (
	attrOverFilteredRows = "Computed over every row that passed the filters, before grouping: a group never re-centres it."
	attrRegListwise      = "The model is fitted to the rows that passed the filters and have the target and every predictor (listwise deletion)."
	attrRegOwnFit        = "Each ATTR_REG_* slot fits its own straight-line model; the request's regressions are not reused."
)

// --- Row-local derivations (self-reading) --------------------------------

var (
	purposeAttrDatePart = descriptor.Purpose{
		Plain:   "Adds a calendar part of a date or timestamp to every row, such as the year, the month, a year-month like 202403 or the hour.",
		Intents: []string{IntentPrepare, IntentChangeOverTime},
		Questions: []string{
			"Which month of the year does each order fall in, so seasons can be compared across years?",
			"What year-month label should each response carry for a monthly breakdown?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Tag each response with its fieldwork year-month.",
			descriptor.DomainOps:     "Month-of-year column for comparing seasonal order volume.",
			descriptor.DomainScience: "Collection year per sample for a by-year breakdown.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you only want rows grouped by day, week, month or year", Use: "GROUP_DATE"},
			{When: "you want several calendar columns at once, including weekday", Use: "FEAT_DATE_FEATURES"},
		},
		Assumptions: []string{
			"Reads a date field (epoch days) or a datetime field; a datetime is read on the local clock of its time zone, and the hour part needs a datetime.",
			"The part is an encoded number (YYYYMM for year_month), so arithmetic on it is meaningless.",
			"A missing date reads 0.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeAttrFormula = descriptor.Purpose{
		Plain:   "Adds a number to every row computed from the row's own fields with an expression, such as price * qty.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What is each order's line total from its price and quantity?",
			"Which rows meet a combined rule, as a 0/1 column for counting?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Sum of several rating items into one score per respondent.",
			descriptor.DomainOps:     "Margin per order from revenue and cost.",
			descriptor.DomainScience: "Body-mass index from recorded weight and height.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to keep or drop rows by a rule rather than add a column", Use: "FILTER_EXPRESSION"},
			{When: "you want the logarithm of a field", Use: "FEAT_LOG"},
		},
		Assumptions: []string{
			"A missing field enters the expression as nil, and an operator that cannot take nil fails the request. A guard such as x ?? 0 turns each missing value " +
				"into a real 0, which pulls averages down, so prefer dropping those rows with FILTER_NULL or pick a fallback that means something.",
			"True / false results become 1 / 0.",
			"It cannot read another attribute's column from the same request.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value"},
	}

	purposeAttrCodeIn = descriptor.Purpose{
		Plain:   "Adds a 1 / 0 column saying whether each row's code is one of a listed set, keeping every row in the base.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What share of all respondents gave a top-two-box answer (codes 4 or 5)?",
			"Which orders carry one of the priority status codes, as a column to average by region?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Top-box flag to average as a share of the whole weighted base: non-answers stay in the denominator unless FILTER_NULL drops them first.",
			descriptor.DomainOps:    "Flag for orders whose status code is in a chosen group.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to keep only the rows with those codes", Use: "FILTER_INCLUDE"},
			{When: "you want how often a single value occurs", Use: "AGG_FREQUENCY"},
			{When: "the field is a multi-select", Use: "ATTR_SET_HAS"},
		},
		Assumptions: []string{
			"On a categorical field a code is a dictionary label; one absent from the dictionary matches nothing.",
			"A row with a missing value reads 0, the same as a row with another code.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeAttrSetHas = descriptor.Purpose{
		Plain:   "Adds a 1 / 0 column saying whether each row's multi-select field includes one named option.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"Which respondents ticked 'price' among their reasons, as a column to cross with other answers?",
			"Which tickets carry the 'urgent' tag?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Flag for one option of a multiple-choice question, to average as a share among those who answered: drop missing answers with FILTER_NULL first, since they read 0.",
			descriptor.DomainOps:    "Flag for orders tagged with a given promotion.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to keep only rows that include any of several options", Use: "FILTER_SET_CONTAINS_ANY"},
			{When: "you want how often each option was chosen", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			"The option must be in the field's dictionary; an unknown one is refused.",
			"A row with a missing value reads 0, the same as a row that did not choose the option.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeAttrSetPopcount = descriptor.Purpose{
		Plain:   "Adds to every row the number of options its multi-select field has selected.",
		Intents: []string{IntentPrepare, IntentDescribe},
		Questions: []string{
			"How many reasons did each respondent tick?",
			"Which customers selected three or more interests?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Number of brands each respondent is aware of.",
			descriptor.DomainOps:    "Number of tags on each support ticket.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the average number of selections per group", Use: "AGG_SET_CARDINALITY_AVG"},
			{When: "you want whether one particular option was chosen", Use: "ATTR_SET_HAS"},
		},
		Assumptions: []string{
			"An empty selection reads 0, and so does a row with a missing value.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Standardised scores and positions (needs reading) --------------------

var (
	purposeAttrZScore = descriptor.Purpose{
		Plain:   "Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean.",
		Intents: []string{IntentBenchmark, IntentPrepare},
		Questions: []string{
			"Which orders are unusually large compared with all orders?",
			"How do scores measured on different scales compare once put on one footing?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Put rating items with different spreads on a common scale before combining them.",
			descriptor.DomainOps:     "Flag deliveries far slower than typical for review.",
			descriptor.DomainScience: "Standardize a measurement before comparing it with another.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the centre and spread of each group, not a score per row", Use: "AGG_ZSCORE"},
			{When: "the field is skewed or has extreme values and you want a position they cannot distort", Use: "ATTR_PERCENTILE"},
			{When: "you want each group's value against all groups", Use: "OVERLAY_ZSCORE_VS_TOTAL"},
		},
		Assumptions: []string{
			attrOverFilteredRows,
			"Uses the population standard deviation (dividing by n).",
			"A missing value reads 0, as does every row when all values are equal.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"z-score", "standard-deviation", "mean", "outlier"},
	}

	purposeAttrTScore = descriptor.Purpose{
		Plain:   "Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10.",
		Intents: []string{IntentBenchmark, IntentPrepare},
		Questions: []string{
			"How does each candidate's test result compare with the group, on a 50-centred scale?",
			"Which respondents score more than one standard deviation above average?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Report standardized scale scores on a 50-centred scale, where negative values are rare.",
			descriptor.DomainScience: "Express assessment scores on a mean-50, SD-10 scale relative to the rows analysed (not to a published norm group).",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want plain standard-deviation units centred on 0", Use: "ATTR_ZSCORE"},
			{When: "you want each row's percentile rank (the share at or below it when values are untied)", Use: "ATTR_PERCENTILE"},
		},
		Assumptions: []string{
			attrOverFilteredRows,
			"Uses the population standard deviation (dividing by n).",
			"A missing value reads 50, as does every row when all values are equal.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"z-score", "standard-deviation", "mean"},
	}

	purposeAttrPercentile = descriptor.Purpose{
		Plain:   "Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied.",
		Intents: []string{IntentBenchmark, IntentDistributionShape},
		Questions: []string{
			"Where does each store's revenue rank among all stores, as a percentage?",
			"Which respondents are in the top tenth for spend?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Percentile of each respondent's household income.",
			descriptor.DomainOps:     "Percentile rank of each order's delivery time.",
			descriptor.DomainScience: "Percentile of each sample's reading within the batch.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the value at a chosen percentile, such as the 90th", Use: "AGG_PERCENTILE"},
			{When: "you want ranks within partitions, or tied values to share a rank", Use: "WIN_RANK"},
			{When: "you want rows split into equal-sized bands", Use: "GROUP_QUANTILE"},
		},
		Assumptions: []string{
			attrOverFilteredRows,
			"Tied values do not share a percentile: each tied row takes its own rank in an arbitrary order, so equal values can read tens of points apart.",
			"Rows tied at a cut-off are split across it arbitrarily and can change between runs; cut on the value (AGG_PERCENTILE, then a filter) for a reproducible top tenth.",
			"Not streamable: every value is sorted before the first row is ready.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"percentile", "rank", "ties"},
	}

	purposeAttrNormalized = descriptor.Purpose{
		Plain:   "Rescales a numeric field to 0 to 1 on every row: 0 is the smallest value, 1 the largest.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"How can fields on very different scales be put on one 0-to-1 scale before combining them?",
			"Where does each row's value sit between the lowest and highest seen?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Rescale items to 0-1 by their observed lowest and highest answers, before comparing their spread.",
			descriptor.DomainOps:     "Scale each site's load between its observed minimum and maximum.",
			descriptor.DomainHarness: "Bound an input to 0..1 before handing it to a scoring model.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field has extreme values that would squeeze the rest of the range", Use: "ATTR_PERCENTILE"},
			{When: "you want distance from the mean in standard deviations", Use: "ATTR_ZSCORE"},
			{When: "you want a composite on the scales' fixed endpoints (1-5, 0-10), such as (x - 1) / 4", Use: "ATTR_FORMULA"},
		},
		Assumptions: []string{
			attrOverFilteredRows,
			"It uses the OBSERVED lowest and highest values, not a scale's endpoints: if nobody answered 1 on a 1-5 item, 2 maps to 0, " +
				"so the same raw score lands at different positions across items, filtered subsets or waves.",
			"A missing value reads 0, the same as the minimum, as does every row when all values are equal; in a composite that pulls the score down.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"outlier", "missing-value"},
	}
)

// --- Regression diagnostics (needs reading; statistically loaded) ---------

var (
	purposeAttrRegFitted = descriptor.Purpose{
		Plain:   "Adds to every row the value a straight-line model predicts for its target from its predictors.",
		Intents: []string{IntentRelationship, IntentBenchmark},
		Questions: []string{
			"What order value would we expect for each customer given their visits and tenure?",
			"Which stores sell above or below what their size and footfall predict?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Expected satisfaction per respondent from their attribute ratings.",
			descriptor.DomainOps:     "Expected handling time per ticket from queue length and agent tenure.",
			descriptor.DomainScience: "Expected response per subject from dose and body weight.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the coefficients, R-squared and p-values of the model", Use: "REG_OLS"},
			{When: "you want how far each row sits from its prediction", Use: "ATTR_REG_RESIDUAL"},
			{When: "the target is yes/no or a count", Use: "REG_GLM"},
		},
		Assumptions: []string{
			attrRegListwise,
			attrRegOwnFit,
			"A prediction reflects association in these rows, never what would happen if a predictor were changed.",
			"A row missing any predictor reads 0, which is not a prediction.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"regression-coefficient", "overfitting", "listwise-deletion"},
	}

	purposeAttrRegResidual = descriptor.Purpose{
		Plain:   "Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it.",
		Intents: []string{IntentBenchmark, IntentDataQuality},
		Questions: []string{
			"Which stores sell far more or less than their size and footfall predict?",
			"Do the model's misses grow with the predicted value, or bend in a curve?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Respondents far more or less satisfied than their ratings predict.",
			descriptor.DomainOps:     "Tickets that took far longer than queue length and tenure predict.",
			descriptor.DomainScience: "Check a dose-response fit for curvature or growing spread.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the model's summary (coefficients, R-squared)", Use: "REG_OLS"},
			{When: "you want how unusual each row's predictor values are", Use: "ATTR_REG_LEVERAGE"},
			{When: "you want how far a value is from the mean, with no model", Use: "ATTR_ZSCORE"},
		},
		Assumptions: []string{
			attrRegListwise,
			attrRegOwnFit,
			"A row missing the target or any predictor reads 0, the same as a row the model fits exactly.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"residual", "heteroscedasticity", "outlier", "listwise-deletion"},
	}

	purposeAttrRegLeverage = descriptor.Purpose{
		Plain:   "Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit.",
		Intents: []string{IntentDataQuality},
		Questions: []string{
			"Which rows have predictor values so far from the rest that they could steer the model?",
			"Which customers have predictor values extreme enough that they could pull the fitted line (read their residuals to see whether they do)?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Find the few very large accounts that could dominate a revenue model; read their residuals before concluding they do.",
			descriptor.DomainScience: "Screen subjects with extreme dose or weight before trusting a fit.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want rows the model predicts badly", Use: "ATTR_REG_RESIDUAL"},
			{When: "you want the model itself", Use: "REG_OLS"},
		},
		Assumptions: []string{
			attrRegListwise,
			attrRegOwnFit,
			"Unpenalized least squares only: any penalty is refused.",
			"It looks at the predictors only, never at the target.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"outlier", "residual", "listwise-deletion"},
	}
)
