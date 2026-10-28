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
}
