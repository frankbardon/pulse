// Package opconfig holds the operator config rules the runtime
// constructors (internal/processing) and predict (internal/descriptor)
// both apply, so the two refuse the same request with the same code,
// message and details by construction (#781). Each rule is a fault of
// the slot's own configuration — its params or its values list — that
// needs no record to judge:
//
//   - AGG_DISTINCT_SUM: params.distinct_by is required (DistinctSumKey).
//   - AGG_CI_LOWER / AGG_CI_UPPER: params.confidence in (0, 1) and
//     params.method "normal" ("bootstrap" is reserved) (CIParams).
//   - ATTR_SET_HAS: a non-empty params.label (SetHasLabel) that the set
//     field's dictionary holds, within the rung's capacity (SetHasBit).
//   - FILTER_SET_CONTAINS_ANY / _ALL / _NONE, FILTER_SET_EQUALS: every
//     values label in the set field's dictionary, within the rung's
//     capacity (SetQueryMask).
//   - FILTER_NULL: exactly one values entry, "is_null" or "is_not_null"
//     (NullMode).
//   - FILTER_TRUE / FILTER_FALSE: at most one values entry, "truthy" or
//     "strict" (BoolMode).
//   - FILTER_RANGE: exactly two numeric values (RangeBounds).
//
// A rule that reads the schema takes the resolved field: the caller has
// already applied the field-reference and accepted-types rules (a slot's
// field refusal precedes its config refusal on both sides).
//
// It is a leaf: it imports only the public encoding / errors / types
// packages and internal/paramread, never internal/processing,
// internal/service or a descriptor package (TestOpConfig_ImportBoundary),
// so internal/descriptor may import it without breaking
// TestPredictNoExecutionImports.
package opconfig

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// DistinctSumParams is the AGG_DISTINCT_SUM params decode.
type DistinctSumParams struct {
	// DistinctBy names the key field; required.
	DistinctBy string `json:"distinct_by"`
}

// DistinctSumKey decodes AGG_DISTINCT_SUM's params and returns its
// distinct_by key field, or the PROCESSING_CONFIG refusal: malformed
// params, or distinct_by absent or empty. Refused, never defaulted:
// falling back to the slot's own field would turn the operator into a
// distinct-VALUE sum, a plausible number for a question nobody asked.
func DistinctSumKey(raw json.RawMessage) (string, *errors.CodedError) {
	var params DistinctSumParams
	name := string(types.AGG_DISTINCT_SUM)
	paramread.Declare(paramread.Aggregator, name, &params)
	if len(raw) > 0 {
		if err := paramread.Decode(paramread.Aggregator, name, raw, &params); err != nil {
			return "", errors.NewCodedError(errors.PROCESSING_CONFIG,
				"invalid distinct_sum params: "+err.Error())
		}
	}
	if params.DistinctBy == "" {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_DISTINCT_SUM requires params.distinct_by")
	}
	return params.DistinctBy, nil
}

// CIMethodNormal is the one AGG_CI_* method that runs: the normal
// critical value over the Welford stderr.
const CIMethodNormal = "normal"

// CIMethodBootstrap is reserved: it is refused until a buffered
// bootstrap arm lands.
const CIMethodBootstrap = "bootstrap"

// CI is the AGG_CI_LOWER / AGG_CI_UPPER params decode, defaults applied.
type CI struct {
	// Confidence is the two-sided level in (0, 1); absent or 0 = 0.95.
	Confidence float64 `json:"confidence"`
	// Method is the interval method; absent or "" = "normal".
	Method string `json:"method"`
}

// CIParams decodes op's (AGG_CI_LOWER / AGG_CI_UPPER) params with the
// defaults applied, or returns the PROCESSING_CONFIG refusal: malformed
// params, a confidence outside (0, 1), the reserved "bootstrap" method
// or an unknown method.
func CIParams(op types.AggregationType, raw json.RawMessage) (CI, *errors.CodedError) {
	params := CI{Confidence: 0.95, Method: CIMethodNormal}
	paramread.Declare(paramread.Aggregator, string(op), &params)
	if len(raw) > 0 {
		if err := paramread.Decode(paramread.Aggregator, string(op), raw, &params); err != nil {
			return CI{}, errors.NewCodedError(errors.PROCESSING_CONFIG, "invalid ci params: "+err.Error())
		}
		if params.Confidence == 0 {
			params.Confidence = 0.95
		}
		if params.Method == "" {
			params.Method = CIMethodNormal
		}
	}
	if !(params.Confidence > 0 && params.Confidence < 1) {
		return CI{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_CI_* confidence must lie in the open interval (0, 1)")
	}
	switch params.Method {
	case CIMethodNormal:
		return params, nil
	case CIMethodBootstrap:
		return CI{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_CI_* method=\"bootstrap\" is reserved for a buffered follow-up; use method=\"normal\" today")
	}
	return CI{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
		"AGG_CI_* method must be \"normal\" or \"bootstrap\"")
}

// SetHasParams is the ATTR_SET_HAS params decode.
type SetHasParams struct {
	// Label is the dictionary label whose bit the attribute tests.
	Label string `json:"label"`
}

// SetHasLabel decodes ATTR_SET_HAS's params and returns its label, or
// the PROCESSING_CONFIG refusal: no params, malformed params or an
// empty label.
func SetHasLabel(raw json.RawMessage) (string, *errors.CodedError) {
	if len(raw) == 0 {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS requires params with a \"label\" field")
	}
	var params SetHasParams
	if err := paramread.Decode(paramread.Attribute, string(types.ATTR_SET_HAS), raw, &params); err != nil {
		return "", errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing ATTR_SET_HAS params")
	}
	if params.Label == "" {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS requires non-empty params.label")
	}
	return params.Label, nil
}

// SetHasBit resolves ATTR_SET_HAS's label against the set field f
// (named field) and returns its bit, or the PROCESSING_CONFIG refusal:
// no dictionary, a label the dictionary does not hold, or a bit beyond
// the declared rung's capacity (no row can ever carry it). f is a set
// field the accepted-types rule admitted.
func SetHasBit(field string, f *encoding.Field, label string) (int, *errors.CodedError) {
	if f.Dictionary == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: field "+field+" has no dictionary")
	}
	id, ok := f.Dictionary.IDFor(label)
	if !ok {
		return 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: label "+label+" not in dictionary for field "+field)
	}
	// Guard against the DECLARED RUNG's capacity, not against 64: a
	// set_u128 / set_u256 column legitimately carries members above bit
	// 63, while a bit beyond the rung can never be set on any row.
	if capacity := int(f.Type.MaxSetEntries()); int(id) >= capacity {
		return 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: label "+strconv.Quote(label)+" maps to bit "+strconv.Itoa(int(id))+
				", beyond the "+strconv.Itoa(capacity)+"-member capacity of "+f.Type.String()+" field "+strconv.Quote(field),
			map[string]any{
				"field":    field,
				"type":     f.Type.String(),
				"capacity": capacity,
				"bit":      int(id),
				"label":    label,
			})
	}
	return int(id), nil
}

// SetQueryMask resolves a set filterer's (op) values labels against the
// set field f (named field) into one query mask, or returns the
// PROCESSING_CONFIG refusal: no dictionary, a label the dictionary does
// not hold, or a label whose bit lies beyond the declared rung's
// capacity (the filter is unsatisfiable, and saying so beats answering
// "no rows matched"). Empty labels is the empty mask. f is a set field
// the accepted-types rule admitted.
func SetQueryMask(op types.FiltererType, field string, f *encoding.Field, labels []string) (encoding.SetMask, *errors.CodedError) {
	var zero encoding.SetMask
	if f.Dictionary == nil {
		return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(op)+": field "+field+" has no dictionary")
	}
	capacity := int(f.Type.MaxSetEntries())
	var mask encoding.SetMask
	for _, label := range labels {
		id, ok := f.Dictionary.IDFor(label)
		if !ok {
			return zero, errors.NewCodedError(errors.PROCESSING_CONFIG,
				string(op)+" value "+strconv.Quote(label)+" not found in dictionary for field "+strconv.Quote(field))
		}
		if int(id) >= capacity {
			return zero, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				string(op)+" value "+strconv.Quote(label)+" maps to bit "+strconv.Itoa(int(id))+
					", beyond the "+strconv.Itoa(capacity)+"-member capacity of "+f.Type.String()+" field "+strconv.Quote(field),
				map[string]any{
					"field":    field,
					"type":     f.Type.String(),
					"capacity": capacity,
					"bit":      int(id),
					"value":    label,
				})
		}
		mask = mask.WithBit(int(id))
	}
	return mask, nil
}

// FILTER_NULL modes.
const (
	NullModeIsNull    = "is_null"
	NullModeIsNotNull = "is_not_null"
)

// NullMode reads FILTER_NULL's values list: keepNull is true for
// ["is_null"] and false for ["is_not_null"]; any other list is the
// PROCESSING_CONFIG refusal.
func NullMode(values []string) (keepNull bool, cerr *errors.CodedError) {
	if len(values) != 1 {
		return false, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_NULL requires exactly one values entry: \"is_null\" or \"is_not_null\"")
	}
	switch values[0] {
	case NullModeIsNull:
		return true, nil
	case NullModeIsNotNull:
		return false, nil
	}
	return false, errors.NewCodedError(errors.PROCESSING_CONFIG,
		"FILTER_NULL values[0]="+strconv.Quote(values[0])+"; must be \"is_null\" or \"is_not_null\"")
}

// FILTER_TRUE / FILTER_FALSE modes.
const (
	BoolModeTruthy = "truthy"
	BoolModeStrict = "strict"
)

// BoolMode reads a FILTER_TRUE / FILTER_FALSE (op) values list: truthy
// is true for ["truthy"] and false for no values or ["strict"]; any
// other list is the PROCESSING_CONFIG refusal, raised before the field's
// type is judged (the type rule depends on the mode).
func BoolMode(op types.FiltererType, values []string) (truthy bool, cerr *errors.CodedError) {
	switch len(values) {
	case 0:
		return false, nil
	case 1:
		switch values[0] {
		case BoolModeTruthy:
			return true, nil
		case BoolModeStrict:
			return false, nil
		}
		return false, errors.NewCodedError(errors.PROCESSING_CONFIG,
			string(op)+" values[0]="+strconv.Quote(values[0])+"; must be \"truthy\" or \"strict\" (or omit values for strict)")
	}
	return false, errors.NewCodedError(errors.PROCESSING_CONFIG,
		string(op)+" accepts at most one values entry (\"truthy\" or \"strict\"); got "+strconv.Itoa(len(values)))
}

// RangeBounds reads FILTER_RANGE's values list [min, max] as numbers,
// or returns the PROCESSING_CONFIG refusal: a count other than 2, or a
// bound that does not parse as a number.
func RangeBounds(values []string) (lo, hi float64, cerr *errors.CodedError) {
	if len(values) != 2 {
		return 0, 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"range filter requires exactly 2 values (min, max)")
	}
	lo, err := strconv.ParseFloat(values[0], 64)
	if err != nil {
		return 0, 0, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			"parsing range min value "+strconv.Quote(values[0]))
	}
	hi, err = strconv.ParseFloat(values[1], 64)
	if err != nil {
		return 0, 0, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			"parsing range max value "+strconv.Quote(values[1]))
	}
	return lo, hi, nil
}
