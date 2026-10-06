package descriptor

import "github.com/frankbardon/pulse/descriptor"

// matrixPurposes is the Purpose registry for the MAT_* operators, keyed
// by type. builtinPurposes assembles it with the other category maps.
// Matrix operators are descriptive: they report figures, no p-values,
// so they declare no Interpretation.
var matrixPurposes = map[string]descriptor.Purpose{
	"MAT_COVARIANCE": purposeMatCovariance,
}

var purposeMatCovariance = descriptor.Purpose{
	Plain:   "How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal.",
	Intents: []string{IntentRelationship, IntentDescribe},
	Questions: []string{
		"How do these five rating scales vary together across respondents?",
		"What is the covariance table of the sensor readings, to feed a later model?",
	},
	UseCases: map[descriptor.Domain]string{
		descriptor.DomainSurvey:  "Covariance table of a battery of rating items, the input a scale or factor analysis starts from.",
		descriptor.DomainScience: "Covariance of several measured responses across units, for a multivariate summary.",
	},
	NotFor: []descriptor.Alternative{
		{When: "you want how closely two numeric fields follow a straight line together, on a scale from -1 to 1", Use: "TEST_PEARSON_R"},
		{When: "you want the spread of one field on its own", Use: "AGG_WELFORD"},
	},
	Assumptions: []string{
		"A row with any member missing is dropped from every cell (listwise deletion).",
		"Sample form by default: the squared deviations are divided by n - 1 (ddof 0 divides by n).",
		"The figures depend on each field's units: a field in larger units gets larger entries.",
	},
	Level:    descriptor.LevelIntermediate,
	Glossary: []string{"covariance", "variance", "listwise-deletion"},
}
