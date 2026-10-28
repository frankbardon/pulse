package types

import "encoding/json"

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
	// count). "null" needs a nullable output column
	// (PULSE_MATERIALIZE_OUTPUT_INVALID, reason
	// on_cast_error_not_nullable). It applies only to a column that
	// re-codes its values — a Type, a Recode or a Bucket
	// (on_cast_error_without_type otherwise); a Recode alone never fails
	// a value, its refusals are all static; a Bucket fails a value
	// outside every range (reason no_range) or a NaN (not_finite).
	OnCastError string `json:"on_cast_error,omitempty"`

	// Recode rewrites a categorical column's labels — relabel, merge,
	// map to null, reorder — before any Type cast reads them. Nil keeps
	// the labels. See MaterializeRecode.
	Recode *MaterializeRecode `json:"recode,omitempty"`

	// Bucket turns a numeric or date-family column into labelled
	// categories: each value is written as the label of the range
	// holding it. Nil keeps the values. See MaterializeBucket.
	Bucket *MaterializeBucket `json:"bucket,omitempty"`
}

// MaterializeRecode is an output column's categorical recode
// (MaterializeOutputColumn.Recode): a rewrite of the dictionary of the
// categorical_* column it reads, never a per-row expression. The order
// of operations on one column is recode (the source labels), then Type
// (the recoded labels — a categorical narrowing is judged on the
// recoded dictionary, so a merge can make it fit), then the nullability
// check; As only names the result.
//
// The written dictionary is the source dictionary's labels in code
// order, each replaced by its Map value (an unmapped label keeps
// itself; a label mapped to null leaves it), first occurrence kept —
// so labels mapped to one value merge into one entry at the position
// of the first of them — unless Order names the final order. Each cell
// is written as its label's recoded value.
//
// Refusals, all before anything is read (predict refuses each with the
// same code and details): Recode on a column that is not categorical_*
// (PULSE_MATERIALIZE_OUTPUT_INVALID, reason recode_not_categorical); a
// Map key absent from the source dictionary
// (PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL, details label — the drift
// guard when a stored request meets a new source cohort); a null value
// on a column whose output is not nullable (reason
// recode_null_not_nullable); an Order naming a label the recode does not
// write (recode_order_unknown), omitting one it does
// (recode_order_missing) or naming one twice (recode_order_duplicate).
type MaterializeRecode struct {
	// Map maps a source label to its new label, or to null (JSON null:
	// the cell is written null). Labels mapped to the same value merge.
	Map map[string]*string `json:"map,omitempty"`

	// Order is the written dictionary's order: every label the recode
	// writes, each exactly once. Empty keeps first-occurrence order.
	Order []string `json:"order,omitempty"`
}

// MaterializeBucket is an output column's bucketing
// (MaterializeOutputColumn.Bucket): the numeric (u4..u64, f32, f64,
// decimal128, or a derived f64) or date-family (date, datetime) column it
// reads is written as a categorical_* of range labels. The ranges are
// exactly one of inline Ranges or RangeTable, a registered range table
// (Extensions.RangeTables / PULSE_RANGE_TABLES_DIR), compiled by the
// shared range-compilation pass GROUP_NUMERIC_RANGES, GROUP_DATE_RANGES
// and FILTER_DATE_RANGES use, with their boundary rules: a numeric range
// is half-open [start, end); a date range is inclusive on both ends and
// a datetime is matched by its local day in Request.TimeZone (else the
// instance default zone, else UTC). An omitted bound is open.
//
// The written type is the narrowest categorical_* holding every label
// (categorical_u8 up to 256), its dictionary the labels in declared
// order — inline order, or the table's own — every label present
// whether or not a value lands in it. A null stays null; a value
// outside every range (or a NaN) follows OnCastError. The order of
// operations on one column is bucket, then Type (a categorical width, or
// a numeric parse of the labels), then the nullability check.
//
// Refusals, all before anything is read (predict refuses each with the
// same code and details, plus column and path): a source that is not
// numeric or date-family (PULSE_MATERIALIZE_OUTPUT_INVALID, reason
// bucket_unsupported_type); both or neither of Ranges / RangeTable
// (PULSE_RANGE_SOURCE_AMBIGUOUS); an unknown table
// (PULSE_RANGE_TABLE_UNKNOWN) or one of the other kind
// (PULSE_RANGE_TABLE_KIND_MISMATCH); inline ranges the shared pass
// refuses (PULSE_RANGE_EMPTY / _INVALID / _DUPLICATE_LABEL / _OVERLAP;
// a bound of the wrong JSON kind is PULSE_RANGE_INVALID).
type MaterializeBucket struct {
	// Ranges are the inline ranges, in the written dictionary's order.
	Ranges []MaterializeBucketRange `json:"ranges,omitempty"`

	// RangeTable names a registered range table: a numeric table for a
	// numeric column, a date table for a date-family one.
	RangeTable string `json:"range_table,omitempty"`
}

// MaterializeBucketRange is one inline bucket range: Label is the
// category written; Start and End are JSON numbers on a numeric column
// (half-open [start, end)) or ISO date strings on a date-family column
// (inclusive). An omitted or null bound is open.
type MaterializeBucketRange struct {
	Label string          `json:"label"`
	Start json.RawMessage `json:"start,omitempty"`
	End   json.RawMessage `json:"end,omitempty"`
}
