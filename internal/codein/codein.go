// Package codein holds ATTR_CODE_IN's slot rules — the params.codes
// parse, the accepted field types and the integer range — shared by the
// runtime constructor (internal/processing) and predict
// (internal/descriptor), so the two refuse the same request with the
// same code, message and details by construction.
//
// The target / predictors refusal is not here: it is the general
// foreign-regression-slot rule every non-regression attribute shares
// (internal/model.ForeignRegressionSlotRefusal, judged in the
// field-reference walk).
//
// It is a leaf: it imports only the public errors / encoding / types
// packages, internal/fieldaccept and internal/paramread, never
// internal/processing or internal/service, so internal/descriptor may
// import it without breaking TestPredictNoExecutionImports
// (TestCodeIn_ImportBoundary).
package codein

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Name is the operator's wire name.
const Name = string(types.ATTR_CODE_IN)

// Params is ATTR_CODE_IN's typed params decode. Each code is a JSON
// string or a JSON integer; RawMessage keeps both spellings until the
// field type says how to resolve them. Both sides decode into this one
// type, so a decode error quotes the same Go type on both.
type Params struct {
	Codes []json.RawMessage `json:"codes"`
}

// IntegerMax is the largest value each accepted unsigned-integer field
// type can hold. A code above it can never match.
var IntegerMax = map[encoding.FieldType]uint64{
	encoding.FieldTypeU4:  15,
	encoding.FieldTypeU8:  math.MaxUint8,
	encoding.FieldTypeU16: math.MaxUint16,
	encoding.FieldTypeU32: math.MaxUint32,
	encoding.FieldTypeU64: math.MaxUint64,
}

// ParseCodes decodes params.codes into the code strings, deduped in
// first-seen order. A JSON integer is carried as its literal text (so 3
// and "3" are the same code); any other JSON kind — a fractional or
// exponent number, bool, null, object, array — is refused, as is a
// missing or empty list.
func ParseCodes(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			Name+" requires params with a non-empty \"codes\" list")
	}
	var p Params
	if err := paramread.Decode(paramread.Attribute, Name, raw, &p); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing "+Name+" params")
	}
	if len(p.Codes) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			Name+" requires params with a non-empty \"codes\" list")
	}
	seen := make(map[string]bool, len(p.Codes))
	out := make([]string, 0, len(p.Codes))
	for _, rc := range p.Codes {
		c, ok := CodeText(rc)
		if !ok {
			lit := string(bytes.TrimSpace(rc))
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				Name+": each params.codes entry must be a JSON string or a JSON integer, got "+lit,
				map[string]any{"attribute": Name, "code": lit})
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// CodeText returns a code's text: a JSON string's value, or a JSON
// integer's literal digits (optionally signed). ok is false for every
// other JSON kind.
func CodeText(rc json.RawMessage) (string, bool) {
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

// UnknownField is the constructor's refusal of a field the schema does
// not carry. The field-reference rule refuses a name no step makes
// available first; this one is left for a derived column (an earlier
// attribute's label, a feature output), which the constructor cannot
// resolve — it reads the schema only.
func UnknownField(field string) error {
	return errors.NewCodedError(errors.PROCESSING_CONFIG, Name+": unknown field "+field)
}

// Check judges codes against field's type ft, in the constructor's
// order: the accepted-types table's refusal (a categorical, or an
// unsigned integer), then — on an integer field — the first code no row
// could ever hold (not an integer, negative, or beyond the type's
// width). It returns the parsed integer codes, in codes order, on an
// integer field and nil on a categorical one.
func Check(field string, ft encoding.FieldType, codes []string) ([]uint64, error) {
	if cerr := fieldaccept.Refusal(Name, fieldaccept.SlotField, field, ft); cerr != nil {
		return nil, cerr
	}
	if ft.IsCategorical() {
		return nil, nil
	}
	maxV := IntegerMax[ft]
	out := make([]uint64, 0, len(codes))
	for _, c := range codes {
		n, perr := strconv.ParseUint(c, 10, 64)
		if perr != nil || n > maxV {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				Name+": code "+strconv.Quote(c)+" can never match "+ft.String()+" field "+strconv.Quote(field)+
					"; codes on an integer field must be whole numbers in 0.."+strconv.FormatUint(maxV, 10),
				map[string]any{
					"attribute": Name,
					"field":     field,
					"type":      ft.String(),
					"code":      c,
					"max":       maxV,
				})
		}
		out = append(out, n)
	}
	return out, nil
}

// Missing returns, sorted, the codes dict lacks — every code when dict
// is nil. The runtime matches such a code against nothing; predict
// reports it (PULSE_ATTR_CODE_NOT_IN_DICTIONARY).
func Missing(dict *encoding.Dictionary, codes []string) []string {
	var missing []string
	for _, c := range codes {
		if dict != nil {
			if _, ok := dict.IDFor(c); ok {
				continue
			}
		}
		missing = append(missing, c)
	}
	sort.Strings(missing)
	return missing
}
