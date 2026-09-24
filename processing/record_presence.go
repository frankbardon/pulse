package processing

// Presence vs value on a Record.
//
// Most of the engine asks a Record one of two questions and the two
// used to share an accessor:
//
//	v, ok := r.NumericValue(f)   // "give me the number"
//	_, ok := r.NumericValue(f)   // "did this row answer at all?"
//
// A set-typed field breaks the sharing. It has no meaningful number —
// the decoder's float64 echo is lossy from set_u64 up and is only the
// LOW 64 BITS of a set_u128 / set_u256 mask — so NumericValue refuses
// set fields outright (see its doc comment). But a set field very much
// has PRESENCE: a respondent who selected nothing still answered, and
// an empty mask is a valid selection distinct from null. The universal
// {n, n_null} components floor, AGG_COUNT / AGG_NULL_COUNT, FILTER_NULL
// and the crosstab cell-admission gates all ask the presence question,
// and every one of them would read a whole wide-set column as null if
// it kept asking it through NumericValue.
//
// FieldPresent is that second question, asked once. Call it wherever
// the numeric value is discarded.

// FieldPresent reports whether the named field carries a value on this
// record — a number, a wide typed value (decimal128), or a set mask
// (any rung, empty mask included). It is false exactly when the field
// is null or absent, which is the same condition Record.IsNull reports
// true for.
//
// The empty mask is deliberately PRESENT. `set_*` nulls ride the
// per-record null bitmap with no in-band sentinel, so an all-zero mask
// means "answered, selected nothing" and counting it as null would
// move real respondents into n_null.
func FieldPresent(r *Record, field string) bool {
	if r == nil || field == "" {
		return false
	}
	if _, ok := r.NumericValue(field); ok {
		return true
	}
	_, ok := r.SetMaskValue(field)
	return ok
}
