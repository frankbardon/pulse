package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// ExtensionsSnapshot is the immutable read-only view that the service
// hands to descriptor.BuildManifestWithExtensions and predict. It
// carries everything the manifest + predict surfaces need without
// importing the live processing registry — keeping
// TestPredictNoExecutionImports satisfied.
type ExtensionsSnapshot struct {
	Aggregators        []descriptor.OperatorMeta
	Attributes         []descriptor.OperatorMeta
	Filterers          []descriptor.OperatorMeta
	Groupers           []descriptor.OperatorMeta
	Windows            []descriptor.OperatorMeta
	Features           []descriptor.OperatorMeta
	Tests              []descriptor.OperatorMeta
	SynthDistributions []descriptor.OperatorMeta
	ExprFunctions      []descriptor.ExprFunctionMeta
	LookupTables       []descriptor.LookupTableMeta
	LabelTables        []descriptor.LabelTableMeta
	RangeTables        []descriptor.RangeTableMeta

	// ComponentSchemas projects per-extension ComponentSchema
	// declarations keyed by registered operator name. The map carries
	// aggregator / grouper / filterer schemas declared via
	// pulse.AggregatorRegistration.ComponentSchema (and the matching
	// grouper / filterer variants) so manifest + predict surface
	// extension operators on the same shape as built-ins. Missing
	// entries are equivalent to floor-only registrations — the
	// orchestrator emits the universal floor without operator-
	// specific extras.
	//
	// Wired by buildExtensionsSnapshot in pulse.New; manifest assembly
	// merges this map into ComponentsSchemas alongside the built-in
	// capability tables, and predict's per-slot ComponentSchema
	// lookups fall through to this map when the built-in capability
	// table does not carry an entry for the slot's operator name.
	ComponentSchemas map[string]descriptor.ComponentSchema

	// OverlayKinds is the forward-compat reservation slot for
	// embedder-registered overlay catalog entries. The runtime
	// pulse.Options.Extensions.OverlayKinds registration surface
	// (referenced in types/overlay.go) is not yet implemented; this
	// snapshot slot exists so the MCP per-facade schema binders
	// (mcp/gosdk/bind.go) can merge custom kind names
	// into the per-facade overlay_kind enum the moment the
	// registration surface lands — no churn on the binder when the
	// runtime catches up. Today the slot is populated by tests
	// only; production code paths leave it nil and the per-facade
	// enums fall back to the built-in catalog drawn from
	// types.AllOverlayKinds() via descriptor.OverlayCapabilities().
	// Each entry carries Name (SCREAMING_SNAKE OVERLAY_* constant)
	// alongside the standard OperatorMeta knobs so the per-facade
	// classifier can route an extension kind onto the right tool's
	// enum once the registration surface declares which facade(s)
	// the custom kind targets. v1 lacks the per-kind facade tag —
	// the binder treats every snapshot entry as Request-facade
	// only (the lowest-risk fallback) until the registration
	// surface lands the tag.
	OverlayKinds []descriptor.OperatorMeta
}

// emptyExtensionsManifest returns a fully-populated ExtensionsManifest
// with empty slices. Used when an ExtensionsSnapshot is nil so the
// JSON shape stays stable.
func emptyExtensionsManifest() descriptor.ExtensionsManifest {
	return descriptor.ExtensionsManifest{
		Aggregators:        []descriptor.OperatorMeta{},
		Attributes:         []descriptor.OperatorMeta{},
		Filterers:          []descriptor.OperatorMeta{},
		Groupers:           []descriptor.OperatorMeta{},
		Windows:            []descriptor.OperatorMeta{},
		Features:           []descriptor.OperatorMeta{},
		Tests:              []descriptor.OperatorMeta{},
		SynthDistributions: []descriptor.OperatorMeta{},
		ExprFunctions:      []descriptor.ExprFunctionMeta{},
		LookupTables:       []descriptor.LookupTableMeta{},
		LabelTables:        []descriptor.LabelTableMeta{},
		RangeTables:        []descriptor.RangeTableMeta{},
	}
}

// extensionsManifestFromSnapshot copies the snapshot's slices into a
// manifest block, sorting each by Name for determinism. Nil snapshot
// yields the empty manifest (stable JSON shape).
func extensionsManifestFromSnapshot(snap *ExtensionsSnapshot) descriptor.ExtensionsManifest {
	if snap == nil {
		return emptyExtensionsManifest()
	}
	out := descriptor.ExtensionsManifest{
		Aggregators:        sortOperatorMeta(snap.Aggregators),
		Attributes:         sortOperatorMeta(snap.Attributes),
		Filterers:          sortOperatorMeta(snap.Filterers),
		Groupers:           sortOperatorMeta(snap.Groupers),
		Windows:            sortOperatorMeta(snap.Windows),
		Features:           sortOperatorMeta(snap.Features),
		Tests:              sortOperatorMeta(snap.Tests),
		SynthDistributions: sortOperatorMeta(snap.SynthDistributions),
		ExprFunctions:      sortExprFunctionMeta(snap.ExprFunctions),
		LookupTables:       sortLookupTableMeta(snap.LookupTables),
		LabelTables:        sortLabelTableMeta(snap.LabelTables),
		RangeTables:        sortRangeTableMeta(snap.RangeTables),
	}
	if out.Aggregators == nil {
		out.Aggregators = []descriptor.OperatorMeta{}
	}
	if out.Attributes == nil {
		out.Attributes = []descriptor.OperatorMeta{}
	}
	if out.Filterers == nil {
		out.Filterers = []descriptor.OperatorMeta{}
	}
	if out.Groupers == nil {
		out.Groupers = []descriptor.OperatorMeta{}
	}
	if out.Windows == nil {
		out.Windows = []descriptor.OperatorMeta{}
	}
	if out.Features == nil {
		out.Features = []descriptor.OperatorMeta{}
	}
	if out.Tests == nil {
		out.Tests = []descriptor.OperatorMeta{}
	}
	if out.SynthDistributions == nil {
		out.SynthDistributions = []descriptor.OperatorMeta{}
	}
	if out.ExprFunctions == nil {
		out.ExprFunctions = []descriptor.ExprFunctionMeta{}
	}
	if out.LookupTables == nil {
		out.LookupTables = []descriptor.LookupTableMeta{}
	}
	if out.LabelTables == nil {
		out.LabelTables = []descriptor.LabelTableMeta{}
	}
	if out.RangeTables == nil {
		out.RangeTables = []descriptor.RangeTableMeta{}
	}
	return out
}

func sortOperatorMeta(in []descriptor.OperatorMeta) []descriptor.OperatorMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]descriptor.OperatorMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortExprFunctionMeta(in []descriptor.ExprFunctionMeta) []descriptor.ExprFunctionMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]descriptor.ExprFunctionMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortLookupTableMeta(in []descriptor.LookupTableMeta) []descriptor.LookupTableMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]descriptor.LookupTableMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortLabelTableMeta(in []descriptor.LabelTableMeta) []descriptor.LabelTableMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]descriptor.LabelTableMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortRangeTableMeta(in []descriptor.RangeTableMeta) []descriptor.RangeTableMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]descriptor.RangeTableMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// extensionsFromOpts returns the snapshot reachable from PredictOptions.
// Nil-safe so every isExtension* helper can take a nil opts argument.
func extensionsFromOpts(opts *PredictOptions) *ExtensionsSnapshot {
	if opts == nil {
		return nil
	}
	return opts.Extensions
}

// snapshotHasName reports whether `name` appears in metas. Used by the
// per-category isExtension* helpers below.
func snapshotHasName(metas []descriptor.OperatorMeta, name string) bool {
	for _, m := range metas {
		if m.Name == name {
			return true
		}
	}
	return false
}

// GrouperFanOut reports the fan-out fact an embedder declared for the
// grouper registered under name, and whether the snapshot carries such
// a grouper at all. Nil-snapshot-safe: ok=false.
//
// This is the descriptor half of the bridge that keeps predict free of
// internal/service/ and processing/ imports (TestPredictNoExecutionImports).
// The runtime half is processing.ExtensionRegistry.GrouperFanOut; both
// feed types.CheckPairwiseSlabPartitionWith, which owns the built-in-
// first resolution order so the two arms cannot drift.
//
// Takes a plain string rather than types.GroupType so the accessor
// stays usable from the manifest side, where names are untyped.
func (s *ExtensionsSnapshot) GrouperFanOut(name string) (fansOut bool, ok bool) {
	if s == nil {
		return false, false
	}
	for _, m := range s.Groupers {
		if m.Name == name {
			return m.FansOut, true
		}
	}
	return false, false
}

// HasAggregator reports whether name is an embedder-registered
// aggregator in the snapshot. Nil-safe.
func (s *ExtensionsSnapshot) HasAggregator(name string) bool {
	if s == nil {
		return false
	}
	for _, m := range s.Aggregators {
		if m.Name == name {
			return true
		}
	}
	return false
}
