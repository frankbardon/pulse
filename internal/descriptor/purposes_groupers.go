package descriptor

import "github.com/frankbardon/pulse/descriptor"

// grouperPurposes is the Purpose registry for the GROUP_* operators.
// builtinPurposes assembles it with the other category maps.
//
// A grouper splits the filtered records into buckets, and every
// aggregation is then worked out once per bucket. All eight are
// self-reading (interpretation_reading.go): the result is a bucket key
// per row, not a figure. A row whose field is missing lands in no bucket
// (it is counted in the grouper's n_null component instead).
//
// Each Purpose states the operator as Pulse implements it
// (internal/processing/grouper*.go), not as the textbook default:
// GROUP_ROUNDED rounds DOWN to a multiple of the interval (19 -> 10), so
// it bands like GROUP_RANGE and differs only in the key; GROUP_RANGE keys
// a band "low-high" with the low edge inside; GROUP_QUANTILE splits by
// rank into equal-count buckets, so equal values can straddle two
// buckets; GROUP_DATE defaults to months. The pinning tests are in
// internal/processing/reading_semantics_filter_group_test.go.
var grouperPurposes = map[string]descriptor.Purpose{
	"GROUP_CATEGORY":        purposeGroupCategory,
	"GROUP_DATE":            purposeGroupDate,
	"GROUP_DATE_RANGES":     purposeGroupDateRanges,
	"GROUP_QUANTILE":        purposeGroupQuantile,
	"GROUP_RANGE":           purposeGroupRange,
	"GROUP_ROUNDED":         purposeGroupRounded,
	"GROUP_SET_PER_ELEMENT": purposeGroupSetPerElement,
	"GROUP_SET_VALUE":       purposeGroupSetValue,
}

// Shared assumption sentences, so sibling groupers say the same thing the
// same way.
const (
	groupAfterFilters = "Buckets are built from the rows the filters kept; each aggregation is then worked out once per bucket."
	groupMissingOut   = "A row whose field is missing lands in no bucket; it is counted as missing, not as a bucket of its own."
	groupOnlySeen     = "Only buckets that hold at least one row appear; an empty band or period is left out, not shown as zero."
)

// --- Categories and dates ----------------------------------------------------

var (
	purposeGroupCategory = descriptor.Purpose{
		Plain:   "Splits the rows into one group per distinct value of a field, such as one group per region or per answer option.",
		Intents: []string{IntentCompareGroups, IntentComposition},
		Questions: []string{
			"What is the average spend in each region?",
			"How many respondents chose each answer?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Satisfaction score by age band or by brand.",
			descriptor.DomainOps:     "Orders and revenue per store.",
			descriptor.DomainScience: "Mean reading per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is a number you want in bands, not one group per value", Use: "GROUP_RANGE"},
			{When: "the field is a date and you want months or quarters", Use: "GROUP_DATE"},
			{When: "the field is a multi-select answer", Use: "GROUP_SET_PER_ELEMENT"},
		},
		Assumptions: []string{
			groupAfterFilters,
			groupMissingOut,
			"Groups appear in alphabetical order unless an include list names them, which also sets their order and drops the rest.",
			"A field with very many distinct values makes very many groups; narrow it with a filter first.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeGroupDate = descriptor.Purpose{
		Plain:   "Splits the rows by calendar period of a date, such as month, quarter, ISO week or weekday, to see a measure over time.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"How many orders came in each month?",
			"Which day of the week gets the most complaints?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Satisfaction by fieldwork month.",
			descriptor.DomainOps:     "Weekly order volume, or revenue by fiscal quarter.",
			descriptor.DomainScience: "Daily count of observations.",
		},
		NotFor: []descriptor.Alternative{
			{When: "your periods are custom, such as campaign windows", Use: "GROUP_DATE_RANGES"},
			{When: "you want each period against the one before it", Use: "OVERLAY_INDEX_VS_PRIOR"},
			{When: "you want a running total down the periods", Use: "WIN_RUNNING_SUM"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"Named with no period it groups by month; picked by default for a date field with no grouper type, it groups by day.",
			"Weeks are ISO weeks, Monday to Sunday, numbered within the ISO year.",
			"A fiscal offset applies to years and quarters only; a fiscal year is named for the calendar year it ends in.",
			"A date-and-time field is grouped by the day it falls on.",
			groupOnlySeen,
			groupMissingOut,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeGroupDateRanges = descriptor.Purpose{
		Plain:   "Labels each row with the named date range its date falls in, such as custom fiscal quarters or before and after a launch.",
		Intents: []string{IntentChangeOverTime, IntentCompareGroups},
		Questions: []string{
			"How did sales compare before, during and after the campaign?",
			"What are the totals for each of our custom fiscal quarters?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Scores per fieldwork wave, where waves have irregular dates.",
			descriptor.DomainOps:    "Revenue per promotion period.",
		},
		NotFor: []descriptor.Alternative{
			{When: "plain calendar months, quarters or weeks are enough", Use: "GROUP_DATE"},
			{When: "you want to keep only the rows inside the ranges", Use: "FILTER_DATE_RANGES"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"Both ends of each range are inclusive; the ranges may not overlap.",
			"A row outside every range goes to an unmatched group (named \"unmatched\" unless you rename it), not dropped.",
			"A date-and-time field is matched by the day it falls on.",
			groupMissingOut,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Numbers into bands -------------------------------------------------------

var (
	purposeGroupQuantile = descriptor.Purpose{
		Plain:   "Splits the rows into equal-sized groups by rank of a number, such as quartiles or deciles of spend, to compare top and bottom.",
		Intents: []string{IntentSegment, IntentDistributionShape},
		Questions: []string{
			"How do the top 25% of spenders differ from the bottom 25%?",
			"What is the average basket in each decile of customer value?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Satisfaction by income quartile.",
			descriptor.DomainOps:     "Return rate by decile of order value.",
			descriptor.DomainScience: "Outcome by quartile of exposure.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want bands of a fixed width, such as every 10 years of age", Use: "GROUP_RANGE"},
			{When: "you want each row's rank as a number, not a group", Use: "ATTR_PERCENTILE"},
			{When: "you want the cut-off values themselves", Use: "AGG_PERCENTILE"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"Groups are cut by rank among the rows with a value, so each holds as close to the same count as possible.",
			"Equal values can fall in two neighbouring groups when a cut lands among them.",
			"Groups are named Q1 to Q4 for quartiles, D1 to D10 for deciles, P1 to P100 for percentiles and B1, B2 and so on otherwise; Q1 is the lowest.",
			"Groups come back in text order of their names, so deciles read D1, D10, D2, ... D9; sort them by number before reading a trend.",
			"It needs every row before it can cut, so it cannot stream.",
			groupMissingOut,
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"percentile", "rank", "ties"},
	}

	purposeGroupRange = descriptor.Purpose{
		Plain:   "Splits a number into bands of equal width, such as ages 20-30 and 30-40, to see how values spread or to compare bands.",
		Intents: []string{IntentSegment, IntentDistributionShape},
		Questions: []string{
			"How many customers fall in each 10-year age band?",
			"What does the spread of delivery times look like in 1-hour bands?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Respondents per age band.",
			descriptor.DomainOps:     "Orders per 50-unit order-value band, as a histogram.",
			descriptor.DomainScience: "Count of readings per band of temperature.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want groups of equal size rather than equal width", Use: "GROUP_QUANTILE"},
			{When: "you want each band named by its lower edge alone", Use: "GROUP_ROUNDED"},
			{When: "you want to keep one band, not split into all of them", Use: "FILTER_RANGE"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"Each band is named low-high and holds values from its low edge up to, but not including, its high edge.",
			"Bands start at multiples of the width, counted from 0; with no width given it is 1, or 10 when picked by default for a numeric field.",
			"Rows come back in text order of the band names (100-150 before 50-100); sort by the low edge and add the missing bands before charting a histogram.",
			groupOnlySeen,
			groupMissingOut,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeGroupRounded = descriptor.Purpose{
		Plain:   "Groups a number by rounding it down to a multiple of a step, such as 23 and 27 both to 20, naming each group by that multiple.",
		Intents: []string{IntentSegment, IntentDistributionShape},
		Questions: []string{
			"How many orders are there at each price point, to the nearest lower 10?",
			"What is the count of scores in each block of 5 points?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Respondents per 5-point block of a 0-100 score.",
			descriptor.DomainOps:     "Orders per 10-unit price step, keyed by the step's start.",
			descriptor.DomainScience: "Readings binned to a whole-number step.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the band's both edges in its name", Use: "GROUP_RANGE"},
			{When: "you want groups of equal size", Use: "GROUP_QUANTILE"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"Values are rounded down, not to the nearest: 19 with a step of 10 goes to 10, and -3 goes to -10.",
			"It makes the same groups as equal-width bands of the same step; only the names differ.",
			groupOnlySeen,
			groupMissingOut,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Multi-select answers -----------------------------------------------------

var (
	purposeGroupSetPerElement = descriptor.Purpose{
		Plain:   "Splits a multi-select answer into one group per option, counting each row once in every option it picked.",
		Intents: []string{IntentComposition, IntentCompareGroups},
		Questions: []string{
			"How many respondents picked each brand they are aware of?",
			"What is the average rating among users of each feature?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Awareness count per brand from a pick-all-that-apply question.",
			descriptor.DomainOps:    "Tickets per tag, where a ticket can carry several tags.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want each exact combination of options as one group", Use: "GROUP_SET_VALUE"},
			{When: "you only want the count per option, without other figures", Use: "AGG_SET_FREQUENCY"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"A row that picked three options is counted in three groups, so group counts add up to more than the number of rows.",
			"A row that picked nothing lands in no group, and neither does a missing answer.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value"},
	}

	purposeGroupSetValue = descriptor.Purpose{
		Plain:   "Groups the rows by the exact combination of options picked in a multi-select answer, such as A only, A and B, or none.",
		Intents: []string{IntentComposition},
		Questions: []string{
			"Which combinations of payment methods do customers use, and how common is each?",
			"How many respondents use our app only, versus our app and a competitor's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Brand-repertoire groups: which sets of brands people buy together.",
			descriptor.DomainOps:    "Orders per combination of add-ons.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want one group per option, counting a row in each it picked", Use: "GROUP_SET_PER_ELEMENT"},
			{When: "you want to keep one combination rather than see them all", Use: "FILTER_SET_EQUALS"},
		},
		Assumptions: []string{
			groupAfterFilters,
			"Each row lands in exactly one group, named by its picked options joined with |.",
			"A row that picked nothing gets its own group with an empty name, which differs from a missing answer.",
			groupMissingOut,
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value"},
	}
)
