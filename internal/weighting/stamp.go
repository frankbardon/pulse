package weighting

import (
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Stamping — the per-slot weight rewrite every engine entry applies
// (processing.StampWeightsWith) and predict replays on a copy before it
// hands an extension slot to its spec-only Validate hook, so both judge
// the same spec. The resolved weight reaches an operator through its
// SPEC (.claude/reference/weighting.md, "Threading"): each slot's own
// `weight` becomes its resolved weight — set when a weight applies,
// `null` when none does.

// ExtensionAware reports whether the embedder-registered operator op of
// category ("aggregator", "attribute", "test", "matrix", "regression")
// declared WeightAware. Nil: no extension operator is weight-aware.
type ExtensionAware func(category, op string) bool

// is reports e(category, op), false on a nil e.
func (e ExtensionAware) is(category, op string) bool {
	return e != nil && e(category, op)
}

// WeightedMeanField returns AGG_WEIGHTED_MEAN's params.weight_field, ""
// for any other slot or an undecodable params object.
func WeightedMeanField(a *types.Aggregation) string {
	if a == nil || a.Type != types.AGG_WEIGHTED_MEAN || len(a.Params) == 0 {
		return ""
	}
	var p struct {
		WeightField string `json:"weight_field"`
	}
	if paramread.Decode(paramread.Aggregator, string(a.Type), a.Params, &p) != nil {
		return ""
	}
	return p.WeightField
}

// effectiveAggWeight is the weight in force on one aggregation slot:
// its own `weight` (null ⇒ none), AGG_WEIGHTED_MEAN's weight_field,
// the request weight, then the instance default.
func effectiveAggWeight(a *types.Aggregation, reqW, def *types.WeightSpec) *types.WeightSpec {
	switch {
	case a.Weight.IsNull():
		return nil
	case !a.Weight.IsZero():
		return a.Weight.Spec()
	}
	if f := WeightedMeanField(a); f != "" {
		return &types.WeightSpec{Field: f, Kind: types.WeightKindProbability}
	}
	if reqW != nil {
		return reqW
	}
	return def
}

// stampAgg returns a copy of a whose `weight` is its resolved state:
// the applied weight (kind spelled out) on a weight-aware operator,
// `null` everywhere else.
func stampAgg(a *types.Aggregation, reqW, def *types.WeightSpec, aware func(string) bool) *types.Aggregation {
	if a == nil {
		return nil
	}
	c := *a
	if w := effectiveAggWeight(a, reqW, def); w != nil && aware(string(a.Type)) {
		c.Weight = types.SlotWeightOf(types.WeightSpec{Field: w.Field, Kind: w.EffectiveKind()})
	} else {
		c.Weight = types.NullSlotWeight()
	}
	return &c
}

// effectiveSlotWeight is the weight in force on a non-aggregation slot
// (a test or an attribute): its own `weight` (null ⇒ none), the
// request weight, then the instance default.
func effectiveSlotWeight(own types.SlotWeight, reqW, def *types.WeightSpec) *types.WeightSpec {
	switch {
	case own.IsNull():
		return nil
	case !own.IsZero():
		return own.Spec()
	case reqW != nil:
		return reqW
	}
	return def
}

// stampSlotWeight is the resolved `weight` of a test or attribute
// slot: the applied weight (kind spelled out) on a weight-aware
// operator — a built-in row test whose class has a kind (IsAware) or a
// WeightAware extension — `null` on any other slot that names a
// weight, and own unchanged otherwise. ok=false means "no change". The
// resolver already refused a weight in force on a non-aware extension
// test or attribute, on every refused built-in test and on every
// post-test, and a probability weight on a frequency-only one, so a
// slot that keeps a set weight here is one the weight applies to.
func stampSlotWeight(own types.SlotWeight, aware bool, reqW, def *types.WeightSpec) (types.SlotWeight, bool) {
	if aware {
		if w := effectiveSlotWeight(own, reqW, def); w != nil {
			return types.SlotWeightOf(types.WeightSpec{Field: w.Field, Kind: w.EffectiveKind()}), true
		}
		return types.NullSlotWeight(), !own.IsNull()
	}
	if own.Spec() != nil {
		// A built-in row-local attribute skips its weight; nulling it
		// keeps the slot out of the invalid-row tally.
		return types.NullSlotWeight(), true
	}
	return own, false
}

// Stamp returns req with every aggregation slot (the top-level
// aggregations, the crosstab cell and margin aggregations) carrying its
// resolved weight, def being pulse.Options.DefaultWeight and ext the
// instance's extension WeightAware declarations (nil: built-ins only).
// An extension aggregator is weight-aware iff its registration
// declared WeightAware; a weight-aware row test (`tests`: a built-in
// whose class has a kind, read by its factory off the slot, or a
// WeightAware extension, which reads it through extend.Record.Weight()),
// a WeightAware extension post-test, a weight-aware attribute (built-in
// or extension), a built-in regression whose class has a kind and a
// weighted grouper (GROUP_QUANTILE on groups[i] or a crosstab axis)
// carries its resolved weight too. When nothing names a weight — no
// request weight, no default, no slot weight, no AGG_WEIGHTED_MEAN
// weight_field — req itself is returned, so an unweighted request
// executes the unchanged object. Otherwise the result is a shallow copy
// with copied slots; req is never mutated. Idempotent: stamping a
// stamped request changes nothing.
func Stamp(req *types.Request, def *types.WeightSpec, ext ExtensionAware) *types.Request {
	if req == nil || !namesWeight(req, def) {
		return req
	}
	// IsAware covers ClassFrequencyOnly too: the resolver has already
	// refused a probability weight on such a slot, so a weight that
	// reaches it here is a frequency weight to apply.
	aware := func(op string) bool {
		return IsAware(op) || ext.is("aggregator", op)
	}
	c := *req
	c.Aggregations = make([]*types.Aggregation, len(req.Aggregations))
	for i, a := range req.Aggregations {
		c.Aggregations[i] = stampAgg(a, req.Weight, def, aware)
	}
	if req.Crosstab != nil {
		ct := *req.Crosstab
		ct.Cell = stampAgg(req.Crosstab.Cell, req.Weight, def, aware)
		if req.Crosstab.Cells != nil {
			ct.Cells = make([]*types.Aggregation, len(req.Crosstab.Cells))
			for i, a := range req.Crosstab.Cells {
				ct.Cells[i] = stampAgg(a, req.Weight, def, aware)
			}
		}
		ct.MarginAggregations = make([]*types.Aggregation, len(req.Crosstab.MarginAggregations))
		for i, a := range req.Crosstab.MarginAggregations {
			ct.MarginAggregations[i] = stampAgg(a, req.Weight, def, aware)
		}
		c.Crosstab = &ct
	}
	// Built-in row tests read the weight; a built-in post-test reads
	// aggregated rows and never does (the resolver refused it).
	c.Tests = stampTests(req.Tests, req.Weight, def, func(op string) bool {
		return IsAware(op) || ext.is("test", op)
	})
	c.PostTests = stampTests(req.PostTests, req.Weight, def, func(op string) bool {
		return ext.is("test", op)
	})
	// A built-in regression reads the weight when its class has a kind,
	// an extension REG_* model when it declared WeightAware; the resolver has already
	// refused a weight in force on a pending regression, on one carrying
	// a bootstrap resample or a selection modifier, and a probability
	// weight on a frequency-only one.
	if len(req.Regressions) > 0 {
		c.Regressions = make([]*types.RegressionSpec, len(req.Regressions))
		for i, r := range req.Regressions {
			c.Regressions[i] = r
			if r == nil {
				continue
			}
			// A bootstrap / selection modifier refits on row subsets with
			// no weighted form: such a slot is never stamped (the resolver
			// refuses any weight in force on it). A jackknife is weighted
			// (JK1): every replicate refits with the remaining weights.
			aware := (IsAware(string(r.Type)) || ext.is("regression", string(r.Type))) &&
				(r.Resample == "" || r.Resample == "jackknife") && r.Selection == ""
			if w, ok := stampSlotWeight(r.Weight, aware, req.Weight, def); ok {
				cp := *r
				cp.Weight = w
				c.Regressions[i] = &cp
			}
		}
	}
	// A built-in matrix operator reads the weight when its class has a
	// kind; an extension MAT_* operator when it declared WeightAware
	// (the resolver refuses a weight in force on one that did not).
	if len(req.Matrices) > 0 {
		c.Matrices = make([]types.MatrixSpec, len(req.Matrices))
		for i, m := range req.Matrices {
			c.Matrices[i] = m
			aware := IsAware(string(m.Type)) || ext.is("matrix", string(m.Type))
			if w, ok := stampSlotWeight(m.Weight, aware, req.Weight, def); ok {
				c.Matrices[i].Weight = w
			}
		}
	}
	// A grouper reads the weight when its class has a kind
	// (GROUP_QUANTILE's weighted cuts) — on groups[i] and on both
	// crosstab axes; any other grouper that names a weight is stamped
	// null (skipped). There is no extension grouper weight declaration.
	c.Groups = stampGroups(req.Groups, req.Weight, def)
	if c.Crosstab != nil {
		ct := *c.Crosstab
		ct.Rows = stampGroups(ct.Rows, req.Weight, def)
		ct.Columns = stampGroups(ct.Columns, req.Weight, def)
		c.Crosstab = &ct
	}
	if len(req.Attributes) > 0 {
		c.Attributes = make([]*types.Attribute, len(req.Attributes))
		for i, a := range req.Attributes {
			c.Attributes[i] = a
			if a == nil {
				continue
			}
			// A built-in attribute reads the weight when its spec's class
			// has a kind (the ATTR_REG_* refits, a total-bearing
			// ATTR_FORMULA — AttributeClassOf); an extension one when it
			// declared WeightAware.
			aware := len(KindsOfClass(AttributeClassOf(a))) > 0 || ext.is("attribute", string(a.Type))
			if w, ok := stampSlotWeight(a.Weight, aware, req.Weight, def); ok {
				cp := *a
				cp.Weight = w
				c.Attributes[i] = &cp
			}
		}
	}
	return &c
}

// stampGroups stamps a grouper slice (groups or a crosstab axis; see
// stampSlotWeight); a nil or empty slice is returned as-is.
func stampGroups(groups []*types.Group, reqW, def *types.WeightSpec) []*types.Group {
	if len(groups) == 0 {
		return groups
	}
	out := make([]*types.Group, len(groups))
	for i, g := range groups {
		out[i] = g
		if g == nil {
			continue
		}
		if w, ok := stampSlotWeight(g.Weight, IsAware(string(g.Type)), reqW, def); ok {
			cp := *g
			cp.Weight = w
			out[i] = &cp
		}
	}
	return out
}

// stampTests stamps a test slice (see stampSlotWeight), aware deciding
// per operator; a nil or empty slice is returned as-is.
func stampTests(tests []*types.Test, reqW, def *types.WeightSpec, aware func(string) bool) []*types.Test {
	if len(tests) == 0 {
		return tests
	}
	out := make([]*types.Test, len(tests))
	for i, t := range tests {
		out[i] = t
		if t == nil {
			continue
		}
		if w, ok := stampSlotWeight(t.Weight, aware(string(t.Type)), reqW, def); ok {
			cp := *t
			cp.Weight = w
			out[i] = &cp
		}
	}
	return out
}

// namesWeight reports whether anything in req (or def) names a weight.
func namesWeight(req *types.Request, def *types.WeightSpec) bool {
	if req.Weight != nil || def != nil {
		return true
	}
	named := func(a *types.Aggregation) bool {
		return a != nil && (!a.Weight.IsZero() || WeightedMeanField(a) != "")
	}
	for _, a := range req.Aggregations {
		if named(a) {
			return true
		}
	}
	if ct := req.Crosstab; ct != nil {
		for _, a := range ct.CellAggregations() {
			if named(a) {
				return true
			}
		}
		for _, a := range ct.MarginAggregations {
			if named(a) {
				return true
			}
		}
	}
	for _, t := range append(append([]*types.Test(nil), req.Tests...), req.PostTests...) {
		if t != nil && t.Weight.Spec() != nil {
			return true
		}
	}
	for _, r := range req.Regressions {
		if r != nil && r.Weight.Spec() != nil {
			return true
		}
	}
	for _, m := range req.Matrices {
		if m.Weight.Spec() != nil {
			return true
		}
	}
	for _, a := range req.Attributes {
		if a != nil && a.Weight.Spec() != nil {
			return true
		}
	}
	for _, g := range RequestGroupers(req) {
		if g != nil && g.Weight.Spec() != nil {
			return true
		}
	}
	return false
}

// RequestGroupers lists a request's grouper slots: groups, then the
// crosstab rows and columns.
func RequestGroupers(req *types.Request) []*types.Group {
	gs := req.Groups
	if ct := req.Crosstab; ct != nil {
		gs = append(append(append([]*types.Group(nil), gs...), ct.Rows...), ct.Columns...)
	}
	return gs
}
