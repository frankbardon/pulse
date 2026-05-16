package pulse

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

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

// reservedExtensionNamespaces is the set of namespace segments
// embedders may not use. Built-ins reserve these to keep the
// "registration source" annotation in the manifest unambiguous.
var reservedExtensionNamespaces = map[string]struct{}{
	"BUILTIN":  {},
	"STANDARD": {},
	"CORE":     {},
	"PULSE":    {},
}
