package processing

import (
	"encoding/json"
	"math/bits"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed attributes — ATTR_SET_POPCOUNT, ATTR_SET_HAS. Both are
// RowLocalAttribute: a single row's set mask determines the derived
// value, no population pass required.

// Derive scalar uint8 per row = popcount(mask). Null input rows yield
// 0 (consistent with other row-local attribute paths that surface 0
// for null inputs through Compute).

type setPopcountAttribute struct{}

func newSetPopcountAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_POPCOUNT requires field")
	}
	if schema != nil {
		f := schema.Field(attr.Field)
		if f == nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"ATTR_SET_POPCOUNT: unknown field "+attr.Field)
		}
		if !f.Type.IsSet() {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"ATTR_SET_POPCOUNT: field "+attr.Field+" is not a set type")
		}
	}
	return &setPopcountAttribute{}, nil
}

func (a *setPopcountAttribute) Compute(records []*Record, field string) ([]float64, error) {
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

func (a *setPopcountAttribute) Row(r *Record, field string) (float64, error) {
	m, ok := r.SetValue(field)
	if !ok {
		return 0, nil
	}
	return float64(bits.OnesCount64(m)), nil
}

// Derive bool (0/1) per row = whether the named label's bit is set.
// Resolved at construction via the field's dictionary so per-row work
// is a single bitwise op against a precomputed bit position.

type setHasParams struct {
	Label string `json:"label"`
}

type setHasAttribute struct {
	bit uint
}

func newSetHasAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS requires field")
	}
	if len(attr.Params) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS requires params with a \"label\" field")
	}
	var params setHasParams
	if err := json.Unmarshal(attr.Params, &params); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing ATTR_SET_HAS params")
	}
	if params.Label == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS requires non-empty params.label")
	}
	if schema == nil {
		// Tolerated for registry tests; runtime always has schema.
		return &setHasAttribute{}, nil
	}
	f := schema.Field(attr.Field)
	if f == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: unknown field "+attr.Field)
	}
	if !f.Type.IsSet() {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: field "+attr.Field+" is not a set type")
	}
	if f.Dictionary == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: field "+attr.Field+" has no dictionary")
	}
	id, ok := f.Dictionary.IDFor(params.Label)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: label "+params.Label+" not in dictionary for field "+attr.Field)
	}
	if id >= 64 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"ATTR_SET_HAS: label "+params.Label+" maps to bit exceeding set width")
	}
	return &setHasAttribute{bit: uint(id)}, nil
}

func (a *setHasAttribute) Compute(records []*Record, field string) ([]float64, error) {
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

func (a *setHasAttribute) Row(r *Record, field string) (float64, error) {
	m, ok := r.SetValue(field)
	if !ok {
		return 0, nil
	}
	if (m>>a.bit)&1 == 1 {
		return 1, nil
	}
	return 0, nil
}
