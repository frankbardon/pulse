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
	Aggregators        []OperatorMeta     `json:"aggregators"`
	Attributes         []OperatorMeta     `json:"attributes"`
	Filterers          []OperatorMeta     `json:"filterers"`
	Groupers           []OperatorMeta     `json:"groupers"`
	Windows            []OperatorMeta     `json:"windows"`
	Features           []OperatorMeta     `json:"features"`
	Tests              []OperatorMeta     `json:"tests"`
	SynthDistributions []OperatorMeta     `json:"synth_distributions"`
	ExprFunctions      []ExprFunctionMeta `json:"expr_functions"`
	LookupTables       []LookupTableMeta  `json:"lookup_tables"`
	LabelTables        []LabelTableMeta   `json:"label_tables"`
	RangeTables        []RangeTableMeta   `json:"range_tables"`
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
// date-range table. RangeCount surfaces how many {label, start, end}
// entries the table carries without exposing the boundaries themselves,
// mirroring how LabelTableMeta.HasRowsData characterises a label table.
type RangeTableMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	RangeCount  int    `json:"range_count"`
}
