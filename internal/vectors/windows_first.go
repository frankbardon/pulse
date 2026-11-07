package vectors

import (
	"encoding/json"

	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
// An extension aggregator's or grouper's FieldInputs names count as its
// column reads (Classifier.Ext, AggregationColumnReads,
// GroupColumnReads): a window label read only through one turns the
// mode on exactly as a built-in's field does, and the windows-first
// pass copies it onto the records the operator folds or keys. Predict hands the classifier its extensions snapshot's hooks,
// the runtime its registry's — the same callbacks over the same params
// (ExtInputs), so the two classify alike.

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

// WindowsFirst reports whether req runs windows-first over c (Route:
// the instance's view of an attribute type; Cat: the bound models an
// ATTR_REG_PREDICT or params.fit attribute reads its terms from; Ext:
// the extension FieldInputs hooks an extension aggregator's column
// reads come from). False when req has no window, or has no group,
// aggregation and crosstab (its windows run over the record rows
// anyway). A window label equal to a schema field or to a named vector
// of req is no window output (as in PostWindowAttributes).
func WindowsFirst(req *types.Request, schema *encoding.Schema, c Classifier) bool {
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
		rt := routeOf(c.Route, attr.Type)
		if !PostWindowEligible(rt, attr) {
			continue
		}
		for _, n := range attributeReads(attr, rt, c.Cat) {
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
	return outputSlotsRead(req, derived, c.Ext)
}

// WindowsOverRecords reports whether req's windows run over the record
// rows: req has windows and either no groups, aggregations or crosstab,
// or runs windows-first (WindowsFirst over c: the site's bound-model
// catalog and extension hooks). Only then is a window label a
// per-record column.
// It is the ONE windows-first decision of every site that reads window
// labels off the records — computed columns (ComputedColumns, so vector
// resolution, projection and the matrix copy), regression window reads,
// the walk's regression and holdout checks — and each passes the
// classifier it holds, the one the post-window attribute plan
// (PostWindowAttributes) classifies with: a request windows-first only
// through a bound model's terms (an aggregation over an
// ATTR_REG_PREDICT label whose model reads a window) or an extension
// aggregator's FieldInputs is windows-first for its regressions,
// holdouts and vector members too.
func WindowsOverRecords(req *types.Request, schema *encoding.Schema, c Classifier) bool {
	return req != nil && len(req.Windows) > 0 && (recordRowShape(req) || WindowsFirst(req, schema, c))
}

// CrosstabWindowRefusal is PULSE_WINDOW_INVALID details.reason
// "crosstab_window_unread" {window_index} for the first window of a
// crosstab request that does not run windows-first (windowsFirst: the
// request's plan, PostWindow.WindowsFirst over the site's catalog). On
// the historical order a window runs over the output rows — a grouped or
// aggregated request's, which may lack the names it reads (then
// refused) — but a crosstab writes no row a window could run over, so
// its windows run only windows-first: one no axis, cell or margin
// aggregation reads (directly or through a post-window attribute) would
// be silently dropped. Nil for any other request. The field-reference
// walk (predict and the service's entry check) and RunCrosstab refuse
// with it.
func CrosstabWindowRefusal(req *types.Request, windowsFirst bool) *errors.CodedError {
	if req == nil || req.Crosstab == nil || windowsFirst {
		return nil
	}
	for i, w := range req.Windows {
		if w == nil {
			continue
		}
		idx := strconv.Itoa(i)
		return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
			"window["+idx+"] ("+string(w.Type)+") output "+strconv.Quote(WindowLabel(w))+
				" is read by no crosstab axis, cell or margin aggregation; a crosstab writes no output row for a window to run over, so its windows run first over the records only when the crosstab reads one: read the window label from an axis grouper, the cell or a margin aggregation, or drop the window",
			map[string]any{"window_index": i, "reason": "crosstab_window_unread"})
	}
	return nil
}

// windowDerived is the set of req's window labels that name a window
// output (not a schema field, not a named vector of req).
func windowDerived(req *types.Request, schema *encoding.Schema) map[string]bool {
	derived := make(map[string]bool, len(req.Windows))
	for _, w := range req.Windows {
		if l, ok := windowDerivedLabel(req, schema, w); ok {
			derived[l] = true
		}
	}
	return derived
}

// outputSlotsRead reports whether any aggregation, grouper or crosstab
// axis / cell / margin aggregation of req reads a name in derived (ext:
// the extension hooks, AggregationColumnReads / GroupColumnReads).
func outputSlotsRead(req *types.Request, derived map[string]bool, ext ExtInputs) bool {
	for _, g := range req.Groups {
		if groupReads(g, derived, ext) {
			return true
		}
	}
	for _, a := range req.Aggregations {
		if aggregationReads(a, derived, ext) {
			return true
		}
	}
	spec := req.Crosstab
	if spec == nil {
		return false
	}
	for _, axis := range [][]*types.Group{spec.Rows, spec.Columns} {
		for _, g := range axis {
			if groupReads(g, derived, ext) {
				return true
			}
		}
	}
	for _, a := range spec.CellAggregations() {
		if aggregationReads(a, derived, ext) {
			return true
		}
	}
	for _, a := range spec.MarginAggregations {
		if aggregationReads(a, derived, ext) {
			return true
		}
	}
	return false
}

// AggregationColumnReads lists the record columns aggregation a reads
// by name: its Field, then each built-in column param
// (AggregationParamFieldKeys) that is set, in key order, then — an
// extension aggregator — every non-empty name its FieldInputs hook
// declares (ext, nil: none), in hook order. Nil for a nil slot.
func AggregationColumnReads(a *types.Aggregation, ext ExtInputs) []string {
	if a == nil {
		return nil
	}
	var out []string
	if a.Field != "" {
		out = append(out, a.Field)
	}
	if keys := AggregationParamFieldKeys[a.Type]; len(keys) > 0 && len(a.Params) > 0 {
		var m map[string]json.RawMessage
		if paramread.Probe(a.Params, &m) == nil {
			for _, k := range keys {
				var s string
				if raw, ok := m[k]; ok && paramread.Probe(raw, &s) == nil && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	if ext != nil {
		for _, n := range ext("aggregator", string(a.Type), a.Params) {
			if n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// GroupColumnReads lists the record columns grouper g reads by name:
// its Field, then — an extension grouper — every non-empty name its
// FieldInputs hook declares (ext, nil: none), in hook order. Nil for a
// nil slot.
func GroupColumnReads(g *types.Group, ext ExtInputs) []string {
	if g == nil {
		return nil
	}
	var out []string
	if g.Field != "" {
		out = append(out, g.Field)
	}
	if ext != nil {
		for _, n := range ext("grouper", string(g.Type), g.Params) {
			if n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// groupReads reports whether g reads a name in derived.
func groupReads(g *types.Group, derived map[string]bool, ext ExtInputs) bool {
	for _, n := range GroupColumnReads(g, ext) {
		if derived[n] {
			return true
		}
	}
	return false
}

// aggregationReads reports whether a reads a name in derived.
func aggregationReads(a *types.Aggregation, derived map[string]bool, ext ExtInputs) bool {
	for _, n := range AggregationColumnReads(a, ext) {
		if derived[n] {
			return true
		}
	}
	return false
}

// WindowsFirstReads lists the window output labels (WindowLabel, not a
// schema field, not a named vector of req) that req's aggregations,
// groupers and crosstab axes / cell / margin aggregations read by name,
// in first-read order: aggregations (AggregationColumnReads over
// c.Ext, so an extension aggregator's FieldInputs count), then
// groupers (GroupColumnReads, likewise), then the crosstab's row and
// column axes, its cell and its margin aggregations. The windows-first engine copies exactly these
// onto the records ahead of the aggregation pass; a post-window
// attribute's label is not listed (the attribute writes it on the
// record itself). Nil when req is not windows-first-shaped or reads
// none.
func WindowsFirstReads(req *types.Request, schema *encoding.Schema, c Classifier) []string {
	if req == nil || len(req.Windows) == 0 || recordRowShape(req) {
		return nil
	}
	derived := windowDerived(req, schema)
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && derived[n] && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, a := range req.Aggregations {
		for _, n := range AggregationColumnReads(a, c.Ext) {
			add(n)
		}
	}
	for _, g := range req.Groups {
		for _, n := range GroupColumnReads(g, c.Ext) {
			add(n)
		}
	}
	if spec := req.Crosstab; spec != nil {
		for _, axis := range [][]*types.Group{spec.Rows, spec.Columns} {
			for _, g := range axis {
				for _, n := range GroupColumnReads(g, c.Ext) {
					add(n)
				}
			}
		}
		for _, a := range spec.CellAggregations() {
			for _, n := range AggregationColumnReads(a, c.Ext) {
				add(n)
			}
		}
		for _, a := range spec.MarginAggregations {
			for _, n := range AggregationColumnReads(a, c.Ext) {
				add(n)
			}
		}
	}
	return out
}
