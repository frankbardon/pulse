package vectors

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// slot_rewrite.go holds the per-run vector-input rewrites the engine
// applies to a request before any operator factory sees it
// (processing.Processor.prepare and the reducers that build operators
// without a Processor). Predict replays them on a copy before it hands
// an extension slot to its spec-only Validate hook, so the hook judges
// the spec the runtime factory receives. One rule, two callers.

// RewriteAttributeInputs returns req with every vector-input attribute
// — a registered extension attribute's included — rewritten for its
// factory (ResolveAttributes): Fields = the resolved literal members,
// Vector cleared, Label = the default label when empty (computed from
// the slot as written), and — on an offered built-in that aligns to
// the vector's labels (ATTR_SCALE_SCORE, a fitted vector attribute,
// ATTR_SEM_SCORE, a comparison attribute) — canonical params written
// out. route maps a type the instance hides to "" (nil: identity);
// ext reports a registered extension attribute. req itself is returned
// when no attribute reads a vector or resolution fails (the
// field-reference pass has refused that request; the factory refuses
// an unresolved Vector). The caller's request is never mutated. cls is
// the run's windows-first classifier (bound models, extension hooks).
func RewriteAttributeInputs(req *types.Request, schema *encoding.Schema, cls Classifier, route func(types.AttributeType) types.AttributeType, ext func(string) bool) *types.Request {
	if req == nil {
		return req
	}
	has := false
	for _, a := range req.Attributes {
		if HasAttributeVectorInput(a) {
			has = true
			break
		}
	}
	if !has {
		return req
	}
	if route == nil {
		route = func(t types.AttributeType) types.AttributeType { return t }
	}
	inputs, err := ResolveAttributes(req, schema, cls, route, ext)
	if err != nil {
		return req
	}
	offered := func(t types.AttributeType) bool { return route(t) != "" }
	c := *req
	c.Attributes = make([]*types.Attribute, len(req.Attributes))
	for i, a := range req.Attributes {
		c.Attributes[i] = a
		if a == nil || i >= len(inputs) || !inputs[i].Has() {
			continue
		}
		cp := *a
		if cp.Label == "" {
			cp.Label = DefaultAttributeLabel(a)
		}
		if cp.Type == types.ATTR_SCALE_SCORE && offered(cp.Type) {
			// The literal members lose the vector's labels and kind
			// bounds: hand the factory canonical params (reverse by
			// member name, the declared range explicit).
			if params, ok := ScaleScoreSlotParams(a, inputs[i].Members); ok {
				cp.Params = params
			}
		}
		if IsFittedAttribute(cp.Type) && offered(cp.Type) {
			// A bound fit aligns to the named vector's labels, which the
			// literal-member rewrite would lose: write them out.
			if params, ok := FittedSlotParams(a, inputs[i].Members); ok {
				cp.Params = params
			}
		}
		if cp.Type == types.ATTR_SEM_SCORE && offered(cp.Type) {
			// The model names its members by label, which the
			// literal-member rewrite would lose: write them out.
			if params, ok := SEMScoreSlotParams(a, inputs[i].Members); ok {
				cp.Params = params
			}
		}
		if IsComparisonAttribute(cp.Type) && offered(cp.Type) {
			// The literal members lose the vector's labels and kind:
			// hand the factory canonical params (the metric written out,
			// the reference in member order, the labels it aligns to).
			if params, ok := ComparisonSlotParams(a, inputs[i].Members, schema); ok {
				cp.Params = params
			}
		}
		cp.Vector = ""
		cp.Fields = append([]string(nil), inputs[i].Members.Members...)
		c.Attributes[i] = &cp
	}
	return &c
}

// RewriteGroupInputs returns req with every vector-input grouper slot
// (groups and both crosstab axes; ResolveGroups) rewritten for its
// factory: Fields = the resolved literal members, Vector cleared, and —
// on a built-in — Field = its key column (GroupKeyField, computed from
// the slot as written; an extension grouper keeps its Field), so every
// downstream site that names a grouper's column reads the key column.
// route maps a type the instance hides to "" (nil: identity); ext
// reports a registered extension grouper. req itself is returned when
// no grouper reads a vector, when every such slot is already rewritten
// (Field set; the rewrite is idempotent) or when resolution fails (the
// field-reference pass has refused that request). The caller's request
// is never mutated.
func RewriteGroupInputs(req *types.Request, schema *encoding.Schema, cls Classifier, route func(types.GroupType) types.GroupType, ext func(string) bool) *types.Request {
	has := false
	for _, s := range GroupSlots(req) {
		// A built-in slot is rewritten once (its key Field set); an
		// extension slot keeps its Field, and re-resolving its literal
		// members is a no-op.
		if HasGroupVectorInput(s.Group) && (s.Group.Field == "" || !IsVectorGroup(s.Group.Type)) {
			has = true
			break
		}
	}
	if !has {
		return req
	}
	inputs, err := ResolveGroups(req, schema, cls, route, ext)
	if err != nil {
		return req
	}
	rewrite := func(gs []*types.Group) []*types.Group {
		if len(gs) == 0 {
			return gs
		}
		out := make([]*types.Group, len(gs))
		for i, g := range gs {
			out[i] = g
			in, ok := inputs[g]
			if !ok {
				continue
			}
			cp := *g
			if !in.Extension {
				cp.Field = GroupKeyField(types.GROUP_KMEANS, g)
			}
			cp.Vector = ""
			cp.Fields = append([]string(nil), in.Members.Members...)
			out[i] = &cp
		}
		return out
	}
	c := *req
	c.Groups = rewrite(req.Groups)
	if req.Crosstab != nil {
		ct := *req.Crosstab
		ct.Rows = rewrite(req.Crosstab.Rows)
		ct.Columns = rewrite(req.Crosstab.Columns)
		c.Crosstab = &ct
	}
	return &c
}

// RewriteAggregationInputs returns req with every vector aggregator
// slot — aggregations, the crosstab cell and its margin aggregations —
// rewritten for its factory by AggregationSlotRewrite: Fields = the
// resolved literal members, Vector cleared, Label = the default label
// when empty, params.labels = the resolved display labels (the
// literal-member rewrite would otherwise drop a named vector's labels).
// req itself is returned when no slot reads a vector or resolution
// fails (the field-reference pass has refused that request; the factory
// refuses an unresolved Vector). The caller's request is never mutated.
// cls is the run's windows-first classifier; kinds is the
// instance's aggregation routing and extension declarations.
func RewriteAggregationInputs(req *types.Request, schema *encoding.Schema, cls Classifier, kinds AggregationKinds) *types.Request {
	if req == nil {
		return req
	}
	if !AnyAggregationVectorInput(req) {
		return req
	}
	inputs, err := ResolveAggregations(req, schema, cls, kinds)
	if err != nil || len(inputs) == 0 {
		return req
	}
	rewrite := func(a *types.Aggregation) *types.Aggregation {
		if in, ok := inputs[a]; ok && in.Has() {
			return AggregationSlotRewrite(a, in)
		}
		return a
	}
	c := *req
	if len(req.Aggregations) > 0 {
		c.Aggregations = make([]*types.Aggregation, len(req.Aggregations))
		for i, a := range req.Aggregations {
			c.Aggregations[i] = rewrite(a)
		}
	}
	if ct := req.Crosstab; ct != nil {
		cc := *ct
		if ct.Cell != nil {
			cc.Cell = rewrite(ct.Cell)
		}
		if ct.Cells != nil {
			cc.Cells = make([]*types.Aggregation, len(ct.Cells))
			for i, a := range ct.Cells {
				cc.Cells[i] = a
				if a != nil {
					cc.Cells[i] = rewrite(a)
				}
			}
		}
		if len(ct.MarginAggregations) > 0 {
			cc.MarginAggregations = make([]*types.Aggregation, len(ct.MarginAggregations))
			for i, a := range ct.MarginAggregations {
				cc.MarginAggregations[i] = a
				if a != nil {
					cc.MarginAggregations[i] = rewrite(a)
				}
			}
		}
		c.Crosstab = &cc
	}
	return &c
}

// TestSlotRewrite returns the spec a tier-1 test's factory receives: t
// itself when in carries no resolved member set, else a copy whose
// Fields / Fields2 are the resolved member lists (Vector / Vector2
// cleared).
func TestSlotRewrite(t *types.Test, in TestInput) *types.Test {
	if t == nil || !in.Has() {
		return t
	}
	cp := *t
	cp.Vector, cp.Fields = "", append([]string(nil), in.Members.Members...)
	cp.Vector2, cp.Fields2 = "", nil
	if in.HasSecond {
		cp.Fields2 = append([]string(nil), in.Members2.Members...)
	}
	return &cp
}

// ExtensionMatrixSpec returns the spec an extension matrix operator's
// factory receives for the resolved plan m of spec: a copy whose Params
// are the operator's own (Matrix.ExtParams — the engine-owned keys
// stripped).
func ExtensionMatrixSpec(spec types.MatrixSpec, m Matrix) types.MatrixSpec {
	spec.Params = append(json.RawMessage(nil), m.ExtParams...)
	return spec
}
