package processing

import (
	"github.com/frankbardon/pulse/types"
)

// SERIES-host overlay → Response wiring for the grouped Process
// orchestrator.
//
// E3-S6 scope: the buffered processGrouped and streaming
// processStreamingGrouped exit points both produce a `[]map[string]any`
// per-group row set on `Response.Data`. This file lands the shared
// post-finalize hook that wraps that row set as a SeriesHostView, runs
// ApplyOverlaysSeries (overlay_series.go) against it, and folds the
// resulting OverlayLayer slice + warnings onto the Response.
//
// Wiring discipline:
//
//   - The hook is GROUPED-Process scoped. The buffered no-group fall-
//     through (one summary row per request) and the streaming two-pass
//     attribute path are out of scope for E3-S6 — overlays on those
//     hosts surface in later epics (E4 windowed Process, etc.). When the
//     hook is called with no Groups slot, it short-circuits.
//   - When `req.Overlays` is empty the hook short-circuits before
//     touching the host or building any SeriesHostView. This keeps the
//     no-overlay Response byte-identical to the pre-E3 grouped path,
//     mirroring the additive byte-identity contract the MATRIX overlay
//     hook already enforces in processing/crosstab.go.
//   - Crosstab requests are handled by the MATRIX-host hook in
//     processing/crosstab.go (applyOverlaysToResponse). The SERIES hook
//     defends against being called on a Crosstab request by short-
//     circuiting when `req.Crosstab != nil`. Predict already routes
//     Crosstab-bearing requests through the MATRIX shape gate, so the
//     SERIES path never sees a Crosstab.
//   - Unknown overlay kinds propagate as a hard coded error (mirrors the
//     MATRIX path: ApplyOverlaysSeries returns a CodedError carrying
//     PULSE_OVERLAY_KIND_UNKNOWN in its details; the wiring lets the
//     error bubble to the caller without populating Response.Overlays).
//
// Mixed-mode downgrade rule (per PRD §6 "Performance / scale"): a
// request that mixes a streamable overlay (E3-S2/S3/S4 — INDEX_VS_TOTAL
// / SHARE_OF_TOTAL / ZSCORE_VS_TOTAL) with a non-streamable overlay
// (E3-S5 — DELTA_VS_SIBLING / INDEX_VS_SIBLING) forces the whole
// Request to the buffered path. The downgrade is enforced inside the
// Processor.canStream gate (processing/processor.go) by walking
// req.Overlays against types.OverlayStreamability — any non-streamable
// kind returns false from canStream and the buffered processRecords
// path runs instead. The post-finalize hook below is path-independent:
// both the buffered processRecords exit and the streaming
// processStreamingGrouped exit call into it identically once the
// per-group Response.Data has landed.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. The overlay
//     fold runs entirely inside processing/ alongside overlay.go and
//     overlay_series.go.
//   - No fmt.Sprintf in any JSON-bearing path; warning details carry
//     structured maps, not formatted strings.

// applyOverlaysSeriesToResponse is the grouped-Process post-finalize
// overlay hook. Called at the buffered processGrouped exit AND the
// streaming processStreamingGrouped exit just before Response is
// returned to the caller. No-op when req.Overlays is empty (additive
// byte-identity), when the request is a Crosstab (the MATRIX hook in
// processing/crosstab.go owns Crosstab response wiring), when the
// request has no Groups slot (overlays attach to grouped output only in
// E3), or when no aggregation produced a primary value to fold over.
//
// On success, resp.Overlays carries one OverlayLayer per spec in
// matching order and resp.Warnings is extended with one
// types.ResponseWarning per OverlayWarning the handlers emitted
// (mirrors processing/crosstab.go applyOverlaysToResponse — the same
// warning-promotion shape labels and stat tests use).
//
// On unknown overlay kind, ApplyOverlaysSeries returns a coded
// PROCESSING_INTERNAL error whose details carry PULSE_OVERLAY_KIND_UNKNOWN.
// The error bubbles to the caller; resp.Overlays is left nil.
func applyOverlaysSeriesToResponse(req *types.Request, resp *types.Response) error {
	if req == nil || len(req.Overlays) == 0 {
		return nil
	}
	if req.Crosstab != nil {
		// Crosstab requests own a MATRIX host; their hook lives in
		// processing/crosstab.go (applyOverlaysToResponse). Predict
		// already routes a Crosstab + Overlays request through the
		// MATRIX shape gate, so the SERIES hook never sees one — this
		// branch is defense in depth.
		return nil
	}
	if len(req.Groups) == 0 {
		// E3 scope is GROUPED Process. Overlays on a single-row
		// (no-Groups) host surface in later epics; predict already
		// rejects this combination, so the runtime hook short-circuits
		// without touching resp.Overlays.
		return nil
	}
	if resp == nil || len(req.Aggregations) == 0 {
		// No primary aggregator → no host metric to fold over. Predict
		// catches Overlays + empty Aggregations at the validation gate;
		// the runtime hook short-circuits defensively.
		return nil
	}

	host := buildSeriesHostFromGroupedResponse(req, resp)
	if host == nil {
		return nil
	}
	layers, warnings, err := ApplyOverlaysSeries(req.Overlays, host)
	if err != nil {
		return err
	}
	if len(layers) > 0 {
		resp.Overlays = layers
	}
	for _, w := range warnings {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    w.Code,
			Message: w.Message,
			Details: w.Details,
		})
	}
	return nil
}

// buildSeriesHostFromGroupedResponse builds a SeriesHostView wrapping
// the per-group rows on resp.Data. Each row's group-key entry (under
// req.Groups[i].Field) becomes one element of the AxisKey tuple at the
// row's ordinal; the primary aggregator's value (under
// AggregationLabel(req.Aggregations[0])) becomes the host's resolver
// output for that ordinal.
//
// Group-key extraction: the row map carries the canonical group-key
// representation the grouped path emits (string-typed for category /
// range / rounded / date keys; mirroring processGrouped /
// processStreamingGrouped). The helper copies the value verbatim onto
// AxisKey[i] so resolveSibling and absent-key handlers see the same
// shape they would against a Crosstab MATRIX axis.
//
// Resolver wiring: the primary aggregator is req.Aggregations[0]; its
// emitted label resolves via processing.AggregationLabel. The resolver
// returns (val, present) where val is the row's labeled value coerced
// to float64 (via toFloat64) and present=true iff the row map carries
// the label AND the value coerces to a finite number. RichAggregator
// outputs (set-frequency maps, set-union slices) coerce to NaN, which
// the (value, present) signature surfaces as present=true but with
// NaN-statistic downstream — handlers like INDEX_VS_TOTAL treat NaN
// the same way they treat absent values (no contribution to the
// running grand total). Returns nil when resp.Data is empty (no host
// groups, no entries to fold over).
//
// GroupFields wiring: the helper threads the grouper field names onto
// the SeriesHostView via NewSeriesHostViewWithFields. resolveSibling
// (overlay_sibling_resolver.go) consults that slot when present so an
// OVERLAY_INDEX_VS_SIBLING / OVERLAY_DELTA_VS_SIBLING spec can resolve
// `Ref.Sibling.Field` to the matching axis-key element index without
// scanning every element.
func buildSeriesHostFromGroupedResponse(req *types.Request, resp *types.Response) *SeriesHostView {
	if resp == nil || len(resp.Data) == 0 {
		return nil
	}
	if len(req.Groups) == 0 || len(req.Aggregations) == 0 {
		return nil
	}
	groupFields := make([]string, 0, len(req.Groups))
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		groupFields = append(groupFields, g.Field)
	}
	primaryLabel := AggregationLabel(req.Aggregations[0])
	data := resp.Data
	keys := make([]types.AxisKey, len(data))
	for i, row := range data {
		key := make(types.AxisKey, len(groupFields))
		for j, field := range groupFields {
			if v, ok := row[field]; ok {
				key[j] = v
			}
		}
		keys[i] = key
	}
	resolver := func(i int) (float64, bool) {
		if i < 0 || i >= len(data) {
			return 0, false
		}
		raw, ok := data[i][primaryLabel]
		if !ok {
			return 0, false
		}
		return toFloat64(raw), true
	}
	return NewSeriesHostViewWithFields(keys, resolver, groupFields)
}

// canStreamOverlays reports whether every overlay kind on req.Overlays
// is streamable per types.OverlayStreamability. Returns true when
// req.Overlays is empty (vacuously streamable). Used by
// Processor.canStream as the central mixed-mode downgrade gate per
// PRD §6: a single non-streamable overlay kind forces the entire
// request through the buffered path so the post-finalize hook still
// fires against a complete per-group SeriesHostView.
//
// Unknown overlay kinds are treated as non-streamable (false) so a
// kind that predict has not yet certified falls through to the
// buffered path where ApplyOverlaysSeries can surface the canonical
// PULSE_OVERLAY_KIND_UNKNOWN error.
func canStreamOverlays(req *types.Request) bool {
	if req == nil || len(req.Overlays) == 0 {
		return true
	}
	for i := range req.Overlays {
		streamable, known := types.OverlayStreamable(req.Overlays[i].Kind)
		if !known {
			return false
		}
		if !streamable {
			return false
		}
	}
	return true
}
