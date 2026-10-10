package descriptor

import "github.com/frankbardon/pulse/descriptor"

// matrixPurposes is the Purpose registry for the MAT_* operators, keyed
// by type. builtinPurposes assembles it with the other category maps.
// Matrix operators are descriptive: they report figures, no p-values.
// MAT_CORRELATION's and MAT_PARTIAL_CORRELATION's primaries are
// standardised figures, so each carries an Interpretation (matrixInterpretations in interpretations_matrices.go);
// MAT_COVARIANCE's is in the members' own units and declares none.
var matrixPurposes = map[string]descriptor.Purpose{
	"MAT_CORRELATION":         purposeMatCorrelation,
	"MAT_COVARIANCE":          purposeMatCovariance,
	"MAT_PARTIAL_CORRELATION": purposeMatPartialCorrelation,
	"MAT_RELIABILITY":         purposeMatReliability,
	"MAT_PCA":                 purposeMatPCA,
	"MAT_COLLINEARITY":        purposeMatCollinearity,
}

var purposeMatCollinearity = descriptor.Purpose{
	Plain:   "Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled.",
	KnownAs: []string{"vif", "variance inflation", "multicollinearity", "condition index"},
	Intents: []string{IntentDrivers, IntentDataQuality},
	Questions: []string{
		"Before I model satisfaction on these eight ratings, are any of them so related that their effects cannot be told apart?",
		"Which of these predictors are near-duplicates of the others?",
	},
	UseCases: map[descriptor.Domain]string{
		descriptor.DomainSurvey:  "Screening a driver-analysis battery for questions that say almost the same thing before regressing the overall score on them.",
		descriptor.DomainScience: "Checking that measured covariates are not near-linear combinations of each other before fitting a model.",
	},
	NotFor: []descriptor.Alternative{
		{When: "you want the fitted effects of the predictors on an outcome", Use: "REG_OLS"},
		{When: "you only want to see which measures go together, pair by pair", Use: "MAT_CORRELATION"},
	},
	Assumptions: []string{
		"The fields are the predictors only; the outcome plays no part, so the check is the same whatever you later model.",
		"It reads straight-line overlap among the predictors; it does not test any relationship with the outcome.",
		"A predictor that is an exact combination of others is refused, naming the fields involved.",
		"A row with any predictor missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.",
	},
	Level:    descriptor.LevelIntermediate,
	Glossary: []string{"multicollinearity", "correlation", "regression-coefficient", "listwise-deletion", "pairwise-deletion"},
}

var purposeMatPCA = descriptor.Purpose{
	Plain:   "How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many.",
	KnownAs: []string{"principal components", "dimension reduction", "kmo", "bartlett's test"},
	Intents: []string{IntentMeasureConstruct, IntentDescribe},
	Questions: []string{
		"How many distinct things do these twelve rating questions actually measure?",
		"Is this set of measures correlated enough to be worth summarising into components?",
	},
	UseCases: map[descriptor.Domain]string{
		descriptor.DomainSurvey:  "Checking how many themes a long attitude battery covers, and which questions load on each, before building scale scores.",
		descriptor.DomainScience: "Reducing many correlated measurements of the same specimens to a few summary dimensions.",
	},
	NotFor: []descriptor.Alternative{
		{When: "you want to check that one set of items measures a single quality reliably", Use: "MAT_RELIABILITY"},
		{When: "you only want to see which measures go together, pair by pair", Use: "MAT_CORRELATION"},
	},
	Assumptions: []string{
		"The measures are numeric and related by straight-line links; components summarise shared spread, they are not proven causes.",
		"On a covariance the measures with the largest spread dominate; use the correlation unless the units are shared and comparable.",
		"KMO and Bartlett's test say whether the correlations are strong enough to summarise; read them before the components.",
		"A row with any measure missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.",
	},
	Level:    descriptor.LevelAdvanced,
	Glossary: []string{"principal-component", "eigenvalue", "loading", "correlation", "listwise-deletion", "pairwise-deletion"},
}

var purposeMatReliability = descriptor.Purpose{
	Plain:   "How consistently a set of rating items measures one thing, before you add them up into a score, with a check of how each item fits the rest.",
	KnownAs: []string{"cronbach's alpha", "scale reliability", "internal consistency", "mcdonald's omega"},
	Intents: []string{IntentMeasureConstruct},
	Questions: []string{
		"Are these five satisfaction questions reliable enough to average into one score?",
		"Which item in this battery fits the others worst and should be dropped?",
	},
	UseCases: map[descriptor.Domain]string{
		descriptor.DomainSurvey:  "Checking a battery of agree-disagree items, some worded in reverse, before summing it into a scale score.",
		descriptor.DomainScience: "Checking that several measured indicators of one trait agree well enough to be combined.",
	},
	NotFor: []descriptor.Alternative{
		{When: "you only want to see which items go together, pair by pair", Use: "MAT_CORRELATION"},
		{When: "you want a p-value for one pair of items", Use: "TEST_PEARSON_R"},
	},
	Assumptions: []string{
		"The items are meant to measure one shared quality; the figures do not check that there is only one.",
		"Items worded in reverse must be named in params.reverse with the scale's range, or they pull the figures down.",
		"Alpha treats every item as equally tied to the shared quality; omega lets each item's tie differ and needs at least 3 items.",
		"A row with any item missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.",
	},
	Level:    descriptor.LevelIntermediate,
	Glossary: []string{"reliability", "correlation", "listwise-deletion", "pairwise-deletion"},
}

var purposeMatPartialCorrelation = descriptor.Purpose{
	Plain:   "How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1.",
	KnownAs: []string{"partial r", "controlling for", "partial correlation matrix", "partial correlations"},
	Intents: []string{IntentRelationship, IntentDrivers},
	Questions: []string{
		"Does satisfaction still track price once delivery time is held fixed?",
		"Which of these ratings are linked directly, rather than only through the overall score?",
	},
	UseCases: map[descriptor.Domain]string{
		descriptor.DomainSurvey:  "Driver analysis: which rating items stay linked to the overall score once the other items are held fixed.",
		descriptor.DomainOps:     "Whether two store metrics still move together once foot traffic, which drives both, is held fixed.",
		descriptor.DomainScience: "Separating direct links among measured responses from links that run through a shared third measure.",
	},
	NotFor: []descriptor.Alternative{
		{When: "you want each pair's link with nothing held fixed", Use: "MAT_CORRELATION"},
		{When: "you want how much each field moves the outcome, in its own units", Use: "REG_OLS"},
		{When: "you need a p-value for one pair", Use: "TEST_PEARSON_R"},
	},
	Assumptions: []string{
		"Each pair is held fixed for the other members by default, or for exactly the fields named in params.control.",
		"Only straight-line links are removed and measured; a curved link to a held-fixed field leaves a trace.",
		"A row with any member or control missing is dropped from every cell (listwise deletion) unless pairwise deletion is chosen, which can leave the input inconsistent; that input is refused unless it is repaired.",
		"A field that is an exact straight-line mix of the others leaves nothing to compare, so the matrix is refused.",
	},
	Level:    descriptor.LevelAdvanced,
	Glossary: []string{"partial-correlation", "correlation", "listwise-deletion", "pairwise-deletion", "multicollinearity"},
}

var purposeMatCorrelation = descriptor.Purpose{
	Plain:   "How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1.",
	KnownAs: []string{"correlation matrix", "cor", "rank correlation", "spearman correlation matrix", "kendall correlation matrix"},
	Intents: []string{IntentRelationship, IntentDescribe},
	Questions: []string{
		"Which of these ten rating items go together most strongly?",
		"How do the sensor readings line up with one another, pair by pair?",
	},
	UseCases: map[descriptor.Domain]string{
		descriptor.DomainSurvey:  "Correlation table of a battery of rating items, to see which items hang together before building a scale.",
		descriptor.DomainOps:     "Which weekly metrics move together across stores, in one table instead of one request per pair.",
		descriptor.DomainScience: "Pairwise straight-line links among several measured responses across units.",
	},
	NotFor: []descriptor.Alternative{
		{When: "you need a p-value or confidence interval for one pair", Use: "TEST_PEARSON_R"},
		{When: "you want the joint spread in the fields' own units", Use: "MAT_COVARIANCE"},
		{When: "you need a p-value for one pair's rank correlation", Use: "TEST_SPEARMAN_R"},
		{When: "you need a p-value for one pair's Kendall tau", Use: "TEST_KENDALL_TAU"},
		{When: "you want each pair's link with other fields held fixed", Use: "MAT_PARTIAL_CORRELATION"},
	},
	Assumptions: []string{
		"A row with any member missing is dropped from every cell (listwise deletion).",
		"Each r reads only a straight-line link; a curved relationship can still be strong.",
		"A few extreme rows can move an r a lot.",
		"r is unit-free: each pair's co-moment divided by both members' spreads, the same arithmetic as TEST_PEARSON_R.",
		"The spearman and kendall methods rank each member first, so they read any steady rise or fall, curved or not, and resist extreme rows; each cell is the TEST_SPEARMAN_R or TEST_KENDALL_TAU figure for its pair.",
	},
	Level:    descriptor.LevelIntermediate,
	Glossary: []string{"correlation", "covariance", "listwise-deletion", "outlier", "rank", "spearman-rho", "kendall-tau"},
}

var purposeMatCovariance = descriptor.Purpose{
	Plain:   "How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal.",
	KnownAs: []string{"covariance matrix", "cov"},
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
