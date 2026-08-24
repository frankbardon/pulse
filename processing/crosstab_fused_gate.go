package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// CanFuseCrosstab reports whether a crosstab request is eligible for
// the fused in-decode execution path that materializes per-cell state
// while iterating records — bypassing the full buffered Row materializa-
// tion the standard processCrosstab path uses today. Pure predicate: no
// side effects, no execution.
//
// Naming mirrors CanMergeRequest / CanStreamRequest / CanChainRequest.
// The returned reason string is short and operator-specific so callers
// (dispatch in service/crosstab.go, predict surfaces in a follow-up)
// can surface a human-readable explanation without re-deriving the
// rule.
//
// Eligibility = ALL of:
//
//   - req.Crosstab != nil — nothing to fuse otherwise.
//   - The cell aggregator is mergeable per AggregationType.Mergeable().
//     The fused path folds per-cell online state row-by-row; non-
//     mergeable aggregators (median/percentile/zscore/skewness/kurtosis)
//     need a finalize-time sorted view that the fused walk cannot
//     provide.
//   - The cell aggregator's MarginReducibility is MarginSummable or
//     MarginMeanReducible. MarginRecompute aggregators force a re-scan
//     of raw rows for margin derivation, which defeats the fused path
//     by construction. The two non-recompute classes overlap exactly
//     with the set of mergeable, scalar, cell-derived aggregators.
//   - Every grouper on req.Crosstab.Rows ∪ req.Crosstab.Columns is
//     constructable and the resulting instance implements
//     StreamableGrouper (i.e. exposes a per-record KeyFor). The static
//     types.GroupType.Streamable() table is too narrow here — it tracks
//     Process-level streamability rather than per-record key derivation
//     (GROUP_DATE is non-streamable at the Process layer but does
//     implement StreamableGrouper.KeyFor and is therefore fusable). We
//     consult the actual interface via a factory-probe to widen the
//     gate while still rejecting truly key-non-derivable groupers
//     (GROUP_QUANTILE).
//   - No req.Features — every FEAT_* operator forces a buffered
//     pre-filter pass that the fused path skips.
//   - No req.Attributes of type ATTR_FORMULA with a non-empty
//     Expression. Expression-runtime field extraction is conservative;
//     #59 bail rules treat it as a forced widen, which the fused path
//     can't honour while keeping per-field decode bounds tight.
//   - No req.Filterers of type FILTER_EXPRESSION (same reason).
//   - No req.Tests and no req.PostTests. Tier-1 row tests and tier-2
//     post-tests fold over the buffered row set after aggregation;
//     the fused path doesn't buffer.
//   - No extension-bound operator anywhere in the request without a
//     registered FieldInputs hook. The fused path's projection bound
//     is built from NeededFields; an opaque extension operator would
//     widen the projection to "every field", which collapses the fused
//     path's decode-cost advantage and is treated as ineligible here.
//   - No mergeable-but-decimal aggregation target on the cell. Decimal-
//     typed fields aggregate via AggregateDecimalField (the wide
//     decimal path); Pulse forces buffered for those today and the
//     fused gate mirrors that constraint.
//
// req.Overlays is explicitly NOT an exclusion. Overlays decorate a
// finalised response and consume no records, so RunCrosstabFused folds
// them at its exit through the same applyOverlaysToResponse hook the
// buffered exit uses. An overlay-carrying crosstab is fusable whenever
// the rest of the request is.
//
// Returns (true, "") for an eligible request. Returns (false, reason)
// for an ineligible one — the reason is intentionally short ("non-
// mergeable cell aggregator (AGG_MEDIAN)", "stat tests force buffered",
// "non-streamable grouper on column axis (GROUP_QUANTILE)",
// "ATTR_FORMULA bail", etc.).
//
// The gate is a pure predicate. It does NOT modify req or schema, and
// it does NOT touch the orchestrator (RunCrosstab / processCrosstab).
// service/crosstab.go wires the dispatch around the result of this
// call.
func CanFuseCrosstab(req *types.Request, schema *encoding.Schema, ext *ExtensionRegistry) (bool, string) {
	if req == nil {
		return false, "nil request"
	}
	if req.Crosstab == nil {
		return false, "no crosstab spec"
	}

	// Cell aggregator must exist; mergeable + scalar margin reducibility.
	cell := req.Crosstab.Cell
	if cell == nil {
		return false, "missing cell aggregator"
	}
	if !cell.Type.Mergeable() {
		return false, fmt.Sprintf("non-mergeable cell aggregator (%s)", cell.Type)
	}
	switch cell.Type.MarginReducibility() {
	case types.MarginSummable, types.MarginMeanReducible:
		// fused-eligible.
	default:
		// MarginRecompute aggregators force a raw-row rescan for margin
		// derivation — the fused path can't satisfy that without
		// buffering. Surfaced separately from Mergeable() because a
		// hypothetical mergeable+recompute aggregator (none today, but
		// the classification is independent) should still fall out.
		return false, fmt.Sprintf("recompute-margin cell aggregator (%s)", cell.Type)
	}

	// Decimal-typed cell field forces the buffered decimal path today.
	if schema != nil && cell.Field != "" {
		if f := schema.Field(cell.Field); f != nil && f.Type.IsDecimal() {
			return false, fmt.Sprintf("decimal128 cell field (%s)", cell.Field)
		}
	}

	// Every grouper on either axis must implement StreamableGrouper.
	// Probe-construct via the registry's factory and assert the
	// interface — this widens past the conservative
	// types.GroupType.Streamable() table to admit per-record-keyable
	// groupers like GROUP_DATE while still rejecting GROUP_QUANTILE
	// (which has no per-record key derivation).
	if reason, ok := axisStreamable(req.Crosstab.Rows, schema, ext, "row"); !ok {
		return false, reason
	}
	if reason, ok := axisStreamable(req.Crosstab.Columns, schema, ext, "column"); !ok {
		return false, reason
	}

	// Features force a buffered pre-filter pass.
	if len(req.Features) > 0 {
		return false, "features force buffered"
	}

	// Tier-1 row tests and tier-2 post-tests fold over the buffered row
	// set after aggregation — the fused path doesn't buffer.
	if len(req.Tests) > 0 || len(req.PostTests) > 0 {
		return false, "stat tests force buffered"
	}

	// NOTE: req.Overlays is deliberately NOT an exclusion. The overlay
	// fold (processing/crosstab.go applyOverlaysToResponse) consumes no
	// records — it reads only the finalised resp.Crosstab.Matrix and
	// resp.Components.Crosstab, both of which
	// FusedCrosstabState.Finalize produces. RunCrosstabFused calls the
	// same hook the buffered exit calls, so Response.Overlays is
	// populated identically on either path. This does NOT make any
	// OverlayKind streamable: types.OverlayStreamability answers
	// whether a kind can be computed INSIDE the streaming pass, and a
	// post-Finalize fold is not that — every row of that table stays
	// false and nothing in the fused path reads it.

	// ATTR_FORMULA with a non-empty expression bails the projection
	// extractor when the expression is malformed; even when it parses,
	// the fused path can't keep its decode bound tight under
	// expression-runtime field access.
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		if a.Type == types.ATTR_FORMULA && a.Expression != "" {
			return false, "ATTR_FORMULA bail"
		}
	}

	// FILTER_EXPRESSION bails for the same reason as ATTR_FORMULA.
	for _, f := range req.Filterers {
		if f == nil {
			continue
		}
		if f.Type == types.FILTER_EXPRESSION {
			return false, "FILTER_EXPRESSION bail"
		}
	}

	// Extension operators without a registered FieldInputs hook force
	// the projection extractor to widen to "every field". The fused
	// path's per-record decode budget depends on a tight projection,
	// so we treat unintrospectable extensions as ineligible. Built-in
	// operators are not stored in the overlay maps; only extension-
	// resolved entries are walked here.
	if ext != nil {
		if reason, ok := unintrospectableExtension(req, ext); !ok {
			return false, reason
		}
	}

	return true, ""
}

// axisStreamable probe-constructs each grouper on an axis via the
// registry factory and asserts that the resulting instance implements
// StreamableGrouper. The probe is cheap — built-in grouper factories
// validate Params (e.g. GROUP_DATE's component / fiscal_offset) and
// stash the field name, but do not touch records.
//
// Returns ("", true) when every grouper is streamable. Returns
// (reason, false) on the first miss, with the reason in the same shape
// the previous types.GroupType.Streamable()-based gate produced:
// "non-streamable grouper on <axis> axis (<TYPE>)". An unknown grouper
// type (factory miss) likewise disqualifies the request — the runtime
// would have errored a moment later anyway.
//
// axisName is the human-readable axis label embedded in the reason
// string ("row" / "column").
func axisStreamable(axis []*types.Group, schema *encoding.Schema, ext *ExtensionRegistry, axisName string) (string, bool) {
	for _, g := range axis {
		if g == nil {
			return fmt.Sprintf("nil grouper on %s axis", axisName), false
		}
		factory, ok := ext.LookupGrouper(g.Type)
		if !ok {
			return fmt.Sprintf("non-streamable grouper on %s axis (%s)", axisName, g.Type), false
		}
		instance, err := factory(g, schema)
		if err != nil {
			// Construction failure surfaces as a buffered fallback —
			// the buffered path's RunCrosstab will surface the same
			// error with a richer code, and the gate just needs to
			// decline fusion. Mirror the reason shape the other branches return.
			return fmt.Sprintf("non-streamable grouper on %s axis (%s)", axisName, g.Type), false
		}
		ApplyGrouperExtensions(instance, ext)
		if _, ok := instance.(StreamableGrouper); !ok {
			return fmt.Sprintf("non-streamable grouper on %s axis (%s)", axisName, g.Type), false
		}
	}
	return "", true
}

// unintrospectableExtension scans every operator slot in req against
// the extension overlay and reports the first extension operator that
// is registered without a FieldInputs hook. Returns ("", true) when
// every extension operator is introspectable (or no extension
// operators are present); returns (reason, false) on the first miss.
//
// The category labels match StreamabilityKey conventions so the reason
// string lines up with the registration error surface.
func unintrospectableExtension(req *types.Request, ext *ExtensionRegistry) (string, bool) {
	check := func(category, name string) (string, bool) {
		if _, ok := ext.FieldInputs[StreamabilityKey(category, name)]; ok {
			return "", true
		}
		return fmt.Sprintf("extension %s %s without FieldInputs", category, name), false
	}

	for _, a := range req.Aggregations {
		if a == nil {
			continue
		}
		if _, custom := ext.Aggregators[a.Type]; custom {
			if reason, ok := check("aggregator", string(a.Type)); !ok {
				return reason, false
			}
		}
	}
	if req.Crosstab != nil && req.Crosstab.Cell != nil {
		a := req.Crosstab.Cell
		if _, custom := ext.Aggregators[a.Type]; custom {
			if reason, ok := check("aggregator", string(a.Type)); !ok {
				return reason, false
			}
		}
	}
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		if _, custom := ext.Attributes[a.Type]; custom {
			if reason, ok := check("attribute", string(a.Type)); !ok {
				return reason, false
			}
		}
	}
	for _, f := range req.Filterers {
		if f == nil {
			continue
		}
		if _, custom := ext.Filterers[f.Type]; custom {
			if reason, ok := check("filterer", string(f.Type)); !ok {
				return reason, false
			}
		}
	}
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		if _, custom := ext.Groupers[g.Type]; custom {
			if reason, ok := check("grouper", string(g.Type)); !ok {
				return reason, false
			}
		}
	}
	if req.Crosstab != nil {
		for _, g := range req.Crosstab.Rows {
			if g == nil {
				continue
			}
			if _, custom := ext.Groupers[g.Type]; custom {
				if reason, ok := check("grouper", string(g.Type)); !ok {
					return reason, false
				}
			}
		}
		for _, g := range req.Crosstab.Columns {
			if g == nil {
				continue
			}
			if _, custom := ext.Groupers[g.Type]; custom {
				if reason, ok := check("grouper", string(g.Type)); !ok {
					return reason, false
				}
			}
		}
	}
	return "", true
}
