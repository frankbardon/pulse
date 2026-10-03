package descriptor

import "github.com/frankbardon/pulse/descriptor"

// filtererPurposes is the Purpose registry for the FILTER_* operators.
// builtinPurposes assembles it with the other category maps.
//
// A filter keeps or drops whole records before attributes, groups and
// aggregations see them (features run first, so a filter can select on a
// feature column). Every filter in a request must keep a record for it to
// stay: they chain as AND, never OR. All twelve are self-reading
// (interpretation_reading.go): the result is fewer rows, not a figure.
//
// Each Purpose states the operator as Pulse implements it
// (internal/processing/filterer*.go), not as the textbook default. The
// one place they differ is a MISSING value: the keep-list filters
// (FILTER_INCLUDE, FILTER_RANGE, FILTER_DATE_RANGES, FILTER_TRUE and the
// contains / equals set filters) drop it, while the block-list filters
// (FILTER_EXCLUDE, FILTER_SET_CONTAINS_NONE) keep it, and FILTER_FALSE in
// truthy mode keeps it as false. The pinning tests are in
// internal/processing/reading_semantics_filter_group_test.go.
var filtererPurposes = map[string]descriptor.Purpose{
	"FILTER_DATE_RANGES":       purposeFilterDateRanges,
	"FILTER_EXCLUDE":           purposeFilterExclude,
	"FILTER_EXPRESSION":        purposeFilterExpression,
	"FILTER_FALSE":             purposeFilterFalse,
	"FILTER_INCLUDE":           purposeFilterInclude,
	"FILTER_NULL":              purposeFilterNull,
	"FILTER_RANGE":             purposeFilterRange,
	"FILTER_SET_CONTAINS_ALL":  purposeFilterSetContainsAll,
	"FILTER_SET_CONTAINS_ANY":  purposeFilterSetContainsAny,
	"FILTER_SET_CONTAINS_NONE": purposeFilterSetContainsNone,
	"FILTER_SET_EQUALS":        purposeFilterSetEquals,
	"FILTER_TRUE":              purposeFilterTrue,
}

// Shared assumption sentences, so sibling filters say the same thing the
// same way.
const (
	filterBeforeAll   = "Runs before attributes, groups and aggregations, so every figure in the result comes from the rows it keeps."
	filterChainAnd    = "Several filters in one request all have to keep a row for it to stay: they combine as AND, never OR."
	filterDropMissing = "A row whose field is missing is dropped."
	filterKeepMissing = "A row whose field is missing is kept, since it does not hold a listed value."
	filterLabelsKnown = "Each listed value must be one of the field's known labels; an unknown label is an error, not an empty match."
	filterValuesTyped = "On a category field each listed value must be a known label, and an unknown label is an error. " +
		"On a number or date field the values are read as numbers, and one that no row holds simply matches nothing."
)

// --- Keeping or dropping listed values ------------------------------------

var (
	purposeFilterInclude = descriptor.Purpose{
		Plain:   "Keeps only the rows whose field holds one of the listed values, such as two regions or three product codes.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What do the figures look like for the North and South regions only?",
			"How did customers on the two premium plans answer?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep only respondents from the three markets in this wave's report.",
			descriptor.DomainOps:    "Keep only orders from the stores in one district.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to drop the listed values and keep everything else", Use: "FILTER_EXCLUDE"},
			{When: "the field is numeric and you want everything between two limits", Use: "FILTER_RANGE"},
			{When: "the field is a multi-select answer", Use: "FILTER_SET_CONTAINS_ANY"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterChainAnd,
			filterDropMissing,
			filterValuesTyped,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterExclude = descriptor.Purpose{
		Plain:   "Drops the rows whose field holds one of the listed values and keeps the rest, such as removing test accounts.",
		Intents: []string{IntentPrepare, IntentDataQuality},
		Questions: []string{
			"What are the totals once internal test accounts are taken out?",
			"How do the results change without the store that was closed for refit?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Drop the \"Don't know\" answers before working out shares.",
			descriptor.DomainOps:    "Drop orders flagged as internal or test.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to keep only the listed values", Use: "FILTER_INCLUDE"},
			{When: "you want to drop the rows where the field is missing", Use: "FILTER_NULL"},
			{When: "the field is a multi-select answer", Use: "FILTER_SET_CONTAINS_NONE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterChainAnd,
			filterKeepMissing,
			filterValuesTyped,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterRange = descriptor.Purpose{
		Plain:   "Keeps only the rows whose number lies between a low and a high limit, both limits included.",
		Intents: []string{IntentPrepare, IntentDataQuality},
		Questions: []string{
			"What do adults aged 18 to 34 say?",
			"What is the average order once impossible values above 10,000 are left out?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Keep respondents aged 18 to 34.",
			descriptor.DomainOps:     "Keep delivery times between 0 and 72 hours, dropping clearly wrong entries.",
			descriptor.DomainScience: "Keep readings inside the instrument's working range.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you need an open limit, such as strictly above a value", Use: "FILTER_EXPRESSION"},
			{When: "the field is a date and you want named periods", Use: "FILTER_DATE_RANGES"},
			{When: "you want to split the values into bands rather than keep one", Use: "GROUP_RANGE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterChainAnd,
			"Both limits are inclusive: a value equal to the low or the high limit is kept.",
			"On a date field the limits are day numbers counted from 1 January 1970, not written dates.",
			filterDropMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"outlier", "missing-value"},
	}

	purposeFilterDateRanges = descriptor.Purpose{
		Plain:   "Keeps only the rows whose date falls inside one of a set of named date ranges, such as custom fiscal quarters.",
		Intents: []string{IntentPrepare, IntentChangeOverTime},
		Questions: []string{
			"What happened during the two campaign periods only?",
			"What are the figures for the first half of our fiscal year?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep interviews from the two fieldwork windows of this wave.",
			descriptor.DomainOps:    "Keep orders placed during the holiday trading periods.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want each row labelled with its range instead of dropping the rest", Use: "GROUP_DATE_RANGES"},
			{When: "you want plain calendar periods such as months", Use: "GROUP_DATE"},
			{When: "the field is a number rather than a date", Use: "FILTER_RANGE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			"Both ends of each range are inclusive; a range with no start or no end is open on that side.",
			"The ranges may not overlap; overlapping or duplicate ranges are refused, not merged.",
			"A date-and-time field is matched by the day it falls on.",
			filterDropMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterExpression = descriptor.Purpose{
		Plain:   "Keeps the rows for which a written yes-or-no condition is true, for rules the other filters cannot state, such as two fields together.",
		Intents: []string{IntentPrepare, IntentDataQuality},
		Questions: []string{
			"Which customers spent over 500 and joined this year?",
			"Which records have an end date before their start date?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Keep respondents who rated the brand 9 or 10 and bought in the last month.",
			descriptor.DomainOps:     "Keep orders where the shipped quantity is below the ordered quantity.",
			descriptor.DomainHarness: "Express a user's free-form condition as one filter.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you only need to keep a list of values", Use: "FILTER_INCLUDE"},
			{When: "you only need a low and a high limit", Use: "FILTER_RANGE"},
			{When: "you only need rows where a field is or is not missing", Use: "FILTER_NULL"},
		},
		Assumptions: []string{
			filterBeforeAll,
			"The condition must give true or false for every row; any other result is an error.",
			"When a missing value makes the condition unknown, such as a missing number compared with 5, the row is dropped.",
			"A missing value compared with == is false and with != is true; write x ?? 0 to fill it first.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"missing-value"},
	}

	purposeFilterNull = descriptor.Purpose{
		Plain:   "Keeps either the rows where a field is missing or the rows where it is present, to find gaps or set them aside.",
		Intents: []string{IntentDataQuality, IntentPrepare},
		Questions: []string{
			"Who skipped the income question, so their other answers can be compared with those who answered?",
			"What are the averages over only the rows that have a value?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Look at who skipped a question to see whether they differ from those who answered.",
			descriptor.DomainOps:     "Find orders with no delivery date recorded.",
			descriptor.DomainScience: "Set aside samples with no measurement before comparing groups.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you only want to count the missing values, not filter on them", Use: "AGG_NULL_COUNT"},
			{When: "you want to drop particular values, not missing ones", Use: "FILTER_EXCLUDE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			"Missing means no recorded value; a 0, an empty text or an empty multi-select answer is a value, not missing.",
			"Dropping rows with gaps can bias the result when the gaps are not random.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value", "listwise-deletion"},
	}
)

// --- Yes / no fields --------------------------------------------------------

var (
	purposeFilterTrue = descriptor.Purpose{
		Plain:   "Keeps only the rows where a yes/no field is yes, such as active accounts or completed surveys.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What do only the completed interviews say?",
			"What are the figures for active subscribers?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep respondents who passed the attention check.",
			descriptor.DomainOps:    "Keep orders flagged as delivered.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the rows where the field is no", Use: "FILTER_FALSE"},
			{When: "the condition involves more than one field", Use: "FILTER_EXPRESSION"},
			{When: "you want yes and no side by side as groups, not one of them", Use: "GROUP_CATEGORY"},
		},
		Assumptions: []string{
			filterBeforeAll,
			"By default the field must be a yes/no field; a truthy option treats any non-zero number and any non-empty text as yes. " +
				"On a category field it tests the label text, so a label such as \"No\" counts as yes; filter a Yes/No category with FILTER_INCLUDE on the yes label instead.",
			filterDropMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterFalse = descriptor.Purpose{
		Plain:   "Keeps only the rows where a yes/no field is no, such as lapsed accounts or unfinished surveys.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What do the customers who did not renew have in common?",
			"How many interviews were left unfinished, by region?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep respondents who did not complete the survey.",
			descriptor.DomainOps:    "Keep orders not yet shipped.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the rows where the field is yes", Use: "FILTER_TRUE"},
			{When: "you want only the rows where the field is missing", Use: "FILTER_NULL"},
		},
		Assumptions: []string{
			filterBeforeAll,
			"By default the field must be a yes/no field; a truthy option treats 0, an empty text and a missing value as no. " +
				"On a category field it tests the label text, so a label such as \"No\" is not no; use FILTER_INCLUDE on the no label instead.",
			"A missing value is dropped by default but kept under the truthy option, where it counts as no.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)

// --- Multi-select answers -----------------------------------------------------

var (
	purposeFilterSetContainsAny = descriptor.Purpose{
		Plain:   "Keeps the rows whose multi-select answer includes at least one of the listed options, such as anyone who uses brand A or B.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What do people who use either of our two apps think of the service?",
			"Which customers paid by any card at least once?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep respondents aware of at least one of the two new brands.",
			descriptor.DomainOps:    "Keep tickets tagged with any of the billing tags.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the row must include every listed option", Use: "FILTER_SET_CONTAINS_ALL"},
			{When: "the row must include none of the listed options", Use: "FILTER_SET_CONTAINS_NONE"},
			{When: "the field holds one value per row, not several", Use: "FILTER_INCLUDE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterLabelsKnown,
			"An empty list of options keeps no rows.",
			filterDropMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterSetContainsAll = descriptor.Purpose{
		Plain:   "Keeps the rows whose multi-select answer includes every listed option, other options allowed, such as people who use both A and B.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"How do people who use both our app and our website rate us?",
			"Which tickets carry both the urgent and the billing tags?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep respondents who bought both brands in the last month.",
			descriptor.DomainOps:    "Keep orders that included both a phone and a case.",
		},
		NotFor: []descriptor.Alternative{
			{When: "one of the listed options is enough", Use: "FILTER_SET_CONTAINS_ANY"},
			{When: "the row must hold exactly the listed options and nothing else", Use: "FILTER_SET_EQUALS"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterLabelsKnown,
			"An empty list of options keeps every row that has an answer.",
			filterDropMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterSetContainsNone = descriptor.Purpose{
		Plain:   "Keeps the rows whose multi-select answer includes none of the listed options, such as people who use neither A nor B.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"What do people who use none of the competitor apps think?",
			"Which orders had no discount code applied?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep respondents unaware of every competitor brand.",
			descriptor.DomainOps:    "Keep tickets with none of the escalation tags.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want rows that include one of the options", Use: "FILTER_SET_CONTAINS_ANY"},
			{When: "the field holds one value per row, not several", Use: "FILTER_EXCLUDE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterLabelsKnown,
			filterKeepMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}

	purposeFilterSetEquals = descriptor.Purpose{
		Plain:   "Keeps the rows whose multi-select answer is exactly the listed options, no more and no fewer, such as people who use only A.",
		Intents: []string{IntentPrepare},
		Questions: []string{
			"How many customers use our app and nothing else?",
			"Which respondents picked exactly these two reasons?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Keep the respondents loyal to one brand only.",
			descriptor.DomainOps:    "Keep orders paid with exactly one card and no other method.",
		},
		NotFor: []descriptor.Alternative{
			{When: "other options are allowed alongside the listed ones", Use: "FILTER_SET_CONTAINS_ALL"},
			{When: "you want to see every combination with its count", Use: "GROUP_SET_VALUE"},
		},
		Assumptions: []string{
			filterBeforeAll,
			filterLabelsKnown,
			"An empty list keeps the rows that picked nothing, which differs from a missing answer.",
			filterDropMissing,
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"missing-value"},
	}
)
