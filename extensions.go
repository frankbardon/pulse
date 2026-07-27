package pulse

import (
	"encoding/json"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
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
// The instance passed in is the value the registration's Factory
// returned (after Aggregate / Finalize); the func should not call
// Aggregate / UpdateRow / Finalize on it. The returned map's keys must
// be a SUBSET of the registration's ComponentSchema.Keys (universal-
// floor keys allowed but not required) — probe-validation enforces
// this contract at pulse.New time and runtime mismatches surface as
// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH.
type AggregatorComponentsFunc func(instance processing.Aggregator) (map[string]any, error)

// GrouperComponentsFunc is the grouper-shape sibling of
// AggregatorComponentsFunc. The orchestrator invokes the func ONCE
// after the grouper's terminal partition pass and routes the result
// onto Response.Components.Groupers[i].Operator. The universal floor
// ({total_n, n_null}) is filled unconditionally by the orchestrator.
// Returning (nil, nil) is the canonical signal for "no operator-
// specific keys".
type GrouperComponentsFunc func(instance processing.Grouper) (map[string]any, error)

// FiltererComponentsFunc is the filterer-shape sibling. In v1 every
// built-in filterer leaves its operator-specific payload empty
// (uniform floor of {n_in, n_out, n_null_input}); extensions may opt
// in by supplying a ComponentSchema + this emitter. The instance passed
// in is the FiltererBuilder the registration's Factory returned.
type FiltererComponentsFunc func(instance processing.FiltererBuilder) (map[string]any, error)

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
	Aggregators        []AggregatorRegistration
	Attributes         []AttributeRegistration
	Filterers          []FiltererRegistration
	Groupers           []GrouperRegistration
	Windows            []WindowRegistration
	Features           []FeatureRegistration
	Tests              []TestRegistration
	SynthDistributions []DistributionRegistration

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

	// LabelTables expose static ID→label maps used by the output-time
	// categorical-label overlay (per-request Labels slot on Request /
	// SampleRequest / FacetRequest / ExportJob). Each table accepts a
	// single string key (the categorical's resolved dictionary value)
	// and returns a display string. Tables are read-only after
	// pulse.New and do not affect filter / formula / sort semantics —
	// only end-user output rendering.
	LabelTables map[string]LabelTable

	// RangeTables expose named sets of labeled date ranges so a
	// GROUP_DATE_RANGES grouper or FILTER_DATE_RANGES filter can
	// reference an ordered {label, start, end} bucketing by name
	// instead of authoring it inline per request. Each table is
	// validated at pulse.New time via the shared range-compilation
	// pass (the same overlap / duplicate-label / empty / invalid-
	// boundary rules the inline path enforces); a table that nothing
	// references is valid and simply visible in the manifest. Tables
	// are read-only after pulse.New. Operator→table resolution is
	// wired separately; this slot only registers the tables.
	RangeTables map[string]RangeTable
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
}

// AggregatorRegistration installs a custom AGG_* operator. The factory
// must obey processing.AggregatorFactory: it builds a fresh Aggregator
// per Process call against the supplied Aggregation spec + schema.
//
// When Streamable=true the factory MUST return a value that also
// implements processing.OnlineAggregator. Probe-validation at
// registration time enforces the contract via
// PULSE_EXTENSION_STREAMABLE_MISMATCH.
type AggregatorRegistration struct {
	Name        types.AggregationType
	Description string
	Factory     processing.AggregatorFactory
	Streamable  bool
	Accepts     []encoding.FieldType
	Params      []ParamMeta
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
	// extra keys. When ComponentSchema.Keys is non-empty, the
	// ComponentsFunc emitter MUST be supplied and its returned map
	// keys MUST be a subset of the declared key set; probe-validation
	// enforces both at pulse.New time.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional sibling-interface emitter. When
	// set, the orchestrator routes the returned map onto
	// Response.Components.Aggregations[i].Operator after the
	// aggregator's Aggregate / Finalize call terminates. Nil is the
	// floor-only path (universal floor fills the entire payload).
	// Mirrors processing.MetaAggregator.Components() in shape.
	ComponentsFunc AggregatorComponentsFunc
}

// AttributeMode declares which streaming tier an attribute factory
// promises to satisfy.
type AttributeMode string

const (
	// AttributeModeRowLocal — factory returns a processing.RowLocalAttribute.
	// Equivalent to built-in ATTR_FORMULA / ATTR_DATE_PART.
	AttributeModeRowLocal AttributeMode = "row_local"
	// AttributeModeTwoPass — factory returns a processing.TwoPassAttribute.
	// Equivalent to ATTR_ZSCORE / ATTR_TSCORE / ATTR_NORMALIZED.
	AttributeModeTwoPass AttributeMode = "two_pass"
	// AttributeModeBuffered — factory returns a plain
	// processing.AttributeComputer; no streaming. Equivalent to
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

// AttributeRegistration installs a custom ATTR_* operator.
type AttributeRegistration struct {
	Name        types.AttributeType
	Description string
	Factory     processing.AttributeFactory
	Mode        AttributeMode
	Accepts     []encoding.FieldType
	Emits       AttributeEmitType
	Params      []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
}

// FiltererRegistration installs a custom FILTER_* operator. Filterers
// are always row-local streamable today — every built-in filterer
// evaluates per-row. The runtime records custom filterers as
// streamable; a buffered-filterer registration shape lands the day
// Pulse grows a buffered-filterer path.
type FiltererRegistration struct {
	Name        types.FiltererType
	Description string
	Factory     processing.FiltererFactory
	Accepts     []encoding.FieldType
	Params      []ParamMeta
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
	// declaring a non-empty ComponentSchema alongside a ComponentsFunc.
	// Mismatch between declared schema and emitted keys is rejected at
	// pulse.New time.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional sibling-interface emitter. When
	// set, the orchestrator routes the returned map onto
	// Response.Components.Filterers[i].Operator after the filter pass
	// terminates. Nil is the floor-only path (the orchestrator's
	// universal floor is the entire payload). Mirrors
	// processing.MetaFilterer.Components() in shape.
	ComponentsFunc FiltererComponentsFunc
}

// GrouperRegistration installs a custom GROUP_* operator. Set
// Streamable=true when the factory returns a processing.Grouper that
// also implements processing.StreamingGrouper (KeyForRow).
type GrouperRegistration struct {
	Name        types.GroupType
	Description string
	Factory     processing.GrouperFactory
	Streamable  bool
	Accepts     []encoding.FieldType
	Params      []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
	// ComponentSchema declares the per-operator components contract
	// surfaced through Response.Components.Groupers[i].Operator.
	// Leaving the schema empty makes the operator
	// floor-only — the orchestrator emits the universal floor
	// ({total_n, n_null}) without operator-specific extras. When
	// ComponentSchema.Keys is non-empty, the ComponentsFunc emitter
	// MUST be supplied and its keys MUST match the declared schema.
	ComponentSchema descriptor.ComponentSchema
	// ComponentsFunc is the optional sibling-interface emitter. When
	// set, the orchestrator routes the returned map onto
	// Response.Components.Groupers[i].Operator after the grouper's
	// terminal partitioning pass. Nil is the floor-only path. Mirrors
	// processing.MetaGrouper.Components() in shape.
	ComponentsFunc GrouperComponentsFunc
}

// WindowRegistration installs a custom WIN_* operator. Window
// operators run buffered today; the runtime records custom windows
// as non-streamable. A streaming-window shape lands when the
// runtime gains a streaming-window pipeline.
type WindowRegistration struct {
	Name        types.WindowType
	Description string
	Factory     window.WindowFactory
	Accepts     []encoding.FieldType
	Params      []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook. See FieldInputsFunc.
	FieldInputs FieldInputsFunc
}

// FeatureRegistration installs a custom FEAT_* operator. Set
// Streamable=true when the factory returns a feature.Computer that
// also implements feature.StreamingComputer.
type FeatureRegistration struct {
	Name        types.FeatureType
	Description string
	Factory     feature.Factory
	Streamable  bool
	Accepts     []encoding.FieldType
	Params      []ParamMeta
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
type TestRegistration struct {
	Name        types.TestType
	Description string
	Tier        TestTier
	RowFactory  processing.RowTestFactory
	PostFactory processing.PostTestFactory
	Streamable  bool
	Accepts     []encoding.FieldType
	Params      []ParamMeta
	// FieldInputs is the optional buffered-projection introspection
	// hook for tier-1 row tests. Tier-2 post-tests run on materialized
	// result rows rather than source records, so projection doesn't
	// apply — leave nil for tier-2 registrations.
	FieldInputs FieldInputsFunc
}

// DistributionRegistration installs a custom synthetic-data
// distribution kind. The factory shape is finalised alongside the
// synth wiring in a later phase; for now this registration is
// validated for naming + duplicates only and the Factory field is
// held opaque.
type DistributionRegistration struct {
	Name        string
	Description string
	// Factory is the sampler-construction callback. Its concrete
	// signature is finalised when the synth distribution overlay
	// lands; until then this field is held opaque to keep the API
	// shape stable.
	Factory any
	Params  []ParamMeta
}

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
}

// RangeTable is a named, ordered set of labeled date ranges. It is the
// reusable, register-once form of the inline range list authored on a
// GROUP_DATE_RANGES grouper or FILTER_DATE_RANGES filter: a caller
// registers the table under a name and references it by that name from
// any number of requests.
//
// Ranges are the ordered {label, start, end} entries. Start / End are
// ISO date literals (nil / empty means an open bound; both bounds are
// inclusive). The set is validated at pulse.New time via the shared
// range-compilation pass — overlap, duplicate label, empty set, and
// unparseable / inverted boundaries each surface the matching
// PULSE_RANGE_* code. Order is preserved as authored; the compiler
// sorts internally for deterministic matching.
type RangeTable struct {
	// Description is an optional human-readable summary surfaced in the
	// manifest projection.
	Description string
	// Ranges is the ordered list of labeled date ranges. Must be
	// non-empty and pass the shared validation pass.
	Ranges []DateRangeSpec
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
