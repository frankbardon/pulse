package descriptor

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/crosstabfuse"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// CrosstabFusion reports whether req is eligible for the engine's fused
// in-decode crosstab path, and every reason it is not — the no-execute
// twin of processing.CanFuseCrosstab. Both run the one shared rule,
// crosstabfuse.Decide: the runtime over its ExtensionRegistry, this over
// the read-only ExtensionsSnapshot plus the instance feature set, so a
// profile-hidden operator answers exactly as a never-registered one.
//
// Eligibility only: Options.DisableCrosstabFusion is a dispatch-time
// override the engine checks outside the rule, and it does not change
// this answer; Predict applies it on top (PredictOptions.
// DisableCrosstabFusion → PredictResult.CrosstabFusable false). The one fact this arm cannot know is whether a keyable
// grouper's factory accepts its params; the runtime declines such a
// request and the buffered path then refuses it with a coded error, so
// the request errors on either answer.
//
// Callers resolve smart defaults first, exactly as the engine does
// before it dispatches. snap, inst and schema may each be nil.
func CrosstabFusion(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot, inst *InstanceSnapshot) (bool, []string) {
	return crosstabfuse.Decide(req, schema, snapshotFuseFacts{snap, inst})
}

// snapshotFuseFacts adapts the snapshot to crosstabfuse.Facts — the
// predict-side twin of processing's registryFuseFacts. Nil-safe on both
// fields. A name inst hides answers as unregistered on every fact.
type snapshotFuseFacts struct {
	s    *ExtensionsSnapshot
	inst *InstanceSnapshot
}

func (f snapshotFuseFacts) AggregatorMergeable(t types.AggregationType) bool {
	if f.inst.Hidden(string(t)) {
		return false
	}
	if f.s != nil {
		if m, ok := findMeta(f.s.Aggregators, string(t)); ok {
			return m.Mergeable
		}
	}
	return t.Mergeable()
}

func (f snapshotFuseFacts) AggregatorMarginClass(t types.AggregationType) types.MarginReducibility {
	if f.inst.Hidden(string(t)) {
		return types.MarginRecompute
	}
	return f.s.AggregatorMarginReducibility(t)
}

// AggregatorSlotMarginClass mirrors the runtime: a vector element slot
// the instance offers margins like its scalar twin
// (vectors.ElementMarginClass).
func (f snapshotFuseFacts) AggregatorSlotMarginClass(a *types.Aggregation) (types.MarginReducibility, bool) {
	if a == nil || f.inst.Hidden(string(a.Type)) {
		return "", false
	}
	return vectors.ElementMarginClass(a, aggregationKinds(f.inst, f.s))
}

func (f snapshotFuseFacts) AttributeTwoPass(t types.AttributeType) bool {
	return attributeTwoPass(&PredictOptions{Extensions: f.s, Instance: f.inst}, t)
}

// AttributeSlotTwoPass mirrors the runtime: an ATTR_VEC_DISTANCE the
// instance offers over a fitted metric, or a comparison slot over a
// centroid reference (vectors.ComparisonTwoPass).
func (f snapshotFuseFacts) AttributeSlotTwoPass(a *types.Attribute, specs []types.VectorSpec, schema *encoding.Schema) bool {
	return a != nil && !f.inst.Hidden(string(a.Type)) && vectors.ComparisonTwoPass(a.Type, a, specs, schema)
}

// AttributeSlotBound mirrors the runtime: a fitted slot the instance
// offers, bound to a prebuilt fit (vectors.FitBound), is row-local.
func (f snapshotFuseFacts) AttributeSlotBound(a *types.Attribute) bool {
	return a != nil && !f.inst.Hidden(string(a.Type)) && vectors.FitBound(a.Type, a)
}

// GrouperKeyable answers a built-in from the static table and an
// extension from its probe-validated declarations: Streamable=true
// guarantees a per-row keying sibling (PULSE_EXTENSION_STREAMABLE_MISMATCH)
// and FansOut=true a MultiKeyStreamingGrouper
// (PULSE_EXTENSION_FANOUT_MISMATCH). schema is unused — no factory is
// constructed here.
func (f snapshotFuseFacts) GrouperKeyable(g *types.Group, _ *encoding.Schema) bool {
	if f.inst.Hidden(string(g.Type)) {
		return false
	}
	if f.s != nil {
		if m, ok := findMeta(f.s.Groupers, string(g.Type)); ok {
			return m.Streamable || m.FansOut
		}
	}
	keyable, _ := crosstabfuse.BuiltinGrouperKeyable(g.Type)
	return keyable
}

// IsExtension is snapshot membership. pulse.New drops hidden extension
// operators before building the snapshot, as it does for the registry.
func (f snapshotFuseFacts) IsExtension(category, name string) bool {
	if f.s == nil {
		return false
	}
	switch category {
	case "aggregator":
		return snapshotHasName(f.s.Aggregators, name)
	case "attribute":
		return snapshotHasName(f.s.Attributes, name)
	case "filterer":
		return snapshotHasName(f.s.Filterers, name)
	case "grouper":
		return snapshotHasName(f.s.Groupers, name)
	}
	return false
}

// Hidden is the instance feature-set check (nil-safe).
func (f snapshotFuseFacts) Hidden(name string) bool { return f.inst.Hidden(name) }

func (f snapshotFuseFacts) HasFieldInputs(category, name string) bool {
	if f.s == nil {
		return false
	}
	fn, ok := f.s.FieldInputs[category+"|"+name]
	return ok && fn != nil
}
