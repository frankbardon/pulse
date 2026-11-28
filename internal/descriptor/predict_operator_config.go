package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/opconfig"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// validateOperatorConfig reports the operator config faults the runtime
// constructors refuse with PROCESSING_CONFIG (#781), through the rules
// both sides call (internal/opconfig): AGG_DISTINCT_SUM without
// params.distinct_by; AGG_CI_LOWER / AGG_CI_UPPER with a confidence
// outside (0, 1) or a method other than "normal"; ATTR_SET_HAS without
// a params.label its set field's dictionary holds; FILTER_NULL /
// FILTER_TRUE / FILTER_FALSE with an unknown values mode; FILTER_RANGE
// without exactly two numeric values; a FILTER_SET_* label the set
// field's dictionary does not hold; GROUP_RANGE / GROUP_ROUNDED with a
// negative interval (#506) and GROUP_QUANTILE with an interval that is
// no whole bucket count (Request.Groups, both crosstab axes, a
// MAT_DISTANCE params.between grouper). Aggregations are walked in
// every slot validateAggregationParams walks (top level, crosstab cell
// and margin aggregations).
//
// A slot whose field a prior rule refuses gets no config finding, as
// the runtime raises that field refusal first: the accepted-types table
// (decimal rule included, fieldRuleRefuses), a set or vector field on a
// numeric filter, a non-set field on a set operator. A slot naming no
// field is the field-reference rule's. The set rules need the field's
// dictionary, so they judge only a field the schema carries — the full
// schema block's dictionary (an archive's canonical union), never
// inspect's truncated view; no record is read.
func validateOperatorConfig(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	check := func(agg *types.Aggregation) {
		if agg == nil {
			return
		}
		route := opRoute(inst, agg.Type)
		switch route {
		case types.AGG_DISTINCT_SUM:
			if fieldRuleRefuses(route, agg, schema) {
				return
			}
			if _, cerr := opconfig.DistinctSumKey(agg.Params); cerr != nil {
				addCodedError(env, cerr)
			}
		case types.AGG_CI_LOWER, types.AGG_CI_UPPER:
			if fieldRuleRefuses(route, agg, schema) {
				return
			}
			if _, cerr := opconfig.CIParams(agg.Type, agg.Params); cerr != nil {
				addCodedError(env, cerr)
			}
		}
	}
	for _, a := range req.Aggregations {
		check(a)
	}
	if req.Crosstab != nil {
		for _, cell := range req.Crosstab.CellAggregations() {
			check(cell)
		}
		for _, a := range req.Crosstab.MarginAggregations {
			check(a)
		}
	}
	for _, a := range req.Attributes {
		if a == nil || a.Field == "" || opRoute(inst, a.Type) != types.ATTR_SET_HAS {
			continue
		}
		f := setFieldFor(string(types.ATTR_SET_HAS), a.Field, schema)
		if f == nil {
			continue
		}
		label, cerr := opconfig.SetHasLabel(a.Params)
		if cerr == nil {
			_, cerr = opconfig.SetHasBit(a.Field, f, label)
		}
		if cerr != nil {
			addCodedError(env, cerr)
		}
	}
	validateFilterConfig(env, req.Filterers, schema, inst)
	group := func(g *types.Group) {
		if g == nil {
			return
		}
		route := opRoute(inst, g.Type)
		if route != types.GROUP_RANGE && route != types.GROUP_ROUNDED && route != types.GROUP_QUANTILE {
			return
		}
		if schema != nil && g.Field != "" {
			if f := schema.Field(g.Field); f != nil && (f.Type.IsSet() || f.Type.IsVector() ||
				fieldaccept.Refusal(string(route), fieldaccept.SlotField, g.Field, f.Type) != nil) {
				return
			}
		}
		if route == types.GROUP_QUANTILE {
			if _, cerr := opconfig.QuantileBuckets(g.Type, g.Interval); cerr != nil {
				addCodedError(env, cerr)
			}
			return
		}
		if _, cerr := opconfig.GroupInterval(g.Type, g.Interval); cerr != nil {
			addCodedError(env, cerr)
		}
	}
	for _, g := range req.Groups {
		group(g)
	}
	if ct := req.Crosstab; ct != nil {
		for _, g := range ct.Rows {
			group(g)
		}
		for _, g := range ct.Columns {
			group(g)
		}
	}
	for _, m := range req.Matrices {
		if opRoute(inst, m.Type) != types.MAT_DISTANCE {
			continue
		}
		if g, ok := vectors.SpecBetween(m); ok {
			group(&g)
		}
	}
}

// validateFilterConfig is validateOperatorConfig's filterer arm, shared
// with the facet predict: FILTER_NULL, FILTER_TRUE / FILTER_FALSE and
// FILTER_RANGE values, and the FILTER_SET_* labels.
func validateFilterConfig(env *descriptor.Envelope, filterers []*types.Filterer, schema *encoding.Schema, inst *InstanceSnapshot) {
	for _, f := range filterers {
		if f == nil || f.Field == "" {
			continue
		}
		route := opRoute(inst, f.Type)
		switch route {
		case types.FILTER_NULL:
			if _, cerr := opconfig.NullMode(f.Values); cerr != nil {
				addCodedError(env, cerr)
			}
		case types.FILTER_TRUE, types.FILTER_FALSE:
			if _, cerr := opconfig.BoolMode(route, f.Values); cerr != nil {
				addCodedError(env, cerr)
			}
		case types.FILTER_RANGE:
			if schema != nil {
				if field := schema.Field(f.Field); field != nil && (field.Type.IsSet() || field.Type.IsVector() ||
					fieldaccept.Refusal(string(route), fieldaccept.SlotField, f.Field, field.Type) != nil) {
					continue
				}
			}
			if _, _, cerr := opconfig.RangeBounds(f.Values); cerr != nil {
				addCodedError(env, cerr)
			}
		case types.FILTER_SET_CONTAINS_ANY, types.FILTER_SET_CONTAINS_ALL,
			types.FILTER_SET_CONTAINS_NONE, types.FILTER_SET_EQUALS:
			field := setFieldFor(string(route), f.Field, schema)
			if field == nil {
				continue
			}
			if _, cerr := opconfig.SetQueryMask(f.Type, f.Field, field, f.Values); cerr != nil {
				addCodedError(env, cerr)
			}
		}
	}
}

// setFieldFor returns the schema's set field name for op when the
// accepted-types table admits it, nil otherwise (no schema, a name the
// schema does not carry, or a type a prior rule refuses).
func setFieldFor(op, name string, schema *encoding.Schema) *encoding.Field {
	if schema == nil {
		return nil
	}
	f := schema.Field(name)
	if f == nil || !f.Type.IsSet() {
		return nil
	}
	if fieldaccept.Refusal(op, fieldaccept.SlotField, name, f.Type) != nil {
		return nil
	}
	return f
}
