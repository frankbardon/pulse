package service

import (
	"context"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// COMPOSE-host overlay wiring — service-layer orchestration of the
// Compose-only overlay handlers at the post-slot-barrier exit of both
// `service.Compose` (serial) and `service.ComposeParallel` (bounded
// worker pool).
//
// The runtime handlers live entirely inside processing/ (the overlay
// catalog stays free of service/ imports per CLAUDE.md "What NOT to
// Do"); this file owns the orchestrator-side hook that:
//
//   - Walks `req.Overlays` in spec-order.
//   - Builds the per-slot Label → Index lookup from the
//     applyComposeLabelDefaults-normalised `labels []string` slice.
//   - Delegates per-spec dispatch to processing.ApplyComposeOverlays,
//     which resolves Reference / Targets, dispatches each via the
//     per-kind handler table, and returns one OverlayLayer per spec
//     in matching index order.
//
// Call-site invariant: both Compose paths invoke this hook AFTER every
// slot's Process call has terminated and BEFORE the response is
// returned. Spec-order layer emission is preserved regardless of the
// parallel worker dispatch order — `ComposeParallel` reorders results
// back into slot-index order before invoking this hook so the
// downstream layer order matches `ComposedRequest.Overlays` spec
// order verbatim.
//
// FailFast handling: the call-site decides whether to invoke this
// hook based on the FailFast policy:
//
//   - FailFast=true (default per CLAUDE.md "Parallel Compose"): when
//     ANY slot fails, the orchestrator does NOT call
//     applyComposeOverlays — the failing call returns immediately
//     with the first observed error. No partial overlay emission.
//   - FailFast=false: the orchestrator calls applyComposeOverlays
//     with the partial responses slice (failed slots are nil holes).
//     `processing.ApplyComposeOverlays` resolves Reference / Targets
//     against the slice; a nil hole at a referenced slot surfaces
//     PULSE_OVERLAY_REFERENCE_UNKNOWN / PULSE_OVERLAY_TARGET_UNKNOWN.
//
// Empty-Overlays fast path: when `req.Overlays` is nil OR empty the
// hook returns `(nil, nil)` with no map allocation, no goroutine, and
// no log line — byte-identical output for callers that do not declare
// any Compose-only overlays.

// distributeComposeWarnings folds the flat warning slice produced by
// the Compose overlay dispatcher into the matching layer's `Warnings`
// slot. The dispatcher (processing.ApplyComposeOverlays) stamps
// `Details["overlay_index"]` on every warning it appends, so routing is
// a single lookup per warning; warnings missing the key (defensive —
// should never happen with the current dispatcher) fall back to layer
// 0 so they are still surfaced rather than silently dropped. Layers
// with no warnings keep `Warnings == nil` (no empty slice allocation)
// so the `omitempty` JSON tag preserves byte identity for the
// overlay-free path.
//
// Mutates `layers` in place and returns the same slice for chained-
// expression convenience. No-op when `layers` is empty or `warnings`
// is empty. Shared between `service.Compose` and
// `service.ComposeParallel` so both serial and parallel paths preserve
// the identical layer-warning routing contract.
func distributeComposeWarnings(layers []types.OverlayLayer, warnings []types.OverlayWarning) []types.OverlayLayer {
	if len(layers) == 0 || len(warnings) == 0 {
		return layers
	}
	for i := range warnings {
		layerIdx := 0
		if warnings[i].Details != nil {
			if v, ok := warnings[i].Details["overlay_index"]; ok {
				if idx, ok := v.(int); ok && idx >= 0 && idx < len(layers) {
					layerIdx = idx
				}
			}
		}
		layers[layerIdx].Warnings = append(layers[layerIdx].Warnings, warnings[i])
	}
	return layers
}

// applyComposeOverlays runs the COMPOSE-host overlay dispatch against
// the materialised per-slot responses and returns the resulting
// layers + warnings in spec order. No-op (returns `(nil, nil)`) when
// `req.Overlays` is empty — additive byte-identity contract.
//
// `req` carries the user-facing Overlays slice (Compose-only specs,
// not per-Request overlays); `requests` is the per-slot Label-default-
// applied request list returned by `applyComposeLabelDefaults`;
// `responses` is the parallel per-slot *Response slice (with nil
// holes for failed slots under FailFast=false). The caller MUST gate
// this hook with the FailFast policy — see the package doc above.
//
// The context parameter is accepted to match the canonical signature
// `applyComposeOverlays(ctx, req, responses) ([]OverlayLayer, error)`
// even though the v1 dispatch does not touch ctx today. Future
// per-kind handlers (multi-ref kinds) may issue follow-up cohort opens
// or population reads that consume the deadline.
func (s *Service) applyComposeOverlays(ctx context.Context, req *types.ComposedRequest, requests []*types.Request, responses []*types.Response) ([]types.OverlayLayer, []types.OverlayWarning, error) {
	_ = ctx
	if req == nil || len(req.Overlays) == 0 {
		// Byte-identity guarantee for overlay-free composes: the
		// caller's facade slot stays nil (no make-with-zero-len
		// allocation, no `overlays: []` empty-array marshalling).
		return nil, nil, nil
	}
	// Extract the per-slot labels from the normalised requests slice.
	// applyComposeLabelDefaults guarantees every non-nil slot carries
	// a non-empty Label by the time this hook runs, so the labels
	// slice exactly parallels `responses` and the resolver in
	// processing.ApplyComposeOverlays binds slot labels to slot
	// indices unambiguously.
	labels := make([]string, len(requests))
	for i, r := range requests {
		if r == nil {
			labels[i] = ""
			continue
		}
		labels[i] = r.Label
	}
	return processing.ApplyComposeOverlays(req.Overlays, responses, labels)
}
