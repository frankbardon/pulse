package descriptor

// MaterializePredictResult is Pulse.PredictMaterialize's envelope Data:
// the no-execute verdict on a materialize request — the cohort the run
// would write, read off the source's header and schema only, never a
// record. Every static refusal is a coded envelope error (Valid false);
// a check only the records can settle is a Risk, not an error.
type MaterializePredictResult struct {
	// Valid mirrors the envelope's errors emptiness.
	Valid bool `json:"valid"`

	// SchemaInfo summarises the SOURCE cohort's schema. Absent when the
	// source header or schema could not be read.
	SchemaInfo *PredictSchemaInfo `json:"schema_info,omitempty"`

	// Columns is the output schema the run writes, in output order —
	// exactly the fields Materialize hands its cohort writer. Empty
	// (never null) when a refusal stops the layout from resolving.
	Columns []MaterializePredictColumn `json:"columns"`

	// DerivedColumns names the written columns that hold derived values,
	// under their output names, in output order —
	// MaterializeResult.DerivedColumns of the same run. Empty, never
	// null.
	DerivedColumns []string `json:"derived_columns"`

	// Risks lists every data-dependent check the run makes on the
	// records — each a refusal the run raises only if a record fails it.
	// Empty (never null) when the run checks nothing data-dependent.
	Risks []MaterializeRisk `json:"risks"`
}

// MaterializePredictColumn is one column of the cohort a materialize run
// writes.
type MaterializePredictColumn struct {
	// Name is the output column name (an output block's `as`, else the
	// name it reads).
	Name string `json:"name"`
	// Type is the field type written (a derived column: f64, or an
	// ATTR_PREVIOUS_STATE output's inherited state-field type).
	Type string `json:"type"`
	// Nullable is the written nullability flag.
	Nullable bool `json:"nullable"`
	// Description is the written field description.
	Description string `json:"description"`
	// From names the column it reads: a source field, or a derived
	// column's label.
	From string `json:"from"`
	// Derived is true when From is a derived column (a feature,
	// attribute or window output), false for a source field.
	Derived bool `json:"derived"`
	// Dictionary is the dictionary the column is written with (a
	// categorical, set or inherited previous-state column), truncated
	// like Inspect's to the first 100 entries. Absent for any other
	// type.
	Dictionary *DictionaryInfo `json:"dictionary,omitempty"`
	// Precision and Scale are a decimal128 column's shape; absent for
	// any other type.
	Precision *uint8 `json:"precision,omitempty"`
	Scale     *uint8 `json:"scale,omitempty"`
}

// MaterializeRisk is one data-dependent check a materialize run makes
// on the records: the column it guards, the request path that asks for
// it, the code the run fails with when a record does not pass, and the
// check in words.
type MaterializeRisk struct {
	Column string `json:"column"`
	Path   string `json:"path,omitempty"`
	Code   string `json:"code"`
	Check  string `json:"check"`
}
