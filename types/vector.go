package types

// VectorSpec names a battery of numeric columns once per request: a
// virtual vector. Matrix operators (Request.Matrices) reference it by
// Name instead of listing every column.
//
// Exactly one of Fields and Pattern is set:
//
//   - Fields lists member names. A literal name keeps its position (the
//     caller's order becomes the axis order); an entry carrying a glob
//     metacharacter (`*`, `?`, `[`) expands, in place, to every matching
//     schema field in schema order.
//   - Pattern is a Go regular expression (unanchored; write `^…$` to
//     match whole names) whose matches are taken in schema order.
//
// A member that appears twice after expansion is refused
// (PULSE_VECTOR_DUPLICATE), as is a resolution that matches nothing
// (PULSE_VECTOR_EMPTY). Members must be integer (u4 / u8 / u16 / u32 /
// u64) or float (f32 / f64) fields; packed_bool is admitted only under
// Coerce "binary"; categorical, set, date, datetime and decimal128
// fields are refused (PULSE_VECTOR_MEMBER_TYPE). Labels, when set, must
// carry exactly one display label per resolved member
// (PULSE_VECTOR_LABELS_MISMATCH). Vector names are unique per request.
//
// Resolution reads only the schema (never a record), so predict reports
// the resolved member lists (PredictResult.ResolvedVectors) exactly as
// the runtime resolves them.
type VectorSpec struct {
	// Name is the vector's request-scoped name.
	Name string `json:"name"`
	// Fields lists literal member names and / or glob entries.
	Fields []string `json:"fields,omitempty"`
	// Pattern is a regular expression over schema field names.
	Pattern string `json:"pattern,omitempty"`
	// Labels are optional display labels, one per resolved member.
	Labels []string `json:"labels,omitempty"`
	// Coerce admits a member type a vector refuses by default; empty
	// admits none. VectorCoerceBinary admits packed_bool as 0 / 1.
	Coerce VectorCoerce `json:"coerce,omitempty"`
}

// VectorCoerce names a member-type coercion a VectorSpec opts into.
type VectorCoerce string

const (
	// VectorCoerceBinary admits packed_bool members, read as 0 (false)
	// and 1 (true).
	VectorCoerceBinary VectorCoerce = "binary"
)

// AllVectorCoerces returns every defined coercion, alphabetically.
func AllVectorCoerces() []VectorCoerce {
	return []VectorCoerce{VectorCoerceBinary}
}
