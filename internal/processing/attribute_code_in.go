package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/codein"
	"github.com/frankbardon/pulse/types"
)

// ATTR_CODE_IN — a row-local 0/1 indicator: 1 when the row's value of
// Field is one of params.codes, 0 otherwise (a null input reads 0, so
// every row stays in the base). It is the "share of the WHOLE base"
// counterpart of FILTER_INCLUDE, which drops the non-matching rows and
// with them the denominator.
//
// Codes are resolved ONCE at construction through the matchValueKey
// rule shared with FILTER_INCLUDE / FILTER_EXCLUDE / AGG_FREQUENCY, so
// the per-row work is one NumericValue read plus one map lookup — no
// per-row string resolution (a u64 field reads the exact word through
// Uint64Value instead, keyed by uint64). A categorical field resolves
// each code as a dictionary LABEL; a code absent from the dictionary is
// dropped
// silently and matches nothing (AGG_FREQUENCY's precedent — the predict
// side flags it). An unsigned-integer field resolves each code as a
// number and refuses one that no row could ever hold (not an integer,
// negative, or beyond the field's width), because that is a caller bug.
//
// The attribute reads only Attribute.Field, so the projection is exact
// and the fused crosstab gate admits it with no special case.

// codeInAttribute holds the resolved key set: the float64 keys
// Record.NumericValue yields for a row holding one of the codes. An
// empty set (registry probe, or every categorical code absent) matches
// nothing.
//
// A u64 field keys EXACTLY instead (exact non-nil, keys unused): codes
// are the parsed uint64 values and a row reads Record.Uint64Value, so
// two distinct values above 2^53 that share a float64 echo never match
// each other's code (#427).
type codeInAttribute struct {
	keys  map[float64]struct{}
	exact map[uint64]struct{}
}

func newCodeInAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN requires field")
	}
	// target / predictors are refused before construction by the general
	// foreign-regression-slot rule (model.ForeignRegressionSlotRefusal,
	// in the field-reference walk every run takes).
	codes, err := codein.ParseCodes(attr.Params)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		// Registry probe: no dictionary or width to resolve against, so
		// the computer matches nothing rather than guessing.
		return &codeInAttribute{keys: map[float64]struct{}{}}, nil
	}
	f := schema.Field(attr.Field)
	if f == nil {
		return nil, codein.UnknownField(attr.Field)
	}
	// The accepted-types table's refusal (a categorical, or an unsigned
	// integer), then the integer range — the shared rule predict runs.
	ints, cerr := codein.Check(attr.Field, f.Type, codes)
	if cerr != nil {
		return nil, cerr
	}
	keys := make(map[float64]struct{}, len(codes))
	if f.Type.IsCategorical() {
		if f.Dictionary == nil {
			// No labels to resolve against: every code is absent. Never
			// let matchValueKey fall through to ParseFloat here — that
			// would read a code as a dictionary ID.
			return &codeInAttribute{keys: keys}, nil
		}
		for _, c := range codes {
			key, found, err := matchValueKey(f, c)
			if err != nil || !found {
				// Absent from the dictionary (or no dictionary at all):
				// matches nothing. Predict flags the absence.
				continue
			}
			keys[key] = struct{}{}
		}
		return &codeInAttribute{keys: keys}, nil
	}
	var exact map[uint64]struct{}
	if f.Type == encoding.FieldTypeU64 {
		exact = make(map[uint64]struct{}, len(codes))
	}
	for i, c := range codes {
		if exact != nil {
			exact[ints[i]] = struct{}{}
			continue
		}
		// The validated integer resolves through the shared rule, so the
		// key is exactly the float64 NumericValue yields for that value.
		key, _, err := matchValueKey(f, c)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
				fmt.Sprintf("ATTR_CODE_IN: parsing code %q", c))
		}
		keys[key] = struct{}{}
	}
	return &codeInAttribute{keys: keys, exact: exact}, nil
}

func (a *codeInAttribute) Compute(records []*Record, field string) ([]float64, error) {
	out := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (a *codeInAttribute) Row(r *Record, field string) (float64, error) {
	if a.exact != nil {
		u, ok := r.Uint64Value(field)
		if !ok {
			return 0, nil
		}
		if _, hit := a.exact[u]; hit {
			return 1, nil
		}
		return 0, nil
	}
	v, ok := r.NumericValue(field)
	if !ok {
		return 0, nil
	}
	if _, hit := a.keys[v]; hit {
		return 1, nil
	}
	return 0, nil
}
