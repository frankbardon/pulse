// Package crosstabfuse is the one fused-crosstab eligibility rule shared
// by the engine and the no-execute predict layer.
// processing.CanFuseCrosstab (the dispatch gate in front of the fused
// in-decode crosstab arm) calls Decide with an adapter over the runtime
// ExtensionRegistry; internal/descriptor calls it with an adapter over
// the read-only ExtensionsSnapshot. Both therefore decline the same
// requests with the same reasons.
//
// The package imports only types and encoding so that both sides can
// reach it: internal/descriptor's predict files may not import
// internal/processing (TestPredictNoExecutionImports), and the engine
// does not import the descriptor layer (TestCrosstabFuse_ImportBoundary).
package crosstabfuse

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Facts answers everything the rule needs to know about operator names
// beyond the request and the schema. An implementation resolves a name
// the instance feature set hides exactly as a name registered nowhere,
// and must be safe to call on its zero / nil-backed value.
//
// Category strings are the lowercase singular forms the runtime's
// StreamabilityKey uses: aggregator, attribute, filterer, grouper.
type Facts interface {
	// AggregatorMergeable reports whether aggregator t's running state
	// folds — the built-in AggregationType.Mergeable() or an extension's
	// DECLARED Mergeable flag. Hidden or unknown: false.
	AggregatorMergeable(t types.AggregationType) bool
	// AggregatorMarginClass reports aggregator t's crosstab margin class
	// — the built-in table or an extension's DECLARED class, where an
	// undeclared class reads as types.MarginRecompute. Hidden:
	// types.MarginRecompute.
	AggregatorMarginClass(t types.AggregationType) types.MarginReducibility
	// AttributeTwoPass reports whether attribute t takes the two-pass
	// drive — the built-in two-pass set or an extension registered with
	// the two_pass mode. Hidden: false.
	AttributeTwoPass(t types.AttributeType) bool
	// GrouperKeyable reports whether grouper g derives its bucket key(s)
	// per record (StreamableGrouper or MultiKeyStreamingGrouper on the
	// engine side). A built-in answers from BuiltinGrouperKeyable; an
	// extension from its probe-validated DECLARED Streamable or FansOut
	// flag. Hidden or unknown: false. The runtime implementation may
	// also decline a grouper whose factory fails to construct against
	// schema — a fact the no-execute side cannot know, and a request the
	// buffered path refuses with a coded error anyway.
	GrouperKeyable(g *types.Group, schema *encoding.Schema) bool
	// IsExtension reports whether name is an embedder-registered
	// operator of category on this instance.
	IsExtension(category, name string) bool
	// HasFieldInputs reports whether the extension operator
	// (category, name) registered a FieldInputs hook.
	HasFieldInputs(category, name string) bool
	// Hidden reports whether the instance feature set hides operator
	// name. The rule's name-keyed checks (the ATTR_FORMULA and
	// FILTER_EXPRESSION bails) consult it so a hidden built-in answers
	// exactly as a never-registered name — every other check already
	// reaches names through the facts above.
	Hidden(name string) bool
}

// builtinGrouperKeyable is the static per-GroupType keyability table:
// true when the built-in grouper implements a per-record keying
// interface. It is deliberately wider than types.GroupType.Streamable(),
// which tracks Process-level streamability: GROUP_DATE is non-streamable
// at the Process layer but keys per record, so it fuses. GROUP_QUANTILE
// needs a finalize-time sorted view and keys nothing per record.
// GROUP_SET_PER_ELEMENT fans one record into N buckets
// (MultiKeyStreamingGrouper). Pinned against the constructed built-in
// instances by internal/processing's TestBuiltinGrouperKeyableMatchesRegistry.
var builtinGrouperKeyable = map[types.GroupType]bool{
	types.GROUP_CATEGORY:        true,
	types.GROUP_DATE:            true,
	types.GROUP_DATE_RANGES:     true,
	types.GROUP_QUANTILE:        false,
	types.GROUP_RANGE:           true,
	types.GROUP_ROUNDED:         true,
	types.GROUP_SET_VALUE:       true,
	types.GROUP_SET_PER_ELEMENT: true,
}

// BuiltinGrouperKeyable reports whether built-in grouper t keys per
// record, and whether t is a built-in at all.
func BuiltinGrouperKeyable(t types.GroupType) (keyable, known bool) {
	keyable, known = builtinGrouperKeyable[t]
	return keyable, known
}

// Decide reports whether req is eligible for the fused in-decode
// crosstab path, and every reason it is not. A true answer carries no
// reasons; a false one carries at least one, in rule order, and the
// first is the diagnostic processing.CanFuseCrosstab returns. Pure: req
// and schema are not modified. schema may be nil (the decimal checks
// are then skipped). A nil facts declines.
//
// Eligibility = ALL of: a crosstab spec; no joins (terminal — the
// schema is the left cohort's, against which right-side fields do not
// resolve); a mergeable cell aggregator whose margin class is summable,
// mean_reducible or independent and which is not a BUILT-IN over a
// decimal128 field; every margin aggregation mergeable and, for a
// built-in, not over a decimal128 field (its margin class is
// deliberately not consulted — an auxiliary has no cells); every axis
// grouper keyable; no features; no tests or post-tests; no two-pass
// attribute; no ATTR_FORMULA expression; no FILTER_EXPRESSION; and no
// extension operator without a FieldInputs hook. Overlays never block.
func Decide(req *types.Request, schema *encoding.Schema, facts Facts) (bool, []string) {
	if req == nil {
		return false, []string{"nil request"}
	}
	if req.Crosstab == nil {
		return false, []string{"no crosstab spec"}
	}
	if len(req.Joins) > 0 {
		return false, []string{"joins force buffered"}
	}
	if facts == nil {
		return false, []string{"no fusion facts"}
	}

	var reasons []string
	decline := func(r string) { reasons = append(reasons, r) }

	if cell := req.Crosstab.Cell; cell == nil {
		decline("missing cell aggregator")
	} else {
		cellOK := true
		if !facts.AggregatorMergeable(cell.Type) {
			decline(fmt.Sprintf("non-mergeable cell aggregator (%s)", cell.Type))
			cellOK = false
		} else {
			switch facts.AggregatorMarginClass(cell.Type) {
			case types.MarginSummable, types.MarginMeanReducible, types.MarginIndependent:
			default:
				decline(fmt.Sprintf("recompute-margin cell aggregator (%s)", cell.Type))
				cellOK = false
			}
		}
		if cellOK && isBuiltinDecimalTarget(schema, cell.Field, cell.Type, facts) {
			decline(fmt.Sprintf("decimal128 cell field (%s)", cell.Field))
		}
	}

	for _, aux := range req.Crosstab.MarginAggregations {
		if aux == nil || aux.Type == "" {
			// Structurally malformed: validateCrosstabSpec refuses both
			// shapes with a coded error on either path.
			continue
		}
		if !facts.AggregatorMergeable(aux.Type) {
			decline(fmt.Sprintf("non-mergeable margin aggregation (%s)", aux.Type))
			continue
		}
		if isBuiltinDecimalTarget(schema, aux.Field, aux.Type, facts) {
			decline(fmt.Sprintf("decimal128 margin aggregation field (%s)", aux.Field))
		}
	}

	axisKeyable(req.Crosstab.Rows, schema, facts, "row", decline)
	axisKeyable(req.Crosstab.Columns, schema, facts, "column", decline)

	if len(req.Features) > 0 {
		decline("features force buffered")
	}
	if len(req.Tests) > 0 || len(req.PostTests) > 0 {
		decline("stat tests force buffered")
	}

	for _, a := range req.Attributes {
		if a != nil && facts.AttributeTwoPass(a.Type) {
			decline(fmt.Sprintf("two-pass attribute (%s)", a.Type))
		}
	}
	for _, a := range req.Attributes {
		if a != nil && a.Type == types.ATTR_FORMULA && a.Expression != "" && !facts.Hidden(string(types.ATTR_FORMULA)) {
			decline("ATTR_FORMULA bail")
			break
		}
	}
	for _, f := range req.Filterers {
		if f != nil && f.Type == types.FILTER_EXPRESSION && !facts.Hidden(string(types.FILTER_EXPRESSION)) {
			decline("FILTER_EXPRESSION bail")
			break
		}
	}

	unintrospectable(req, facts, decline)

	return len(reasons) == 0, reasons
}

// isBuiltinDecimalTarget reports whether a BUILT-IN aggregator targets
// a decimal128 field. An extension aggregator reads decimals itself
// through DecimalValue on both arms, so it is exempt.
func isBuiltinDecimalTarget(schema *encoding.Schema, field string, t types.AggregationType, facts Facts) bool {
	if schema == nil || field == "" || facts.IsExtension("aggregator", string(t)) {
		return false
	}
	f := schema.Field(field)
	return f != nil && f.Type.IsDecimal()
}

// axisKeyable declines every grouper on axis that is nil or not
// keyable, in axis order. The reason wording is an internal diagnostic
// retained verbatim from the probe-based gate.
func axisKeyable(axis []*types.Group, schema *encoding.Schema, facts Facts, axisName string, decline func(string)) {
	for _, g := range axis {
		if g == nil {
			decline(fmt.Sprintf("nil grouper on %s axis", axisName))
			continue
		}
		if !facts.GrouperKeyable(g, schema) {
			decline(fmt.Sprintf("non-streamable grouper on %s axis (%s)", axisName, g.Type))
		}
	}
}

// unintrospectable declines every extension operator in req registered
// without a FieldInputs hook: the fused path's decode budget rests on a
// tight NeededFields projection, which an opaque operator widens to
// every field. Built-ins are never checked.
func unintrospectable(req *types.Request, facts Facts, decline func(string)) {
	check := func(category, name string) {
		if facts.IsExtension(category, name) && !facts.HasFieldInputs(category, name) {
			decline(fmt.Sprintf("extension %s %s without FieldInputs", category, name))
		}
	}
	for _, a := range req.Aggregations {
		if a != nil {
			check("aggregator", string(a.Type))
		}
	}
	if req.Crosstab.Cell != nil {
		check("aggregator", string(req.Crosstab.Cell.Type))
	}
	for _, a := range req.Attributes {
		if a != nil {
			check("attribute", string(a.Type))
		}
	}
	for _, f := range req.Filterers {
		if f != nil {
			check("filterer", string(f.Type))
		}
	}
	for _, axis := range [][]*types.Group{req.Groups, req.Crosstab.Rows, req.Crosstab.Columns} {
		for _, g := range axis {
			if g != nil {
				check("grouper", string(g.Type))
			}
		}
	}
}
