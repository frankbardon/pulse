package vectors

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// windows_first.go is the windows-first classification (mmm-followups
// E4-S1): the ONE rule deciding where a request's windows run, read by
// the post-window attribute plan (PostWindowAttributes), the computed
// columns a vector or regression may read (ComputedColumns,
// RegressionWindowReads), the field-reference walk (predict and the
// runtime entry check), predict's streamability, Explain and the
// PULSE_ATTRIBUTE_WINDOW_ORDER text — so none of them can disagree.
//
// Historically windows run AFTER the groups, aggregations and crosstab,
// over their output rows (a window over a grouped AGG_SUM), and over
// the record rows only when the request has none of the three. A
// request is WINDOWS-FIRST when it has groups, aggregations or a
// crosstab AND one of them reads a window output: an aggregation's
// field (or a built-in's column param, AggregationParamFieldKeys), a
// grouper's field, or a crosstab axis grouper / cell / margin
// aggregation names a window label — or the label of a row-local
// attribute that reads one, directly or through an earlier such
// attribute (the post-window chain). Its windows then run first, over
// the filtered record rows, and the groups, aggregations and crosstab
// read the window (and post-window attribute) values off the records.
// A request no slot of which reads a window output keeps the historical
// order: every such name was refused before (unknown field, or
// PULSE_ATTRIBUTE_WINDOW_ORDER), so no valid request changes meaning.
//
// An extension aggregator's FieldInputs names are not visible here (the
// classification is static and extension-free): a window label read
// only through one does not turn the mode on, and the walk refuses it
// as an unknown field.

// AggregationParamFieldKeys lists, per built-in aggregation, the Params
// keys that name a record column (the set the projection extractor's
// addAggParamFields decodes). The field-reference walk judges them; the
// windows-first classification reads them.
var AggregationParamFieldKeys = map[types.AggregationType][]string{
	types.AGG_WEIGHTED_MEAN: {"weight_field"},
	types.AGG_RATIO:         {"numerator_field", "denominator_field"},
	types.AGG_DISTINCT_SUM:  {"distinct_by"},
	types.AGG_MAPE:          {"prediction_field"},
	types.AGG_RMSE:          {"prediction_field"},
	types.AGG_MAE:           {"prediction_field"},
	types.AGG_AUC:           {"outcome"},
}

// recordRowShape reports whether req has no groups, no aggregations and
// no crosstab: its windows (if any) run over the record rows in every
// mode.
func recordRowShape(req *types.Request) bool {
	return len(req.Groups) == 0 && len(req.Aggregations) == 0 && req.Crosstab == nil
}

// WindowsFirst reports whether req runs windows-first (route: the
// instance's view of an attribute type, nil the identity; cat: the bound
// models an ATTR_REG_PREDICT or params.fit attribute reads its terms
// from, nil none). False when req has no window, or has no group,
// aggregation and crosstab (its windows run over the record rows
// anyway). A window label equal to a schema field or to a named vector
// of req is no window output (as in PostWindowAttributes).
func WindowsFirst(req *types.Request, schema *encoding.Schema, route func(types.AttributeType) types.AttributeType, cat model.Catalog) bool {
	if req == nil || len(req.Windows) == 0 || recordRowShape(req) {
		return false
	}
	derived := windowDerived(req, schema)
	if len(derived) == 0 {
		return false
	}
	// The post-window chain, as the windows over the record rows would
	// run it: a row-local attribute reading a derived name derives its
	// own label.
	for _, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		rt := routeOf(route, attr.Type)
		if !PostWindowEligible(rt, attr) {
			continue
		}
		for _, n := range attributeReads(attr, rt, cat) {
			if derived[n] {
				label := attr.Label
				if label == "" {
					label = DefaultAttributeLabel(attr)
				}
				derived[label] = true
				break
			}
		}
	}
	return outputSlotsRead(req, derived)
}

// WindowsOverRecords reports whether req's windows run over the record
// rows: req has windows and either no groups, aggregations or crosstab,
// or runs windows-first (WindowsFirst, catalog- and route-free — the
// view of every site that holds no model catalog: computed columns,
// regression window reads, the walk's regression and holdout checks).
// Only then is a window label a per-record column. A windows-first
// request turned on solely by a bound model's terms (an aggregation over
// an ATTR_REG_PREDICT label whose model reads a window) is windows-first
// for its attributes (PostWindowAttributes takes the catalog) but not
// here: a regression or vector reading a window label in it is refused
// on both sides alike.
func WindowsOverRecords(req *types.Request, schema *encoding.Schema) bool {
	return req != nil && len(req.Windows) > 0 && (recordRowShape(req) || WindowsFirst(req, schema, nil, nil))
}

// windowDerived is the set of req's window labels that name a window
// output (not a schema field, not a named vector of req).
func windowDerived(req *types.Request, schema *encoding.Schema) map[string]bool {
	derived := make(map[string]bool, len(req.Windows))
	for _, w := range req.Windows {
		if w == nil {
			continue
		}
		l := WindowLabel(w)
		if (schema != nil && schema.Field(l) != nil) || vectorNamed(req, l) {
			continue
		}
		derived[l] = true
	}
	return derived
}

// outputSlotsRead reports whether any aggregation, grouper or crosstab
// axis / cell / margin aggregation of req reads a name in derived.
func outputSlotsRead(req *types.Request, derived map[string]bool) bool {
	for _, g := range req.Groups {
		if g != nil && g.Field != "" && derived[g.Field] {
			return true
		}
	}
	for _, a := range req.Aggregations {
		if aggregationReads(a, derived) {
			return true
		}
	}
	spec := req.Crosstab
	if spec == nil {
		return false
	}
	for _, axis := range [][]*types.Group{spec.Rows, spec.Columns} {
		for _, g := range axis {
			if g != nil && g.Field != "" && derived[g.Field] {
				return true
			}
		}
	}
	for _, a := range spec.CellAggregations() {
		if aggregationReads(a, derived) {
			return true
		}
	}
	for _, a := range spec.MarginAggregations {
		if aggregationReads(a, derived) {
			return true
		}
	}
	return false
}

// AggregationColumnReads lists the record columns aggregation a reads
// by name: its Field, then each built-in column param
// (AggregationParamFieldKeys) that is set, in key order. Nil for a nil
// slot.
func AggregationColumnReads(a *types.Aggregation) []string {
	if a == nil {
		return nil
	}
	var out []string
	if a.Field != "" {
		out = append(out, a.Field)
	}
	keys := AggregationParamFieldKeys[a.Type]
	if len(keys) == 0 || len(a.Params) == 0 {
		return out
	}
	var m map[string]json.RawMessage
	if paramread.Probe(a.Params, &m) != nil {
		return out
	}
	for _, k := range keys {
		var s string
		if raw, ok := m[k]; ok && paramread.Probe(raw, &s) == nil && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// aggregationReads reports whether a reads a name in derived.
func aggregationReads(a *types.Aggregation, derived map[string]bool) bool {
	for _, n := range AggregationColumnReads(a) {
		if derived[n] {
			return true
		}
	}
	return false
}
