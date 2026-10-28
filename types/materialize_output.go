package types

// MaterializeOutput is the materialize-only `output` slot
// (Request.Output): it lays out the schema of the cohort a
// Pulse.Materialize run writes. Absent, the run writes every source
// column verbatim followed by the derived columns — byte-identical to a
// request that never had the slot. Every other operation (Process,
// Compose, ProcessChain, Predict and their streaming / api / MCP
// surfaces) refuses a request carrying it with
// PULSE_MATERIALIZE_OUTPUT_ONLY.
//
// Projection is applied LAST: filters, sort, weight, features,
// attributes and windows read the full source schema plus the derived
// columns, so they may reference a column the output drops. With an
// `output` block present a request needs no derived column at all — a
// pure reshape of the source is legal.
//
// Columns and Exclude are exclusive (both set is
// PULSE_MATERIALIZE_OUTPUT_INVALID, reason columns_and_exclude). With
// neither, every source column then every derived column is written.
// Gated by capability:materialize_output.
type MaterializeOutput struct {
	// Columns is the closed column list: exactly these columns, in this
	// order. A present but empty list is refused (reason no_columns).
	Columns []MaterializeOutputColumn `json:"columns,omitempty"`

	// Exclude drops the named columns (source fields or derived
	// labels) and keeps every other one in source-then-derived order.
	// An unknown name is PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN; a list
	// that drops every column is refused (reason no_columns).
	Exclude []string `json:"exclude,omitempty"`
}

// MaterializeOutputColumn is one entry of MaterializeOutput.Columns.
// Every key left out inherits from the column named by From: a source
// field keeps its type, nullability, dictionary and description; a
// derived column stays a nullable f64 (an ATTR_PREVIOUS_STATE column:
// its state field's type and dictionary) with its generated
// description.
type MaterializeOutputColumn struct {
	// From names the column read: a source field or a derived column's
	// label (a feature output, an attribute or window label). Required;
	// an unknown name is PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN. The same
	// From may appear twice under different As names (a duplicated
	// column).
	From string `json:"from"`

	// As renames the output column; empty keeps From. Output names must
	// be unique after renaming (PULSE_MATERIALIZE_OUTPUT_DUPLICATE).
	As string `json:"as,omitempty"`

	// Description replaces the inherited field description. Nil
	// inherits; an empty string clears it.
	Description *string `json:"description,omitempty"`

	// Nullable overrides the inherited nullability. Nil inherits.
	// false on a column holding a null at run time fails the run with
	// PULSE_MATERIALIZE_OUTPUT_NULL (details column + row), nothing
	// written.
	Nullable *bool `json:"nullable,omitempty"`

	// Type retypes the column: a field type name ("u8", "f64",
	// "decimal128", "packed_bool", "date", "datetime", "set_u16", ...).
	// Empty (or the column's own type) keeps it. The legal pairs:
	// numeric (u4..u64, f32, f64, decimal128) → numeric; numeric ↔
	// packed_bool (0 / 1 only); numeric epoch ↔ date (epoch days) /
	// datetime (epoch seconds); date ↔ datetime in Request.TimeZone
	// (else the instance default zone, else UTC: midnight, and the
	// local day); set_* → a wider set_* rung; categorical_* → another
	// categorical_* width (the whole dictionary kept; a known dictionary
	// larger than the target is refused) or → numeric (each label parsed
	// as a number); numeric → categorical_* (each value stringified into
	// a dictionary built in first-seen order). vec_* keeps its type. Any
	// other pair is PULSE_MATERIALIZE_CAST_UNSUPPORTED before anything is
	// read; an unknown name is PULSE_MATERIALIZE_OUTPUT_INVALID (reason
	// unknown_type). Every cast is exact or fails: a stored value that
	// does not fit (a fraction to an integer, a value past the range, a
	// non-0/1 to packed_bool, a label that is not a number, a distinct
	// value past a categorical's capacity) follows OnCastError.
	Type string `json:"type,omitempty"`

	// Precision and Scale shape a decimal128 Type (1 ≤ precision ≤ 38,
	// 0 ≤ scale ≤ precision). Nil inherits a decimal128 column's own
	// shape; from any other type the default is precision 38, scale 0.
	// Set on any other Type they are refused (reason decimal_shape).
	Precision *int `json:"precision,omitempty"`
	Scale     *int `json:"scale,omitempty"`

	// OnCastError is what a stored value the cast cannot represent does:
	// "" / "fail" (the default) fails the run with
	// PULSE_MATERIALIZE_CAST_FAILED (details column + row + value +
	// reason), nothing written; "null" writes a null in its place and
	// warns once per column with PULSE_MATERIALIZE_CAST_NULLED (details
	// count). "null" needs a nullable output column and a Type
	// (PULSE_MATERIALIZE_OUTPUT_INVALID otherwise).
	OnCastError string `json:"on_cast_error,omitempty"`
}
