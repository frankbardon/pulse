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

	// ClassRefuse: the operator has no weighted form; any weight in
	// force on its slot — the instance default included — is
	// PULSE_WEIGHT_UNSUPPORTED. The slot's `weight: null` opts out. An
	// operator with a RefusalReason is refused permanently and the
	// refusal states why; one without is an inferential operator whose
	// weighted computation has not landed yet.
	ClassRefuse

	// ClassFrequencyOnly: the operator computes a weighted figure under
	// kind frequency only (exact on the expanded rows) and has no
	// standard probability-weighted form. A frequency weight is applied;
	// a probability weight in force on its slot — the instance default
	// included — is PULSE_WEIGHT_UNSUPPORTED naming the kind. The slot's
	// `weight: null` opts out.
	ClassFrequencyOnly
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

// attributeClasses classes the attributes the table governs: the
// reference-distribution attributes place each row's value against a
// whole-cohort mean / spread / rank (refused until weighted, or
// permanently — RefusalReason); a min-max rescale has no weighted
// meaning. Every other attribute is row-local (ClassNone).
var attributeClasses = map[types.AttributeType]Class{
	types.ATTR_ZSCORE:     ClassRefuse,
	types.ATTR_TSCORE:     ClassRefuse,
	types.ATTR_PERCENTILE: ClassRefuse,
	types.ATTR_NORMALIZED: ClassNotWeightable,
}

// testClasses are the built-in tests whose weighted computation exists
// (every other TEST_* refuses). The moment tests read the shared
// weighted Welford bucket (Welford, inference.go) on w* under both
// kinds; TEST_PROP_Z reads Σw_success/Σw_g with N*_g and TEST_CHISQ
// the Σw table (scaled to n_eff under probability — a first-order Kish
// approximation, not Rao-Scott). The rank tests are frequency-only:
// weighted mid-ranks (a row of weight w ranks as w identical rows), the
// tie corrections on the expanded tie sizes and Kendall's pair weights
// w_i·w_j equal the unweighted test on the expanded rows; no standard
// probability-weighted rank test exists. Fisher's exact test (the
// exact test on the Σw table), KS (weighted ECDFs, asymptotic p on the
// expanded sizes) and Brown-Forsythe (ANOVA on |x − the frequency
// weighted median|) are frequency-only for the same reason: each is the
// unweighted test on the expanded rows. A row test reads its slot's
// stamped weight.
var testClasses = map[types.TestType]Class{
	types.TEST_T:            ClassAware,
	types.TEST_WELCH:        ClassAware,
	types.TEST_PAIRED_T:     ClassAware,
	types.TEST_Z_TWO_SAMPLE: ClassAware,
	types.TEST_ANOVA_F:      ClassAware,
	types.TEST_ANOVA_WELCH:  ClassAware,
	types.TEST_PEARSON_R:    ClassAware,
	types.TEST_PROP_Z:       ClassAware,
	types.TEST_CHISQ:        ClassAware,

	types.TEST_MANN_WHITNEY_U: ClassFrequencyOnly,
	types.TEST_WILCOXON_SR:    ClassFrequencyOnly,
	types.TEST_KRUSKAL_WALLIS: ClassFrequencyOnly,
	types.TEST_SPEARMAN_R:     ClassFrequencyOnly,
	types.TEST_KENDALL_TAU:    ClassFrequencyOnly,
	types.TEST_FISHER_EXACT:   ClassFrequencyOnly,
	types.TEST_KS:             ClassFrequencyOnly,
	types.TEST_BROWN_FORSYTHE: ClassFrequencyOnly,
}

// regressionClasses are the built-in regressions whose weighted fit
// exists (every other REG_* refuses). REG_OLS — plain and penalised
// (ridge / lasso / elastic net) — fits on the Σw-weighted streaming
// moments: β is WLS (the penalty scaled by Σw, so β is kind-free and
// invariant to rescaling the weights) and every inferential figure is
// the frequency formula on w* (df = N* − p − 1). REG_BAYES_LINEAR is
// frequency-only: the conjugate posterior with X'WX, X'Wy, y'Wy and Σw
// equals the posterior on the expanded rows, while under probability
// weights it is a pseudo-posterior with no reference form. A regression
// carrying a resample or selection modifier is refused at the slot,
// whatever its class (the resolver's weightSlot.reason).
var regressionClasses = map[types.RegressionType]Class{
	types.REG_OLS:          ClassAware,
	types.REG_BAYES_LINEAR: ClassFrequencyOnly,
}

// refusalReasons are the PERMANENT refusals: operators with no standard
// weighted form any reference software reproduces. The reason rides
// the PULSE_WEIGHT_UNSUPPORTED refusal (message and details.reason).
// Every key must be ClassRefuse (TestRefusalReasons_AreRefused).
var refusalReasons = map[string]string{
	string(types.TEST_SHAPIRO_WILK): "the Shapiro-Francia W' statistic and its p-value calibration are defined only for unweighted samples; no standard weighted form exists",
	string(types.TEST_TUKEY_HSD):    "it reads the aggregated per-group result rows, which carry no row weights, and weighted Tukey-Kramer has no reference form",
	string(types.TEST_ANOVA_RM):     "a per-row weight has no defined meaning in the subject-by-condition table repeated-measures ANOVA reads",
	string(types.TEST_TREND):        "it runs Mann-Kendall over the aggregated result rows, which carry no row weights",
	string(types.ATTR_PERCENTILE):   "a weighted percentile rank shares tied rows inclusively, which cannot reproduce the unweighted attribute's distinct ranks at unit weights",
}

// operatorClasses is the whole built-in table: the aggregators above
// plus the non-aggregator families (.claude/reference/weighting.md,
// Refusal rules) — every TEST_* and REG_*, the reference-distribution
// attributes and GROUP_QUANTILE refuse (each flips to ClassAware or
// ClassFrequencyOnly only once its weighted computation exists, never
// before: that would run it unweighted silently); ATTR_NORMALIZED and
// every WIN_* are not weightable.
var operatorClasses = func() map[string]Class {
	m := make(map[string]Class, len(aggregatorClasses))
	for op, c := range aggregatorClasses {
		m[string(op)] = c
	}
	for _, t := range types.AllTestTypes() {
		m[string(t)] = ClassRefuse
	}
	for t, c := range testClasses {
		m[string(t)] = c
	}
	for _, r := range types.AllRegressionTypes() {
		m[string(r)] = ClassRefuse
	}
	for r, c := range regressionClasses {
		m[string(r)] = c
	}
	for a, c := range attributeClasses {
		m[string(a)] = c
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
// weighted figure under at least one weight kind (ClassAware or
// ClassFrequencyOnly); KindsOf says which.
func IsAware(op string) bool { return len(KindsOf(op)) > 0 }

// KindsOf returns the weight kinds under which the named built-in
// operator computes a weighted figure, in manifest order: frequency
// then probability for ClassAware, frequency alone for
// ClassFrequencyOnly, nil for every other class. A fresh slice per
// call.
func KindsOf(op string) []types.WeightKind {
	return KindsOfClass(ClassOf(op))
}

// KindsOfClass is KindsOf over a class already looked up (the overlay
// classes live in internal/descriptor).
func KindsOfClass(c Class) []types.WeightKind {
	switch c {
	case ClassAware:
		return []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
	case ClassFrequencyOnly:
		return []types.WeightKind{types.WeightKindFrequency}
	}
	return nil
}

// RefusalReason returns why the named built-in operator is refused
// permanently under a weight, "" when it is not (a pending ClassRefuse
// operator, or any other class).
func RefusalReason(op string) string { return refusalReasons[op] }

// OverrideClassForTest sets op's class until the returned restore is
// called. Test seam only — a stand-in for an operator whose class a
// later change flips — and not safe alongside parallel tests.
func OverrideClassForTest(op string, c Class) (restore func()) {
	prev, had := operatorClasses[op]
	operatorClasses[op] = c
	return func() {
		if had {
			operatorClasses[op] = prev
		} else {
			delete(operatorClasses, op)
		}
	}
}

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
