package processing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
// per-row string resolution. A categorical field resolves each code as
// a dictionary LABEL; a code absent from the dictionary is dropped
// silently and matches nothing (AGG_FREQUENCY's precedent — the predict
// side flags it). An unsigned-integer field resolves each code as a
// number and refuses one that no row could ever hold (not an integer,
// negative, or beyond the field's width), because that is a caller bug.
//
// The attribute reads only Attribute.Field, so the projection is exact
// and the fused crosstab gate admits it with no special case.

// codeInParams is the ATTR_CODE_IN params blob. Each code is a JSON
// string or a JSON integer; RawMessage keeps both spellings until the
// field type says how to resolve them.
type codeInParams struct {
	Codes []json.RawMessage `json:"codes"`
}

// codeInAttribute holds the resolved key set: the float64 keys
// Record.NumericValue yields for a row holding one of the codes. An
// empty set (registry probe, or every categorical code absent) matches
// nothing.
type codeInAttribute struct {
	keys map[float64]struct{}
}

// codeInIntegerMax is the largest value each accepted unsigned-integer
// field type can hold. A code above it can never match.
var codeInIntegerMax = map[encoding.FieldType]uint64{
	encoding.FieldTypeU4:  15,
	encoding.FieldTypeU8:  math.MaxUint8,
	encoding.FieldTypeU16: math.MaxUint16,
	encoding.FieldTypeU32: math.MaxUint32,
	encoding.FieldTypeU64: math.MaxUint64,
}

// codeInAccepts reports whether ATTR_CODE_IN accepts field type ft:
// categorical_u8/u16/u32 and u4/u8/u16/u32/u64 only. Floats (equality
// is a trap), dates (FILTER_DATE_RANGES' job), packed_bool (already
// 0/1), decimal128 and set_* (ATTR_SET_HAS' job) are refused.
func codeInAccepts(ft encoding.FieldType) bool {
	if ft.IsCategorical() {
		return true
	}
	_, ok := codeInIntegerMax[ft]
	return ok
}

func newCodeInAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN requires field")
	}
	// target / predictors belong to the ATTR_REG_* family and would be
	// silently ignored here — the usual slip is naming the OUTPUT with
	// target, which is label's job.
	if attr.Target != "" || len(attr.Predictors) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN does not take target or predictors (those are regression-attribute slots); name the output column with label",
			map[string]any{
				"attribute":  string(types.ATTR_CODE_IN),
				"field":      attr.Field,
				"target":     attr.Target,
				"predictors": attr.Predictors,
			})
	}
	codes, err := parseCodeInCodes(attr.Params)
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
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN: unknown field "+attr.Field)
	}
	if !codeInAccepts(f.Type) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("ATTR_CODE_IN: field %q is %s; it accepts only categorical_u8/u16/u32 and u4/u8/u16/u32/u64 fields",
				attr.Field, f.Type),
			map[string]any{
				"attribute": string(types.ATTR_CODE_IN),
				"field":     attr.Field,
				"type":      f.Type.String(),
			})
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
	maxV := codeInIntegerMax[f.Type]
	for _, c := range codes {
		n, perr := strconv.ParseUint(c, 10, 64)
		if perr != nil || n > maxV {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("ATTR_CODE_IN: code %q can never match %s field %q; codes on an integer field must be whole numbers in 0..%d",
					c, f.Type, attr.Field, maxV),
				map[string]any{
					"attribute": string(types.ATTR_CODE_IN),
					"field":     attr.Field,
					"type":      f.Type.String(),
					"code":      c,
					"max":       maxV,
				})
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
	return &codeInAttribute{keys: keys}, nil
}

// parseCodeInCodes decodes params.codes into the code strings, deduped
// in first-seen order. A JSON integer is carried as its literal text
// (so 3 and "3" are the same code); any other JSON kind — a fractional
// or exponent number, bool, null, object, array — is refused, as is a
// missing or empty list.
func parseCodeInCodes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN requires params with a non-empty \"codes\" list")
	}
	var p codeInParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing ATTR_CODE_IN params")
	}
	if len(p.Codes) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_CODE_IN requires params with a non-empty \"codes\" list")
	}
	seen := make(map[string]bool, len(p.Codes))
	out := make([]string, 0, len(p.Codes))
	for _, rc := range p.Codes {
		c, ok := codeInCodeText(rc)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"ATTR_CODE_IN: each params.codes entry must be a JSON string or a JSON integer, got "+string(bytes.TrimSpace(rc)),
				map[string]any{"attribute": string(types.ATTR_CODE_IN), "code": string(bytes.TrimSpace(rc))})
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// codeInCodeText returns a code's text: a JSON string's value, or a
// JSON integer's literal digits (optionally signed). ok is false for
// every other JSON kind.
func codeInCodeText(rc json.RawMessage) (string, bool) {
	t := bytes.TrimSpace(rc)
	if len(t) == 0 {
		return "", false
	}
	if t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return "", false
		}
		return s, true
	}
	digits := t
	if digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 {
		return "", false
	}
	for _, b := range digits {
		if b < '0' || b > '9' {
			return "", false
		}
	}
	return string(t), true
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
	v, ok := r.NumericValue(field)
	if !ok {
		return 0, nil
	}
	if _, hit := a.keys[v]; hit {
		return 1, nil
	}
	return 0, nil
}
