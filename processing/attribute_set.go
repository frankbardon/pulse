package processing

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Set-typed attributes — ATTR_SET_POPCOUNT, ATTR_SET_HAS. Both are
// RowLocalAttribute: a single row's set mask determines the derived
// value, no population pass required.
//
// Both read the row through Record.SetMaskValue and have ONE body
// covering every set rung from set_u8 to set_u256 — no narrow arm and
// no wide arm. Neither may read a set column through NumericValue: the
// float64 echo of a set field is the low 64 bits of the bitmask, which
// is a plausible wrong number rather than an error.

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
	m, ok := r.SetMaskValue(field)
	if !ok {
		return 0, nil
	}
	return float64(m.PopCount()), nil
}

// Derive bool (0/1) per row = whether the named label's bit is set.
// Resolved at construction via the field's dictionary so per-row work
// is a single bitwise op against a precomputed bit position.

type setHasParams struct {
	Label string `json:"label"`
}

// setHasAttribute tests one dictionary bit per row. bit is the
// dictionary ID resolved at construction, or -1 when no schema was
// available to resolve it — SetMask.Has(-1) is false, so an unresolved
// label reports ABSENT rather than silently testing dictionary entry 0.
type setHasAttribute struct {
	bit int
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
		// Tolerated for registry tests; runtime always has schema. The
		// label cannot be resolved to a bit, so the attribute reports
		// absent for every row instead of guessing bit 0.
		return &setHasAttribute{bit: -1}, nil
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
	// Guard against the DECLARED RUNG's capacity, not against 64: a
	// set_u128 / set_u256 column legitimately carries members above bit
	// 63, while a bit beyond the rung can never be set on any row.
	if capacity := int(f.Type.MaxSetEntries()); int(id) >= capacity {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("ATTR_SET_HAS: label %q maps to bit %d, beyond the %d-member capacity of %s field %q",
				params.Label, id, capacity, f.Type, attr.Field),
			map[string]any{
				"field":    attr.Field,
				"type":     f.Type.String(),
				"capacity": capacity,
				"bit":      int(id),
				"label":    params.Label,
			})
	}
	return &setHasAttribute{bit: int(id)}, nil
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
	m, ok := r.SetMaskValue(field)
	if !ok {
		return 0, nil
	}
	if m.Has(a.bit) {
		return 1, nil
	}
	return 0, nil
}

// rejectSetFieldForNumericAttribute refuses a numeric attribute bound to
// a set column. ATTR_ZSCORE / ATTR_TSCORE / ATTR_NORMALIZED /
// ATTR_PERCENTILE read Record.NumericValue on every row, and a set
// column has no meaningful number there — the echo is the LOW 64 BITS of
// the membership bitmask for set_u128 / set_u256, and a float64 that has
// already lost precision above 2^53 for set_u64.
//
// These four factories ignored their schema argument entirely, so the
// refusal had nowhere to live: a set column produced a whole emitted
// attribute column of zeroes (NumericValue returns ok=false for every
// row, PrePass sees n=0, and the standardisation divides by a zero
// stddev) with no error anywhere. That is the silent class this guard
// closes; it needs no descriptor change, because all four already
// declare numericFieldTypesNoDecimal, which has never carried a set
// rung.
//
// A nil schema (registry probe construction) has nothing to check.
func rejectSetFieldForNumericAttribute(attr *types.Attribute, schema *encoding.Schema) error {
	if schema == nil || attr == nil || attr.Field == "" {
		return nil
	}
	f := schema.Field(attr.Field)
	if f == nil || !f.Type.IsSet() {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(attr.Type)+": field "+attr.Field+" is a set column ("+f.Type.String()+
			"); it has no numeric value to standardise. Use ATTR_SET_POPCOUNT for set size or ATTR_SET_HAS for membership.",
		map[string]any{
			"field":      attr.Field,
			"type":       f.Type.String(),
			"attribute":  string(attr.Type),
			"alternates": []string{string(types.ATTR_SET_POPCOUNT), string(types.ATTR_SET_HAS)},
		})
}
