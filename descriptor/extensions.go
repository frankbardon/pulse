package descriptor

// ExtensionsManifest is the manifest-side projection of embedder-
// registered operators and expression-side state. Built-in operators
// continue to live in Manifest.Components; this block lists everything
// the host process adds on top, so a reviewer can see at a glance
// which subset is Pulse-shipped vs registered by the embedder.
//
// All slices are sorted by Name for deterministic output. An empty
// Extensions block emits as nested empty slices (not null) for
// JSON-stability across releases.
type ExtensionsManifest struct {
	Aggregators        []OperatorMeta `json:"aggregators"`
	Attributes         []OperatorMeta `json:"attributes"`
	Filterers          []OperatorMeta `json:"filterers"`
	Groupers           []OperatorMeta `json:"groupers"`
	Windows            []OperatorMeta `json:"windows"`
	Features           []OperatorMeta `json:"features"`
	Tests              []OperatorMeta `json:"tests"`
	SynthDistributions []OperatorMeta `json:"synth_distributions"`
	// MatrixOps lists the embedder-registered MAT_* matrix operators
	// in the built-in Matrices shape (MatrixMeta): member types, the
	// engine-owned params then the operator's own, OutputKeys, the
	// declared tier and the matrix floor plus the operator's component
	// keys. A request names one in Request.Matrices like a built-in.
	MatrixOps     []MatrixMeta       `json:"matrix_ops"`
	ExprFunctions []ExprFunctionMeta `json:"expr_functions"`
	LookupTables  []LookupTableMeta  `json:"lookup_tables"`
	// ReferenceVectors lists the registered reference-vector tables
	// (Options.Extensions.ReferenceVectors) an expression reads with
	// ref("table", key).
	ReferenceVectors []ReferenceVectorMeta `json:"reference_vectors"`
	LabelTables      []LabelTableMeta      `json:"label_tables"`
	RangeTables      []RangeTableMeta      `json:"range_tables"`
	// Populations lists the instance's registered populations
	// (Options.Extensions.Populations + Options.PopulationsDir /
	// PULSE_POPULATIONS_DIR): saved vector fits every request sees
	// bound by name, so a fitted attribute or MAT_* slot binds one with
	// params.fit without a `populations` entry.
	Populations []PopulationMeta `json:"populations"`
}

// OperatorMeta is the manifest projection for an embedder-registered
// operator. The shape mirrors descriptor.Operator but adds Namespace
// (parsed from the registered name) and Mode (attribute-only) so
// reviewers can group operators by source without re-parsing.
type OperatorMeta struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Description string `json:"description,omitempty"`
	Streamable  bool   `json:"streamable"`
	// FansOut is the grouper-only projection of
	// pulse.GrouperRegistration.FansOut — the embedder-side sibling of
	// types.GroupType.FansOut(), which knows built-in constants only.
	// True means one record can land in more than one bucket, so the
	// bucket counts SUM to more than the record total. Omitted (and
	// meaningless) for every other operator category; absent reads as
	// false, which is also the registration default.
	FansOut bool `json:"fans_out,omitempty"`
	// Mergeable is the aggregator / grouper projection of
	// pulse.AggregatorRegistration.Mergeable and
	// pulse.GrouperRegistration.Mergeable — the embedder-side sibling
	// of types.AggregationType.Mergeable() / types.GroupType.Mergeable().
	// True means partial states fold (extend.MergeableAggregator.Merge,
	// extend.MergeableGrouper.MergeState), so the parallel reducers
	// (ShardWorkers / DecodeWorkers) and ProcessChain accept the
	// operator. Omitted for every other category; absent reads as
	// false, which is also the registration default.
	Mergeable bool `json:"mergeable,omitempty"`
	// MarginReducibility is the aggregator-only projection of
	// pulse.AggregatorRegistration.MarginReducibility — the
	// embedder-side sibling of types.AggregationType.MarginReducibility()
	// ("summable", "mean_reducible", "independent" or "recompute").
	// A non-recompute class admits the operator as a fused crosstab
	// cell. Omitted when undeclared, which reads as "recompute": the
	// crosstab cell runs buffered.
	MarginReducibility string `json:"margin_reducibility,omitempty"`
	// VectorInput is the aggregator-only projection of
	// pulse.AggregatorRegistration.VectorInput: the slot reads a member
	// set from vector or fields (exactly one; field refused), resolved
	// by the built-in vector rules, like AGG_VEC_MEAN. Omitted
	// otherwise: the slot reads field.
	VectorInput bool `json:"vector_input,omitempty"`
	// ArrayValued is the aggregator-only projection of
	// pulse.AggregatorRegistration.ArrayValued (implies VectorInput):
	// the value is one figure per member, so the slot is refused as a
	// crosstab cell or margin, a sort key, an overlay host and a chain
	// stage feeding another, and params.expand writes one column per
	// member. Omitted otherwise.
	ArrayValued bool `json:"array_valued,omitempty"`
	// NonScalar is the aggregator-only projection of
	// pulse.AggregatorRegistration.NonScalar: the value a Data row
	// carries is not one number (a label list, a count map, a struct),
	// so the slot is refused as the aggregation an overlay scores.
	// Omitted otherwise.
	NonScalar bool `json:"non_scalar,omitempty"`
	// EmitsInputValue is the aggregator-only projection of
	// pulse.AggregatorRegistration.EmitsInputValue: the slot's one
	// number is one of its input field's own encoded values, so over a
	// categorical_* or packed_bool field it is a code and the slot is
	// refused as the aggregation an overlay scores. Omitted otherwise.
	EmitsInputValue bool `json:"emits_input_value,omitempty"`
	// WeightAware is the aggregator / attribute / test projection of
	// the registration's WeightAware declaration — the extension half
	// of the built-in Operator.WeightAware. True means the operator
	// reads the resolved row weight (extend.Record.Weight) and computes
	// a weighted figure; omitted otherwise.
	WeightAware bool                `json:"weight_aware,omitempty"`
	Accepts     []string            `json:"accepts,omitempty"`
	Emits       string              `json:"emits,omitempty"`
	Mode        string              `json:"mode,omitempty"`
	Tier        string              `json:"tier,omitempty"`
	Params      []OperatorParamMeta `json:"params,omitempty"`
	// Intents lists the intent-taxonomy IDs (Manifest.Intents) the
	// registration's purpose declares, sorted. Omitted when the
	// registration declares none.
	Intents []string `json:"intents,omitempty"`
}

// OperatorParamMeta is the manifest-friendly mirror of pulse.ParamMeta.
type OperatorParamMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	JSONType    string `json:"json_type"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	// ExactlyOneOf is the mutually-exclusive group name, mirroring
	// Param.ExactlyOneOf: a request supplies exactly one member.
	ExactlyOneOf string `json:"exactly_one_of,omitempty"`
}

// ExprFunctionMeta is the manifest projection of an embedder-
// registered expression function.
type ExprFunctionMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Signature   string `json:"signature,omitempty"`
	Pure        bool   `json:"pure,omitempty"`
}

// LookupTableMeta is the manifest projection of a registered lookup
// table. HasRowsData distinguishes the static Rows-backed table from
// the function-driven Lookup table without exposing the embedder's
// data.
type LookupTableMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	HasRowsData bool   `json:"has_rows_data"`
}

// ReferenceVectorMeta is the manifest projection of a registered
// reference-vector table: its element labels and its profile keys —
// what an author needs to write ref("table", key) and give the compared
// vector matching labels — without the profile values.
type ReferenceVectorMeta struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Labels      []string `json:"labels"`
	Keys        []string `json:"keys"`
}

// LabelTableMeta is the manifest projection of a registered string-
// valued label table. HasRowsData distinguishes the static Rows-backed
// table from the function-driven Lookup table without exposing the
// embedder's data.
type LabelTableMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	HasRowsData bool   `json:"has_rows_data"`
}

// RangeTableMeta is the manifest projection of a registered labeled-
// range table. Kind is "date" (ISO date bounds; GROUP_DATE_RANGES /
// FILTER_DATE_RANGES) or "numeric" (number bounds; GROUP_NUMERIC_RANGES).
// RangeCount surfaces how many {label, start, end} entries the table
// carries without exposing the boundaries themselves, mirroring how
// LabelTableMeta.HasRowsData characterises a label table.
type RangeTableMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
	RangeCount  int    `json:"range_count"`
}

// PopulationMeta is the manifest projection of a registered population:
// its bound name, the battery it was fitted by, its weight basis, its
// member labels in axis order (what a consuming slot's vector aligns
// to, by label) and its fit row count — never the stored moments.
type PopulationMeta struct {
	Name     string   `json:"name"`
	Operator string   `json:"operator,omitempty"`
	Basis    string   `json:"basis"`
	Labels   []string `json:"labels"`
	N        int64    `json:"n"`
}
