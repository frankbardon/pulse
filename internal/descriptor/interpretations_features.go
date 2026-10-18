package descriptor

import "github.com/frankbardon/pulse/descriptor"

// featureInterpretations is the Interpretation registry for the
// needs-reading FEAT_* operators (interpretation_reading.go): each reads
// the derived column through the static `value` path, or `value.*` for
// FEAT_POLY's family of power columns. builtinInterpretations assembles
// it with the other category maps.
//
// No feature reading carries bands: a transformed value is on a new
// scale, an encoding is a share or an average in the outcome's units, and
// no published convention grades them.
//
// Every fact follows internal/processing/feature/*.go, pinned by
// internal/processing/feature/reading_semantics_test.go and the
// per-operator tests: FEAT_LOG is ln(1 + x), missing at x <= -1;
// FEAT_SQRT is missing below 0; FEAT_POLY emits x^2 .. x^degree only;
// FEAT_FREQUENCY_ENCODE divides by the records with a category; and
// FEAT_TARGET_ENCODE without params.fit_on averages every record's
// outcome — the row's own and the test rows' included.
var featureInterpretations = map[string][]descriptor.Interpretation{
	"FEAT_FREQUENCY_ENCODE": interpFeatFrequencyEncode,
	"FEAT_LOG":              interpFeatLog,
	"FEAT_POLY":             interpFeatPoly,
	"FEAT_SQRT":             interpFeatSqrt,
	"FEAT_TARGET_ENCODE":    interpFeatTargetEncode,
}

// Shared caveat sentences, so sibling features say the same thing the
// same way.
const (
	featWholeCohortCaveat = "Computed over every record of the cohort before any filter runs, so a filtered result still carries figures " +
		"from the records it filtered out."
	featNewScaleCaveat = "Averages, sums and differences of the transformed column are on the new scale: transforming them back does not give " +
		"the average of the original values."
)

// --- Transformed numbers ---------------------------------------------------

var (
	interpFeatLog = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The natural log of 1 + the value: ln(1 + x). 0 reads 0, e - 1 (about 1.72) reads 1, 9 reads about 2.3 and 99 about 4.6; " +
				"each step of about 0.69 is a doubling of 1 + x, so equal steps are equal ratios of 1 + x, not equal amounts.",
			Sign: map[string]string{
				"+": "the original value is above 0",
				"-": "the original value is between -1 and 0",
			},
			Caveats: []string{
				"A value of -1 or less has no log and reads missing, with no error raised: count the missing rows before trusting a model built on the column.",
				"The + 1 shift keeps 0 usable, but equal steps are equal ratios of 1 + x, not of x, and the shape depends on the units: " +
					"on values much smaller than 1 the result is close to the value itself, on values much larger than 1 it is close to ln(x), " +
					"so rescaling the field (dollars vs thousands) changes the shape of the result.",
				featNewScaleCaveat,
				"A missing input gives a missing output.",
			},
		},
	}

	interpFeatSqrt = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The square root of the value: 4 reads 2, 100 reads 10, 10,000 reads 100. Large values are pulled in more than small ones, " +
				"but less sharply than a log.",
			Caveats: []string{
				"A negative value has no real square root and reads missing, with no error raised; 0 reads 0.",
				"Between 0 and 1 the root is LARGER than the value (0.25 reads 0.5), so a field of fractions is stretched, not squeezed.",
				featNewScaleCaveat,
				"A missing input gives a missing output.",
			},
		},
	}

	interpFeatPoly = []descriptor.Interpretation{
		{
			Field: "value.*",
			Means: "One column per power k from 2 up to the degree, named <prefix>_<k> (prefix is the label, by default <field>_poly), " +
				"each holding the value raised to that power: x_poly_2 is x squared, x_poly_3 is x cubed. There is no power-1 column; the original field is it.",
			Caveats: []string{
				"A single power column means little alone: the curve shows in a model's coefficients on x and its powers together, " +
					"and those change when the field is centred first.",
				"Powers of a raw field move almost in step with each other and with x, which makes a model's coefficients unstable; " +
					"centre or standardise the field before expanding it.",
				"Values grow fast: x = 100 to the power 10 is 1e20, and very large inputs can overflow to infinity rather than read missing.",
				"Even powers lose the sign: x = -3 and x = 3 both give 9 in the squared column.",
				"A missing input gives a missing value in every power column.",
			},
		},
	}
)

// --- Encoded categories ----------------------------------------------------

var (
	interpFeatFrequencyEncode = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The share of records carrying this record's category, as a fraction between 0 and 1: 0.25 means one in four records with a category have this one. " +
				"Every record in a category gets the same value.",
			Caveats: []string{
				"The share is out of records WITH a category: records missing the field read missing and are left out of the total, " +
					"so shares across categories add up to 1 among those records only.",
				featWholeCohortCaveat,
				"Two different categories with the same count get the same value, so the column cannot tell them apart.",
				"It is a share, not a count: the same category reads differently on a larger or smaller cohort.",
			},
		},
	}

	interpFeatTargetEncode = []descriptor.Interpretation{
		{
			Field: "value",
			Means: "The average of the outcome field over the training records (params.fit_on, else every record) in this record's category, in the outcome's units; with smoothing s, " +
				"(n * category average + s * overall average) / (n + s), so a category with few records sits nearer the overall average.",
			Caveats: []string{
				"Target leakage without params.fit_on: every average includes the record's own outcome and the outcomes of validation and test " +
					"records. Placing FEAT_TRAIN_TEST_SPLIT first does not change a single value, and filtering to split 0 afterwards keeps the " +
					"leaked figures; set fit_on to the training records (for example split == 0) so only they feed the averages.",
				"A training record's average still holds its own outcome unless folds is set, so a model trained on the column looks better " +
					"than it is: with folds k each training record reads the averages of the training records outside its fold, and every other " +
					"record reads the full training averages. Smooth as well, since a category seen once encodes exactly its own outcome.",
				featWholeCohortCaveat,
				"Only training records with both a category and an outcome count; a category with no such record reads the overall training " +
					"average, a record missing the category reads missing, and so does a training record whose fold leaves no outcome outside it.",
			},
		},
	}
)
