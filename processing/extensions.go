package processing

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
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
	Streamable map[string]bool

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

	// FieldInputs is the per-(category, name) field-introspection
	// callback consulted by the buffered-projection extractor
	// (NeededFields). When the registry contains an entry for an
	// extension operator the projection extractor calls the callback
	// with the operator's raw Params; otherwise the extractor widens
	// the projection to "every field" so the runtime stays correct
	// for embedders that haven't opted in.
	FieldInputs map[string]FieldInputsFunc
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

// ExtensionAware is the optional interface that AttributeComputer /
// FiltererBuilder instances implement when they want the Processor
// to inject the live ExtensionRegistry after construction. The
// formula attribute and expression filterer use this hook to expose
// embedder-registered ExprFunctions and LookupTables to the expr
// environment.
type ExtensionAware interface {
	SetExtensions(r *ExtensionRegistry)
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
		out = append(out, fn)
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
	if r != nil {
		if f, ok := r.Windows[t]; ok {
			return f, true
		}
	}
	return window.Lookup(t)
}

// LookupFeature returns the factory for a feature type. Overlay wins.
// Falls through to feature.Lookup for built-ins.
func (r *ExtensionRegistry) LookupFeature(t types.FeatureType) (feature.Factory, bool) {
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
	if r != nil {
		if f, ok := r.PostTests[t]; ok {
			return f, true
		}
	}
	f, ok := postTestRegistry[t]
	return f, ok
}

// IsStreamable consults the Streamable overlay first, then the
// built-in per-type Streamable() method. Unknown categories return
// false. The cross-check between this and the runtime path lives in
// the per-category integration phases.
func (r *ExtensionRegistry) IsStreamable(category, name string) bool {
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
