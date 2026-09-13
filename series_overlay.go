package pulse

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// SERIES-overlay facade — applying the ordered-series overlay catalog to a
// series the CALLER already holds, rather than to one a grouped Process just
// produced.
//
// Why this exists. The overlay catalog's SERIES handlers fold over an ordered
// (keys, values) pair and nothing else: processing.ApplyOverlaysSeries takes a
// SeriesHostView, which is an ordered key list plus a sparse value resolver,
// and every windowed kind's own documentation describes the dispatch route as
// "the post-host finalize entry point for the streaming-Process orchestrator
// AND the buffered fallback entry point for any callers that materialise the
// host series before calling into the overlay surface." That second caller had
// no way in. Reaching the overlays meant running a full Process — filter,
// aggregate, group — even when the values were already in hand.
//
// That gap forced a real choice on embedders with pre-aggregated data. A
// survey tracker storing one already-computed metric row per (entity, period)
// reads those rows by point lookup against a sidecar index; it has the ordered
// series immediately and has no aggregation to perform. Asked for a
// period-over-period change it could either re-run the values through a
// grouped Process purely to reach an overlay — paying a scan to re-derive
// numbers it already had, and risking a grouper that silently averages across
// an unbound dimension — or subtract them itself, moving arithmetic out of
// Pulse and into the caller. Neither is good, and the second is how analytical
// logic leaks out of the engine that owns it one subtraction at a time.
//
// So the surface is opened directly. The math stays here, in the one
// implementation the grouped-Process path uses, and a caller with a
// materialised series gets the identical numbers, warnings and layer shape
// without inventing an aggregation it does not need.
//
// WHAT THIS DOES NOT DO. There is no types.Request on this path, so the
// predict-time validators in descriptor/ — which check overlay specs against a
// Request's Groups / Crosstab shape — do not and cannot run. The SERIES host
// shape they exist to enforce is instead guaranteed by construction here: the
// caller hands over an ordered series, which is what a SERIES host IS. What
// remains checkable is checked below (kind is SERIES-registered, scope is
// GROUP); what is not checkable is not pretended.

// SeriesPoint is one ordered point of a caller-materialised series.
//
// Present distinguishes "this ordinal has no value" from "this ordinal's value
// is zero", which the windowed kinds treat very differently: an absent point
// does not advance a lag carrier, where a present zero does. A caller reading
// stored cells sets Present=false for a period that holds no row — never
// Value=0, which would assert that the measured value was zero.
type SeriesPoint struct {
	// Key is the group key this point sits at — the ordered axis position,
	// carried through to the emitted SeriesEntry so a caller can align
	// layers back onto its own series.
	Key types.AxisKey

	// Value is the host metric's value at this ordinal. Read only when
	// Present is true.
	Value float64

	// Present reports whether this ordinal has a value at all.
	Present bool
}

// SeriesOverlayRequest is the input to Pulse.ApplySeriesOverlays.
type SeriesOverlayRequest struct {
	// Points is the series IN AXIS ORDER. The windowed kinds are
	// order-dependent — "prior" means the preceding element of THIS slice —
	// so a caller that assembles points out of order gets a well-formed
	// answer to a different question. Sort before calling.
	Points []SeriesPoint

	// Overlays is the spec list, applied in order. One layer per spec comes
	// back in matching order.
	Overlays []types.OverlaySpec
}

// SeriesOverlayResult is what ApplySeriesOverlays returns: one layer per
// requested spec, in matching order, plus the flat warning slice the fold
// produced.
type SeriesOverlayResult struct {
	Layers   []types.OverlayLayer
	Warnings []types.OverlayWarning
}

// ApplySeriesOverlays applies the SERIES overlay catalog to a series the
// caller already holds.
//
// It is the same fold the grouped-Process path runs at host finalize, reached
// without a Process: build the host view from Points, dispatch each spec
// through the per-kind SERIES handler table, return the layers. A caller with
// pre-aggregated values — stored metric cells read by point lookup, a series
// assembled from several archives — gets Pulse-computed statistics over them
// without re-deriving values it already has.
//
// Empty Overlays returns an empty result and no error, mirroring
// processing.ApplyOverlaysSeries' own unconditional-call contract.
func (p *Pulse) ApplySeriesOverlays(ctx context.Context, req *SeriesOverlayRequest) (*SeriesOverlayResult, error) {
	if req == nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"ApplySeriesOverlays requires a non-nil SeriesOverlayRequest",
			map[string]any{})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.Overlays) == 0 {
		return &SeriesOverlayResult{}, nil
	}

	// Scope check. The Request-shaped predict-time validators cannot run
	// here (see this file's header), but scope is a property of the spec
	// alone and every SERIES kind in the catalog is GROUP-scoped — one entry
	// per host group key is the only footprint an ordered series has. A CELL
	// or MATRIX scope on this path names a host that does not exist.
	for i := range req.Overlays {
		if req.Overlays[i].Scope != types.OverlayScopeGroup {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay "+string(req.Overlays[i].Kind)+" on a caller-materialised series requires scope group",
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
					"index": i,
					"kind":  string(req.Overlays[i].Kind),
					"scope": string(req.Overlays[i].Scope),
				})
		}
	}

	keys := make([]types.AxisKey, len(req.Points))
	for i := range req.Points {
		keys[i] = req.Points[i].Key
	}
	// The resolver closes over the caller's points by index. Absent ordinals
	// report (0, false) — the SeriesHostView contract the windowed kinds
	// read to decide whether to advance a lag carrier.
	points := req.Points
	host := processing.NewSeriesHostView(keys, func(i int) (float64, bool) {
		if i < 0 || i >= len(points) {
			return 0, false
		}
		if !points[i].Present {
			return 0, false
		}
		return points[i].Value, true
	})

	// An unknown or MATRIX-only kind short-circuits inside the dispatch with
	// a coded PULSE_OVERLAY_KIND_UNKNOWN, which is the same answer this path
	// should give — so kind membership is not re-checked here.
	layers, warnings, err := processing.ApplyOverlaysSeries(req.Overlays, host)
	if err != nil {
		return nil, err
	}
	return &SeriesOverlayResult{Layers: layers, Warnings: warnings}, nil
}
