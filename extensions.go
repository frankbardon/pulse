package pulse

import (
	"encoding/json"
	iofs "io/fs"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// AggregatorComponentsFunc is the optional emitter an embedder may
// supply alongside an AggregatorRegistration to make the operator
// participate in the per-operator components contract.
// The orchestrator invokes the func ONCE after Aggregate / Finalize and
// routes the result onto Response.Components.Aggregations[i].Operator;
// the universal floor ({"n", "n_null"}) is filled unconditionally by
// the orchestrator. Returning (nil, nil) is the canonical signal for
// "no operator-specific keys; orchestrator's universal floor is the
// entire payload" (floor-only operators).
//
// The instance passed in is the extend.Aggregator the registration's
// Factory returned (after Aggregate / Finalize), so a type assertion to
// the embedder's concrete type succeeds; the func should not call
// Aggregate / UpdateRow / Finalize on it. The returned map's keys must
// be a SUBSET of the registration's ComponentSchema.Keys (universal-
// floor keys allowed but not required) — probe-validation enforces
// this contract at pulse.New time and runtime mismatches surface as
// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH.
type AggregatorComponentsFunc func(instance extend.Aggregator) (map[string]any, error)

// GrouperComponentsFunc is the grouper-shape sibling of
// AggregatorComponentsFunc. The orchestrator invokes the func ONCE
// after the grouper's terminal partition pass and routes the result
// onto Response.Components.Groupers[i].Operator. The universal floor
// ({total_n, n_null}) is filled unconditionally by the orchestrator.
// Returning (nil, nil) is the canonical signal for "no operator-
// specific keys". The instance passed in is the extend.Grouper the
// registration's Factory returned, so a type assertion to the
// embedder's concrete type succeeds.
type GrouperComponentsFunc func(instance extend.Grouper) (map[string]any, error)

// FiltererComponentsFunc is the filterer-shape sibling. In v1 every
// built-in filterer leaves its operator-specific payload empty
// (uniform floor of {n_in, n_out, n_null_input}); extensions may opt
// in by supplying a ComponentSchema + this emitter. The instance passed
// in is the extend.FiltererBuilder the registration's Factory returned.
type FiltererComponentsFunc func(instance extend.FiltererBuilder) (map[string]any, error)

// AttributeComponentsFunc is the attribute-shape sibling. The
// orchestrator invokes the func ONCE after the attribute has scored
// every filter-passing row and routes the result onto that slot's
// Response.Components.Attributes entry (Operator); the attribute floor
// ({n, n_null}: rows whose output is present / null) is filled by the
// orchestrator. Only a registration that supplies it (with a
// ComponentSchema) emits an entry. The instance passed in is the
// extend.AttributeComputer the registration's Factory returned.
//
// A registration that supplies it is not admitted to the parallel
// reducers or ProcessChain even when its Mode is row_local: one
// instance per worker would leave per-worker figures no orchestrator
// merge can fold, so the request runs on the serial arm instead.
type AttributeComponentsFunc func(instance extend.AttributeComputer) (map[string]any, error)

// FieldInputsFunc is the optional introspection callback an extension
// registration may supply so the buffered-projection extractor can
// determine which extra schema fields the operator reads beyond the
// spec's explicit Field/Field2/PartitionBy/etc. references. raw is the
// operator's Params block. Return value lists schema field names to
// include; nil/empty means "no extra fields."
//
// Embedders that omit this hook leave their operator opaque to the
// projection extractor — the runtime then widens the buffered-decode
// field set to "every field" so the operator stays correct.
type FieldInputsFunc func(raw json.RawMessage) []string

// Extensions bundles every per-category registration slot plus the
// expression-environment additions an embedder wants exposed to
// runtime ATTR_FORMULA and FILTER_EXPRESSION evaluation.
//
// Zero value is the zero-extension case — pulse.New(Options{}) with no
// Extensions field behaves identically to a Pulse instance shipped
// without this surface. The registration types live in a separate
// namespace from the built-in registries (no collision risk) and the
// runtime treats them identically to built-ins.
type Extensions struct {
	Aggregators []AggregatorRegistration
	Attributes  []AttributeRegistration
	Filterers   []FiltererRegistration
	Groupers    []GrouperRegistration
	Windows     []WindowRegistration
	Features    []FeatureRegistration
	Tests       []TestRegistration
	// MatrixOps installs embedder MAT_* matrix operators for
	// Request.Matrices (see MatrixOpRegistration).
	MatrixOps []MatrixOpRegistration
	// Regressions installs embedder REG_* regression models for
	// Request.Regressions (see RegressionRegistration).
	Regressions []RegressionRegistration

	// ExprFunctions are merged into the runtime expression environment
	// used by ATTR_FORMULA and FILTER_EXPRESSION (plus any future
	// expression hook). Each entry is callable from request expressions
	// under its declared Name.
	ExprFunctions []ExprFunction

	// LookupTables expose static keyed maps to the runtime expression
	// environment. Callers reference them as
	//   lookup("table_name", key1, key2, ...)
	// The built-in lookup() function is added to the expression env
	// whenever LookupTables is non-empty. Tables are read-only after
	// pulse.New.
	LookupTables map[string]LookupTable

	// ReferenceVectors expose named sets of labelled reference profiles
	// to the runtime expression environment. Callers reference them as
	//   ref("table_name", key)
	// which yields the profile as a numeric vector aligned BY LABEL to
	// the vector it is compared with (dot / dist / cosine, directly or
	// under vcenter / vzscore / vnormalize); a label set that differs is
	// PULSE_VECTOR_REFERENCE_MISMATCH, an unregistered table or key
	// PULSE_VECTOR_REFERENCE_UNKNOWN — both at predict and at build
	// when the names are literals. Validated at pulse.New
	// (types.ReferenceVectorTable); programmatic only (no environment
	// directory). Tables are read-only after pulse.New.
	ReferenceVectors map[string]types.ReferenceVectorTable

	// LabelTables expose static ID→label maps used by the output-time
	// categorical-label overlay (per-request Labels slot on Request /
	// SampleRequest / FacetRequest / ExportJob). Each table accepts a
	// single string key (the categorical's resolved dictionary value)
	// and returns a display string. Tables are read-only after
	// pulse.New and do not affect filter / formula / sort semantics —
	// only end-user output rendering.
	LabelTables map[string]LabelTable

	// RangeTables expose named sets of labeled ranges so a
	// GROUP_DATE_RANGES grouper or FILTER_DATE_RANGES filter (a date
	// table) or a GROUP_NUMERIC_RANGES grouper (a numeric table) can
	// reference an ordered {label, start, end} bucketing by name
	// instead of authoring it inline per request. Each table is
	// validated at pulse.New time via the shared range-compilation
	// pass (the same overlap / duplicate-label / empty / invalid-
	// boundary rules the inline path enforces); a table that nothing
	// references is valid and simply visible in the manifest. Tables
	// are read-only after pulse.New. Operator→table resolution is
	// wired separately; this slot only registers the tables.
	RangeTables map[string]RangeTable

	// Populations registers saved vector-fit populations by name: each
	// a population file document (the bytes a Compose vector fit's
	// `save` writes, *.pulse-population.json). Every request sees them
	// bound as vector fits, so a fitted attribute or MAT_* slot binds
	// one with params.fit and no `populations` entry; a request entry
	// rebinding a registered name is PULSE_MODEL_NAME_COLLISION. Each is
	// decoded at pulse.New (PULSE_MODEL_FILE_INVALID /
	// _VERSION_UNSUPPORTED, details.registered = the name) and frozen.
	// Merged with Options.PopulationsDir: a name declared both here and
	// on disk is PULSE_EXTENSION_DUPLICATE. Gated by capability:models —
	// a feature profile hiding it registers none.
	Populations map[string]Population

	// Skills is an optional fs.FS of embedder skill files, served to
	// agents exactly like the built-in pack (p.Skills / p.Skill,
	// pulse_skills_list / pulse_skills_get, pulse-skill:// resources, the
	// manifest skills list, p.Ontology). Only top-level .md files, in
	// two shapes: an atomic op-<category>-<kebab>.md (kind: operator)
	// documenting ONE operator registered here — its stem is op- plus
	// the operator name lowercased with _ → - — and a topical
	// ext-<kebab>.md (kind: design, optional requires:). Every file is
	// validated HARD at pulse.New (frontmatter, name = stem, the
	// family's required ## sections, body budget, feature fences and
	// their coverage, ## See stems): a failure is
	// PULSE_EXTENSION_SKILL_INVALID, a stem a built-in skill or another
	// file already takes PULSE_EXTENSION_SKILL_COLLISION. A feature
	// profile hiding the operator hides its skill — it then reads as
	// nonexistent everywhere. Read once at pulse.New; nil adds none.
	Skills iofs.FS

	// Examples is an optional fs.FS of embedder request examples, served
	// to agents exactly like the built-in library (p.ExamplesSearch /
	// p.ExampleGet, pulse_examples_search / pulse_examples_get, the
	// manifest examples count / categories / tags, p.Ontology). Only
	// top-level .json files, each a request body plus a _meta block —
	// name, category, description, tags, operators, optional intents and
	// capabilities — exactly as in the built-in library. Every file is
	// validated HARD at pulse.New (the _meta keys and values, operators
	// registered on this instance and equal to the body's "type" values,
	// a strict decode of the body as its request root, every operator
	// it names joined to it by an ontology edge): a failure is
	// PULSE_EXTENSION_EXAMPLE_INVALID, a name a built-in example or
	// another file already takes PULSE_EXTENSION_EXAMPLE_COLLISION. A
	// feature profile hiding an operator or capability the example uses
	// hides it — it then reads as nonexistent everywhere. Read once at
	// pulse.New; nil adds none.
	Examples iofs.FS
}

// ParamMeta describes one operator-specific parameter for manifest
// emission. It is descriptive only — runtime parameter resolution
// happens inside each factory.
type ParamMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	JSONType    string `json:"json_type"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	// ExactlyOneOf names a group of mutually exclusive params: of the
	// params sharing a group name, a request supplies exactly one. A
	// group needs at least two members, none Required and none with a
	// Default (PULSE_EXTENSION_PARAM_INVALID otherwise). A request
	// carrying none, or more than one, of a group's members is refused
	// PULSE_REQUEST_PARAM_INVALID reason exactly_one_of at predict and
	// runtime alike; the manifest says it and Recommend drafts the
	// first member. Empty = ungrouped.
	ExactlyOneOf string `json:"exactly_one_of,omitempty"`
	// Example is an optional example value of the param, of its
	// JSONType (pulse.New refuses one that is not, or that does not
	// marshal: PULSE_EXTENSION_PARAM_INVALID, details.reason
	// example_type_mismatch). The registration probe hands it to the
	// factory in place of the generic JSONType placeholder for a
	// Required param or the first member of an ExactlyOneOf group, so a
	// factory that accepts only certain values (an enum-like string)
	// passes the probe. On any other param it is documentation only.
	// Projected into the manifest as example. Nil = none.
	Example any `json:"example,omitempty"`
}

// AggregatorRegistration installs a custom AGG_* operator. The factory
// is an extend.AggregatorFactory: it builds a fresh extend.Aggregator
// per Process call against the supplied Aggregation spec + schema.
//
// When Streamable=true the factory MUST return a value that also
// implements extend.OnlineAggregator. Probe-validation at
// registration time enforces the contract via
// PULSE_EXTENSION_STREAMABLE_MISMATCH. The declaration decides the
// run-time path: Streamable=false runs buffered even when the value
// implements extend.OnlineAggregator. Optional siblings
// (extend.OnlineAggregator, extend.RichAggregator,
// extend.MergeableAggregator) are honoured whether or not
// ComponentsFunc is set.
//
// When Mergeable=true (which requires Streamable=true) the factory MUST
// return an extend.MergeableAggregator and any ComponentSchema keys
// must be classified "mergeable" or "partial"; probe-validation refuses
// anything else with PULSE_EXTENSION_MERGEABLE_MISMATCH. The
// declaration decides whether ShardWorkers / DecodeWorkers fan the
// request out and whether a ProcessChain stage accepts it; omitted, the
// request runs serially.
//
// MarginReducibility declares the operator's crosstab margin class —
// the embedder-side sibling of types.AggregationType.MarginReducibility.
// A fusable class (types.MarginSummable, types.MarginMeanReducible,
// types.MarginIndependent) admits the operator as a crosstab CELL to
// the fused in-decode arm and requires Mergeable=true (and so
// Streamable=true); probe-validation refuses an unknown class or a
// fusable class without Mergeable with
// PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH. The fused walk feeds
// each margin its own accumulator record by record, so the class gates
// admission only and never changes a margin figure. Omitted (or
// types.MarginRecompute) keeps the crosstab cell on the buffered arm.
type AggregatorRegistration struct {
	Name        types.AggregationType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn  []string
	Factory    extend.AggregatorFactory
	Streamable bool
	// Mergeable declares that partial states fold via
	// extend.MergeableAggregator.Merge, admitting the operator to the
	// parallel reducers and ProcessChain. Requires Streamable.
	Mergeable bool
	// MarginReducibility declares the crosstab margin class; a fusable
	// class admits the operator as a fused crosstab cell. Requires
	// Mergeable unless empty or types.MarginRecompute.
	MarginReducibility types.MarginReducibility
	// WeightAware declares that the operator computes a weighted
	// figure: on a slot where a row weight is in force (slot weight,
	// request weight or pulse.Options.DefaultWeight) the factory sees
	// the resolved weight on spec.Weight and every row's VALID weight
	// is readable through extend.Record.Weight() — rows with an invalid
	// weight are excluded before they reach the operator, and the
	// orchestrator emits the weighted floor keys (sum_weights, n_eff,
	// n_weight_invalid). Probe-validation constructs the factory both
	// with and without a weight. Undeclared, an explicit (slot or
	// request) weight on the slot is PULSE_EXTENSION_NOT_WEIGHT_AWARE
	// and the instance default is skipped. Projected as the manifest's
	// weight_aware.
	WeightAware bool
	// VectorInput declares that the operator reads a member set instead
	// of one field: the slot names a Request.Vectors entry (vector) or
	// inline members (fields — names and globs), exactly one, and no
	// field. Predict and the runtime resolve it by the built-in vector
	// rules (the AGG_VEC_MEAN contract: PULSE_VECTOR_* refusals,
	// numeric source columns only, labels) and the factory receives a
	// copy of the spec whose Fields are the resolved literal members,
	// Vector empty, Label the default "<NAME>_<vector or first fields
	// entry>" when unset, and params.labels the resolved display label
	// per member (every other params key is kept). Each row's members
	// read through extend.Record.NumericValue / IsNull as usual — a
	// native vector's element member "f[k]" included (a NaN element or
	// a null vector is a null member). params.labels and
	// params.expand are the engine's: a VectorInput registration may not
	// declare a ParamMeta of either name (PULSE_EXTENSION_PARAM_INVALID,
	// reason reserved_vector_param). The universal floor counts a row
	// present when ANY member answered. Undeclared, a vector input on
	// the slot is refused (vector_not_accepted).
	VectorInput bool
	// ArrayValued declares that the value is one figure per member, in
	// member order: the instance implements extend.RichAggregator and
	// Rich returns a []float64 of the member count (NaN is null on the
	// wire). It requires VectorInput and leaves MarginReducibility empty
	// (PULSE_EXTENSION_ARRAY_VALUED_MISMATCH). The slot is then refused
	// wherever one number is needed — a crosstab cell or margin
	// aggregation, a sort key, an overlay host, a chain stage feeding
	// another — exactly like AGG_VEC_MEAN, and params.expand writes one
	// column per member, "<label>[<element label>]", in place of the
	// array. A Rich value of another shape is PROCESSING_INTERNAL at
	// run time.
	ArrayValued bool
	// NonScalar declares that the value the slot writes into a
	// Response.Data row is not one number: the instance implements
	// extend.RichAggregator and Rich returns a label list, a count map,
	// a struct — anything but a single figure (ArrayValued implies it).
	// Predict and the runtime then refuse the slot as the aggregation
	// an overlay scores (request, Compose or chain host: params.field
	// where the kind honours it, else aggregations[0]) with
	// PROCESSING_CONFIG, reason "non_scalar", exactly as for the
	// built-in AGG_WELFORD / AGG_SET_UNION / AGG_SET_INTERSECTION /
	// AGG_SET_FREQUENCY, and so is a chain stage carrying it that feeds
	// another; an unscored one beside it runs. Undeclared,
	// a non-scalar Rich value scored by an overlay folds as no value.
	// Projected as the manifest's non_scalar.
	NonScalar bool
	// EmitsInputValue declares that the slot's one number is one of its
	// INPUT field's own encoded values — a value picked from the column
	// (a mode, a first / last pick), not a figure computed over it. Over
	// a categorical_* field that value is the dictionary index
	// (extend.Record.NumericValue), over a packed_bool field the 0/1
	// truth value: a code, not a magnitude. Predict (from request plus
	// schema) and the runtime then refuse the slot over such a field as
	// the aggregation an overlay scores (request, Compose or chain host:
	// params.field where the kind honours it, else aggregations[0]) with
	// PROCESSING_CONFIG, reason "categorical_code", exactly as for the
	// built-in AGG_MODE; over a numeric field, or unscored, it runs.
	// Undeclared, an overlay scores the code as a magnitude. Projected
	// as the manifest's emits_input_value.
	EmitsInputValue bool
	// CountSemantic declares that the operator's finished value is a
	// count carried as a float64 (the extension half of the built-in
	// count classification — AGG_COUNT, AGG_DISTINCT_COUNT, …). Under
	// Request.Return precision the slot's data column, a count crosstab
	// cell's matrix figures (normalize none; a multi-measure matrix only
	// when every measure counts) and a count margin aggregation's figures
	// are then written exact instead of rounded. Refused on a
	// VectorInput or ArrayValued registration (PULSE_EXTENSION_PARAM_INVALID,
	// reason count_semantic_vector_input): a member-set value is not one
	// count. Undeclared, the column rounds like any measure. Projected as
	// the manifest's count_semantic.
	CountSemantic bool
	// ValueState declares that the operator's state grows with the
	// distinct values it sees (a map entry per value, like AGG_MODE or
	// AGG_DISTINCT_COUNT) rather than staying a fixed-size accumulator.
	// The MaxEstimatedMemory estimate then charges the slot the same
	// per-distinct-value term a built-in value-state aggregator pays.
	// Undeclared, the slot is priced as a fixed accumulator. Projected
	// as the manifest's value_state.
	ValueState bool
	Accepts    []encoding.FieldType
	Params     []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc. Omit to keep the operator opaque to
	// projection (runtime widens the field set when this operator
	// appears in a request).
	FieldInputs FieldInputsFunc
	// ComponentSchema declares the per-operator components contract
	// surfaced through Response.Components.Aggregations[i].Operator.
	// Leaving the schema empty (zero-value Keys plus
	// empty Mergeability) makes the operator floor-only — the
	// orchestrator emits the universal floor ({"n", "n_null"}) and no
	// extra keys. When ComponentSchema.Keys is non-empty an emitter
	// MUST exist — ComponentsFunc, or the value's own Components()
	// method — and its returned map keys MUST be a subset of the
	// declared key set; an emitter in turn MUST declare Keys.
	// Probe-validation enforces all three at pulse.New time, for either
	// emitter.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional sibling-interface emitter. When
	// set, the orchestrator routes the returned map onto
	// Response.Components.Aggregations[i].Operator after the
	// aggregator's Aggregate / Finalize call terminates. Nil is the
	// floor-only path (universal floor fills the entire payload).
	ComponentsFunc AggregatorComponentsFunc
}

// AttributeMode declares which streaming tier an attribute factory
// promises to satisfy.
type AttributeMode string

const (
	// AttributeModeRowLocal — factory returns an extend.RowLocalAttribute.
	// Equivalent to built-in ATTR_FORMULA / ATTR_DATE_PART.
	AttributeModeRowLocal AttributeMode = "row_local"
	// AttributeModeTwoPass — factory returns an extend.TwoPassAttribute.
	// Equivalent to ATTR_ZSCORE / ATTR_TSCORE / ATTR_NORMALIZED.
	AttributeModeTwoPass AttributeMode = "two_pass"
	// AttributeModeBuffered — factory returns a plain
	// extend.AttributeComputer; no streaming. Equivalent to
	// ATTR_PERCENTILE.
	AttributeModeBuffered AttributeMode = "buffered"
)

// AttributeEmitType is a manifest hint declaring the dtype the
// attribute emits per record. Defaults to "float64" when empty.
type AttributeEmitType string

const (
	AttributeEmitFloat64 AttributeEmitType = "float64"
	AttributeEmitBool    AttributeEmitType = "bool"
	AttributeEmitString  AttributeEmitType = "string"
)

// AttributeRegistration installs a custom ATTR_* operator. The factory
// is an extend.AttributeFactory; Mode declares which extend sibling the
// returned value implements, and probe-validation at pulse.New refuses
// a mismatch with PULSE_EXTENSION_STREAMABLE_MISMATCH.
type AttributeRegistration struct {
	Name        types.AttributeType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn []string
	Factory   extend.AttributeFactory
	Mode      AttributeMode
	Accepts   []encoding.FieldType
	Emits     AttributeEmitType
	Params    []ParamMeta
	// WeightAware declares that the attribute reads the row weight:
	// on a slot where a weight is in force the factory sees it on
	// spec.Weight and extend.Record.Weight() reports each row's valid
	// weight (false on a row whose weight is invalid — the attribute
	// still owes that row a value). Undeclared, ANY weight in force on
	// the slot — the instance default included — is
	// PULSE_EXTENSION_NOT_WEIGHT_AWARE (a non-aware attribute may
	// depend on the whole population, whose weighted form it cannot
	// give); `weight: null` on the slot opts out. Probe-validation
	// constructs the factory both with and without a weight.
	WeightAware bool
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
	// ComponentSchema declares the per-operator components contract
	// surfaced through the slot's Response.Components.Attributes entry
	// (Operator). Empty (zero Keys and Mergeability) emits no entry.
	// When Keys is non-empty an emitter MUST exist — ComponentsFunc,
	// or the value's own Components() method (adopted when no
	// ComponentsFunc is set) — and its returned keys MUST be a subset of
	// the declared set; an emitter MUST declare Keys. Probe-validation
	// enforces all three at pulse.New time, for either emitter.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional emitter (see
	// AttributeComponentsFunc). Nil emits no entry.
	ComponentsFunc AttributeComponentsFunc
	// Mergeable declares that the attribute's per-run tally — the
	// state behind its components figures — folds across input
	// partitions through extend.MergeableAttribute.MergeTally. A
	// component-emitting row_local attribute then runs under the
	// parallel reducers (ShardWorkers / DecodeWorkers) and in
	// ProcessChain stages; undeclared, a request carrying it runs
	// serially and a chain refuses it. A row_local attribute emitting
	// no components merges either way (it has no tally). Requires Mode
	// row_local; an emitting value MUST implement
	// extend.MergeableAttribute, and the ComponentSchema's Mergeability
	// must not be "none" — pulse.New refuses either mismatch with
	// PULSE_EXTENSION_MERGEABLE_MISMATCH.
	Mergeable bool
}

// FiltererRegistration installs a custom FILTER_* operator. Filterers
// are always row-local streamable today — every built-in filterer
// evaluates per-row. The runtime records custom filterers as
// streamable; a buffered-filterer registration shape lands the day
// Pulse grows a buffered-filterer path.
type FiltererRegistration struct {
	Name        types.FiltererType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn []string
	Factory   extend.FiltererFactory
	Accepts   []encoding.FieldType
	Params    []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc. Filterers don't carry a Params
	// block today; the callback receives nil raw bytes and should
	// return the static set of extra fields the filterer reads
	// beyond Filterer.Field.
	FieldInputs FieldInputsFunc
	// ComponentSchema declares the per-operator components contract
	// surfaced through Response.Components.Filterers[i].Operator.
	// v1 ships every built-in filterer as floor-only
	// (uniform {n_in, n_out, n_null_input}); extensions may opt in by
	// declaring a non-empty ComponentSchema alongside a ComponentsFunc
	// or a builder with its own Components() method. Mismatch between
	// declared schema and emitted keys (either emitter) is rejected at
	// pulse.New time, as is an emitter with no declared Keys.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional sibling-interface emitter. When
	// set, the orchestrator routes the returned map onto
	// Response.Components.Filterers[i].Operator after the filter pass
	// terminates. Nil is the floor-only path (the orchestrator's
	// universal floor is the entire payload). Mirrors
	// the engine's per-operator Components() emission in shape.
	ComponentsFunc FiltererComponentsFunc
}

// GrouperRegistration installs a custom GROUP_* operator. Set
// Streamable=true when the factory returns an extend.Grouper that
// also implements extend.StreamingGrouper (KeyForRow) or
// extend.MultiKeyStreamingGrouper (KeysForRow); the grouped Process
// request then streams. Probe-validation refuses Streamable=true
// without either sibling (PULSE_EXTENSION_STREAMABLE_MISMATCH);
// Streamable=false runs buffered through Group.
//
// Set FansOut=true when the factory returns a value that also
// implements extend.MultiKeyStreamingGrouper — see the field
// comment. Probe-validation at registration time enforces the claim in
// BOTH directions via PULSE_EXTENSION_FANOUT_MISMATCH, mirroring the
// Streamable contract on AggregatorRegistration.
type GrouperRegistration struct {
	Name        types.GroupType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn  []string
	Factory    extend.GrouperFactory
	Streamable bool
	// FansOut declares that a single record can land in MORE THAN ONE
	// bucket of this grouper — the embedder-registered sibling of
	// types.GroupType.FansOut(), which knows built-in constants only.
	//
	// The runtime expression of the same fact is the optional
	// extend.MultiKeyStreamingGrouper interface (KeysForRow). A
	// registration whose factory returns that interface MUST declare
	// FansOut=true, and one that declares FansOut=true MUST return it:
	// probe-validation at pulse.New() rejects either mismatch with
	// PULSE_EXTENSION_FANOUT_MISMATCH.
	//
	// The declaration exists because consumers that reason about
	// per-record denominators cannot assert a runtime interface —
	// under a fan-out grouper the bucket counts SUM to more than the
	// record total, so an n taken from a slab total double-counts
	// records. Omitting the field defaults it to false, so a multi-key
	// factory is refused rather than silently over-counting.
	FansOut bool
	// Mergeable declares that the grouper may run under the parallel
	// reducers (ShardWorkers / DecodeWorkers) and in ProcessChain
	// stages: one instance per partition, components state folded
	// through extend.MergeableGrouper.MergeState. Requires Streamable
	// (the reducers key rows one at a time). A Mergeable grouper that
	// emits components (ComponentsFunc, or a value with its own
	// Components() method) MUST return an extend.MergeableGrouper,
	// and its ComponentSchema must not classify the keys "none"; a
	// grouper that emits none needs no merge method. Probe-validation
	// refuses anything else with PULSE_EXTENSION_MERGEABLE_MISMATCH.
	// Omitted, a request naming the grouper runs serially.
	Mergeable bool
	Accepts   []encoding.FieldType
	Params    []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
	// ComponentSchema declares the per-operator components contract
	// surfaced through Response.Components.Groupers[i].Operator.
	// Leaving the schema empty makes the operator
	// floor-only — the orchestrator emits the universal floor
	// ({total_n, n_null}) without operator-specific extras. When
	// ComponentSchema.Keys is non-empty an emitter MUST exist —
	// ComponentsFunc, or the value's own Components() method — and its
	// keys MUST match the declared schema; an emitter MUST declare
	// Keys. Probe-validation enforces both at pulse.New time.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional sibling-interface emitter. When
	// set, the orchestrator routes the returned map onto
	// Response.Components.Groupers[i].Operator after the grouper's
	// terminal partitioning pass. Nil is the floor-only path. Mirrors
	// the engine's per-operator Components() emission in shape.
	ComponentsFunc GrouperComponentsFunc
}

// WindowRegistration installs a custom WIN_* operator. Window
// operators run buffered today; the runtime records custom windows
// as non-streamable. A streaming-window shape lands when the
// runtime gains a streaming-window pipeline.
type WindowRegistration struct {
	Name        types.WindowType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn []string
	Factory   extend.WindowFactory
	Accepts   []encoding.FieldType
	Params    []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
}

// FeatureRegistration installs a custom FEAT_* operator. Set
// Streamable=true when the factory returns an extend.FeatureComputer
// that also implements extend.StreamingFeatureComputer. The operator
// only reads rows and returns extend.FeatureOutput columns; the engine
// writes them.
type FeatureRegistration struct {
	Name        types.FeatureType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn  []string
	Factory    extend.FeatureFactory
	Streamable bool
	Accepts    []encoding.FieldType
	Params     []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
}

// TestTier selects which factory shape a TestRegistration provides.
// Tier-1 row tests fold per-row during the streaming aggregation pass;
// tier-2 post-tests consume the materialized result row set after
// windows.
type TestTier string

const (
	TestTierRow  TestTier = "tier1"
	TestTierPost TestTier = "tier2"
)

// TestRegistration installs a custom TEST_* operator. Exactly one of
// RowFactory / PostFactory must be non-nil, matching Tier.
//
// Streamable applies to tier-1 only and indicates whether the test
// can co-stream with online aggregators (no extra pass over the data).
// The flag alone decides: Streamable=false forces the request onto the
// buffered path.
type TestRegistration struct {
	Name        types.TestType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier (NotFor.Use resolves against built-ins, the
	// other registered extensions and the feature table); a broken rule
	// fails with PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs
	// reach the manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// Interpretation optionally says how to read the test's outputs,
	// one entry per normalised output path (e.g. "statistic",
	// "details.effect_size.cohens_d"). Checked for structure only at
	// pulse.New — path syntax, Means or Shared, Bands with a
	// Convention — since an extension declares no output keys to probe;
	// a broken rule fails with PULSE_EXTENSION_PURPOSE_INVALID.
	Interpretation []descriptor.Interpretation
	// DependsOn names the features this operator needs, each an AND
	// edge (a single-name group): a feature profile that enables the
	// operator must enable every named feature too, or pulse.New fails
	// with PULSE_FEATURE_PROFILE_DEPENDENCY. Every entry must name a
	// feature this build knows — a built-in feature or another
	// registered extension operator — whether or not a profile is set;
	// otherwise pulse.New fails with PULSE_FEATURE_PROFILE_UNKNOWN.
	// Optional; nil adds no dependency. Extensions carry no Since.
	DependsOn   []string
	Tier        TestTier
	RowFactory  extend.RowTestFactory
	PostFactory extend.PostTestFactory
	Streamable  bool
	Accepts     []encoding.FieldType
	Params      []ParamMeta
	// WeightAware declares that the test computes a weighted result:
	// on a slot where a weight is in force the factory sees it on
	// spec.Weight and a tier-1 row test reads each row's valid weight
	// through extend.Record.Weight() (rows with an invalid weight never
	// reach UpdateRow). Undeclared, ANY weight in force on the slot —
	// the instance default included — is
	// PULSE_EXTENSION_NOT_WEIGHT_AWARE, as the built-in tests refuse
	// one; `weight: null` on the slot opts out. Probe-validation
	// constructs a WeightAware test's factory both with and without a
	// weight (a test that does not declare it is not probed).
	WeightAware bool
	// FieldInputs is the optional buffered-projection introspection
	// hook for tier-1 row tests. Tier-2 post-tests run on materialized
	// result rows rather than source records, so projection doesn't
	// apply — leave nil for tier-2 registrations.
	FieldInputs FieldInputsFunc
}

// MatrixOpRegistration installs a custom MAT_* matrix operator: a
// finalize-only extend.MatrixOperator the engine hands the merged,
// read-only co-moment of the slot's members (extend.MatrixInput), so
// accumulation, the 4096-row block merge tree, grouped buckets,
// missing-value modes and row weights stay the engine's and the result
// is bit-identical across every worker count and shard split. The name
// follows the extension policy (MAT_<NAMESPACE>_<NAME>).
type MatrixOpRegistration struct {
	Name        types.MatrixType
	Description string
	// Purpose is the optional plain-language guidance for the
	// operator — what it is for, the intents it answers, when to use
	// something else. It is validated at pulse.New with the same rules
	// as the built-in tier; a broken rule fails with
	// PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs reach the
	// manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// DependsOn names the features this operator needs, each an AND
	// edge; see AggregatorRegistration.DependsOn. Optional; extensions
	// carry no Since.
	DependsOn []string
	// Factory builds the operator per matrix slot. Required
	// (PULSE_EXTENSION_PARAM_INVALID, reason missing_factory).
	Factory extend.MatrixFactory
	// Streamable declares the co-moment tier: the operator needs only
	// extend.MatrixInput.CoMoment, so the request streams and merges
	// across DecodeWorkers / ShardWorkers through the engine's blocked
	// merge. False selects the buffered tier: the engine also keeps the
	// admitted rows (extend.MatrixInput.Rows) and runs the request
	// buffered and serially.
	Streamable bool
	// WeightAware declares that Finalize computes a weighted matrix:
	// on a slot where a row weight is in force the engine folds it into
	// the co-moment (and Rows) and reports its kind on
	// extend.MatrixInput.WeightKind. Undeclared, a weight in force on
	// the slot is refused with PULSE_EXTENSION_NOT_WEIGHT_AWARE.
	WeightAware bool
	// Accepts lists the member column types the operator reads; empty
	// means the built-in matrix member types.
	Accepts []encoding.FieldType
	// Params describes the operator's own params keys for the
	// manifest. The factory still decodes and validates them. The
	// engine owns missing, max_drop_share and fit on every matrix
	// operator: it decodes them as for a built-in and strips them from
	// the params the factory and Finalize see (under fit, Finalize gets
	// the bound vector fit's co-moment), so a ParamMeta naming one is
	// refused (PULSE_EXTENSION_PARAM_INVALID, reserved_matrix_param).
	Params []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook (extra fields read beyond the members). See FieldInputsFunc.
	FieldInputs FieldInputsFunc
	// PValues opts the operator into multiple-comparison correction:
	// it names the OutputKeys.Auxiliary key whose matrix holds per-cell
	// p-values (off-diagonal upper triangle of a square_symmetric one,
	// every cell of a rectangular one). A `multiplicity` block (the
	// spec's, the request's or Options.DefaultMultiplicity) then adjusts
	// them like a built-in inferential operator's auxiliary.p: the fold
	// writes <PValues>_adjusted and significant_adjusted beside it (with
	// PValues "p" exactly the built-ins' p_adjusted), predict counts them
	// in p_values, and the raw matrix never moves. Empty opts out: a
	// spec-level multiplicity block on the operator is refused.
	PValues string
	// OutputKeys names the MatrixResult parts the operator fills —
	// Primary's meaning (required) and the Auxiliary, Vectors and
	// Scalars keys, each a non-empty name listed once. Finalize may
	// fill only declared keys (a declared key it leaves unfilled is
	// fine); probe-validation at pulse.New finalizes the operator over
	// a synthetic co-moment and refuses an undeclared key or a missing
	// Primary with PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH.
	OutputKeys descriptor.MatrixOutputKeys
	// ComponentSchema declares the operator's own keys of its
	// Response.Components.Matrices entry (extend.MatrixOutput.Components),
	// emitted after the engine's matrix floor. Empty (zero Keys and
	// Mergeability) declares none; non-empty Keys need a Mergeability,
	// a non-empty Name each, listed once, and may not restate a floor key
	// (n, n_null, n_listwise_dropped, min_pair_n, max_pair_n,
	// sum_weights, n_eff, n_weight_invalid).
	ComponentSchema descriptor.ComponentSchema
}

// RegressionRegistration installs a custom REG_* regression model for
// Request.Regressions, authored against extend.Regression: a streaming
// model (extend.StreamingRegression — UpdateRow, Merge, Finalize) or a
// buffered one (extend.BufferedRegression — Fit over every row, free to
// iterate). The name follows the extension policy
// (REG_<NAMESPACE>_<NAME>; a built-in such as REG_OLS collides).
//
// The model returns a narrow extend.RegressionOutput — coefficients
// (each an estimate plus an optional standard error, statistic and
// p-value), optional fit statistics from the fixed set, an optional
// vcov and its declared Outputs — and the engine maps it onto
// types.RegressionResult, owning every engine figure: the name and
// type, n_obs, the weighted floor (sum_weights, n_eff), the
// multiplicity-adjusted p-values (a PValues model's coefficients
// carrying a p-value join a family), advisories and a per-slot failure (a model error
// coded PROCESSING_REGRESSION_INSUFFICIENT_DATA, _RANK_DEFICIENT,
// _NO_CONVERGE or _SEPARATION fails its slot alone; any other error
// fails the run). Admission is the engine's too: the model sees only
// rows whose target and every predictor are present (and, weighted,
// whose weight is valid and positive).
//
// pulse.New probes the factory against a small synthetic schema and
// fits it over a few synthetic rows (weighted too, for a WeightAware
// registration): a model that does not implement the declared form is
// PULSE_EXTENSION_STREAMABLE_MISMATCH, an output the registration does
// not allow PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, a factory or
// fit panic or error PULSE_EXTENSION_FACTORY_PANIC (a data-dependent
// regression refusal on the synthetic rows is tolerated).
type RegressionRegistration struct {
	Name        types.RegressionType
	Description string
	// Purpose is the optional plain-language guidance for the model —
	// what it is for, the intents it answers, when to use something
	// else. It is validated at pulse.New with the same rules as the
	// built-in tier; a broken rule fails with
	// PULSE_EXTENSION_PURPOSE_INVALID. Only its intent IDs reach the
	// manifest entry's intents list; nil declares no intents.
	Purpose *descriptor.Purpose
	// Interpretation optionally says how to read the model's outputs,
	// one entry per normalised output path: a figure the model can
	// report (coefficients.* / std_errors.* / statistics.* / p_values.*,
	// r2, adj_r2, residual_std_err, df, deviance, null_deviance,
	// pseudo_r2, converged_iters, n_obs) or outputs.<key> for a key its
	// OutputSchema declares. Any other path, or a broken rule (path
	// syntax, Means or Shared, Bands with a Convention), fails with
	// PULSE_EXTENSION_PURPOSE_INVALID at pulse.New.
	Interpretation []descriptor.Interpretation
	// DependsOn names the features this model needs, each an AND edge;
	// see AggregatorRegistration.DependsOn. Optional; extensions carry
	// no Since.
	DependsOn []string
	// Accepts lists the field types the model's target and predictors
	// accept; empty means the built-in regressions' set (the
	// analytics-numeric types). A field of any other type is refused
	// with PULSE_FIELD_TYPE_UNSUPPORTED at predict and at runtime alike.
	// Each entry a known, non-vector type listed once
	// (PULSE_EXTENSION_PARAM_INVALID, reason invalid_accepts).
	Accepts []encoding.FieldType
	// FieldInputs is the optional buffered-projection introspection
	// hook: the fields the model reads beyond its target, predictors and
	// weight (named in its params, say). See FieldInputsFunc; without
	// one a buffered request carrying the model decodes every field.
	FieldInputs FieldInputsFunc
	// Factory builds the model per regression slot. Required
	// (PULSE_EXTENSION_PARAM_INVALID, reason missing_factory).
	Factory extend.RegressionFactory
	// Streamable declares the form: true for an
	// extend.StreamingRegression, which streams wherever a built-in
	// regression would (no grouper, feature or two-pass attribute on the
	// request; no Resample, Selection or robust covariance on the slot)
	// and is otherwise fed the buffered rows one at a time; false for an
	// extend.BufferedRegression, which always runs buffered. The
	// declaration decides the path at runtime and in predict.
	Streamable bool
	// WeightAware declares that the model computes a weighted fit: on a
	// slot where a row weight is in force (slot, request or
	// pulse.Options.DefaultWeight) the factory sees it on spec.Weight
	// and every admitted row reports its valid, positive weight through
	// extend.Record.Weight(); rows with an invalid or zero weight never
	// reach the model, and the engine writes sum_weights / n_eff.
	// Undeclared, any weight in force on the slot — the instance default
	// included — is PULSE_EXTENSION_NOT_WEIGHT_AWARE; `weight: null` on
	// the slot opts out. A WeightAware registration is probed weighted
	// too.
	WeightAware bool
	// Vcov declares that the model can return its coefficient
	// covariance (extend.RegressionOutput.Vcov): a spec setting vcov:
	// true then gets it (and its correlation) on the result.
	// Undeclared, vcov: true is refused with PROCESSING_CONFIG, details
	// {regression, slot: "vcov"}, at predict and at runtime alike.
	Vcov bool
	// PValues declares that the model emits coefficient p-values
	// (extend.RegressionCoefficient.PValue), so its slot takes a
	// multiplicity block: the family is every coefficient carrying a
	// defined p (the intercept excluded unless the block names it), and
	// predict counts one p-value per non-intercept predictor in
	// p_values (basis lower_bound — the model may report more or
	// fewer). Under resample the engine writes the replicate p-values.
	// Undeclared (false), the model emits none: p_values is null on
	// every coefficient (under resample too), predict counts none, a
	// multiplicity block resolving to any method but none is refused
	// with PULSE_MULTIPLICITY_INVALID (details.key "method") at predict
	// and at runtime alike, and a fit that reports a finite p-value
	// anyway is PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (reason
	// undeclared_p_value) at pulse.New's probe and on every run.
	PValues bool
	// Penalty declares that the model reads the shared penalty /
	// alpha / l1_ratio slots of types.RegressionSpec itself (their
	// values and meaning are the model's). Undeclared, any of them set
	// is refused with PROCESSING_CONFIG, details {regression, slot:
	// "penalty"} (key names a companion), at predict and at runtime.
	Penalty bool
	// SupportsSelection declares that the model can be refit over a
	// predictor subset, so the engine's selection driver (forward /
	// backward / stepwise) may wrap it: every candidate is a fresh model
	// whose spec carries the candidate predictors, scored by AIC =
	// deviance + 2k or BIC = deviance + ln(n)·k on its fit's reported
	// RegressionFit.Deviance (−2·log-likelihood up to a constant the
	// candidates share; k its coefficient count, n its n_obs). A fit
	// that reports no deviance is
	// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (reason
	// missing_deviance), at pulse.New's probe and on any candidate.
	// Undeclared, selection / criterion are refused (PROCESSING_CONFIG
	// {regression, slot: "selection"}) at predict and at runtime alike.
	SupportsSelection bool
	// CovarianceTypes declares the covariance.type values the model
	// computes itself (it reads spec.Covariance): a covariance block
	// whose type (absent = classical) is listed reaches the model after
	// the shared shape rule; any other is refused with
	// PROCESSING_CONFIG {regression, slot: "covariance"}. Each entry a
	// known type listed once (PULSE_EXTENSION_PARAM_INVALID, reason
	// invalid_covariance_type).
	//
	// Every other shared slot is fixed: the built-ins' own knobs
	// (family, link, max_iters, tol, prior*, credible_level) are refused
	// with PROCESSING_CONFIG {regression, slot}; save follows Scorer;
	// weight follows WeightAware; multiplicity follows PValues; resample
	// (with jackknife_groups, jackknife_by, bootstrap_iters) is honoured
	// unless NoResample opts out; params, conf_level and rng_seed are
	// always honoured. conf_level (0 < c
	// < 1, 0.95 unset) sets the engine's Wald intervals ci_lower /
	// ci_upper = estimate ∓ q·SE on every coefficient with a standard
	// error (null where it is null): q the Student-t quantile on the
	// fit's reported RegressionFit.DF — REG_OLS's rule, so an OLS-shaped
	// model reporting its residual df matches REG_OLS — else the
	// standard normal (REG_GLM's Wald rule), and the normal on the
	// replicate SE under resample, as every built-in. Resample runs the engine's jackknife / bootstrap
	// driver around the model — every replicate a fresh model refit
	// over a row subset, drawn exactly as for REG_OLS — and replaces
	// each coefficient's standard error with the replicate one, its
	// statistic with the Wald z, its p-value with the Wald-z
	// (jackknife) or percentile (bootstrap) p (PValues only) and its
	// interval with the normal one on the replicate SE (the result's df
	// then absent, as on a built-in); a replicate reporting
	// other coefficients than the full fit is
	// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (reason
	// replicate_coefficients). Limits.RequestTimeout is read before
	// every refit.
	CovarianceTypes []types.CovarianceType
	// NoResample opts the model out of the engine's resample driver —
	// for a model a row-subset refit would break (one that reads row
	// order, say) or make prohibitively slow (a sampler refit
	// bootstrap_iters times). Set, resample and its knobs
	// (jackknife_groups, jackknife_by, bootstrap_iters) are refused
	// with PROCESSING_CONFIG {regression, slot: "resample"} (key names a
	// set knob) at predict and at runtime alike, and the manifest row
	// lists no resample slot; rng_seed still reaches the factory
	// (spec.RNGSeed) for the model's own use. Unset (false), every
	// model accepts resample.
	NoResample bool
	// Params declares the keys of the slot's own params object
	// (types.RegressionSpec.Params), which the factory reads off the
	// spec. Declared, a params key outside the list is refused
	// PULSE_REQUEST_UNKNOWN_FIELD at regressions[i].params.<key>, at
	// predict and at runtime alike; undeclared (nil), params pass
	// through unchecked and predict notes it
	// (PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED). Validated like every
	// category's ParamMeta (PULSE_EXTENSION_PARAM_INVALID).
	Params []ParamMeta
	// OutputSchema declares the keys the model may fill in
	// extend.RegressionOutput.Outputs (types.RegressionResult.Outputs):
	// each a unique non-empty Key with a JSONType from the ParamMeta set
	// (PULSE_EXTENSION_PARAM_INVALID, reason invalid_output_key,
	// otherwise). A filled key it does not declare is
	// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH at pulse.New and on
	// every run; a declared key left unfilled is fine.
	OutputSchema []RegressionOutputMeta
	// Scorer optionally builds a fitted model's row scorer from the fit
	// alone (extend.FittedRegression: its coefficients and Outputs), so
	// ATTR_REG_PREDICT scores the model's fits — a Compose `fits` entry,
	// or a saved model file reloaded through `models` — and `save` may
	// write one (model_format_version 3, carrying the Outputs). The
	// scorer scores the whole model: params.terms and a lower / upper
	// params.bound are refused on it. Nil: the fits run, but scoring one
	// is refused with PULSE_MODEL_SCORE_INVALID (reason no_scorer) and
	// save with PROCESSING_CONFIG {regression, slot: "save"}, at predict
	// and at runtime alike. pulse.New probes it over the probe fit; a
	// panic or error is PULSE_EXTENSION_FACTORY_PANIC (stage scorer).
	Scorer extend.RegressionScorerFactory
}

// RegressionOutputMeta declares one key of a regression model's
// Outputs: its name, the JSON type of its value (string, number,
// boolean, array or object) and a description. It is the manifest's
// shape (descriptor.RegressionOutputMeta, the extension regression
// row's output_schema).
type RegressionOutputMeta = descriptor.RegressionOutputMeta

// ExprFunction describes a function callable from runtime expressions
// (ATTR_FORMULA / FILTER_EXPRESSION). At pulse.New time the Fn value
// is registered with the expr-lang engine under Name.
type ExprFunction struct {
	Name        string
	Description string
	// Signature is a human-readable signature surfaced in the
	// manifest and in predict error messages when typecheck fails
	// (e.g. `rank_familiarity(value float64, total_pop bool) float64`).
	Signature string
	// Fn is the Go function. expr-lang accepts any func value;
	// the runtime resolves argument types against the call site.
	Fn any
	// Pure declares the function is side-effect-free and depends
	// only on its arguments. Reserved for a future memoization
	// optimisation; declaring it has no runtime effect today.
	Pure bool
}

// LookupTable exposes a static keyed map to the runtime expression
// environment. Exactly one of Rows / Lookup must be non-nil.
//
// Rows is the simple path — a composite-key map where the caller of
// lookup() supplies the joined key. Lookup is the escape hatch for
// embedders that compose keys, perform partial-match fallback, or
// pull from an external store.
type LookupTable struct {
	Description string
	// Rows is the canonical static map. The map key is the joined
	// composite key (caller joins arguments with the table's
	// configured separator, conventionally "|").
	Rows map[string]float64
	// Lookup is the function-driven accessor. Returns the value
	// and true on hit; the second return is the miss signal. An
	// error from Lookup surfaces as PULSE_LOOKUP_MISS with the
	// embedder's message attached.
	Lookup func(keys ...string) (value float64, ok bool, err error)
}

// LabelTable exposes an ID→label map for display-time categorical
// translation. Exactly one of Rows / Lookup must be non-nil.
//
// Rows is the simple path — a string→string map keyed by the resolved
// categorical dictionary value. Lookup is the escape hatch for tables
// that wrap an external store (e.g. a code-system service) or that
// compose the label from multiple sources.
//
// LabelTable is intentionally distinct from LookupTable: LookupTable
// returns float64 for the numeric expression environment, LabelTable
// returns string for output rendering. Conflating them would force
// every embedder to choose one shape over the other; the two surfaces
// are independent.
type LabelTable struct {
	Description string
	// Rows is the canonical static map. Key is the categorical's
	// resolved dictionary value (the on-disk string the field
	// presents, e.g. "US"); value is the display label (e.g.
	// "United States").
	Rows map[string]string
	// Lookup is the function-driven accessor. Returns the label and
	// true on hit; the second return is the miss signal. An error
	// from Lookup surfaces as PULSE_LABEL_LOOKUP_MISS with the
	// embedder's message attached.
	Lookup func(key string) (label string, ok bool, err error)
	// Order is the table's entry order, optional: the keys in the order
	// the table lists them. A `.sav` export bound to the table codes the
	// categorical by it (entry i -> code i+1); without it the codes follow
	// sorted dictionary order. A table loaded from PULSE_LABEL_TABLES_DIR
	// gets its file's key order. Each entry appears once and, on a Rows
	// table, is one of its keys (PULSE_EXTENSION_PARAM_INVALID at New).
	Order []string
}

// RangeTable is a named, ordered set of labeled ranges. It is the
// reusable, register-once form of the inline range list authored on a
// GROUP_DATE_RANGES grouper or FILTER_DATE_RANGES filter (a DATE table,
// Ranges) or on a GROUP_NUMERIC_RANGES grouper (a NUMERIC table,
// NumericRanges): a caller registers the table under a name and
// references it by that name from any number of requests.
//
// A table is typed by its bounds and carries exactly one of Ranges /
// NumericRanges (both is PULSE_EXTENSION_PARAM_INVALID at New). Ranges
// are ISO date literals, inclusive on both sides; NumericRanges are
// numbers, half-open [start, end). nil / empty is an open bound. A range
// operator naming a table of the other kind is refused with
// PULSE_RANGE_TABLE_KIND_MISMATCH. The set is validated at pulse.New
// time via the shared range-compilation pass — overlap, duplicate label,
// empty set, and unparseable / inverted boundaries each surface the
// matching PULSE_RANGE_* code. Order is preserved as authored; the
// compiler sorts internally for deterministic matching.
type RangeTable struct {
	// Description is an optional human-readable summary surfaced in the
	// manifest projection.
	Description string
	// Ranges is a date table's ordered list of labeled date ranges.
	Ranges []DateRangeSpec
	// NumericRanges is a numeric table's ordered list of labeled
	// half-open numeric ranges.
	NumericRanges []NumericRangeSpec
}

// Kind is the table's kind, typed by its bounds: "numeric" when it
// carries NumericRanges, else "date".
func (t RangeTable) Kind() string {
	if len(t.NumericRanges) > 0 {
		return "numeric"
	}
	return "date"
}

// Population is one registered population: a saved vector fit's
// population file document, registered under its
// Extensions.Populations key (Options.PopulationsDir names it by file
// stem). The stored state is bound exactly as a `populations` entry
// loading the same file binds it.
type Population struct {
	// File is the encoded population file document
	// (population_format_version, labels, members, basis, counts, mean,
	// m2, provenance) — the bytes a vector fit's `save` writes.
	File []byte
}

// reservedExtensionNamespaces is the set of namespace segments
// embedders may not use. Built-ins reserve these to keep the
// "registration source" annotation in the manifest unambiguous.
var reservedExtensionNamespaces = map[string]struct{}{
	"BUILTIN":  {},
	"STANDARD": {},
	"CORE":     {},
	"PULSE":    {},
}
