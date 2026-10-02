package processing

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/feature"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
)

// FieldInputsFunc reports the additional source-field names an
// extension operator reads beyond the spec's explicit Field/Field2/
// PartitionBy/etc. references. raw carries the operator's Params
// block (may be nil for filterers). Return value is consumed by the
// buffered-projection extractor; nil/empty means "no extra fields."
type FieldInputsFunc func(raw json.RawMessage) []string

// ExtensionRegistry holds per-Service overlays for every operator
// category that supports the public extension API. A nil receiver is
// the no-extension case — all Lookup methods fall through to the
// built-in package-level registries.
//
// The overlay maps are read-only after pulse.New populates them; the
// runtime never mutates them. Two Service instances with different
// Extensions inputs hold distinct ExtensionRegistry values, so a
// process can host more than one Pulse with disjoint extension sets.
type ExtensionRegistry struct {
	Aggregators map[types.AggregationType]AggregatorFactory
	Attributes  map[types.AttributeType]AttributeFactory
	Filterers   map[types.FiltererType]FiltererFactory
	Groupers    map[types.GroupType]GrouperFactory
	Windows     map[types.WindowType]window.WindowFactory
	Features    map[types.FeatureType]feature.Factory
	RowTests    map[types.TestType]RowTestFactory
	PostTests   map[types.TestType]PostTestFactory

	// Streamable is the per-(category, name) override consulted by
	// IsStreamable. Built-in entries are not stored here; the
	// fallback path consults the per-type Streamable() method.
	//
	// For an extension operator this is the DECLARED registration flag
	// and it is authoritative: Processor.canStream routes aggregators,
	// groupers, attributes and tier-1 row tests on it, never on the
	// built-in per-type tables (which know no extension name) nor on
	// whichever optional interface the constructed value carries.
	// Probe-validation at pulse.New guarantees a true entry's factory
	// returns the matching streaming interface.
	Streamable map[string]bool

	// Mergeable is the per-(category, name) merge declaration consulted
	// by IsMergeable, keyed like Streamable. Built-in entries are not
	// stored here; the fallback consults the per-type Mergeable()
	// method. For an extension aggregator or grouper this is the
	// DECLARED pulse.AggregatorRegistration.Mergeable /
	// pulse.GrouperRegistration.Mergeable flag, probe-validated at
	// pulse.New (PULSE_EXTENSION_MERGEABLE_MISMATCH) so a true entry's
	// adapted value implements MergeableAggregator / MergeableGrouper. Absent
	// reads as the built-in answer, which is false for any name the
	// built-in tables do not know.
	Mergeable map[string]bool

	// MarginReducibility is the per-aggregator crosstab margin class
	// declared by pulse.AggregatorRegistration.MarginReducibility — the
	// extension half of types.AggregationType.MarginReducibility().
	// Consulted through AggregatorMarginReducibility by the fused
	// crosstab gate. A registered aggregator always has an entry (empty
	// included, which reads as types.MarginRecompute), so a missing key
	// means "not an extension" and falls back to the built-in table.
	// Probe-validated at pulse.New
	// (PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH): a fusable class is
	// only ever recorded alongside a true Mergeable entry.
	MarginReducibility map[types.AggregationType]types.MarginReducibility

	// TwoPassAttributes records which extension attributes declared the
	// two-pass streaming tier (pulse.AttributeModeTwoPass) — the
	// extension half of the built-in two-pass set (ZSCORE, TSCORE,
	// NORMALIZED, REG_*). A two-pass attribute takes the PrePass +
	// Finalize streaming drive and, like its built-in siblings, does not
	// compose with grouped or feature streaming. Absent reads false.
	TwoPassAttributes map[types.AttributeType]bool

	// FansOut is the per-grouper fan-out declaration recorded from
	// pulse.GrouperRegistration.FansOut — true when one record can
	// land in MORE THAN ONE bucket, so the bucket counts SUM to more
	// than the record total. Grouper-only: no other category fans out.
	//
	// Built-in entries are NOT stored here; types.GroupType.FansOut()
	// answers those, and types.CheckPairwiseSlabPartitionWith owns the
	// built-in-first resolution order shared with the predict arm.
	// A registered grouper always has an entry (false included), so a
	// missing key means "registered nowhere", not "declared false".
	//
	// Probe-validated against processing.MultiKeyStreamingGrouper at
	// pulse.New (PULSE_EXTENSION_FANOUT_MISMATCH), so the runtime may
	// trust the declaration without reconstructing the grouper — which
	// matters because the overlay hook has no schema in reach.
	FansOut map[types.GroupType]bool

	// ExprFunctions are merged into the runtime expression environment
	// used by ATTR_FORMULA and FILTER_EXPRESSION. Each entry is
	// callable from a request expression under its declared Name. The
	// shape mirrors pulse.ExprFunction (pulse package can't be
	// imported here without a cycle) — the public pulse.ExprFunction
	// is the canonical embedder-facing surface.
	ExprFunctions []ExprFunction

	// LookupTables are exposed to the runtime expression environment
	// via the built-in lookup() function. Mirrors pulse.LookupTable.
	LookupTables map[string]LookupTable

	// LabelTables are the string-valued ID→label maps consumed by the
	// output-time label resolver. Mirrors pulse.LabelTable. Indexed
	// by the user-facing table name.
	LabelTables map[string]LabelTable

	// RangeTables are the named labeled-date-range sets a
	// GROUP_DATE_RANGES grouper or FILTER_DATE_RANGES filter may
	// reference by name. Mirrors pulse.RangeTable. Indexed by the
	// user-facing table name. Each entry's ranges are validated at
	// pulse.New time via compileDateRanges; operator→table resolution
	// consumes this map.
	RangeTables map[string]RangeTable

	// FieldInputs is the per-(category, name) field-introspection
	// callback consulted by the buffered-projection extractor
	// (NeededFields). When the registry contains an entry for an
	// extension operator the projection extractor calls the callback
	// with the operator's raw Params; otherwise the extractor widens
	// the projection to "every field" so the runtime stays correct
	// for embedders that haven't opted in.
	FieldInputs map[string]FieldInputsFunc

	// hidden is the instance feature-set predicate installed by
	// WithHidden: true for an operator name that resolves on this
	// build but is not offered by the instance's feature profile. Every
	// Lookup* and every streamability / mergeability fact consults it
	// FIRST, so a hidden name takes exactly the path a never-registered
	// name takes — its site's own unknown-name error, no re-wording and
	// no pre-check. Nil hides nothing.
	hidden func(name string) bool
}

// WithHidden returns a registry that resolves exactly like r except that
// every operator name hidden reports true for answers as never
// registered. r is not mutated: the result is a shallow copy sharing r's
// read-only maps. A nil r yields a registry carrying only the predicate
// (no extension operators), which every method treats like a nil
// registry for any name the predicate does not hide. A nil hidden
// returns r unchanged, so an instance without a feature profile keeps
// the exact registry it had.
func (r *ExtensionRegistry) WithHidden(hidden func(name string) bool) *ExtensionRegistry {
	if hidden == nil {
		return r
	}
	var out ExtensionRegistry
	if r != nil {
		out = *r
	}
	out.hidden = hidden
	return &out
}

// isHidden reports whether name is hidden by the instance feature set.
// Nil-receiver-safe: a nil registry hides nothing.
func (r *ExtensionRegistry) isHidden(name string) bool {
	return r != nil && r.hidden != nil && r.hidden(name)
}

// ExprFunction is the runtime-side mirror of pulse.ExprFunction. The
// processor injects every entry into the expr-lang environment when
// compiling ATTR_FORMULA / FILTER_EXPRESSION expressions.
type ExprFunction struct {
	Name string
	Fn   any
}

// LookupTable is the runtime-side mirror of pulse.LookupTable. The
// expr environment's lookup() built-in consults this map; exactly one
// of Rows or Lookup is populated per table.
type LookupTable struct {
	Rows   map[string]float64
	Lookup func(keys ...string) (float64, bool, error)
}

// LabelTable is the runtime-side mirror of pulse.LabelTable. The
// label resolver consults this map when a request supplies a
// LabelBinding referencing the table by name; exactly one of Rows or
// Lookup is populated per table.
type LabelTable struct {
	Rows   map[string]string
	Lookup func(key string) (string, bool, error)
}

// RangeTable is the runtime-side mirror of pulse.RangeTable. It holds
// the ordered labeled date-range specs registered under a table name.
// The specs are validated at pulse.New time; a consumer resolving a
// table by name compiles them into a *dateRangeSet via compileDateRanges
// (validation is idempotent — the compile cannot fail post-registration).
type RangeTable struct {
	Ranges []DateRangeSpec
}

// LookupRangeTable returns the RangeTable registered under name, and the
// standard "found" signal. Nil-safe: a nil registry or absent name
// returns ok=false.
func (r *ExtensionRegistry) LookupRangeTable(name string) (RangeTable, bool) {
	if r == nil {
		return RangeTable{}, false
	}
	t, ok := r.RangeTables[name]
	return t, ok
}

// ExtensionAware is the optional interface that AttributeComputer /
// FiltererBuilder instances implement when they want the Processor
// to inject the live ExtensionRegistry after construction. The
// formula attribute and expression filterer use this hook to expose
// embedder-registered ExprFunctions and LookupTables to the expr
// environment.
type ExtensionAware interface {
	SetExtensions(r *ExtensionRegistry)
}

// ApplyGrouperExtensions injects the live registry into a freshly
// constructed grouper when it implements ExtensionAware. Unlike filterers
// (which have a factory→SetExtensions→Build lifecycle), grouper factories
// return a ready Grouper with no post-construction Build step, so this hook
// is threaded through every grouper construction site. It is the mechanism
// GROUP_DATE_RANGES uses to resolve a named `table:` source (the grouper
// factory signature cannot reach the ExtensionRegistry). No-op for every
// grouper that does not implement ExtensionAware, so existing groupers are
// unaffected. Nil-registry-safe; returns g for call-site convenience.
func ApplyGrouperExtensions(g Grouper, exts *ExtensionRegistry) Grouper {
	if aware, ok := g.(ExtensionAware); ok {
		aware.SetExtensions(exts)
	}
	return g
}

// BuildFilters compiles a slice of types.Filterer into runtime
// FilterFuncs against the given schema. Mirrors the per-Processor
// helper so non-Processor consumers (FacetSchema, Sample variants) can
// reuse the same factory + extension semantics. Returns nil when
// filterers is empty.
func BuildFilters(filterers []*types.Filterer, schema *encoding.Schema, exts *ExtensionRegistry) ([]FilterFunc, error) {
	if len(filterers) == 0 {
		return nil, nil
	}
	out := make([]FilterFunc, 0, len(filterers))
	for _, f := range filterers {
		factory, ok := exts.LookupFilterer(f.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"unknown filter type: "+string(f.Type))
		}
		builder := factory()
		if aware, ok := builder.(ExtensionAware); ok {
			aware.SetExtensions(exts)
		}
		fn, err := builder.Build(f, schema)
		if err != nil {
			return nil, err
		}
		out = append(out, wrapFilterPrecompute(fn, f, schema, exts))
	}
	return out, nil
}

// StreamabilityKey is the canonical map key for ExtensionRegistry.Streamable.
// Format: "<category>|<name>". The category strings are the lowercase
// singular forms used in error details and the manifest: aggregator,
// attribute, filterer, grouper, window, feature, test.
func StreamabilityKey(category, name string) string {
	return category + "|" + name
}

// LookupAggregator returns the factory for an aggregator type. The
// overlay map wins over the built-in registry. The boolean second
// return is the standard "found" signal.
func (r *ExtensionRegistry) LookupAggregator(t types.AggregationType) (AggregatorFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.Aggregators[t]; ok {
			return f, true
		}
	}
	f, ok := aggregatorRegistry[t]
	return f, ok
}

// LookupAttribute returns the factory for an attribute type. Overlay
// wins.
func (r *ExtensionRegistry) LookupAttribute(t types.AttributeType) (AttributeFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.Attributes[t]; ok {
			return f, true
		}
	}
	f, ok := attributeRegistry[t]
	return f, ok
}

// LookupFilterer returns the factory for a filterer type. Overlay wins.
func (r *ExtensionRegistry) LookupFilterer(t types.FiltererType) (FiltererFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.Filterers[t]; ok {
			return f, true
		}
	}
	f, ok := filtererRegistry[t]
	return f, ok
}

// LookupGrouper returns the factory for a grouper type. Overlay wins.
func (r *ExtensionRegistry) LookupGrouper(t types.GroupType) (GrouperFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.Groupers[t]; ok {
			return f, true
		}
	}
	f, ok := grouperRegistry[t]
	return f, ok
}

// LookupWindow returns the factory for a window type. Overlay wins.
// Falls through to window.Lookup for built-ins.
func (r *ExtensionRegistry) LookupWindow(t types.WindowType) (window.WindowFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.Windows[t]; ok {
			return f, true
		}
	}
	return window.Lookup(t)
}

// LookupRegression returns the factory for a regression type. Only
// built-ins register regressions; a type the instance feature set hides
// misses exactly like one nothing registered, so regression.BuildWith
// raises its own "unknown regression type" error.
func (r *ExtensionRegistry) LookupRegression(t types.RegressionType) (regression.Factory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	return regression.Lookup(t)
}

// hiddenOverlayRoute is the kind a hidden overlay kind is ROUTED as: a
// value no handler table, kind switch or types-side catalog knows.
const hiddenOverlayRoute types.OverlayKind = ""

// overlayRoute returns the kind every kind-keyed decision on an overlay
// spec is taken on: the authored kind, or hiddenOverlayRoute when the
// instance feature set hides it. The five per-host handler tables, the
// OVERLAY_FORMULA special case, the streamability downgrade and every
// per-kind pre-dispatch gate (Level/Within, pairwise and panel slab
// partitions, compose MATRIX-shape) key on the route, so a hidden kind
// takes exactly the branches a never-registered kind takes. Messages
// and details keep naming the authored kind. Nil-receiver-safe.
func (r *ExtensionRegistry) overlayRoute(kind types.OverlayKind) types.OverlayKind {
	if r.isHidden(string(kind)) {
		return hiddenOverlayRoute
	}
	return kind
}

// LookupFeature returns the factory for a feature type. Overlay wins.
// Falls through to feature.Lookup for built-ins.
func (r *ExtensionRegistry) LookupFeature(t types.FeatureType) (feature.Factory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.Features[t]; ok {
			return f, true
		}
	}
	return feature.Lookup(t)
}

// LookupRowTest returns the tier-1 row-test factory for a test type.
// Overlay wins.
func (r *ExtensionRegistry) LookupRowTest(t types.TestType) (RowTestFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.RowTests[t]; ok {
			return f, true
		}
	}
	f, ok := rowTestRegistry[t]
	return f, ok
}

// LookupPostTest returns the tier-2 post-test factory for a test type.
// Overlay wins.
func (r *ExtensionRegistry) LookupPostTest(t types.TestType) (PostTestFactory, bool) {
	if r.isHidden(string(t)) {
		return nil, false
	}
	if r != nil {
		if f, ok := r.PostTests[t]; ok {
			return f, true
		}
	}
	f, ok := postTestRegistry[t]
	return f, ok
}

// GrouperFanOut reports the fan-out fact declared for an
// extension-registered grouper, and whether the registry carries a
// declaration for t at all. Nil-receiver-safe: ok=false.
//
// Built-in group types deliberately return ok=false — they are not in
// the map and types.GroupType.FansOut() is their authority. Callers
// go through types.CheckPairwiseSlabPartitionWith, which tries the
// built-in first and only then this resolver.
func (r *ExtensionRegistry) GrouperFanOut(t types.GroupType) (fansOut bool, ok bool) {
	if r == nil || r.FansOut == nil {
		return false, false
	}
	v, found := r.FansOut[t]
	return v, found
}

// ExtensionGroupFanOut adapts GrouperFanOut to the types-side resolver
// signature the pairwise slab-partition gate takes. Returns nil for a
// nil registry, which the gate reads as "no extension groupers".
//
// The gate consults types.ResolveBuiltinGroupFanOut BEFORE this
// resolver, so it would read a hidden built-in fan-out grouper as
// fanning out. That is unreachable for a hidden name: both slab gates
// (crosstab pairwise, Compose panel) run only once their host has
// materialised, and materialising it resolved every grouper through
// this registry — a hidden grouper has already failed there with its
// unknown-group-type error.
func (r *ExtensionRegistry) ExtensionGroupFanOut() types.ExtensionGroupFanOutFunc {
	if r == nil {
		return nil
	}
	return r.GrouperFanOut
}

// IsStreamable consults the Streamable overlay first, then the
// built-in per-type Streamable() method. Unknown categories return
// false. Nil-receiver-safe (built-in answers only). Processor.canStream
// reads every aggregator / grouper / attribute / tier-1 test fact
// through here, so an extension's declared flag is what routes it;
// predict reads the same declaration off the ExtensionsSnapshot.
func (r *ExtensionRegistry) IsStreamable(category, name string) bool {
	if r.isHidden(name) {
		return false
	}
	if r != nil && r.Streamable != nil {
		if v, ok := r.Streamable[StreamabilityKey(category, name)]; ok {
			return v
		}
	}
	switch category {
	case "aggregator":
		return types.AggregationType(name).Streamable()
	case "attribute":
		return types.AttributeType(name).Streamable()
	case "filterer":
		return types.FiltererType(name).Streamable()
	case "grouper":
		return types.GroupType(name).Streamable()
	case "window":
		return types.WindowType(name).Streamable()
	case "feature":
		return types.FeatureType(name).Streamable()
	case "test":
		return types.TestType(name).Streamable()
	}
	return false
}

// IsMergeable reports whether the (category, name) operator's running
// state folds across input partitions: the Mergeable overlay first (an
// extension's declaration), then the built-in per-type Mergeable()
// method. Only aggregators and groupers carry a merge fact; every other
// category answers false. Nil-receiver-safe (built-in answers only).
func (r *ExtensionRegistry) IsMergeable(category, name string) bool {
	if r.isHidden(name) {
		return false
	}
	if r != nil && r.Mergeable != nil {
		if v, ok := r.Mergeable[StreamabilityKey(category, name)]; ok {
			return v
		}
	}
	switch category {
	case "aggregator":
		return types.AggregationType(name).Mergeable()
	case "grouper":
		return types.GroupType(name).Mergeable()
	}
	return false
}

// AggregatorMarginReducibility reports the crosstab margin class of
// aggregator t: an extension's DECLARED class (empty reads as
// types.MarginRecompute, the not-fusable default), else the built-in
// per-type MarginReducibility(). Nil-receiver-safe (built-in answers
// only).
func (r *ExtensionRegistry) AggregatorMarginReducibility(t types.AggregationType) types.MarginReducibility {
	if r.isHidden(string(t)) {
		return types.MarginRecompute // the built-in table's answer for any unregistered name
	}
	if r != nil && r.MarginReducibility != nil {
		if v, ok := r.MarginReducibility[t]; ok {
			if v == "" {
				return types.MarginRecompute
			}
			return v
		}
	}
	return t.MarginReducibility()
}

// attributeRequiresTwoPass reports whether an attribute type takes the
// two-pass streaming drive: the built-in two-pass set, or an extension
// attribute that declared pulse.AttributeModeTwoPass. Nil-receiver-safe.
func (r *ExtensionRegistry) attributeRequiresTwoPass(t types.AttributeType) bool {
	if r.isHidden(string(t)) {
		return false
	}
	if requiresTwoPass(t) {
		return true
	}
	return r != nil && r.TwoPassAttributes[t]
}

// isExtensionAggregator reports whether t is an embedder-registered
// (overlay) aggregator rather than a built-in. Extension names cannot
// collide with built-ins (rejected at pulse.New), so overlay membership
// is the whole test. Used by the decimal128 dispatch: the built-in
// decimal table governs built-ins only, while an extension decides what
// to do with a decimal field itself.
func (r *ExtensionRegistry) isExtensionAggregator(t types.AggregationType) bool {
	if r == nil {
		return false
	}
	_, ok := r.Aggregators[t]
	return ok
}

// isExtensionAttribute reports whether t is an embedder-registered
// (overlay) attribute rather than a built-in. Nil-receiver-safe.
func (r *ExtensionRegistry) isExtensionAttribute(t types.AttributeType) bool {
	if r == nil {
		return false
	}
	_, ok := r.Attributes[t]
	return ok
}

// HasAggregator reports whether name resolves either via overlay or
// built-in registry. Same shape for the remaining categories.
func (r *ExtensionRegistry) HasAggregator(t types.AggregationType) bool {
	_, ok := r.LookupAggregator(t)
	return ok
}
func (r *ExtensionRegistry) HasAttribute(t types.AttributeType) bool {
	_, ok := r.LookupAttribute(t)
	return ok
}
func (r *ExtensionRegistry) HasFilterer(t types.FiltererType) bool {
	_, ok := r.LookupFilterer(t)
	return ok
}
func (r *ExtensionRegistry) HasGrouper(t types.GroupType) bool {
	_, ok := r.LookupGrouper(t)
	return ok
}
func (r *ExtensionRegistry) HasWindow(t types.WindowType) bool {
	_, ok := r.LookupWindow(t)
	return ok
}
func (r *ExtensionRegistry) HasFeature(t types.FeatureType) bool {
	_, ok := r.LookupFeature(t)
	return ok
}
func (r *ExtensionRegistry) HasRowTest(t types.TestType) bool {
	_, ok := r.LookupRowTest(t)
	return ok
}
func (r *ExtensionRegistry) HasPostTest(t types.TestType) bool {
	_, ok := r.LookupPostTest(t)
	return ok
}

// WindowFactories returns the overlay map for window operators, or
// nil when the registry has no entries. Safe to call on a nil
// receiver — the runtime treats nil as "no overlay" and falls
// through to the built-in window registry.
func (r *ExtensionRegistry) WindowFactories() map[types.WindowType]window.WindowFactory {
	if r == nil {
		return nil
	}
	return r.Windows
}

// FeatureFactories returns the overlay map for feature operators.
// Nil-receiver-safe.
func (r *ExtensionRegistry) FeatureFactories() map[types.FeatureType]feature.Factory {
	if r == nil {
		return nil
	}
	return r.Features
}

// FieldInputsFor consults the FieldInputs overlay for the operator
// identified by (category, name). Returns (inputs, true) when the
// registration supplied a callback, ([], true) when no extra fields
// are read, or (nil, false) when the operator is custom but has no
// registered callback — caller should treat that as "can't introspect"
// and widen the projection.
//
// Built-in operators are not stored in this map; callers should only
// reach FieldInputsFor for extension-resolved operators.
func (r *ExtensionRegistry) FieldInputsFor(category, name string, raw json.RawMessage) ([]string, bool) {
	if r == nil || r.FieldInputs == nil {
		return nil, false
	}
	fn, ok := r.FieldInputs[StreamabilityKey(category, name)]
	if !ok {
		return nil, false
	}
	if fn == nil {
		return nil, true
	}
	return fn(raw), true
}

// CustomAggregatorNames returns the overlay-only aggregator names in
// sorted-ish order (map iteration; callers sort if needed). Used by
// manifest emission to list extension-shipped operators separately
// from built-ins.
func (r *ExtensionRegistry) CustomAggregatorNames() []types.AggregationType {
	if r == nil {
		return nil
	}
	out := make([]types.AggregationType, 0, len(r.Aggregators))
	for t := range r.Aggregators {
		out = append(out, t)
	}
	return out
}

func (r *ExtensionRegistry) CustomAttributeNames() []types.AttributeType {
	if r == nil {
		return nil
	}
	out := make([]types.AttributeType, 0, len(r.Attributes))
	for t := range r.Attributes {
		out = append(out, t)
	}
	return out
}

func (r *ExtensionRegistry) CustomFiltererNames() []types.FiltererType {
	if r == nil {
		return nil
	}
	out := make([]types.FiltererType, 0, len(r.Filterers))
	for t := range r.Filterers {
		out = append(out, t)
	}
	return out
}

func (r *ExtensionRegistry) CustomGrouperNames() []types.GroupType {
	if r == nil {
		return nil
	}
	out := make([]types.GroupType, 0, len(r.Groupers))
	for t := range r.Groupers {
		out = append(out, t)
	}
	return out
}

func (r *ExtensionRegistry) CustomWindowNames() []types.WindowType {
	if r == nil {
		return nil
	}
	out := make([]types.WindowType, 0, len(r.Windows))
	for t := range r.Windows {
		out = append(out, t)
	}
	return out
}

func (r *ExtensionRegistry) CustomFeatureNames() []types.FeatureType {
	if r == nil {
		return nil
	}
	out := make([]types.FeatureType, 0, len(r.Features))
	for t := range r.Features {
		out = append(out, t)
	}
	return out
}

// CustomRowTestNames returns the tier-1 overlay-only test names.
func (r *ExtensionRegistry) CustomRowTestNames() []types.TestType {
	if r == nil {
		return nil
	}
	out := make([]types.TestType, 0, len(r.RowTests))
	for t := range r.RowTests {
		out = append(out, t)
	}
	return out
}

// CustomPostTestNames returns the tier-2 overlay-only test names.
func (r *ExtensionRegistry) CustomPostTestNames() []types.TestType {
	if r == nil {
		return nil
	}
	out := make([]types.TestType, 0, len(r.PostTests))
	for t := range r.PostTests {
		out = append(out, t)
	}
	return out
}
