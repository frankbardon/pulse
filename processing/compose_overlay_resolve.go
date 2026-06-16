package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Slot-name → *Response resolution helper used by every COMPOSE-host
// overlay handler.
//
// The COMPOSE-host overlay dispatch (overlay_compose_dispatch.go) needs
// to bind two distinct slot-label arms — `ComposeOverlaySpec.Reference`
// (a single slot label that anchors the comparison) and
// `ComposeOverlaySpec.Targets` (one or more slot labels that the
// reference is folded against) — to the matching *types.Response
// already materialised by the orchestrator's slot-loop barrier. This
// file lands the single point of that binding so the per-kind handlers
// (E7-S9..S15) share one resolver shape rather than each handler
// reimplementing its own slot-label walk.
//
// Architecture choice: the resolver intentionally takes the
// *types.ComposedRequest directly (rather than a pre-extracted labels
// slice) so callers cannot drift between the post-validate normalised
// request and the labels they pass in. The auto-default (empty Label →
// `request_<index+1>`) lives in service/compose_label.go as the
// pre-barrier normaliser; this resolver mirrors the same rule against
// `req.Requests[i].Label` directly so the COMPOSE-host fold path stays
// honest even when callers reach it from a code path that never went
// through `applyComposeLabelDefaults` (descriptor predict / future MCP
// tools).
//
// Structural invariants:
//
//   - Pure function. No I/O. No goroutines. No global state. No
//     mutation of `req` or `responses`. Same inputs → same outputs.
//   - MUST NOT import service/ or descriptor/. The resolver stays
//     inside processing/ so descriptor.ValidateComposedRequest
//     (no-execute) can lift the same per-spec lookup at predict time
//     without dragging in a service/ import.
//   - No fmt.Sprintf in any JSON-bearing path. Detail keys are plain
//     map[string]any; the chassis-side warning emission rules are
//     unchanged by this file.
//
// Failure modes (the resolver itself):
//
//   - Two slots resolve to the same final label after auto-default →
//     PULSE_COMPOSE_LABEL_COLLISION. service/compose_label.go's
//     applyComposeLabelDefaults already rejects this at the barrier,
//     so reaching this branch is a defensive guard against callers
//     that skip the normaliser (descriptor predict, MCP).
//
//   - len(req.Requests) != len(responses) → PROCESSING_INTERNAL with
//     a "compose resolver slot-count mismatch" detail. This is a
//     structural invariant the orchestrator MUST hold; the resolver
//     fails closed rather than indexing past the end of either slice.
//
// LookupReference / LookupTarget — the per-spec lookup helpers — own
// the canonical-code dispatch (PULSE_OVERLAY_REFERENCE_UNKNOWN /
// PULSE_OVERLAY_TARGET_UNKNOWN) so the per-kind handlers can use them
// uniformly without re-coding the same "which arm" branching twice per
// handler.

// ResolveComposeSlots builds the slot-name → *types.Response lookup
// the COMPOSE-host overlay dispatch consumes. Walks `req.Requests` in
// index order and produces two parallel maps keyed by the final
// (post-auto-default) slot label:
//
//   - byLabel maps each final label to the *types.Response at the
//     same slot index. The value MAY be nil — under FailFast=false
//     the orchestrator passes a partial responses slice with nil
//     holes for failed slots, and the resolver preserves those holes
//     so the downstream LookupReference / LookupTarget helpers can
//     surface a coded error (rather than silently skipping the slot).
//
//   - byIndex maps each final label to the originating slot index in
//     `req.Requests`. Handlers carry this through to warning Details
//     so MCP fix-ups can address the offending slot by its
//     ComposedRequest.Requests[i] position.
//
// Final label rule (mirrors applyComposeLabelDefaults):
//
//   - Non-empty `req.Requests[i].Label` → used verbatim.
//   - Empty `req.Requests[i].Label` → `request_<i+1>` (1-based, per
//     E7-S1).
//   - Nil *Request slot → skipped. The slot index is NOT registered
//     in either map; downstream Reference / Target lookups against a
//     nil slot surface PULSE_OVERLAY_REFERENCE_UNKNOWN /
//     PULSE_OVERLAY_TARGET_UNKNOWN at handler time (the same code
//     path an unknown caller-supplied label takes).
//
// Returns a coded error when:
//
//   - len(req.Requests) != len(responses) — orchestrator-side
//     invariant violation. PROCESSING_INTERNAL.
//
//   - Two non-nil slots resolve to the same final label after
//     auto-default. PULSE_COMPOSE_LABEL_COLLISION. The
//     applyComposeLabelDefaults normaliser rejects this at the
//     pre-barrier validate, so this branch is the defense in depth
//     for callers that reach the resolver without going through the
//     normaliser (descriptor predict, MCP, tests).
//
// Returns (nil, nil, nil) when req is nil or req.Requests is empty —
// matches the chassis short-circuit shape so callers that compute the
// lookup unconditionally still see a clean zero-allocation path.
func ResolveComposeSlots(req *types.ComposedRequest, responses []*types.Response) (map[string]*types.Response, map[string]int, error) {
	if req == nil || len(req.Requests) == 0 {
		return nil, nil, nil
	}
	if len(req.Requests) != len(responses) {
		return nil, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay resolver: request count does not match response count",
			map[string]any{
				"requests_count":  len(req.Requests),
				"responses_count": len(responses),
			})
	}
	byLabel := make(map[string]*types.Response, len(req.Requests))
	byIndex := make(map[string]int, len(req.Requests))
	for i, src := range req.Requests {
		// Nil *Request slots are skipped — downstream lookups against
		// a label that was never registered fall through the same
		// "unknown label" coded-error branch as a caller typo.
		if src == nil {
			continue
		}
		label := src.Label
		if label == "" {
			label = composeDefaultLabel(i)
		}
		if _, dup := byLabel[label]; dup {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_COMPOSE_LABEL_COLLISION,
				"compose overlay resolver: label collision",
				map[string]any{
					"label":             label,
					"first_slot_index":  byIndex[label],
					"second_slot_index": i,
				})
		}
		byLabel[label] = responses[i]
		byIndex[label] = i
	}
	return byLabel, byIndex, nil
}

// LookupReference resolves a `ComposeOverlaySpec.Reference` label
// against the byLabel map produced by ResolveComposeSlots. Returns a
// coded error carrying PULSE_OVERLAY_REFERENCE_UNKNOWN when the label
// is empty, absent from the map, or resolves to a nil *Response (the
// FailFast=false failed-slot hole).
//
// The handler-side wrapping pattern: every per-kind COMPOSE handler
// (E7-S9..S15) calls LookupReference once at the top of its body and
// returns the coded error verbatim. Keeping the canonical-code
// dispatch on this single helper means handlers never re-code the
// "which arm" branching twice; the call site reads as a one-line
// guard.
//
// `specIdx` is the originating ComposeOverlaySpec index in
// `ComposedRequest.Overlays`. The MCP fix-up surface uses the index
// to point the caller at the failing spec in their request body.
func LookupReference(byLabel map[string]*types.Response, label string, specIdx int) (*types.Response, error) {
	if label == "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay reference label is empty",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
				"index": specIdx,
				"which": "reference",
			})
	}
	resp, ok := byLabel[label]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay reference label not found: "+label,
			map[string]any{
				"code":       string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
				"index":      specIdx,
				"which":      "reference",
				"slot_label": label,
			})
	}
	if resp == nil {
		// FailFast=false hole: the slot at the resolved index failed
		// and the orchestrator passed a nil entry in the responses
		// slice. The resolver flags it as unknown so the overlay
		// dispatch fails closed rather than panicking downstream.
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay reference slot is unavailable (slot failed): "+label,
			map[string]any{
				"code":       string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
				"index":      specIdx,
				"which":      "reference",
				"slot_label": label,
			})
	}
	return resp, nil
}

// LookupTarget resolves a single `ComposeOverlaySpec.Targets[j]`
// label against the byLabel map produced by ResolveComposeSlots.
// Returns a coded error carrying PULSE_OVERLAY_TARGET_UNKNOWN when
// the label is empty, absent from the map, or resolves to a nil
// *Response (the FailFast=false failed-slot hole).
//
// `specIdx` is the originating ComposeOverlaySpec index in
// `ComposedRequest.Overlays`; `targetIdx` is the index inside the
// spec's `Targets` slice. Both echo into the Details so the MCP
// fix-up surface can address the offending target by its
// (spec, position) coordinate.
func LookupTarget(byLabel map[string]*types.Response, label string, specIdx, targetIdx int) (*types.Response, error) {
	if label == "" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay target label is empty",
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_TARGET_UNKNOWN),
				"index":        specIdx,
				"target_index": targetIdx,
				"which":        "target",
			})
	}
	resp, ok := byLabel[label]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay target label not found: "+label,
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_TARGET_UNKNOWN),
				"index":        specIdx,
				"target_index": targetIdx,
				"which":        "target",
				"slot_label":   label,
			})
	}
	if resp == nil {
		// Same FailFast=false hole as LookupReference — the slot
		// failed and the orchestrator passed a nil entry. Surface
		// the missing-target coded error.
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay target slot is unavailable (slot failed): "+label,
			map[string]any{
				"code":         string(errors.PULSE_OVERLAY_TARGET_UNKNOWN),
				"index":        specIdx,
				"target_index": targetIdx,
				"which":        "target",
				"slot_label":   label,
			})
	}
	return resp, nil
}

// composeDefaultLabel returns the synthesised default label for the
// slot at `index` in `ComposedRequest.Requests`. Mirrors the
// `request_<i+1>` rule applied by service.applyComposeLabelDefaults at
// the pre-barrier normalisation step. Kept as a private helper so the
// two normalisers (this one, applyComposeLabelDefaults) stay in lockstep
// without exporting a "default label" surface a third caller could fork.
//
// Implementation detail: builds the string without fmt.Sprintf so the
// resolver path stays clean against the descriptor-side
// no-fmt.Sprintf-in-JSON-bearing-code gate (CLAUDE.md "Output Format
// Contract"). The integer-to-string conversion uses the standard
// recursion-free decimal walk — small alloc, no format-string parsing
// cost on every resolver entry.
func composeDefaultLabel(index int) string {
	return ComposeDefaultLabel(index)
}

// ComposeDefaultLabel is the exported sibling of the package-internal
// composeDefaultLabel helper. The descriptor-side compose validator
// (descriptor.ValidateCompose, E7-S14) re-derives the same default
// slot label rule (`request_<i+1>`, 1-based) so a predict-time
// reference / target lookup matches the runtime's view of the slot
// labels; the per-helper sync test in descriptor/compose_test.go
// (TestComposeDescriptorDefaultLabel_MatchesProcessingHelper) pins
// the two implementations in lockstep so a future tweak on either
// side fails loud.
func ComposeDefaultLabel(index int) string {
	// index is the zero-based slot index; the default label is
	// `request_<index+1>` for human-friendly 1-based display.
	return "request_" + composeItoa(index+1)
}

// composeItoa converts a non-negative integer to its decimal string
// form without going through fmt.Sprintf. Used by composeDefaultLabel
// to keep the resolver's JSON-bearing path grep-clean against the
// structural-defense ban on fmt.Sprintf in any envelope-shaping
// surface. The implementation handles the full int range but only the
// non-negative branch is exercised in practice (slot indices are
// always >= 0). The `compose` prefix avoids a name clash with the
// identically-named test helper in grouper_keyfor_test.go.
func composeItoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	// Pre-size the buffer for the worst-case 20 digits (max int64
	// decimal width); the slot-index range is tiny in practice so
	// the buffer is over-allocated by design.
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
