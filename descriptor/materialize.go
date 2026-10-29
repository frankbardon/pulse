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

	// Container is how the run writes the cohort: a single file or a
	// shard archive (output.shards), and whether constants are elided
	// (output.elide_constants). Absent when a refusal stops the layout
	// from resolving.
	Container *MaterializeContainer `json:"container,omitempty"`
}

// MaterializeContainer is the container plan of a materialize run
// (MaterializePredictResult.Container).
type MaterializeContainer struct {
	// Kind is "file" (a single-file cohort) or "shard_archive".
	Kind string `json:"kind"`
	// MaxRecords is a shard archive's rows per shard; absent for a file.
	MaxRecords int `json:"max_records,omitempty"`
	// MaxShards is the most shards a shard archive run writes —
	// ceil(source records / max_records), at least 1 — reached when
	// every source record passes the filters. Absent for a file, or when
	// the source's record count is unknown.
	MaxShards *int64 `json:"max_shards,omitempty"`
	// ElideConstants is output.elide_constants: a column holding one
	// value on every written row is stored once. Which columns qualify
	// only the rows settle, so none is named here.
	ElideConstants bool `json:"elide_constants"`
	// Groups is the parent-group plan (output.groups, or the source's
	// groups inherited when it is absent): every group handed to the
	// writer, in the order it is declared to it, then every source group
	// left out. Empty when the written cohort has no group to judge.
	Groups []MaterializeGroupPlan `json:"groups,omitempty"`
}

// MaterializeGroupPlan is one parent group of a materialize run's
// container plan (MaterializeContainer.Groups).
type MaterializeGroupPlan struct {
	// Label names the group as the run's result and warnings do: "group
	// 1 [key: region]" for a group handed to the writer, "source group
	// 2 [...]" (source names) for one left out.
	Label string `json:"label"`
	// Origin is "declared" (output.groups) or "inherited" (a source
	// group).
	Origin string `json:"origin"`
	// Key and Members are the group's key and determined members by
	// written name (by source name for a group left out); no Key means
	// every member is key.
	Key     []string `json:"key,omitempty"`
	Members []string `json:"members"`
	// SourceFields names an inherited group's source members (key and
	// determined, in source order); absent for a declared one.
	SourceFields []string `json:"source_fields,omitempty"`
	// Verdict is "planned" (handed to the writer, whose viability gate
	// over the written rows decides admitted or low_ratio),
	// "dropped_too_narrow" (no wider than its index: never formed,
	// PULSE_GROUP_TOO_NARROW) or "not_inherited" (a member is not written
	// unchanged, PULSE_MATERIALIZE_GROUP_NOT_INHERITED).
	Verdict string `json:"verdict"`
	// Reason is the code behind a dropped verdict.
	Reason string `json:"reason,omitempty"`
	// Member and Change say why a source group is not inherited: the
	// first member that changed, and how — "dropped", "retyped",
	// "recoded" or "bucketed".
	Member string `json:"member,omitempty"`
	Change string `json:"change,omitempty"`
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
// it, the code the run fails with when a record does not pass (or, for a
// cast column with on_cast_error: "null", the warning it raises), and
// the check in words — a cast's condition reads "f64 → u8: integral,
// 0 ≤ v ≤ 255".
type MaterializeRisk struct {
	Column string `json:"column"`
	Path   string `json:"path,omitempty"`
	Code   string `json:"code"`
	Check  string `json:"check"`
}
