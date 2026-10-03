package descriptor

import "github.com/frankbardon/pulse/descriptor"

// windowInterpretations is the Interpretation registry for the
// needs-reading WIN_* operators (interpretation_reading.go): each reads
// the per-row column through the static `value` path.
// builtinInterpretations assembles it with the other category maps.
//
// No window reading carries bands: a change, a smoothed level and a rank
// are in the field's units or are positions, and no published convention
// grades them.
//
// Every fact follows internal/processing/window/*.go, pinned by
// internal/processing/window/reading_semantics_test.go and the
// per-operator tests: WIN_PCT_CHANGE is (cur - prev) / prev as a
// fraction, null on a zero earlier value; WIN_DELTA keeps a zero earlier
// value; WIN_EWMA seeds from the first value present and carries its
// state over a missing row; the averages skip missing values and read
// null on a frame with none; the rank pair ties rows equal on every
// order_by key and puts missing keys last, sharing the last rank.
var windowInterpretations = map[string][]descriptor.Interpretation{
	"WIN_DELTA":       interpWinDelta,
	"WIN_DENSE_RANK":  interpWinDenseRank,
	"WIN_EWMA":        interpWinEWMA,
	"WIN_MOVING_AVG":  interpWinMovingAvg,
	"WIN_PCT_CHANGE":  interpWinPctChange,
	"WIN_RANK":        interpWinRank,
	"WIN_RUNNING_AVG": interpWinRunningAvg,
}

// Shared caveat sentences, so sibling windows say the same thing the same
// way.
const (
	winRowsNotTimeCaveat = "A step is one row of the result in order_by order, not a calendar period: when a period has no row " +
		"(a month with no sales), the comparison silently spans the gap, so check the series has a row for every period."
	winGroupRowsCaveat = "With groups, each row is one group's figure and counts once whatever the group's size: " +
		"an average over rows is an average of group figures, not the average over all records."
	winSingleChangeCaveat = "One period's change mixes real movement with ordinary noise; " +
		"judge it against how much the series usually moves from period to period."
	winRankDirection = "Rank 1 is the first row in order_by order: the smallest value with an ascending key, the largest with desc. " +
		"Say which when reporting a 'top' rank."
	winRankPositions = "A rank is a position, not a distance: rank 1 and rank 2 can be almost equal or far apart, so read the values beside the ranks. " +
		"Rank 3 of 5 rows is not rank 3 of 500; compare positions across partitions of different size with care."
	winRankNulls = "Rows tie only when equal on every order_by key; rows with a missing order_by value sort last and tie with each other, " +
		"so they share the last rank."
)

// --- Changes between rows --------------------------------------------------

var (
	interpWinPctChange = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The change since the row periods rows earlier, as a fraction of that earlier value: (current - earlier) / earlier. " +
				"0.05 is a 5% rise, -0.2 a 20% fall and 1 a doubling; the value is not multiplied by 100.",
			Sign: map[string]string{
				"+": "the current value is above the earlier one (when the earlier value is positive)",
				"-": "the current value is below the earlier one (when the earlier value is positive)",
			},
			Caveats: []string{
				"A small earlier value makes the fraction large and jumpy: a move from 0.5 to 3 reads 5 (a 500% rise), so a big percentage on a tiny base says little; " +
					"read WIN_DELTA beside it. An earlier value of exactly 0 reads null.",
				"On a field that can be negative the sign follows the division, not the direction: a rise from -10 to -5 reads -0.5. Use WIN_DELTA for such a field.",
				"Rises and falls are not symmetric: after a fall of 0.5 (50%) it takes a rise of 1 (100%) to get back, " +
					"so averaging or adding period changes misstates the overall change.",
				"On a field that is itself a percentage, 40% to 45% reads 0.125 (a 12.5% relative rise), not 5 percentage points; WIN_DELTA gives the points.",
				winRowsNotTimeCaveat,
				winSingleChangeCaveat,
				"The first periods rows of each partition, and any row where either value is missing, read null.",
			},
		},
	}

	interpWinDelta = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The change since the row periods rows earlier, in the field's own units: current - earlier. " +
				"A delta of 12 on an order count is 12 more orders than the earlier row.",
			Sign: map[string]string{
				"+": "the current value is above the earlier one",
				"-": "the current value is below the earlier one",
			},
			Caveats: []string{
				"It is in the field's units, so a change of 10 is large on a small field and trivial on a large one; " +
					"read WIN_PCT_CHANGE for the change relative to the earlier value.",
				"On a field that is itself a percentage the delta is in percentage points: 40% to 45% reads 5, a 12.5% relative rise.",
				"An earlier value of 0 is a real change (the delta is the current value), unlike WIN_PCT_CHANGE, which reads null there.",
				winRowsNotTimeCaveat,
				winSingleChangeCaveat,
				"The first periods rows of each partition, and any row where either value is missing, read null.",
			},
		},
	}
)

// --- Smoothed levels --------------------------------------------------------

var (
	interpWinMovingAvg = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The mean of the field over the rows in the frame around this one: from preceding rows before it to following rows after it, " +
				"within the partition and in order_by order. preceding 6, following 0 is a trailing average of 7 rows.",
			Caveats: []string{
				"Near the start and end of each partition the frame runs off the edge and the mean covers fewer rows, " +
					"so the first values are noisier and not comparable with the rest.",
				"A trailing frame (following 0) lags behind turns in the series by about half its width; " +
					"a centred frame (preceding equal to following) does not lag, and an uneven one lags by half the difference, " +
					"but any frame with following above 0 uses later rows, so it cannot be computed until they exist.",
				"Missing values are skipped, so the mean is over the values present, not the frame width; a frame with no value reads null.",
				winGroupRowsCaveat,
				winRowsNotTimeCaveat,
			},
		},
	}

	interpWinRunningAvg = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The mean of the field over the rows in the frame. With the usual frame (no preceding bound, following 0) it is the cumulative average: " +
				"the mean of every row from the partition's start up to and including this one.",
			Caveats: []string{
				"Early values rest on few rows and swing; later ones rest on the whole history and barely move, " +
					"so a late cumulative average says little about the recent level (use WIN_MOVING_AVG for that).",
				"A frame with a following bound above 0, or none, takes in later rows too.",
				"Missing values are skipped, so the mean is over the values present; a frame with no value reads null.",
				winGroupRowsCaveat,
			},
		},
	}

	interpWinEWMA = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "A smoothed level of the field: s = alpha * current + (1 - alpha) * previous s, seeded with the partition's first value present. " +
				"A value k rows back carries weight alpha * (1 - alpha)^k, so alpha near 1 follows the latest value closely and alpha near 0 changes slowly.",
			Caveats: []string{
				"The seed enters at full weight, so the start of each partition leans on the first value: with alpha 0.1 it still carries about 35% of the weight " +
					"ten rows later (0.9^10). Read the early rows with care.",
				"It lags behind a trend: on a steadily rising series the smoothed value sits below the latest value.",
				"alpha is given directly; some tools set it from a span (alpha = 2 / (span + 1)), which is not accepted here, so convert first.",
				"A missing value reads null and the smoothing carries over it: the next value is blended in as if the gap were one step.",
				"The required frame is not used: the smoothing always runs from the partition's first row.",
				winRowsNotTimeCaveat,
			},
		},
	}
)

// --- Ranks -------------------------------------------------------------------

var (
	interpWinRank = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The row's position in order_by order within its partition, starting at 1. Rows equal on every order_by key share a rank, " +
				"and the next rank skips past them: 1, 2, 2, 4. A row's rank is 1 plus the number of rows strictly before it.",
			Caveats: []string{
				winRankDirection,
				winRankPositions,
				winRankNulls,
				"Tied rows make the ranks skip numbers, so the ranks used are fewer than the rows; to count distinct values use WIN_DENSE_RANK.",
			},
		},
	}

	interpWinDenseRank = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The row's position among the distinct order_by values within its partition, starting at 1. Rows equal on every order_by key share a rank, " +
				"and the next rank follows on with no gap: 1, 2, 2, 3. The largest rank is the number of distinct values, not the number of rows.",
			Caveats: []string{
				winRankDirection,
				winRankPositions,
				winRankNulls,
				"Dense rank 3 does not mean two rows came first: many rows can share ranks 1 and 2. Use WIN_RANK when the count of rows ahead matters.",
			},
		},
	}
)
