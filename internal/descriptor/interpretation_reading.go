package descriptor

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/types"
)

// ---- descriptive readings: the `value` path and the two lists --------
//
// A descriptive operator's primary result is not a struct with tagged
// fields: an aggregator writes one value per group, an attribute /
// window / feature one value (or a family of columns) per row. An
// Interpretation reads that primary result through a static path:
//
//   - `value`   — a single-column result (a number, a label, a list)
//   - `value.*` — a multi-column result, read once per operator; its
//     Means names the columns (multiColumnOutputs says which)
//
// Both are FieldStatic: the shape is decided here from each operator's
// real output, never probed at run time. Filterers, groupers, tests,
// regressions and overlays have no `value` path.
//
// Every descriptive built-in (aggregator, attribute, filterer, grouper,
// window, feature, synth distribution) sits in EXACTLY ONE of two lists,
// by this rule:
//
//	NEEDS READING — a non-statistician cannot read the number from the
//	operator's name: the result is standardised, scale-free,
//	model-based, transformed, or a rank / relative figure.
//	SELF-READING — the name says what the number is (a count, a sum, a
//	minimum, a date part, a row filter, a bucket, a generated draw).
//
// A needs-reading operator must carry an Interpretation on its `value`
// / `value.*` path (TestInterpretationCoversOutputs, against the
// exemption ledger). A newly registered descriptive operator is in
// neither list and fails TestDescriptiveReadingLists until it is
// classified here — extend this data, not the tests.

// valuePathCategories are the categories whose primary result an
// Interpretation can read through `value` / `value.*`.
var valuePathCategories = map[string]bool{
	"aggregator": true,
	"attribute":  true,
	"window":     true,
	"feature":    true,
}

// multiColumnOutputs are the value-path operators whose primary result
// is several named columns or keys (read as `value.*`), with where that
// shape comes from. Every other value-path operator is single-column
// (`value`).
var multiColumnOutputs = map[string]string{
	string(types.AGG_WELFORD):        "Rich() emits WelfordTriple {mean, variance, n} (internal/processing/aggregator_welford.go)",
	string(types.AGG_SET_FREQUENCY):  "Rich() emits a label -> row-count map (internal/processing/aggregator_set.go)",
	string(types.FEAT_POLY):          "one column <prefix>_<k> per power k = 2..degree (internal/processing/feature/poly.go)",
	string(types.FEAT_ONE_HOT):       "one column <prefix>_<category> per dictionary entry",
	string(types.FEAT_DATE_FEATURES): "five columns <prefix>_year / _month / _day / _dow / _quarter",
}

// needsReadingOperators — descriptive built-ins whose result needs an
// Interpretation (see the rule above).
var needsReadingOperators = []string{
	// aggregator
	string(types.AGG_CI_LOWER), string(types.AGG_CI_UPPER), string(types.AGG_KURTOSIS),
	string(types.AGG_PERCENTILE), string(types.AGG_RATIO), string(types.AGG_SKEWNESS),
	string(types.AGG_STDDEV), string(types.AGG_VARIANCE), string(types.AGG_WEIGHTED_MEAN),
	string(types.AGG_WELFORD), string(types.AGG_ZSCORE),
	// attribute
	string(types.ATTR_NORMALIZED), string(types.ATTR_PERCENTILE), string(types.ATTR_REG_FITTED),
	string(types.ATTR_REG_LEVERAGE), string(types.ATTR_REG_RESIDUAL), string(types.ATTR_TSCORE),
	string(types.ATTR_ZSCORE),
	// window
	string(types.WIN_DELTA), string(types.WIN_DENSE_RANK), string(types.WIN_EWMA),
	string(types.WIN_MOVING_AVG), string(types.WIN_PCT_CHANGE), string(types.WIN_RANK),
	string(types.WIN_RUNNING_AVG),
	// feature
	string(types.FEAT_FREQUENCY_ENCODE), string(types.FEAT_LOG), string(types.FEAT_POLY),
	string(types.FEAT_SQRT), string(types.FEAT_TARGET_ENCODE),
}

// selfReadingOperators — descriptive built-ins whose name says what the
// result is. Filterers, groupers and synth distributions are listed by
// NAME, never category-wide, so a new one must be classified.
var selfReadingOperators = []string{
	// aggregator
	string(types.AGG_AVERAGE), string(types.AGG_COUNT), string(types.AGG_DISTINCT_COUNT),
	string(types.AGG_DISTINCT_SUM), string(types.AGG_FREQUENCY), string(types.AGG_MAX),
	string(types.AGG_MEDIAN), string(types.AGG_MIN), string(types.AGG_MODE),
	string(types.AGG_NULL_COUNT), string(types.AGG_RANGE), string(types.AGG_SET_CARDINALITY_AVG),
	string(types.AGG_SET_CARDINALITY_SUM), string(types.AGG_SET_DISTINCT_VALUES),
	string(types.AGG_SET_FREQUENCY), string(types.AGG_SET_INTERSECTION),
	string(types.AGG_SET_UNION), string(types.AGG_SUM),
	// attribute
	string(types.ATTR_DATE_PART), string(types.ATTR_FORMULA), string(types.ATTR_SET_HAS),
	string(types.ATTR_SET_POPCOUNT),
	// filterer
	string(types.FILTER_DATE_RANGES), string(types.FILTER_EXCLUDE), string(types.FILTER_EXPRESSION),
	string(types.FILTER_FALSE), string(types.FILTER_INCLUDE), string(types.FILTER_NULL),
	string(types.FILTER_RANGE), string(types.FILTER_SET_CONTAINS_ALL),
	string(types.FILTER_SET_CONTAINS_ANY), string(types.FILTER_SET_CONTAINS_NONE),
	string(types.FILTER_SET_EQUALS), string(types.FILTER_TRUE),
	// grouper
	string(types.GROUP_CATEGORY), string(types.GROUP_DATE), string(types.GROUP_DATE_RANGES),
	string(types.GROUP_QUANTILE), string(types.GROUP_RANGE), string(types.GROUP_ROUNDED),
	string(types.GROUP_SET_PER_ELEMENT), string(types.GROUP_SET_VALUE),
	// window
	string(types.WIN_LAG), string(types.WIN_LEAD), string(types.WIN_ROW_NUMBER),
	string(types.WIN_RUNNING_SUM),
	// feature
	string(types.FEAT_BUCKETIZE), string(types.FEAT_DATE_FEATURES), string(types.FEAT_ONE_HOT),
	string(types.FEAT_TRAIN_TEST_SPLIT),
	// synth_distribution
	"bernoulli", "constant", "discrete", "exponential", "lognormal", "mixture",
	"monotonic_from", "normal", "pareto", "poisson", "regex", "set_bernoulli", "uniform",
	"uniform_date", "weighted_categorical",
}

// valueField returns the path an operator's primary result is read
// through: `value.*` for a multi-column result, `value` otherwise.
func valueField(name string) string {
	if _, ok := multiColumnOutputs[name]; ok {
		return "value.*"
	}
	return "value"
}

// valueOutputCheck judges field against name's value path. handled is
// false when field is not a value path at all (the caller resolves it
// some other way).
func valueOutputCheck(name, field string) (check FieldCheck, why string, handled bool) {
	if field != "value" && field != "value.*" {
		if strings.HasPrefix(field, "value.") {
			return FieldUnknown, fmt.Sprintf("%s: a value path is %q, read once per operator — no per-column key", name, valueField(name)), true
		}
		return FieldUnknown, "", false
	}
	if want := valueField(name); field != want {
		shape := "single-column"
		if want == "value.*" {
			shape = "multi-column"
		}
		return FieldUnknown, fmt.Sprintf("%s has a %s result; read it as %q", name, shape, want), true
	}
	return FieldStatic, "", true
}

// valueOutputResolver resolves attribute / window / feature operators,
// whose only readable output is the value path.
func valueOutputResolver(name, cat string) OutputResolver {
	return func(field string) (FieldCheck, string) {
		if check, why, ok := valueOutputCheck(name, field); ok {
			return check, why
		}
		return FieldUnknown, fmt.Sprintf("%s operators emit only %q for an Interpretation to read", cat, valueField(name))
	}
}
