package extend

import "github.com/frankbardon/pulse/encoding"

// Record is the read-only view of one decoded row handed to extension
// operators. It is valid only for the duration of the call that
// received it (see the package doc's reuse contract). Consumer-only:
// Pulse implements it and may add methods in a minor release.
type Record interface {
	// Schema returns the cohort schema the row was decoded against.
	// Callers MUST NOT mutate it.
	Schema() *encoding.Schema

	// IsNull reports whether the named field is null on this row. A
	// field that is missing or projected out also reports null.
	IsNull(field string) bool

	// NumericValue returns the field's value as float64 and true, or
	// (0, false) when the field is null, missing or projected out.
	// It is ALWAYS false for set-typed fields — read them through
	// SetMaskValue. A categorical field yields its dictionary index;
	// date yields epoch days and datetime epoch seconds; u64 values
	// above 2^53 lose precision through the float64 echo; decimal128
	// yields only a rounded echo (use DecimalValue).
	NumericValue(field string) (float64, bool)

	// StringValue returns the dictionary-resolved label of a
	// categorical field. It is (“”, false) for a null field and for
	// every non-categorical field.
	StringValue(field string) (string, bool)

	// SetMaskValue returns the membership bitmask of a set-typed field
	// at any rung (set_u8 .. set_u256): bit i is dictionary entry i. An
	// empty mask with true is a valid "nothing selected", distinct from
	// null, which reports false. The mask is a value and safe to keep.
	SetMaskValue(field string) (encoding.SetMask, bool)

	// DecimalValue returns the exact value of a decimal128 field. It is
	// (zero, false) for a null, missing or non-decimal field. The value
	// is safe to keep.
	DecimalValue(field string) (encoding.Decimal128, bool)

	// Weight returns the row weight the engine resolved for the slot
	// this operator runs in (slot weight → request weight →
	// pulse.Options.DefaultWeight) and true, or (0, false) when no
	// weight is in force on the slot. It reports a weight only to an
	// operator whose registration declares WeightAware, and only a
	// VALID one: finite and non-negative (zero included), and an
	// integer under kind frequency. A row whose weight is invalid never
	// reaches a weight-aware aggregator or row test (it is excluded and
	// counted by the engine); a weight-aware attribute, which owes a
	// value for every row, still receives it with Weight reporting
	// false. Every other operator — and every operator on an unweighted
	// slot — always sees (0, false). The orchestrator emits the
	// weighted floor keys (sum_weights, n_eff, n_weight_invalid); the
	// operator never does.
	Weight() (float64, bool)
}

// Rows is a read-only, zero-copy view over the rows of one buffered
// call. It is valid only for the duration of that call; do not retain
// it or any Record obtained from it. Consumer-only: Pulse implements it
// and may add methods in a minor release.
type Rows interface {
	// Len returns the number of rows in the view.
	Len() int
	// At returns row i, 0 <= i < Len().
	At(i int) Record
}
