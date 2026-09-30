package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Runtime twin of the panel's within-prefix slab partition gate —
// the COMPOSE-host counterpart of checkPairwiseSlabPartition
// (processing/crosstab.go).
//
// WHERE IT LIVES AND WHY. The MATRIX arm's twin sits at
// applyOverlaysToResponse because both crosstab arms funnel through
// there and that hook holds the request. The panel does NOT flow
// through it: it folds through service.(*Service).applyComposeOverlays
// ⇒ processing.ApplyComposeOverlays, and neither the handler signature
// (spec, reference, targets, refIdx, targetIdxs) nor the dispatcher's
// own (specs, responses, labels) carries a *types.Request. The
// offending fact is the declared GROUPER TYPE on each slot's authored
// Crosstab.Rows, which no materialised *Response carries — a matrix
// records its row KEYS, never the grouper that produced them.
//
// So the requests are threaded in at the dispatcher instead
// (ApplyComposeOverlaysWithRequests) and the gate runs per spec inside
// the dispatch loop, and the per-kind handler signature stays exactly
// as it was — the same constraint the per-slot components view had to
// respect.
//
// GATE ORDER. The panel's runtime ladder is cap ⇒ params ⇒ depth shape
// ⇒ (this) ⇒ per-slot depth range, and the predict arm's is the same
// (Gate 3 cap, Gate 3b params). This gate runs BEFORE the handler, so
// it preserves that order by DECLINING whenever an earlier gate would
// fire: an over-cap spec, a params blob that does not decode and an
// unknown n_source all fall through untouched so the handler raises
// them with its own words. Declining is safe — every one of those
// conditions is a hard refusal a few lines later, so nothing executes
// on a spec this gate waved past.

// checkPanelSlabPartition refuses one COMPOSE overlay spec whose
// within-prefix leg sums a slot's row margins across a FAN-OUT
// row-axis dim.
//
// `requests` is the per-slot authored request list in
// ComposedRequest.Requests order (nil holes tolerated); refIdx and
// targetIdxs are the resolved slot indexes the dispatcher already
// computed. exts carries the embedder's grouper registrations so a
// custom fan-out grouper is gated exactly like GROUP_SET_PER_ELEMENT;
// the predict arm reaches the same fact through
// descriptor.ExtensionsSnapshot, and both call the same types-side
// predicate so the two cannot drift.
//
// Returns nil when `requests` is nil — the legacy
// ApplyComposeOverlays entry point has no request list to judge, and a
// gate that cannot see the request must not invent a refusal.
func checkPanelSlabPartition(spec *types.ComposeOverlaySpec, specIdx, refIdx int, targetIdxs []int, requests []*types.Request, exts *ExtensionRegistry) error {
	if spec == nil || requests == nil || !types.IsPanelOverlayParamsKind(spec.Kind) {
		return nil
	}
	// Cap first — the standing rule on both arms. An over-cap spec is
	// refused by the handler with the structural failure the caller
	// must fix before anything else is worth saying.
	if len(spec.Targets) > resolveMaxPanelTargets(spec.Options) {
		return nil
	}
	params, err := types.DecodePanelParamsMap(spec.Params)
	if err != nil || !types.ValidPanelNSource(params.NSource) {
		// The handler owns both diagnostics; raising a second,
		// worse-targeted error first would bury them.
		return nil
	}
	slots := make([]types.PanelSlabPartitionSlot, 0, 1+len(spec.Targets))
	slots = append(slots, types.PanelSlabPartitionSlot{
		Rows:       composeRequestRowAxis(requests, refIdx),
		PanelIndex: 0,
		SlotIndex:  refIdx,
		Label:      spec.Reference,
	})
	for i, idx := range targetIdxs {
		label := ""
		if i < len(spec.Targets) {
			label = spec.Targets[i]
		}
		slots = append(slots, types.PanelSlabPartitionSlot{
			Rows:       composeRequestRowAxis(requests, idx),
			PanelIndex: i + 1,
			SlotIndex:  idx,
			Label:      label,
		})
	}
	v, bad := types.CheckPanelSlabPartitionWith(slots, params, exts.ExtensionGroupFanOut())
	if !bad {
		return nil
	}
	// Like the rest of the overlay family this raises the canonical
	// PULSE_OVERLAY_* code as the CodedError's OWN Code, so the runtime
	// refusal carries the same errors[0].code the predict envelope does
	// and `pulse errors lookup` resolves it. No wrapping into
	// PROCESSING_INTERNAL.
	return errors.NewCodedErrorWithDetails(
		errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED,
		v.Message(spec.Kind, params),
		v.Details(spec.Kind, params, specIdx))
}

// composeRequestRowAxis reads one authored slot's crosstab ROW axis.
// Out-of-range index, nil slot or a slot with no crosstab all answer
// nil — an axis with no dims has no dim to fan out at, and a
// non-MATRIX slot is refused by the shape gate with its own code.
func composeRequestRowAxis(requests []*types.Request, idx int) []*types.Group {
	if idx < 0 || idx >= len(requests) {
		return nil
	}
	req := requests[idx]
	if req == nil || req.Crosstab == nil {
		return nil
	}
	return req.Crosstab.Rows
}
