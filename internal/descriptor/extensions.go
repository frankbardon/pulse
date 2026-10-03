package descriptor

import (
	"encoding/json"
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/types"
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

	// FieldInputs carries each registration's optional FieldInputs hook
	// (pulse.FieldInputsFunc — the callback the runtime's buffered
	// projection already consults), keyed "<category>|<name>" with the
	// category spelled as processing.StreamabilityKey spells it
	// (aggregator, attribute, filterer, grouper, window, feature, test).
	// FieldRefRefusals calls it with the slot's Params so the
	// no-execute layer judges the names an extension declares it reads,
	// exactly as the runtime does; a registration without the hook is
	// absent and its params are not judged. Never serialised.
	FieldInputs map[string]func(raw json.RawMessage) []string `json:"-"`

	// Purposes carries each registration's optional Purpose, keyed by
	// registered operator name, already validated at pulse.New. The
	// manifest projects only its intent IDs (OperatorMeta.Intents); the
	// prose stays on demand. A registration without a Purpose is absent.
	// Never serialised.
	Purposes map[string]descriptor.Purpose `json:"-"`

	// Interpretations carries each test registration's optional
	// Interpretation entries, keyed by registered test name, already
	// structure-checked at pulse.New. Never serialised.
	Interpretations map[string][]descriptor.Interpretation `json:"-"`
}

// PurposeOf returns the Purpose the extension operator name declares.
// Nil-safe.
func (s *ExtensionsSnapshot) PurposeOf(name string) (descriptor.Purpose, bool) {
	if s == nil {
		return descriptor.Purpose{}, false
	}
	p, ok := s.Purposes[name]
	return p, ok
}

// DeclaredFieldInputs returns the field names the extension operator
// (category, name) declares it reads for params raw, and whether it
// declares any hook at all. A hook that panics is treated as no
// declaration (ok=false) rather than crashing predict or the runtime
// check — the same input the projection extractor would then widen
// for. Nil-safe.
func (s *ExtensionsSnapshot) DeclaredFieldInputs(category, name string, raw json.RawMessage) (names []string, ok bool) {
	if s == nil || s.FieldInputs == nil {
		return nil, false
	}
	fn, found := s.FieldInputs[category+"|"+name]
	if !found || fn == nil {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			names, ok = nil, false
		}
	}()
	return fn(raw), true
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
		Aggregators:        snap.withExtIntents(sortOperatorMeta(snap.Aggregators)),
		Attributes:         snap.withExtIntents(sortOperatorMeta(snap.Attributes)),
		Filterers:          snap.withExtIntents(sortOperatorMeta(snap.Filterers)),
		Groupers:           snap.withExtIntents(sortOperatorMeta(snap.Groupers)),
		Windows:            snap.withExtIntents(sortOperatorMeta(snap.Windows)),
		Features:           snap.withExtIntents(sortOperatorMeta(snap.Features)),
		Tests:              snap.withExtIntents(sortOperatorMeta(snap.Tests)),
		SynthDistributions: snap.withExtIntents(sortOperatorMeta(snap.SynthDistributions)),
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

// withExtIntents stamps each entry with the sorted intent IDs its
// registration's Purpose declares — the extension twin of
// withOpIntents. An entry without a Purpose keeps nil Intents, so the
// manifest omits the key. ops is a fresh copy (sortOperatorMeta), so
// writing in place never reaches the snapshot.
func (s *ExtensionsSnapshot) withExtIntents(ops []descriptor.OperatorMeta) []descriptor.OperatorMeta {
	for i := range ops {
		p, ok := s.PurposeOf(ops[i].Name)
		if !ok || len(p.Intents) == 0 {
			ops[i].Intents = nil
			continue
		}
		ids := append([]string(nil), p.Intents...)
		sort.Strings(ids)
		ops[i].Intents = ids
	}
	return ops
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
// internal/service/ and internal/processing/ imports (TestPredictNoExecutionImports).
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

// AggregatorMarginReducibility reports the crosstab margin class of
// aggregator t: an extension's DECLARED class from the snapshot (empty
// reads as types.MarginRecompute, the not-fusable default), else the
// built-in per-type MarginReducibility(). The no-execute twin of
// processing.ExtensionRegistry.AggregatorMarginReducibility. Nil-safe.
func (s *ExtensionsSnapshot) AggregatorMarginReducibility(t types.AggregationType) types.MarginReducibility {
	if s != nil {
		for _, m := range s.Aggregators {
			if m.Name == string(t) {
				if m.MarginReducibility == "" {
					return types.MarginRecompute
				}
				return types.MarginReducibility(m.MarginReducibility)
			}
		}
	}
	return t.MarginReducibility()
}

// mergeFacts adapts the snapshot to mergegate.Extensions — the
// predict-side twin of the runtime ExtensionRegistry adapter, so the
// chain validator and ProcessChain run the one shared gate
// (internal/mergegate) over the same DECLARED facts: an aggregator's or
// grouper's Mergeable flag, a filterer's Streamable flag, and an
// attribute's row_local mode (the runtime's "streamable and not
// two-pass"). Nil-safe. inst is the instance feature set: a name it
// hides answers (false, true) — known and not mergeable — which refuses
// it with the same reason the built-in tables give a never-registered
// name, exactly as the runtime adapter does. Nil hides nothing.
func (s *ExtensionsSnapshot) mergeFacts(inst *InstanceSnapshot) mergegate.Extensions {
	return snapshotMergeFacts{s, inst}
}

type snapshotMergeFacts struct {
	s    *ExtensionsSnapshot
	inst *InstanceSnapshot
}

func findMeta(metas []descriptor.OperatorMeta, name string) (descriptor.OperatorMeta, bool) {
	for _, m := range metas {
		if m.Name == name {
			return m, true
		}
	}
	return descriptor.OperatorMeta{}, false
}

// Hidden reports a built-in the instance does not offer, for the
// gate's refusal prose (mergegate's optional hider).
func (f snapshotMergeFacts) Hidden(name string) bool { return f.inst.Hidden(name) }

func (f snapshotMergeFacts) Aggregator(name string) (bool, bool) {
	if f.inst.Hidden(name) {
		return false, true
	}
	if f.s == nil {
		return false, false
	}
	m, ok := findMeta(f.s.Aggregators, name)
	return m.Mergeable, ok
}

func (f snapshotMergeFacts) Grouper(name string) (bool, bool) {
	if f.inst.Hidden(name) {
		return false, true
	}
	if f.s == nil {
		return false, false
	}
	m, ok := findMeta(f.s.Groupers, name)
	return m.Mergeable, ok
}

func (f snapshotMergeFacts) Filterer(name string) (bool, bool) {
	if f.inst.Hidden(name) {
		return false, true
	}
	if f.s == nil {
		return false, false
	}
	m, ok := findMeta(f.s.Filterers, name)
	return m.Streamable, ok
}

func (f snapshotMergeFacts) Attribute(name string) (bool, bool) {
	if f.inst.Hidden(name) {
		return false, true
	}
	if f.s == nil {
		return false, false
	}
	m, ok := findMeta(f.s.Attributes, name)
	return m.Mode == "row_local", ok
}
