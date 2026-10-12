package processing

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// TestOverlayHostTable_MatchesRuntimeHandlers pins the shared slot
// table (overlayhost, read by predict and every runtime dispatcher) to
// the runtime handler tables, so a kind is declared on exactly the
// slots that can execute it. OVERLAY_FORMULA's Compose arm is the one
// kind dispatched outside a Compose table.
func TestOverlayHostTable_MatchesRuntimeHandlers(t *testing.T) {
	for _, k := range types.AllOverlayKinds() {
		_, matrix := overlayHandlers[k]
		_, series := seriesOverlayHandlers[k]
		_, compose := composeOverlayHandlers[k]
		_, composeMulti := composeOverlayMultiLayerHandlers[k]
		_, chain := chainOverlayHandlers[k]
		_, facet := facetOverlayHandlers[k]
		for _, c := range []struct {
			host overlayhost.Host
			runs bool
		}{
			{overlayhost.Request, matrix || series},
			{overlayhost.Compose, compose || composeMulti || k == types.OverlayKindFormula},
			{overlayhost.Chain, chain},
			{overlayhost.Facet, facet},
		} {
			if got := overlayhost.Accepts(c.host, k); got != c.runs {
				t.Errorf("%s on %s: declared %v, runtime handler %v", k, c.host, got, c.runs)
			}
		}
	}
}

// TestCanStreamOverlays_WrongHostRoutesBuffered: a streamable kind that
// does not run on a Process host (OVERLAY_INDEX_VS_POP) routes the run
// buffered, whose hook raises the wrong-slot refusal.
func TestCanStreamOverlays_WrongHostRoutesBuffered(t *testing.T) {
	if s, _ := types.OverlayStreamable(types.OverlayKindIndexVsPop); !s {
		t.Fatal("vacuous: OVERLAY_INDEX_VS_POP is not streamable")
	}
	req := &types.Request{Overlays: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsPop}}}
	if canStreamOverlays(req, nil) {
		t.Error("a FACET kind on Request.Overlays streams")
	}
	req.Overlays[0].Kind = types.OverlayKindIndexVsTotal
	if !canStreamOverlays(req, nil) {
		t.Error("vacuous: OVERLAY_INDEX_VS_TOTAL does not stream")
	}
}

// TestRefuseWrongHostOverlays_KeepMask: a spec the ComputePlan skips
// raises nothing; the first kept wrong-slot spec is refused at its own
// index.
func TestRefuseWrongHostOverlays_KeepMask(t *testing.T) {
	specs := []types.OverlaySpec{
		{Kind: types.OverlayKindTCell},
		{Kind: types.OverlayKindShareOfTotal},
		{Kind: types.OverlayKindRank},
	}
	if err := refuseWrongHostOverlays(specs, nil, func(i int) bool { return i == 1 }); err != nil {
		t.Fatalf("skipped specs refused: %v", err)
	}
	err := refuseWrongHostOverlays(specs, nil, func(i int) bool { return i != 0 })
	var ce *errors.CodedError
	if !asCoded(err, &ce) {
		t.Fatalf("kept wrong-slot spec: err = %v, want a coded refusal", err)
	}
	if ce.Details["index"] != 2 || ce.Details["kind"] != string(types.OverlayKindRank) {
		t.Errorf("details = %v, want index 2 / OVERLAY_RANK", ce.Details)
	}
	if WrongHostRefusal(overlayhost.Request, types.OverlayKindShareOfTotal, nil, 0) != nil {
		t.Error("WrongHostRefusal refused a Process kind on a Process host")
	}
	if WrongHostRefusal(overlayhost.Facet, types.OverlayKindShareOfTotal, nil, 0) == nil {
		t.Error("WrongHostRefusal accepted a Process kind on a FACET host")
	}
}

// TestApplyOverlays_WrongHostRefusedInDispatch: the MATRIX and SERIES
// dispatch loops raise the shared wrong-slot refusal themselves, so a
// caller that hands specs straight to them (no Process hook in front —
// Pulse.ApplySeriesOverlays, say) gets the refusal, not
// PULSE_OVERLAY_KIND_UNKNOWN.
func TestApplyOverlays_WrongHostRefusedInDispatch(t *testing.T) {
	specs := []types.OverlaySpec{{Kind: types.OverlayKindShareOfTotal}, {Kind: types.OverlayKindZVsRef}}
	want := overlayhost.Refusal(overlayhost.Request, types.OverlayKindZVsRef, types.OverlayKindZVsRef, 1)
	host := &SeriesHostView{}
	_, _, err := ApplyOverlaysSeries(specs, host)
	var ce *errors.CodedError
	if !asCoded(err, &ce) || ce.Code != want.Code || ce.Message != want.Message || ce.Details["index"] != 1 {
		t.Errorf("SERIES dispatch: err = %v, want %v", err, want)
	}
	matrix := newCrosstabHostViewWithComponents(&types.MatrixPayload{}, nil)
	_, _, err = ApplyOverlaysWithExtensions([]types.OverlaySpec{{Kind: types.OverlayKindZVsRef}}, matrix, nil)
	ce = nil
	if !asCoded(err, &ce) || ce.Code != want.Code || ce.Details["index"] != 0 {
		t.Errorf("MATRIX dispatch: err = %v, want the wrong-slot refusal at index 0", err)
	}
}
