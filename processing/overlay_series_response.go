package processing

import (
	"encoding/json"

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
// types.ResponseWarning per types.OverlayWarning the handlers emitted
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
	// E4-S7 OVERLAY_YOY frequency promotion. The YoY handler reads
	// the frequency value from spec.Params["frequency"] only; the
	// orchestrator promotes req.Groups[0].Params["frequency"] (the
	// canonical GROUP_DATE authoring slot) onto every YoY spec before
	// dispatch so the per-handler surface stays consistent. When the
	// spec already carries its own frequency Param (the override path),
	// the orchestrator leaves it unchanged.
	specs := promoteYoYFrequencyFromGroupParams(req)
	layers, warnings, err := ApplyOverlaysSeries(specs, host)
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
	grouperKinds := make([]types.GroupType, 0, len(req.Groups))
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		groupFields = append(groupFields, g.Field)
		grouperKinds = append(grouperKinds, g.Type)
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
	// E4-S7 OVERLAY_YOY consumes the grouper-kind list via
	// SeriesHostView.GrouperKinds to enforce its "host grouper must be
	// GROUP_DATE" requirement at runtime. The existing E3-S5 sibling
	// resolver continues to consume the grouper-field list. Both lists
	// are populated unconditionally here so handlers that consume them
	// see the same view shape regardless of whether the request carries
	// overlay specs that introspect kind.
	return NewSeriesHostViewWithGrouper(keys, resolver, groupFields, grouperKinds)
}

// promoteYoYFrequencyFromGroupParams walks req.Overlays and promotes
// the host GROUP_DATE grouper's `frequency` Param onto every
// OVERLAY_YOY spec whose own `Params["frequency"]` slot is empty. The
// YoY handler reads the frequency from spec.Params["frequency"] only
// (the runtime surface is intentionally narrow — the handler does NOT
// see req.Groups), so the orchestrator centralises the spec.Params /
// req.Groups[0].Params fallback chain here. When the spec already
// carries its own frequency Param (the override path), the slot is
// left unchanged; when the host grouper does not carry a frequency
// Param either, the spec is left unchanged and the runtime handler
// fires PULSE_OVERLAY_YOY_FREQUENCY_MISSING from its own surface
// (defense in depth — predict and the orchestrator should catch the
// missing-frequency case first).
//
// Returns a fresh slice when any spec was promoted; returns the
// caller's req.Overlays slice header verbatim when no spec needed
// promotion. The fresh-slice path copies every spec by value so the
// original Request remains byte-identical after the call (the YoY
// frequency promotion is purely a runtime adapter and must not mutate
// the caller's request shape — Canonical hash byte-identity demands
// it). When the request carries no OVERLAY_YOY specs the helper
// returns the caller's slice unchanged.
func promoteYoYFrequencyFromGroupParams(req *types.Request) []types.OverlaySpec {
	if req == nil || len(req.Overlays) == 0 {
		return nil
	}
	if len(req.Groups) == 0 {
		return req.Overlays
	}
	// Read req.Groups[0].Params["frequency"] once up front. The grouper
	// Params is a raw JSON blob; a missing / empty / non-object blob
	// surfaces as ("", false) and the caller's slice is returned
	// unchanged.
	groupFrequency, hasGroupFrequency := readYoYFrequencyFromParams(req.Groups[0].Params)
	if !hasGroupFrequency {
		return req.Overlays
	}
	// Walk the overlay specs and identify YoY specs that need
	// promotion. If none, return the caller's slice unchanged.
	needsPromotion := false
	for i := range req.Overlays {
		if req.Overlays[i].Kind != types.OverlayKindYoY {
			continue
		}
		if _, specHasFrequency := readYoYFrequencyFromParams(req.Overlays[i].Params); specHasFrequency {
			continue
		}
		needsPromotion = true
		break
	}
	if !needsPromotion {
		return req.Overlays
	}
	// Build a fresh slice with the promoted Params blob on every YoY
	// spec that needed it. Copy every spec by value so the caller's
	// Request stays byte-identical.
	out := make([]types.OverlaySpec, len(req.Overlays))
	copy(out, req.Overlays)
	for i := range out {
		if out[i].Kind != types.OverlayKindYoY {
			continue
		}
		if _, specHasFrequency := readYoYFrequencyFromParams(out[i].Params); specHasFrequency {
			continue
		}
		out[i].Params = mergeYoYFrequencyParams(out[i].Params, groupFrequency)
	}
	return out
}

// mergeYoYFrequencyParams takes the original spec.Params blob (which
// may be nil / empty / non-object / object-without-frequency) and
// returns a fresh JSON blob carrying the original keys plus
// `frequency = freq`. When the original blob is unparseable as an
// object the helper returns a fresh `{"frequency": freq}` blob —
// the caller treats the original blob as discarded (predict already
// rejects malformed Params blobs upstream, so this branch is
// defense in depth).
func mergeYoYFrequencyParams(orig json.RawMessage, freq string) json.RawMessage {
	out := map[string]any{}
	if len(orig) > 0 {
		_ = json.Unmarshal(orig, &out)
	}
	out["frequency"] = freq
	encoded, err := json.Marshal(out)
	if err != nil {
		// Unreachable — out is a map[string]any with only string /
		// scalar values; json.Marshal cannot fail. The defense matches
		// the encoding/json safety pattern used elsewhere in the
		// overlay surface.
		return orig
	}
	return encoded
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
