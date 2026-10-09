package descriptor

import "github.com/frankbardon/pulse/descriptor"

// windowPurposes is the Purpose registry for the WIN_* operators.
// builtinPurposes assembles it with the other category maps.
//
// A window adds one value to every row of the RESULT, computed from other
// rows of the same partition in order_by order. It runs after grouping
// and aggregation, so with groups a row is one group's figures, and with
// no groups and no aggregations it is one filtered record. The
// self-reading windows (interpretation_reading.go) return an earlier or
// later value, a row number or a running total; the needs-reading ones
// (changes, smoothed levels, ranks) also carry a `value` Interpretation
// in interpretations_window.go.
//
// Each Purpose states the operator as Pulse implements it
// (internal/processing/window/*.go), not as the textbook default: steps
// are ROWS, never calendar periods, WIN_PCT_CHANGE is a fraction (0.05 =
// 5%), and rows with a missing order_by value sort last in both
// directions. The pinning tests are in
// internal/processing/window/reading_semantics_test.go.
var windowPurposes = map[string]descriptor.Purpose{
	"WIN_DELTA":       purposeWinDelta,
	"WIN_DENSE_RANK":  purposeWinDenseRank,
	"WIN_EWMA":        purposeWinEWMA,
	"WIN_LAG":         purposeWinLag,
	"WIN_LEAD":        purposeWinLead,
	"WIN_MOVING_AVG":  purposeWinMovingAvg,
	"WIN_PCT_CHANGE":  purposeWinPctChange,
	"WIN_RANK":        purposeWinRank,
	"WIN_ROW_NUMBER":  purposeWinRowNumber,
	"WIN_RUNNING_AVG": purposeWinRunningAvg,
	"WIN_RUNNING_SUM": purposeWinRunningSum,
}

// Shared assumption sentences, so sibling windows say the same thing the
// same way.
const (
	winOnResultRows = "Runs on the result's rows after grouping: one row per group, or one per filtered record when nothing is grouped."
	winRowsNotTime  = "Steps are rows, not calendar periods: a period with no row is skipped, not filled, so one row back can span two periods."
	winPerPartition = "Computed separately within each partition_by slice, in order_by order; with no partition the whole result is one series."
	winNullsLast    = "Rows with a missing order_by value sort last, in both directions."
	winNullSkipped  = "Missing values are skipped, so the figure covers the values present; a frame with no value reads null."
)

// --- Shifting and numbering (self-reading) --------------------------------

var (
	purposeWinLag = descriptor.Purpose{
		Plain:   "Adds to every row the value of a field from a set number of rows earlier in the ordered series, such as last month's sales.",
		Intents: []string{IntentChangeOverTime, IntentPrepare},
		Questions: []string{
			"What were each store's sales in the month before, on the same row as this month's?",
			"What score did each respondent give in the previous wave?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Previous wave's score beside the current one for each tracked brand.",
			descriptor.DomainOps:     "Last week's order count beside this week's, per warehouse.",
			descriptor.DomainScience: "The previous reading beside each measurement in a time series.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the difference from the earlier row, not its value", Use: "WIN_DELTA"},
			{When: "you want the value from a later row", Use: "WIN_LEAD"},
			{When: "you want each period against the period before it as an index", Use: "OVERLAY_INDEX_VS_PRIOR"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			winRowsNotTime,
			"The first rows of each partition have no earlier row and read null, or the default when one is set.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeWinLead = descriptor.Purpose{
		Plain:   "Adds to every row the value of a field from a set number of rows later in the ordered series, such as next month's sales.",
		Intents: []string{IntentChangeOverTime, IntentPrepare},
		Questions: []string{
			"What did each customer spend in the following month, beside this month's spend?",
			"What reading came next after each measurement?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Next wave's answer beside the current one, to see who changed their mind.",
			descriptor.DomainOps:     "Next shipment date beside each order, to measure the wait.",
			descriptor.DomainScience: "The following reading beside each measurement in a series.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the value from an earlier row", Use: "WIN_LAG"},
			{When: "you want how much the field changed between rows", Use: "WIN_DELTA"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			winRowsNotTime,
			"The last rows of each partition have no later row and read null, or the default when one is set.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeWinRowNumber = descriptor.Purpose{
		Plain:   "Numbers the rows 1, 2, 3 within each partition in the chosen order, for picking the first few rows of every group.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"Which are the top three products by revenue in each region?",
			"What is each customer's first order, by date?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Number each respondent's answers in the order given.",
			descriptor.DomainOps:     "Top five stores by sales within each region.",
			descriptor.DomainHarness: "Number result rows to page through them in a fixed order.",
		},
		NotFor: []descriptor.Alternative{
			{When: "rows with equal values should share a position", Use: "WIN_RANK"},
			{When: "you want ranks without gaps after equal values", Use: "WIN_DENSE_RANK"},
		},
		Assumptions: []string{
			winOnResultRows,
			"Row 1 goes to the first row in order_by order: the smallest value, or the largest when the key is desc, so set desc for a top-N.",
			"Rows with equal order_by values still get different numbers, in an arbitrary but repeatable order.",
			winNullsLast,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"rank", "ties"},
	}

	purposeWinRunningSum = descriptor.Purpose{
		Plain:   "Adds a running total of a field down the ordered rows, such as revenue for the year to date.",
		Intents: []string{IntentChangeOverTime, IntentDescribe},
		Questions: []string{
			"What is the year-to-date revenue at the end of each month?",
			"How many responses had come in by each day of fieldwork?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Cumulative completes per day against the fieldwork quota.",
			descriptor.DomainOps:     "Year-to-date sales per region, month by month.",
			descriptor.DomainScience: "Cumulative dose received by each visit.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want one total per group, not a running one", Use: "AGG_SUM"},
			{When: "you want the running average instead of the total", Use: "WIN_RUNNING_AVG"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			"The frame sets which rows add up: no preceding bound with following 0 gives the total from the start to this row.",
			winNullSkipped,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Changes between rows (needs reading) ---------------------------------

var (
	purposeWinDelta = descriptor.Purpose{
		Plain:   "Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month.",
		Intents: []string{IntentChangeOverTime, IntentBenchmark},
		Questions: []string{
			"By how many orders did each month go up or down on the month before?",
			"How many points did satisfaction move between waves?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Change in a brand's awareness, in percentage points, from the last wave.",
			descriptor.DomainOps:     "Week-on-week change in tickets opened per queue.",
			descriptor.DomainScience: "Change in each subject's reading since the previous visit.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the change relative to the earlier value", Use: "WIN_PCT_CHANGE"},
			{When: "you want each period's change from the one before as a decoration on the result", Use: "OVERLAY_DELTA_VS_PRIOR"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			winRowsNotTime,
			"The first rows of each partition, and any row where either value is missing, read null; an earlier value of 0 is a real change.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"percentage-point", "baseline", "missing-value"},
	}

	purposeWinPctChange = descriptor.Purpose{
		Plain:   "Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month.",
		Intents: []string{IntentChangeOverTime, IntentBenchmark},
		Questions: []string{
			"By what percentage did each month's revenue grow on the month before?",
			"Which regions grew fastest relative to their own previous quarter?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Relative change in sample size per wave.",
			descriptor.DomainOps:     "Month-on-month growth in revenue per product line.",
			descriptor.DomainScience: "Relative change in a measurement since the previous visit.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the earlier value can be 0, near 0 or negative, or the field is itself a percentage", Use: "WIN_DELTA"},
			{When: "you want the same year-ago period as the comparison", Use: "OVERLAY_YOY"},
			{When: "you want each period as an index value against the one before", Use: "OVERLAY_INDEX_VS_PRIOR"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			winRowsNotTime,
			"The first rows of each partition, any row where either value is missing, and any row whose earlier value is 0 read null.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "percentage-point", "index-value", "missing-value"},
	}
)

// --- Smoothed levels (needs reading) --------------------------------------

var (
	purposeWinMovingAvg = descriptor.Purpose{
		Plain:   "Adds a moving average: the mean of a field over a fixed number of neighbouring rows, smoothing short-term swings in a series.",
		KnownAs: []string{"rolling mean", "simple moving average"},
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"What is the trailing seven-day average of daily orders?",
			"Is the trend in weekly sign-ups rising once the week-to-week noise is smoothed out?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Three-wave average of a tracking score to steady small-sample waves.",
			descriptor.DomainOps:     "Seven-day average of daily orders.",
			descriptor.DomainScience: "Smooth a noisy sensor series before reading its trend.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the average of everything up to this row", Use: "WIN_RUNNING_AVG"},
			{When: "you want recent rows to count more than older ones", Use: "WIN_EWMA"},
			{When: "you want each period against its recent average", Use: "OVERLAY_INDEX_VS_ROLLING_MEAN"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			winRowsNotTime,
			"The frame must be bounded on both sides; near a partition's edges it holds fewer rows.",
			winNullSkipped,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"rolling-mean", "mean", "missing-value"},
	}

	purposeWinRunningAvg = descriptor.Purpose{
		Plain:   "Adds the average of a field over the rows so far in the ordered series, such as the average order value to date.",
		Intents: []string{IntentChangeOverTime, IntentDescribe},
		Questions: []string{
			"What is the average monthly revenue for the year so far, at each month?",
			"How has the cumulative average score settled as more waves came in?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Average satisfaction to date across fieldwork days.",
			descriptor.DomainOps:     "Average handling time to date, month by month.",
			descriptor.DomainScience: "Cumulative mean of repeated measurements as each one is added.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the average of a fixed number of recent rows", Use: "WIN_MOVING_AVG"},
			{When: "you want one average per group, not a running one", Use: "AGG_AVERAGE"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			"The frame sets which rows count: no preceding bound with following 0 gives the average from the start to this row.",
			winNullSkipped,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"mean", "missing-value"},
	}

	purposeWinEWMA = descriptor.Purpose{
		Plain:   "Adds a smoothed series: each new value counts for a fixed share w, the previous smoothed level for 1 - w, so older values fade.",
		KnownAs: []string{"exponential smoothing", "exponentially weighted moving average"},
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"What is the underlying level of daily demand once day-to-day noise is damped?",
			"Is the smoothed defect rate drifting up over recent batches?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Smoothed tracking score that reacts to recent waves more than old ones.",
			descriptor.DomainOps:     "Smoothed daily demand for a stock forecast.",
			descriptor.DomainScience: "Smoothed sensor reading that follows slow drift and damps spikes.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want every row in a fixed window to count equally", Use: "WIN_MOVING_AVG"},
			{When: "you want how unusual each period is against its recent run", Use: "OVERLAY_ZSCORE_VS_ROLLING"},
		},
		Assumptions: []string{
			winOnResultRows,
			winPerPartition,
			winRowsNotTime,
			"The share w is set directly (params.alpha, above 0 and at most 1); it seeds from the partition's first value present.",
			"A missing value reads null and the smoothing carries over it.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"mean", "missing-value"},
	}
)

// --- Ranks (needs reading) -------------------------------------------------

var (
	purposeWinRank = descriptor.Purpose{
		Plain:   "Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Where does each store rank on revenue within its region?",
			"Which product came first in each month by units sold?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Rank of each brand on preference within every market.",
			descriptor.DomainOps:     "Rank of each warehouse on on-time delivery within its region.",
			descriptor.DomainScience: "Rank of each site's yield within each season.",
		},
		NotFor: []descriptor.Alternative{
			{When: "equal values should share a rank with no gap after them", Use: "WIN_DENSE_RANK"},
			{When: "every row needs its own number even when values are equal", Use: "WIN_ROW_NUMBER"},
			{When: "you want each record's position as a percentage of all records", Use: "ATTR_PERCENTILE"},
		},
		Assumptions: []string{
			winOnResultRows,
			"Rank 1 goes to the first row in order_by order: the smallest value, or the largest when the key is desc.",
			"Rows tie only when equal on every order_by key.",
			winNullsLast,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"rank", "ties"},
	}

	purposeWinDenseRank = descriptor.Purpose{
		Plain:   "Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Which price tier is each product in, counting equal prices as one tier?",
			"With scores ordered desc, how many distinct scores sit above each respondent's (the dense rank minus 1)?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Position of each rating level from the top, with equal ratings sharing one position.",
			descriptor.DomainOps:     "Tier number of each store by sales, equal sales sharing a tier.",
			descriptor.DomainScience: "Order of distinct dose levels within each trial arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the rank should skip past equal values, as in sports standings", Use: "WIN_RANK"},
			{When: "every row needs its own number even when values are equal", Use: "WIN_ROW_NUMBER"},
		},
		Assumptions: []string{
			winOnResultRows,
			"Rank 1 goes to the first row in order_by order: the smallest value, or the largest when the key is desc.",
			"Rows tie only when equal on every order_by key.",
			winNullsLast,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"rank", "ties"},
	}
)
