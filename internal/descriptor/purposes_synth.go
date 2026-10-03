package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/synth"
)

// synthPurposes is the Purpose registry for the synth distributions,
// keyed by the bare lowercase distribution kind exactly as
// synth.AllDistributions() and PurposeSurfaces spell it. builtinPurposes
// assembles it with the other category maps.
//
// A distribution is the recipe one field of a synthetic cohort is drawn
// from; it returns rows, not a figure, so all fifteen are self-reading
// (interpretation_reading.go) and declare only the `simulate` intent.
//
// Each Purpose states the distribution as Pulse implements it
// (internal/synth/distributions.go, discrete.go and the writer arms in
// writer.go), not as the textbook default: lognormal's mu and sigma are on
// the LOG scale; exponential's lambda is a RATE (mean 1/lambda); pareto's
// xm is the smallest value drawn; poisson switches to a rounded normal
// approximation at lambda >= 30; normal's bounds CLIP rather than redraw;
// regex caps open-ended repeats at max_repeat (default 8); uniform_date
// includes both ends and accepts end == start; and a u8..u64 field rounds
// each draw to the nearest whole number, stores a negative as 0 and
// saturates at the type's ceiling. The pinning tests are in
// distribution_docs_test.go and internal/synth.
var synthPurposes = map[string]descriptor.Purpose{
	synth.DistBernoulli:           purposeSynthBernoulli,
	synth.DistConstant:            purposeSynthConstant,
	synth.DistDiscrete:            purposeSynthDiscrete,
	synth.DistExponential:         purposeSynthExponential,
	synth.DistLogNormal:           purposeSynthLogNormal,
	synth.DistMixture:             purposeSynthMixture,
	synth.DistMonotonicFrom:       purposeSynthMonotonicFrom,
	synth.DistNormal:              purposeSynthNormal,
	synth.DistPareto:              purposeSynthPareto,
	synth.DistPoisson:             purposeSynthPoisson,
	synth.DistRegex:               purposeSynthRegex,
	synth.DistSetBernoulli:        purposeSynthSetBernoulli,
	synth.DistUniform:             purposeSynthUniform,
	synth.DistUniformDate:         purposeSynthUniformDate,
	synth.DistWeightedCategorical: purposeSynthWeightedCategorical,
}

// Shared assumption sentences, so sibling distributions say the same
// thing the same way.
const (
	synthSeeded   = "The same spec and seed give the same rows every time; change the seed for a fresh draw."
	synthOnItsOwn = "The field is drawn on its own unless the spec adds correlations, models or rules that tie it to other fields."
	synthIntCast  = "On a u8 to u64 field each draw is rounded to the nearest whole number; a negative draw is stored as 0 and one past the type's top stops there."
	synthNoClamp  = "There is no bound: a rare extreme value can appear, so cap it with a constraint when the field must stay in range."
	synthNoRandom = "It uses no randomness, so adding or removing this field leaves every other field's draws unchanged."
)

// --- Flat and bell-shaped numbers -------------------------------------------

var (
	purposeSynthUniform = descriptor.Purpose{
		Plain:   "Draws numbers between a low and a high bound, every value equally likely, for flat filler columns or random noise.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I fill a test column with random numbers between 0 and 100?",
			"How do I add flat random noise to a synthetic cohort?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainHarness: "A placeholder score column in a test fixture.",
			descriptor.DomainOps:     "Random handling times between 1 and 5 minutes for a load test.",
		},
		NotFor: []descriptor.Alternative{
			{When: "values should cluster around a typical value", Use: synth.DistNormal},
			{When: "the field is a calendar date", Use: synth.DistUniformDate},
			{When: "the field is a coded scale with a few fixed levels", Use: synth.DistDiscrete},
		},
		Assumptions: []string{
			"The low bound can be drawn and the high bound never is, so min 0 and max 1 gives values from 0 up to just under 1.",
			synthIntCast,
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelBasic,
	}

	purposeSynthNormal = descriptor.Purpose{
		Plain:    "Draws numbers from a bell curve around a chosen centre and spread, optionally clipped to a low and a high bound.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"normal-distribution", "standard-deviation"},
		Questions: []string{
			"How do I generate realistic test scores that cluster around 70?",
			"How do I make a numeric column with a known average and spread?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Synthetic satisfaction scores averaging 7 with a standard deviation of 1.5.",
			descriptor.DomainScience: "Simulated measurement error around a true reading.",
		},
		NotFor: []descriptor.Alternative{
			{When: "values are always positive with a long right tail, like income", Use: synth.DistLogNormal},
			{When: "the data has two or more peaks", Use: synth.DistMixture},
			{When: "the field is a coded scale with a few levels, like 1 to 7", Use: synth.DistDiscrete},
		},
		Assumptions: []string{
			"mean sets the centre and std the spread; std must be above 0.",
			"Bounds clip rather than redraw: a draw past a bound is set to that bound, so tight bounds pile values up at the edges.",
			synthIntCast,
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelBasic,
	}

	purposeSynthMixture = descriptor.Purpose{
		Plain:    "Draws from two or more bell curves blended by weight, for data with several peaks, like weekday and weekend order sizes.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"normal-distribution"},
		Questions: []string{
			"How do I simulate a column with two separate peaks?",
			"How do I generate order sizes where small and bulk orders form distinct groups?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Basket values from a mix of small top-up orders and large weekly shops.",
			descriptor.DomainScience: "Readings from two instruments with different calibrations pooled into one column.",
		},
		NotFor: []descriptor.Alternative{
			{When: "one peak is enough", Use: synth.DistNormal},
			{When: "there is one peak with a long right tail", Use: synth.DistLogNormal},
			{When: "the field is a coded scale with a few fixed levels", Use: synth.DistDiscrete},
		},
		Assumptions: []string{
			"Each row first picks a component by weight, then draws from that component's bell curve; without weights each component is equally likely.",
			"means and stds are listed per component and must be the same length, at least two.",
			"A synthetic copy fits two components automatically when they describe the data clearly better than one; three or more are written by hand.",
			synthNoClamp,
			synthSeeded,
		},
		Level: descriptor.LevelAdvanced,
	}

	purposeSynthDiscrete = descriptor.Purpose{
		Plain:   "Draws whole-number levels at set shares, such as a 1 to 7 rating scale, so each level appears about as often as declared.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I generate answers on a 1 to 5 scale where most people choose 4?",
			"How do I reproduce the exact spread of a rating question?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "A 0 to 10 likelihood-to-recommend item with the source's real share at each point.",
			descriptor.DomainOps:    "Items per order, where 1, 2 and 3 cover nearly every order.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is a continuous measure", Use: synth.DistNormal},
			{When: "the levels are text labels", Use: synth.DistWeightedCategorical},
			{When: "the counts follow an average rate rather than fixed shares", Use: synth.DistPoisson},
		},
		Assumptions: []string{
			"values are listed in strictly ascending order with no repeats; weights may be raw counts and default to equal.",
			"A synthetic copy rebuilds every whole-number field with up to 64 distinct levels this way; above 64 it falls back to a clipped bell curve.",
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelIntermediate,
	}
)

// --- Skewed and long-tailed numbers ------------------------------------------

var (
	purposeSynthLogNormal = descriptor.Purpose{
		Plain:    "Draws positive numbers with a long right tail, like incomes or order values: most are modest and a few are very large.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"skew", "median"},
		Questions: []string{
			"How do I simulate skewed order values?",
			"How do I generate realistic household incomes?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Order values with a typical basket near 40 and occasional orders in the thousands.",
			descriptor.DomainScience: "Particle sizes or concentrations that vary by multiples rather than by fixed amounts.",
		},
		NotFor: []descriptor.Alternative{
			{When: "values are spread evenly around a centre", Use: synth.DistNormal},
			{When: "the tail should follow a power law, as with wealth or city sizes", Use: synth.DistPareto},
			{When: "the values are waiting times between events", Use: synth.DistExponential},
		},
		Assumptions: []string{
			"mu and sigma describe the LOG of the value, not the value: the median value is e^mu and the average is e^(mu + sigma²/2).",
			"A larger sigma gives a longer tail; sigma must be above 0.",
			synthNoClamp,
			synthIntCast,
			synthSeeded,
		},
		Level: descriptor.LevelIntermediate,
	}

	purposeSynthExponential = descriptor.Purpose{
		Plain:    "Draws positive waiting times where short waits are common and long ones rare, set by how often events happen.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"mean"},
		Questions: []string{
			"How do I simulate the time between customer arrivals?",
			"How do I generate realistic call durations for a load test?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Minutes between support tickets at one every 4 minutes on average (lambda 0.25).",
			descriptor.DomainScience: "Time to failure of parts that fail at a steady rate.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want whole-number counts of events per period", Use: synth.DistPoisson},
			{When: "a few values should be extremely large", Use: synth.DistPareto},
			{When: "values bunch around a typical size rather than near zero", Use: synth.DistLogNormal},
		},
		Assumptions: []string{
			"lambda is a rate, not the average: the mean is 1/lambda, so lambda 0.5 gives an average of 2.",
			synthNoClamp,
			synthIntCast,
			synthSeeded,
		},
		Level: descriptor.LevelIntermediate,
	}

	purposeSynthPareto = descriptor.Purpose{
		Plain:    "Draws heavy-tailed values above a minimum, where a small share of rows holds most of the total, like wealth or file sizes.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"mean", "variance"},
		Questions: []string{
			"How do I simulate customers where the top fifth spend most of the money?",
			"How do I generate file sizes with a few huge files?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Account balances with xm 10,000 and alpha 1.16, close to an 80/20 split.",
			descriptor.DomainScience: "Event sizes that follow a power law, such as city populations.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the tail is long but values have a typical size", Use: synth.DistLogNormal},
			{When: "the values are waiting times between events", Use: synth.DistExponential},
			{When: "every value in a range should be equally likely", Use: synth.DistUniform},
		},
		Assumptions: []string{
			"xm is the smallest value drawn; alpha sets the tail, and a smaller alpha means a heavier tail. Both must be above 0.",
			"With alpha at or below 1 the mean is infinite, and at or below 2 the variance is, so averages over the generated rows never settle.",
			synthNoClamp,
			synthIntCast,
			synthSeeded,
		},
		Level: descriptor.LevelAdvanced,
	}

	purposeSynthPoisson = descriptor.Purpose{
		Plain:    "Draws whole-number counts of events per period, such as visits per day, around a chosen average.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"variance", "overdispersion"},
		Questions: []string{
			"How many orders might a store get per hour?",
			"How do I simulate defect counts per batch?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Support calls per hour at an average of 6.",
			descriptor.DomainScience: "Particle counts per time window from a steady source.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the time between events, not how many", Use: synth.DistExponential},
			{When: "each row is a yes/no outcome", Use: synth.DistBernoulli},
			{When: "you know the exact share of each count", Use: synth.DistDiscrete},
		},
		Assumptions: []string{
			"lambda is both the average count and its variance; real counts often vary more than that (overdispersion), which this cannot reproduce.",
			"From an average of 30 up, the draw is a rounded bell-curve approximation instead of the exact method.",
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelIntermediate,
	}
)

// --- Flags, labels and multi-select answers ----------------------------------

var (
	purposeSynthBernoulli = descriptor.Purpose{
		Plain:    "Draws a yes/no value per row: 1 with a chosen chance p and 0 otherwise, such as whether a customer churned.",
		Intents:  []string{IntentSimulate},
		Glossary: []string{"probit"},
		Questions: []string{
			"How do I mark 12% of synthetic customers as churned?",
			"How do I generate a random true/false flag?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Whether each respondent is a current customer, at the source's 35% share.",
			descriptor.DomainOps:    "A late-delivery flag on about 8% of orders.",
		},
		NotFor: []descriptor.Alternative{
			{When: "there are more than two outcomes", Use: synth.DistWeightedCategorical},
			{When: "each row can tick several options", Use: synth.DistSetBernoulli},
			{When: "every row should get the same value", Use: synth.DistConstant},
		},
		Assumptions: []string{
			"p is between 0 and 1; on a true/false field 1 is true, on a number field it is the number 1.",
			"The observed share wanders from p in a small cohort and settles as rows grow.",
			"A synthetic copy rebuilds every true/false field this way from its observed share; a model on it shifts which rows get 1 (a probit), not the share.",
			synthSeeded,
		},
		Level: descriptor.LevelBasic,
	}

	purposeSynthWeightedCategorical = descriptor.Purpose{
		Plain:   "Picks one label per row from a list, each at its own share, such as 50% North, 30% South and 20% West.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I give synthetic respondents a realistic region mix?",
			"How do I assign 70% of test users to plan A?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Age band and region with the same mix as the real sample.",
			descriptor.DomainHarness: "Plan tier on test accounts at a fixed 70/20/10 split.",
		},
		NotFor: []descriptor.Alternative{
			{When: "there are just two outcomes stored as 1 and 0 or true and false", Use: synth.DistBernoulli},
			{When: "each row can pick several options", Use: synth.DistSetBernoulli},
			{When: "the levels are numbers on a scale", Use: synth.DistDiscrete},
		},
		Assumptions: []string{
			"weights may be raw counts and need not sum to 1; without weights every label is equally likely.",
			"The labels become the field's dictionary, so the list must fit the categorical width (256 labels for categorical_u8).",
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelBasic,
	}

	purposeSynthSetBernoulli = descriptor.Purpose{
		Plain:   "Fills a multi-select answer: each option is ticked on its own at its own rate, such as 40% picking email and 25% SMS.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I simulate a tick-all-that-apply question?",
			"How do I generate which channels each customer has opted into?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Brands each respondent has bought in the last year.",
			descriptor.DomainOps:    "Features enabled on each account.",
		},
		NotFor: []descriptor.Alternative{
			{When: "each row picks exactly one answer", Use: synth.DistWeightedCategorical},
			{When: "there is only one yes/no flag", Use: synth.DistBernoulli},
		},
		Assumptions: []string{
			"options are listed in bit order and become the field's dictionary; frequencies default to 0.5 each.",
			"Options are drawn independently unless the spec pairs this field with another, in which case a paired option follows the captured joint pattern.",
			"A row with no option ticked is an empty answer, not a missing one.",
			synthSeeded,
		},
		Level: descriptor.LevelIntermediate,
	}
)

// --- Fixed values, IDs, codes and dates --------------------------------------

var (
	purposeSynthConstant = descriptor.Purpose{
		Plain:   "Puts the same value on every row, for a placeholder field or a fixed flag in a test fixture.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I fill a column with the same country code?",
			"How do I add a fixed version field to test data?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainHarness: "A schema-version field that is always 3.",
			descriptor.DomainOps:     "A single-site extract where every row carries the same store code.",
		},
		NotFor: []descriptor.Alternative{
			{When: "each row needs a distinct ID", Use: synth.DistMonotonicFrom},
			{When: "the value should vary over a few labels", Use: synth.DistWeightedCategorical},
			{When: "a flag should be set on only some rows", Use: synth.DistBernoulli},
		},
		Assumptions: []string{
			"The value is checked against the field type when the spec is read: text on a number field is refused, and a multi-select field takes a list of option names.",
			synthNoRandom,
		},
		Level: descriptor.LevelBasic,
	}

	purposeSynthMonotonicFrom = descriptor.Purpose{
		Plain:   "Counts up (or down) by a fixed step from a start value, one step per row, to make unique IDs or row numbers.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I give each synthetic row a unique customer ID?",
			"How do I number rows from 1000 upward?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainHarness: "A primary-key column for a fixture that a lookup index is built on.",
			descriptor.DomainOps:     "Sequential invoice numbers starting at 50,000.",
		},
		NotFor: []descriptor.Alternative{
			{When: "every row should get the same value", Use: synth.DistConstant},
			{When: "the numbers should be random, not in order", Use: synth.DistUniform},
			{When: "IDs should be text codes like AB-1234", Use: synth.DistRegex},
		},
		Assumptions: []string{
			"The first row gets start, the next start plus step, and so on; step must not be 0, and a negative step counts down.",
			"A row a constraint rejects still uses up a number, so the IDs can have gaps.",
			"Pick a field wide enough: past the type's top value every row repeats the top, so the IDs stop being unique.",
			synthNoRandom,
		},
		Level: descriptor.LevelBasic,
	}

	purposeSynthRegex = descriptor.Purpose{
		Plain:   "Generates text matching a pattern, such as order codes like AB-1234, for realistic-looking IDs and labels.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I generate postcodes that look real?",
			"How do I fill a SKU column with codes like SKU-00042?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainHarness: "Order references shaped like the production format, for a parser test.",
			descriptor.DomainOps:     "Masked account codes that keep the real shape but none of the real values.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the values come from a short known list", Use: synth.DistWeightedCategorical},
			{When: "IDs must be unique", Use: synth.DistMonotonicFrom},
			{When: "every row should carry the same text", Use: synth.DistConstant},
		},
		Assumptions: []string{
			"Open-ended repeats such as * and + are capped at max_repeat copies (default 8); back-references are not supported.",
			"Generated strings are not guaranteed unique, and each distinct one becomes a dictionary entry, so a wide pattern on many rows can overflow a narrow categorical field.",
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelIntermediate,
	}

	purposeSynthUniformDate = descriptor.Purpose{
		Plain:   "Draws calendar dates between a start and an end date, every day equally likely, both ends included.",
		Intents: []string{IntentSimulate},
		Questions: []string{
			"How do I spread synthetic orders across 2024?",
			"How do I generate sign-up dates within the last quarter?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Interview dates spread over a six-week fieldwork period.",
			descriptor.DomainOps:    "Order dates across a fiscal year for a reporting test.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is a plain number, not a date", Use: synth.DistUniform},
			{When: "every row shares one date", Use: synth.DistConstant},
		},
		Assumptions: []string{
			"start and end are written as YYYY-MM-DD; end may equal start, which puts every row on that day, but may not come before it.",
			"Dates before 1970-01-01 are allowed.",
			synthOnItsOwn,
			synthSeeded,
		},
		Level: descriptor.LevelBasic,
	}
)
