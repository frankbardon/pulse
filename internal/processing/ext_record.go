package processing

import (
	"math"

	"github.com/frankbardon/pulse/extend"
)

// ExtRecord is the extend.Record view of an engine row: every handoff
// to an extend operator wraps its *Record in it. It is a
// one-pointer struct, so converting it to the interface allocates
// nothing and a per-row adapter costs nothing.
//
// It overrides two reads so an element member "f[k]" of a native
// vector field f (vec_f32 / vec_f64) — the literal member names a
// VectorInput slot hands its factory — reads like the scalar twin's
// column: NumericValue resolves it through the shared member reader
// (Record.MemberValue, the fill's own rule: a NaN element
// and a null vector are a null member) and IsNull agrees. A plain name
// is looked up first, so a real column spelled "x[2]" and every
// non-element name behave exactly as on the engine row. Built-ins
// authored on the engine keep the concrete *Record path; the model
// regressions (authored on extend) see this view like any extension.
type ExtRecord struct{ *Record }

var _ extend.Record = ExtRecord{}

// NumericValue is the engine row's, plus native element members.
func (r ExtRecord) NumericValue(field string) (float64, bool) {
	return r.Record.MemberValue(field)
}

// Uint64Value is the engine row's exact read (a u64 field's stored
// word on every decode path), plus native element members, which read
// their element's float through EchoToUint64 — so it is (0, false)
// exactly when NumericValue is.
func (r ExtRecord) Uint64Value(field string) (uint64, bool) {
	if u, ok := r.Record.Uint64Value(field); ok {
		return u, true
	}
	v, ok := r.Record.MemberValue(field)
	if !ok {
		return 0, false
	}
	return EchoToUint64(v), true
}

// EchoToUint64 converts a float64 echo to uint64 as
// Record.Uint64Value's fallback does: uint64(v), except that
// an echo at or above 2^64 saturates to math.MaxUint64.
func EchoToUint64(v float64) uint64 {
	if v >= 1<<64 {
		return math.MaxUint64
	}
	return uint64(v)
}

// StringValue is the engine row's CHECKED label read under the extend
// signature, which has no error: a categorical code its dictionary has
// no entry for (corrupt record data) reads ("", false) to the embedder
// AND is latched on the row (Record.NoteReadFault). The
// adapter that handed the row over takes it when the extension call
// returns (readFault / readFaultRows) and returns the ENCODING_INVALID
// refusal in place of the call's own result — so a corrupt code is never
// a silent null, on any extension category (U51 E5-S10).
func (r ExtRecord) StringValue(field string) (string, bool) {
	s, ok, err := r.Record.StringValueChecked(field)
	if err != nil {
		r.Record.NoteReadFault(err)
		return "", false
	}
	return s, ok
}

// IsNull is the engine row's, except that a native element member
// holding a value is not null.
func (r ExtRecord) IsNull(field string) bool {
	if !r.Record.IsNull(field) {
		return false
	}
	_, ok := r.Record.MemberValue(field)
	return !ok
}
