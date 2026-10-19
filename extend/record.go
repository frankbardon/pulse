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
	// field that is missing or projected out also reports null. It
	// agrees with NumericValue on a native vector element member
	// "f[k]": null exactly when that read reports false.
	IsNull(field string) bool

	// NumericValue returns the field's value as float64 and true, or
	// (0, false) when the field is null, missing or projected out.
	// It is ALWAYS false for set-typed fields — read them through
	// SetMaskValue — and for vector fields (VectorValue). A
	// categorical field yields its dictionary index; date yields epoch
	// days and datetime epoch seconds; a u64 value above 2^53 is only
	// its float64 echo (2^53 and 2^53+1 read alike — use Uint64Value);
	// decimal128 yields only a rounded echo (use DecimalValue).
	//
	// An element member "f[k]" of a native vector field f — the
	// literal member names a vector-input slot hands its factory in
	// spec.Fields — reads element k (1-based, canonical base-10, 1..dim)
	// in place, without allocating: a NaN element and a null vector
	// are a null member (false), as in every built-in vector consumer,
	// so a native vector reads exactly like its scalar-column twin. The
	// plain name is looked up first, so a real column spelled "x[2]"
	// and every name whose base is not a vector field (or whose k is
	// out of range or non-canonical) keep the ordinary lookup.
	NumericValue(field string) (float64, bool)

	// Uint64Value returns the field's value as an EXACT uint64 and true,
	// or (0, false) exactly when NumericValue reports false (null,
	// missing, projected out, set, vector). On a u64 field it is the
	// stored on-wire word on every decode path — streaming, buffered,
	// parallel, shard archive, point lookup, filter-to-file — so values
	// above 2^53 (where distinct u64s share one float64 echo) and up to
	// math.MaxUint64 stay distinct: read it to key or match on a u64.
	// Any other numeric field (or a value a feature wrote) converts its
	// float64 echo to uint64, an echo at or above 2^64 saturating to
	// math.MaxUint64; it is meaningful only for non-negative integers.
	Uint64Value(field string) (uint64, bool)

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

	// VectorValue returns the elements of a vector field (vec_f32 /
	// vec_f64) as float64 — vec_f32 elements widen exactly — and true.
	// It is (nil, false) for a null, missing, projected-out or
	// non-vector field; NumericValue is always false for a vector. A
	// NaN or ±Inf element is a stored value, returned verbatim. The
	// slice is a fresh copy and safe to keep.
	VectorValue(field string) ([]float64, bool)

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
