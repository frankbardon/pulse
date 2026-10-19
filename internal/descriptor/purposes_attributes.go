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
// a missing input gives every score (standardised, rank, min-max, date
// part, selection count, model score) a missing value, never 0 (U50
// E6-S1) — only the 0/1 membership flags ATTR_CODE_IN / ATTR_SET_HAS
// read it as "not in the set" — and ATTR_PERCENTILE gives tied values
// distinct ranks.
var attributePurposes = map[string]descriptor.Purpose{
	"ATTR_CODE_IN":    purposeAttrCodeIn,
	"ATTR_DATE_PART":  purposeAttrDatePart,
	"ATTR_FORMULA":    purposeAttrFormula,
	"ATTR_NORMALIZED": purposeAttrNormalized,
	"ATTR_PERCENTILE": purposeAttrPercentile,

	"ATTR_PREVIOUS_STATE": purposeAttrPreviousState,

	"ATTR_PROFILE_SIMILARITY": purposeAttrProfileSimilarity,
	"ATTR_VEC_DISTANCE":       purposeAttrVecDistance,

	"ATTR_REG_FITTED":   purposeAttrRegFitted,
	"ATTR_MAHALANOBIS":  purposeAttrMahalanobis,
	"ATTR_PC_SCORE":     purposeAttrPCScore,
	"ATTR_FACTOR_SCORE": purposeAttrFactorScore,
	"ATTR_REG_LEVERAGE": purposeAttrRegLeverage,
	"ATTR_REG_PREDICT":  purposeAttrRegPredict,
	"ATTR_REG_RESIDUAL": purposeAttrRegResidual,
	"ATTR_SCALE_SCORE":  purposeAttrScaleScore,
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
			"A missing date gives a missing part, never 0.",
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
			"An empty selection reads 0; a row with a missing value gets no count (null), never 0.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Standardised scores and positions (needs reading) --------------------

var (
	purposeAttrZScore = descriptor.Purpose{
		Plain:   "Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean.",
		KnownAs: []string{"standard score", "standardized score"},
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
			"A missing value gets no score (null) and is left out of the mean and standard deviation; every row gets no score (null) when all values are equal (no spread).",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"z-score", "standard-deviation", "mean", "outlier", "missing-value"},
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
			"A missing value gets no score (null) and is left out of the mean and standard deviation; every row gets no score (null) when all values are equal (no spread).",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"z-score", "standard-deviation", "mean", "missing-value"},
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
			"A missing value gets no percentile (null) and is not counted in n.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"percentile", "rank", "ties", "missing-value"},
	}

	purposeAttrPreviousState = descriptor.Purpose{
		Plain:   "Adds to each row of a long panel the state the same subject held at its previous date, so moves can be counted from-state by to-state.",
		Intents: []string{IntentFlows, IntentPrepare},
		Questions: []string{
			"How many customers moved from each plan to each other plan between snapshots, when the data has one row per customer per snapshot?",
			"Which tier was each member in at the previous monthly snapshot?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Turn a plan-history log into a last-plan by current-plan table for switching and churn analysis.",
			descriptor.DomainSurvey:  "Pair each respondent's answer with their answer in the previous wave of a panel.",
			descriptor.DomainScience: "Pair each visit's disease stage with the stage at the same patient's previous visit.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the data already holds one row per subject with a from column and a to column", Use: "GROUP_CATEGORY"},
			{When: "you want the previous value of a number on result rows", Use: "WIN_LAG"},
		},
		Assumptions: []string{
			"Each subject's rows are put in order by a date or date-time, never by row order; rows on the same date each read the date before it.",
			"A subject's first date has no previous state, and neither does a row after a missing state: both are missing, never a guessed earlier state, so they drop out of a from-state by to-state table.",
			"When rows on one date disagree on the state, the next date has no single previous state: it is missing, and a warning counts it.",
			"It reads each subject's whole history across the cohort, and no weight applies: it is a value copied from another row, not an estimate.",
		},
		Level:    descriptor.LevelIntermediate,
		KnownAs:  []string{"previous state", "lagged state"},
		Glossary: []string{"cross-tabulation", "missing-value"},
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
			"A missing value gets no value (null) and is left out of the minimum and maximum; every row gets no value (null) when all values are equal.",
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
			"A row missing any predictor gets no value (null), never 0.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"regression-coefficient", "overfitting", "listwise-deletion"},
	}

	purposeAttrRegPredict = descriptor.Purpose{
		Plain:   "Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values.",
		Intents: []string{IntentRelationship, IntentBenchmark, IntentDrivers},
		Questions: []string{
			"How close are the model's predictions on weeks it was not fitted to (a holdout)?",
			"How much of each week's sales does the TV term of the fitted model account for?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Score this wave's respondents with the satisfaction model fitted on last wave.",
			descriptor.DomainOps:     "Predict handling time for new tickets from a model fitted on last quarter's.",
			descriptor.DomainScience: "Predict the held-out subjects' response from the dose model fitted on the rest.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the model should be fitted to these same rows inside the slot", Use: "ATTR_REG_FITTED"},
			{When: "you want the coefficients, their standard errors and p-values", Use: "REG_OLS"},
		},
		Assumptions: []string{
			"The model is fitted once, on the fit request's own rows, before any slot runs; scoring never refits.",
			"The scoring rows must carry every predictor column the fit used, built the same way (same features); an absent one-hot column is the reference level.",
			"A row missing any predictor the score reads gets no value (null), never 0.",
			"A prediction reflects association in the fitted rows, never what would happen if a predictor were changed.",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"regression-coefficient", "overfitting", "link-function", "missing-value"},
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
			"A row missing the target or any predictor gets no value (null), never 0.",
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

// --- Fitted vector scores (one fit across several fields) ---------------

var (
	purposeAttrMahalanobis = descriptor.Purpose{
		Plain:   "Adds to every row how unusual its mix of several numeric fields is, allowing for how those fields usually vary together.",
		KnownAs: []string{"mahalanobis distance", "multivariate outlier score", "d-squared"},
		Intents: []string{IntentDataQuality},
		Questions: []string{
			"Which respondents gave an unusual pattern of answers across the whole battery, even if no single answer is extreme?",
			"Which transactions have an odd combination of amount, frequency and basket size?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag straight-liners and odd answer patterns across a rating battery before analysis.",
			descriptor.DomainOps:     "Screen orders whose size, discount and delivery time together look unlike the rest.",
			descriptor.DomainScience: "Find samples whose set of measurements is unusual as a whole.",
		},
		NotFor: []descriptor.Alternative{
			{When: "only one field matters", Use: "ATTR_ZSCORE"},
			{When: "you want how far apart groups are, not single rows", Use: "TEST_HOTELLING_T2"},
		},
		Assumptions: []string{
			"The typical row and the spread come from one fit over every row that passed the filters (or the subset fit_on selects), never per group; every filtered row is scored.",
			"A row missing any of the fields is left out of the fit and gets no score (listwise deletion).",
			"The p output reads the score against a chi-square curve, which holds only when the fields are roughly jointly normal.",
			"There must be more complete rows than fields, and no field may be constant or a sum of the others; otherwise every score is empty, with a warning.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"distance", "outlier", "covariance", "mean", "listwise-deletion",
			"chi-square", "p-value", "normal-distribution", "degrees-of-freedom",
		},
	}

	purposeAttrScaleScore = descriptor.Purpose{
		Plain:   "Adds to every row its score on a rating battery: the average of its answered items or a prorated total, reverse-worded items flipped.",
		KnownAs: []string{"scale score", "summated rating scale", "composite score"},
		Intents: []string{IntentMeasureConstruct, IntentPrepare},
		Questions: []string{
			"What is each respondent's satisfaction score across the five satisfaction items, counting only people who answered at least four?",
			"What is each patient's symptom total when two of the items are worded the other way round?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Turn an agree-disagree battery into one score per respondent, flipping the negatively worded items.",
			descriptor.DomainScience: "Score a questionnaire instrument per participant, prorating the total when an item is skipped.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how consistently the items measure one thing", Use: "MAT_RELIABILITY"},
			{When: "the items should be weighted by how much each reflects a common trait", Use: "ATTR_FACTOR_SCORE"},
			{When: "you want an arbitrary formula of the items", Use: "ATTR_FORMULA"},
		},
		Assumptions: []string{
			"Each row is scored from its own answers only; no other row changes it, and a row weight does not apply.",
			"A skipped item is left out, not counted as 0: the sum is the average of the answered items times the item count.",
			"A row with fewer answered items than min_valid gets no score rather than a low one.",
			"Flipping an item needs the scale's true range, which is declared and never guessed from the data; an answer outside it is treated as skipped and reported.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"mean", "missing-value", "reliability"},
	}

	purposeAttrProfileSimilarity = descriptor.Purpose{
		Plain:   "Adds to every row how closely its related fields match a target profile: an ideal pattern, a benchmark, or its own segment target.",
		KnownAs: []string{"profile similarity", "profile match", "pattern similarity", "fit to target"},
		Intents: []string{IntentBenchmark, IntentPrepare},
		Questions: []string{
			"How closely does each respondent's brand ratings follow the ideal loyalist profile?",
			"How similar is each store's channel mix to its region's target mix?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Score each respondent's fit to an ideal rating profile, the pattern rather than the level (correlation).",
			descriptor.DomainOps:    "Compare each account's spend mix with the target mix for its segment, kept in a registered reference table.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how far apart the values are rather than how alike the pattern is", Use: "ATTR_VEC_DISTANCE"},
			{When: "you want how unusual a row is against the whole cohort", Use: "ATTR_MAHALANOBIS"},
			{When: "you want how far apart whole groups are", Use: "MAT_DISTANCE"},
		},
		Assumptions: []string{
			"The target is matched to the fields by label, never by position; a target whose labels differ is refused before any row is read.",
			"Each row is compared from its own values only; a row weight does not apply.",
			"A field missing on a row drops that pair; a row with fewer complete pairs than min_valid gets no value.",
			"A row whose segment has no target profile gets no value and is counted, rather than refusing the run.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"similarity", "correlation", "pairwise-deletion"},
	}

	purposeAttrVecDistance = descriptor.Purpose{
		Plain:   "Adds to every row how far its set of related fields lies from a target profile, optionally in units of each field's spread.",
		KnownAs: []string{"distance to target", "gap to benchmark", "profile distance", "standardized distance"},
		Intents: []string{IntentBenchmark, IntentPrepare},
		Questions: []string{
			"How far is each store's KPI profile from the benchmark store, with every KPI on its own scale?",
			"Which respondents' ratings sit furthest from the target profile?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Rank stores by their standardized gap to a benchmark KPI profile.",
			descriptor.DomainScience: "Measure each specimen's distance from a reference composition.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how alike the pattern is rather than how far apart the values are", Use: "ATTR_PROFILE_SIMILARITY"},
			{When: "the target is the cohort's own centre", Use: "ATTR_MAHALANOBIS"},
			{When: "you want how far apart whole groups are", Use: "MAT_DISTANCE"},
		},
		Assumptions: []string{
			"The target is matched to the fields by label, never by position; a target whose labels differ is refused before any row is read.",
			"A field missing on a row drops that pair and the sum is scaled up for the dropped pairs; a row with fewer complete pairs than min_valid gets no value.",
			"The standardized and covariance-based distances learn each field's spread from one fit over the filtered rows (weighted when a weight is set), never per group; the plain ones read each row alone.",
			"A row whose segment has no target profile gets no value and is counted, rather than refusing the run.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"distance", "standard-deviation", "covariance", "pairwise-deletion"},
	}

	purposeAttrPCScore = descriptor.Purpose{
		Plain:   "Adds to every row its score on one principal component of several related numeric fields: one number standing in for what they share.",
		KnownAs: []string{"principal component score", "pc score", "component score", "index score"},
		Intents: []string{IntentMeasureConstruct, IntentPrepare},
		Questions: []string{
			"What is each respondent's score on the main dimension behind these rating questions?",
			"Can I replace these five correlated measures with one summary column for a later model?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Score each respondent on the leading dimension of an attitude battery, to segment or chart by it.",
			descriptor.DomainScience: "Reduce many correlated measurements of each specimen to a few component columns before further analysis.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how many dimensions there are and which fields load on each", Use: "MAT_PCA"},
			{When: "you want how unusual a row is across the fields", Use: "ATTR_MAHALANOBIS"},
		},
		Assumptions: []string{
			"The components come from one fit over every row that passed the filters (or the subset fit_on selects), never per group; every filtered row is scored.",
			"A component's direction (which end is high) follows MAT_PCA's rule, not the meaning of the fields: read the loadings before naming a score.",
			"A row missing any of the fields is left out of the fit and gets no score (listwise deletion).",
			"There must be more complete rows than fields, and no field may be constant or a sum of the others; otherwise every score is empty, with a warning.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"principal-component", "eigenvalue", "loading", "correlation", "covariance",
			"z-score", "listwise-deletion",
		},
	}

	purposeAttrFactorScore = descriptor.Purpose{
		Plain:   "Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together.",
		KnownAs: []string{"factor score", "regression factor score", "Thurstone score", "Bartlett score", "latent score"},
		Intents: []string{IntentMeasureConstruct, IntentPrepare},
		Questions: []string{
			"What is each respondent's score on the satisfaction factor behind these rating questions?",
			"Can I score every row on the two constructs a factor analysis found, to compare segments on them?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Score each respondent on the constructs an attitude battery measures, then crosstab or test the scores by segment.",
			descriptor.DomainScience: "Estimate each subject's standing on a latent trait from several noisy indicators before a later model.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want how many factors there are and which fields load on each", Use: "MAT_FACTOR"},
			{When: "you want a summary of all the fields' variance rather than a common trait", Use: "ATTR_PC_SCORE"},
		},
		Assumptions: []string{
			"The factors come from one fit over every row that passed the filters (or the subset fit_on selects), never per group; every filtered row is scored.",
			"The fit is MAT_FACTOR's with the same settings, so factor numbers and directions match its loadings: read them before naming a score.",
			"A row missing any of the fields is left out of the fit and gets no score (listwise deletion).",
			"There must be more complete rows than fields, the factor count must be identified, and no field may be constant or a sum of the others; otherwise every score is empty, with a warning.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"factor", "loading", "correlation", "z-score", "listwise-deletion",
		},
	}
)
