// Package fieldaccept is the ONE accepted-field-types table: for each
// built-in operator slot that reads a field's value, the field types it
// accepts and the coded refusal it raises for any other type.
//
// Both sides read it. The runtime constructors in internal/processing
// (window application, FEAT_POLY, the regression build, the tier-1 test
// constructors) refuse with Refusal / Check, and predict
// (internal/descriptor) reports the very same *errors.CodedError at the
// same slot, so the two agree on code, message, details and count by
// construction. The manifest's accepts_types for a tabled slot is
// TypeNames (or, where noted, a subset of it), so the declaration
// cannot drift from the rule either.
//
// The PROCESSING_CONFIG rows are different in one way: they table the
// type checks the aggregator / attribute / grouper / filterer
// constructors already make, which keep their own message (a remedy
// naming only the operators the instance offers). Predict reports the
// same code once per slot from here; the root acceptance gate holds each
// row equal to its constructor over every field type.
//
// It is a leaf: it imports only the public errors / encoding packages,
// never internal/processing or internal/service, so internal/descriptor
// may import it without breaking TestPredictNoExecutionImports.
//
// Only built-in operators are tabled. An extension operator declares its
// accepted types through its registration's capability (accepts_types)
// and is never looked up here: Lookup misses for it, and every helper
// answers "no rule" (accept).
//
// A vector field (vec_f32 / vec_f64) is never refused here: every slot
// that reads one value refuses a vector with PULSE_VECTOR_FIELD_NOT_SCALAR
// through the field-reference walk, first, and a second type refusal
// would only repeat it. TypeNames never lists a vector type for a
// tabled slot.
package fieldaccept

import (
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Slot names the request position a field is judged in.
type Slot string

const (
	// SlotField is the operator's primary `field`.
	SlotField Slot = "field"
	// SlotField2 is a paired / bivariate test's `field2`.
	SlotField2 Slot = "field2"
	// SlotTarget is a regression's `target`.
	SlotTarget Slot = "target"
	// SlotPredictor is one entry of a regression's `predictors`.
	SlotPredictor Slot = "predictor"
)

// Rule is one tabled slot: the accepted types and the refusal code.
type Rule struct {
	// Accepts reports whether the slot accepts a field of type ft.
	Accepts func(ft encoding.FieldType) bool
	// Code is the refusal's code for any other (non-vector) type.
	Code errors.Code
}

// Accepted-type classes. Each is a predicate over the registered field
// types; TypeNames enumerates it for the manifest.

// windowValue is the value-window set: the unsigned integers, the
// floats and date. Excluded: categorical (a dictionary index is not a
// value), set_* (a bitmask), decimal128 (no decimal window path),
// packed_bool (a 0/1 series), datetime.
func windowValue(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate:
		return true
	}
	return false
}

// polyValue is FEAT_POLY's set: windowValue plus decimal128, which the
// expansion reads through its float64 echo.
func polyValue(ft encoding.FieldType) bool {
	return windowValue(ft) || ft == encoding.FieldTypeDecimal128
}

// analyticsValue is the regression set: encoding's analytics-numeric
// predicate (integers, floats, decimal128, date, datetime, u4,
// packed_bool).
func analyticsValue(ft encoding.FieldType) bool {
	return ft.IsNumericForAnalytics()
}

// testValue is the tier-1 numeric-test set: every scalar type but a
// categorical (a dictionary index) and a set_* (a bitmask).
func testValue(ft encoding.FieldType) bool {
	return ft.IsKnown() && !ft.IsCategorical() && !ft.IsSet() && !ft.IsVector()
}

// nonSetValue is the value-reading set of the operators that read a
// field's NUMBER or dictionary value (distinct counts, the standardising
// attributes, the value groupers, FILTER_INCLUDE / EXCLUDE): every
// scalar type but a set_* (a membership bitmask, whose numeric echo is
// its low 64 bits).
func nonSetValue(ft encoding.FieldType) bool {
	return ft.IsKnown() && !ft.IsSet() && !ft.IsVector()
}

// nonSetNonDecimal is nonSetValue without decimal128: the value-matching
// aggregators with no decimal implementation (AGG_FREQUENCY, AGG_MODE,
// AGG_MODE_COUNT).
func nonSetNonDecimal(ft encoding.FieldType) bool {
	return nonSetValue(ft) && !ft.IsDecimal()
}

// rangeBound is FILTER_RANGE's set: nonSetValue without a categorical,
// whose dictionary label cannot be parsed as a range bound.
func rangeBound(ft encoding.FieldType) bool {
	return nonSetValue(ft) && !ft.IsCategorical()
}

// strictScalar is AGG_WELFORD's set: the six plain integer and float
// types its single float64 hot path reads (no u4 / packed_bool bit
// packing, no temporal ordinal, no decimal128, categorical or set).
func strictScalar(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64:
		return true
	}
	return false
}

// setValue is the set-operator set: a set_* column's membership
// bitmask, which every AGG_SET_* / ATTR_SET_* / FILTER_SET_* /
// GROUP_SET_* operator reads. Every rung, narrow and wide alike:
// encoding.SetMask is width-agnostic.
func setValue(ft encoding.FieldType) bool {
	return ft.IsSet()
}

// temporalValue is the labeled date-range set: a date (epoch days) or
// a datetime (epoch seconds), the two columns the range bounds resolve
// against.
func temporalValue(ft encoding.FieldType) bool {
	return ft == encoding.FieldTypeDate || ft == encoding.FieldTypeDateTime
}

// packedBool is FILTER_TRUE / FILTER_FALSE's STRICT-mode set (no
// values, or ["strict"]): a packed_bool alone. Values ["truthy"] opts
// into coercing any scalar, and the type is then not judged — the
// caller (predict, like the runtime constructor) consults this row only
// in strict mode.
func packedBool(ft encoding.FieldType) bool {
	return ft == encoding.FieldTypePackedBool
}

// table is keyed by operator name, then slot.
var table = map[string]map[Slot]Rule{}

func add(class func(encoding.FieldType) bool, code errors.Code, slots []Slot, ops ...string) {
	for _, op := range ops {
		if table[op] == nil {
			table[op] = map[Slot]Rule{}
		}
		for _, s := range slots {
			table[op][s] = Rule{Accepts: class, Code: code}
		}
	}
}

func init() {
	field := []Slot{SlotField}
	add(windowValue, errors.PULSE_FIELD_TYPE_UNSUPPORTED, field,
		"WIN_LAG", "WIN_LEAD", "WIN_RUNNING_SUM", "WIN_RUNNING_AVG", "WIN_MOVING_AVG",
		"WIN_EWMA", "WIN_PCT_CHANGE", "WIN_DELTA")
	add(polyValue, errors.PULSE_FIELD_TYPE_UNSUPPORTED, field, "FEAT_POLY")
	add(analyticsValue, errors.PULSE_FIELD_TYPE_UNSUPPORTED, []Slot{SlotTarget, SlotPredictor},
		"REG_OLS", "REG_GLM", "REG_BAYES_LINEAR")
	add(testValue, errors.PULSE_TEST_FIELD_NOT_NUMERIC, field,
		"TEST_T", "TEST_WELCH", "TEST_ANOVA_F", "TEST_ANOVA_RM", "TEST_ANOVA_WELCH",
		"TEST_BROWN_FORSYTHE", "TEST_KS", "TEST_KRUSKAL_WALLIS", "TEST_MANN_WHITNEY_U",
		"TEST_SHAPIRO_WILK", "TEST_Z_TWO_SAMPLE",
		"TEST_PAIRED_T", "TEST_PEARSON_R", "TEST_SPEARMAN_R", "TEST_KENDALL_TAU", "TEST_WILCOXON_SR")
	add(testValue, errors.PULSE_TEST_FIELD2_NOT_NUMERIC, []Slot{SlotField2},
		"TEST_PAIRED_T", "TEST_PEARSON_R", "TEST_SPEARMAN_R", "TEST_KENDALL_TAU", "TEST_WILCOXON_SR")

	// The rows below are the runtime constructors' own type refusals,
	// which keep their PROCESSING_CONFIG code and their message (a remedy
	// naming the operators the instance offers); predict reports the
	// same code from here. TestFieldTypeAcceptance_PredictMatchesRuntime
	// holds every row equal to its constructor over every field type.
	cfg := errors.PROCESSING_CONFIG
	add(nonSetValue, cfg, field,
		"AGG_DISTINCT_COUNT", "ATTR_NORMALIZED", "ATTR_PERCENTILE", "ATTR_TSCORE", "ATTR_ZSCORE",
		"GROUP_CATEGORY", "GROUP_QUANTILE", "GROUP_RANGE", "GROUP_ROUNDED", "FILTER_INCLUDE", "FILTER_EXCLUDE")
	add(nonSetNonDecimal, cfg, field, "AGG_FREQUENCY", "AGG_MODE", "AGG_MODE_COUNT")
	add(strictScalar, cfg, field, "AGG_WELFORD")
	add(rangeBound, cfg, field, "FILTER_RANGE")
	add(analyticsValue, cfg, []Slot{SlotTarget, SlotPredictor},
		"ATTR_REG_FITTED", "ATTR_REG_RESIDUAL", "ATTR_REG_LEVERAGE")
	add(setValue, cfg, field,
		"AGG_SET_CARDINALITY_AVG", "AGG_SET_CARDINALITY_SUM", "AGG_SET_DISTINCT_VALUES", "AGG_SET_FREQUENCY",
		"AGG_SET_INTERSECTION", "AGG_SET_UNION", "ATTR_SET_HAS", "ATTR_SET_POPCOUNT",
		"FILTER_SET_CONTAINS_ALL", "FILTER_SET_CONTAINS_ANY", "FILTER_SET_CONTAINS_NONE", "FILTER_SET_EQUALS",
		"GROUP_SET_PER_ELEMENT", "GROUP_SET_VALUE")
	add(temporalValue, cfg, field, "GROUP_DATE_RANGES", "FILTER_DATE_RANGES")
	add(packedBool, cfg, field, "FILTER_TRUE", "FILTER_FALSE")

	memoTypeNames()
}

// Lookup returns the rule for op's slot; false when the slot is not
// tabled (an extension operator, or a slot whose types are judged
// elsewhere).
func Lookup(op string, slot Slot) (Rule, bool) {
	r, ok := table[op][slot]
	return r, ok
}

// Accepts reports whether op's slot accepts ft. An untabled slot and a
// vector type (the field-reference walk's refusal) answer true.
func Accepts(op string, slot Slot, ft encoding.FieldType) bool {
	r, ok := Lookup(op, slot)
	if !ok || ft.IsVector() {
		return true
	}
	return r.Accepts(ft)
}

// Refusal is the coded refusal for a field named field, of type ft, in
// op's slot — nil when the slot accepts it (Accepts). Message, code and
// details are the same on every side that raises it.
func Refusal(op string, slot Slot, field string, ft encoding.FieldType) *errors.CodedError {
	if Accepts(op, slot, ft) {
		return nil
	}
	r, _ := Lookup(op, slot)
	accepts := TypeNames(op, slot)
	return errors.NewCodedErrorWithDetails(r.Code,
		op+" "+string(slot)+" "+field+" has type "+ft.String()+"; accepted: "+strings.Join(accepts, ", "),
		map[string]any{
			"type":          op,
			"slot":          string(slot),
			"field":         field,
			"field_type":    ft.String(),
			"accepts_types": accepts,
		})
}

// Check is Refusal for a field named in a request: nil when the schema
// has no such field (an unknown name is the field-reference walk's
// refusal; a derived column carries no schema type) or the slot accepts
// it.
func Check(op string, slot Slot, field string, schema *encoding.Schema) *errors.CodedError {
	if schema == nil || field == "" {
		return nil
	}
	f := schema.Field(field)
	if f == nil {
		return nil
	}
	return Refusal(op, slot, field, f.Type)
}

// typeNames memoises typeNamesOf per tabled slot, filled once the table
// is built: the manifest's capability rows (rebuilt per predict call)
// read it on every call, and must not allocate for it.
var typeNames = map[string]map[Slot][]string{}

// memoTypeNames fills typeNames; the table's init calls it last.
func memoTypeNames() {
	for op, slots := range table {
		typeNames[op] = map[Slot][]string{}
		for slot, r := range slots {
			typeNames[op][slot] = typeNamesOf(r)
		}
	}
}

// TypeNames returns the names of every registered field type op's slot
// accepts, sorted byte-wise (the manifest's accepts_types order); nil
// for an untabled slot. Vector types are never listed. The slice is
// shared (capacity clipped, so an append copies): callers must not
// modify it in place.
func TypeNames(op string, slot Slot) []string {
	return typeNames[op][slot]
}

func typeNamesOf(r Rule) []string {
	var out []string
	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		if ft.IsKnown() && !ft.IsVector() && r.Accepts(ft) {
			out = append(out, ft.String())
		}
	}
	sort.Strings(out)
	return slices.Clip(out)
}

// Operators returns every tabled operator name, sorted.
func Operators() []string {
	out := make([]string, 0, len(table))
	for op := range table {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

// OperatorsRaising returns every tabled operator with a slot whose
// refusal carries code, sorted — the owner list of that code.
func OperatorsRaising(code errors.Code) []string {
	var out []string
	for _, op := range Operators() {
		for _, r := range table[op] {
			if r.Code == code {
				out = append(out, op)
				break
			}
		}
	}
	return out
}
