// Package weighting is the one declaration of how built-in operators
// treat a resolved row weight, plus the per-row weight-validity rule.
// It is a leaf (stdlib + types only) so the no-execute layer
// (internal/descriptor: resolver, predict, manifest) and the engine
// (internal/processing, internal/service) read the SAME table and the
// SAME validity rule — predict cannot claim a weight the runtime does
// not apply, and no two execution modes can disagree on which rows a
// weight excludes. Contract: .claude/reference/weighting.md.
package weighting

import (
	"math"

	"github.com/frankbardon/pulse/types"
)

// Class is an operator's weight classification.
type Class int

const (
	// ClassNone: the table does not govern the operator — a row-local
	// attribute, a non-quantile grouper, a filterer, a feature, an
	// overlay (classed by its Inferential flag in internal/descriptor),
	// an extension operator or an unknown name. A weight does not
	// change it; the resolver reports a resolved weight on it as
	// skipped.
	ClassNone Class = iota

	// ClassAware: the operator computes a weighted figure.
	ClassAware

	// ClassNotWeightable: the operator has no weighted meaning. A
	// default (Options) weight is skipped on it; an explicit weight
	// (slot or request) is PROCESSING_CONFIG.
	ClassNotWeightable

	// ClassRefuse: the operator is inferential (U12's territory); any
	// weight in force on its slot — the instance default included — is
	// PULSE_WEIGHT_UNSUPPORTED. The slot's `weight: null` opts out.
	ClassRefuse
)

// aggregatorClasses classifies every built-in aggregator. TestClassify
// in this package holds it complete against types.AllAggregationTypes.
var aggregatorClasses = map[types.AggregationType]Class{
	types.AGG_COUNT:         ClassAware,
	types.AGG_SUM:           ClassAware,
	types.AGG_AVERAGE:       ClassAware,
	types.AGG_WEIGHTED_MEAN: ClassAware,
	types.AGG_VARIANCE:      ClassAware,
	types.AGG_STDDEV:        ClassAware,
	types.AGG_WELFORD:       ClassAware,
	types.AGG_SKEWNESS:      ClassAware,
	types.AGG_KURTOSIS:      ClassAware,
	types.AGG_MEDIAN:        ClassAware,
	types.AGG_PERCENTILE:    ClassAware,
	types.AGG_MODE:          ClassAware,
	types.AGG_MODE_COUNT:    ClassAware,

	types.AGG_FREQUENCY:           ClassAware,
	types.AGG_RATIO:               ClassAware,
	types.AGG_SET_FREQUENCY:       ClassAware,
	types.AGG_SET_CARDINALITY_SUM: ClassAware,
	types.AGG_SET_CARDINALITY_AVG: ClassAware,

	types.AGG_MIN:                 ClassNotWeightable,
	types.AGG_MAX:                 ClassNotWeightable,
	types.AGG_RANGE:               ClassNotWeightable,
	types.AGG_DISTINCT_COUNT:      ClassNotWeightable,
	types.AGG_DISTINCT_SUM:        ClassNotWeightable,
	types.AGG_NULL_COUNT:          ClassNotWeightable,
	types.AGG_SET_UNION:           ClassNotWeightable,
	types.AGG_SET_INTERSECTION:    ClassNotWeightable,
	types.AGG_SET_DISTINCT_VALUES: ClassNotWeightable,
	types.AGG_ZSCORE:              ClassNotWeightable,

	types.AGG_CI_LOWER: ClassRefuse,
	types.AGG_CI_UPPER: ClassRefuse,
}

// refusedAttributes are the reference-distribution attributes: each
// row's value is placed against a whole-cohort mean / spread / rank,
// whose weighted form is U12's.
var refusedAttributes = map[types.AttributeType]bool{
	types.ATTR_ZSCORE:     true,
	types.ATTR_TSCORE:     true,
	types.ATTR_PERCENTILE: true,
	types.ATTR_NORMALIZED: true,
}

// operatorClasses is the whole built-in table: the aggregators above
// plus the non-aggregator families (.claude/reference/weighting.md,
// Refusal rules) — every TEST_* and REG_*, the reference-distribution
// attributes and GROUP_QUANTILE refuse; every WIN_* is not weightable.
var operatorClasses = func() map[string]Class {
	m := make(map[string]Class, len(aggregatorClasses))
	for op, c := range aggregatorClasses {
		m[string(op)] = c
	}
	for _, t := range types.AllTestTypes() {
		m[string(t)] = ClassRefuse
	}
	for _, r := range types.AllRegressionTypes() {
		m[string(r)] = ClassRefuse
	}
	for a := range refusedAttributes {
		m[string(a)] = ClassRefuse
	}
	m[string(types.GROUP_QUANTILE)] = ClassRefuse
	for _, w := range types.AllWindowTypes() {
		m[string(w)] = ClassNotWeightable
	}
	return m
}()

// ClassOf returns the weight class of the named built-in operator;
// ClassNone for any name the table does not govern.
func ClassOf(op string) Class {
	return operatorClasses[op]
}

// IsAware reports whether the named built-in operator computes a
// weighted figure.
func IsAware(op string) bool { return ClassOf(op) == ClassAware }

// Reason classifies one row's weight value.
type Reason int

const (
	// Valid: a finite, non-negative weight (and an integer one under
	// kind frequency). Zero is valid and contributes nothing.
	Valid Reason = iota
	// Null: the weight column is null on the row.
	Null
	// Negative: a negative weight.
	Negative
	// NaNInf: NaN or ±Inf.
	NaNInf
	// NonIntegerFrequency: a fractional weight under kind frequency.
	NonIntegerFrequency
)

// InvalidReasons lists the invalid reasons in their reporting order;
// Key spells each as its `by_reason` key.
var InvalidReasons = []Reason{Null, Negative, NaNInf, NonIntegerFrequency}

// Key is the reason's `by_reason` key in the PULSE_WEIGHT_INVALID_ROWS
// warning details.
func (r Reason) Key() string {
	switch r {
	case Null:
		return "null"
	case Negative:
		return "negative"
	case NaNInf:
		return "nan_inf"
	case NonIntegerFrequency:
		return "non_integer_frequency"
	}
	return "valid"
}

// Classify judges one weight value: present=false is a null weight.
// Invalid weights are excluded and counted by the caller, never
// coerced.
func Classify(w float64, present bool, kind types.WeightKind) Reason {
	switch {
	case !present:
		return Null
	case math.IsNaN(w) || math.IsInf(w, 0):
		return NaNInf
	case w < 0:
		return Negative
	case kind == types.WeightKindFrequency && w != math.Trunc(w):
		return NonIntegerFrequency
	}
	return Valid
}
