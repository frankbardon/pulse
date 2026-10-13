package processing

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// TestOverlayHostTable_MatchesRuntimeHandlers pins the shared slot and
// Process-shape tables (overlayhost, read by predict and every runtime
// dispatcher) to the runtime handler tables, so a kind is declared on
// exactly the slots — and, on Request.Overlays, exactly the MATRIX /
// SERIES / MATRIX_RESULT host shapes — that can execute it.
func TestOverlayHostTable_MatchesRuntimeHandlers(t *testing.T) {
	for _, k := range types.AllOverlayKinds() {
		_, matrix := overlayHandlers[k]
		_, series := seriesOverlayHandlers[k]
		_, matrixResult := matrixResultOverlayHandlers[k]
		_, compose := composeOverlayHandlers[k]
		_, composeMulti := composeOverlayMultiLayerHandlers[k]
		_, chain := chainOverlayHandlers[k]
		_, facet := facetOverlayHandlers[k]
		for _, c := range []struct {
			host overlayhost.Host
			runs bool
		}{
			{overlayhost.Request, matrix || series || matrixResult},
			{overlayhost.Compose, compose || composeMulti},
			{overlayhost.Chain, chain},
			{overlayhost.Facet, facet},
		} {
			if got := overlayhost.Accepts(c.host, k); got != c.runs {
				t.Errorf("%s on %s: declared %v, runtime handler %v", k, c.host, got, c.runs)
			}
		}
		for _, c := range []struct {
			shape types.OverlayShape
			runs  bool
		}{
			{types.OverlayShapeMatrix, matrix},
			{types.OverlayShapeSeries, series},
			{overlayhost.ShapeMatrixResult, matrixResult},
		} {
			declared := false
			for _, s := range overlayhost.Shapes(k) {
				declared = declared || s == c.shape
			}
			if declared != c.runs {
				t.Errorf("%s on a %s Process host: declared %v, runtime handler %v", k, c.shape, declared, c.runs)
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
	req := &types.Request{Overlays: specs}
	if err := refuseWrongHostOverlays(req, nil, func(i int) bool { return i == 1 }); err != nil {
		t.Fatalf("skipped specs refused: %v", err)
	}
	err := refuseWrongHostOverlays(req, nil, func(i int) bool { return i != 0 })
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
	// A kind of the other Process shape: the shared shape refusal, not
	// PULSE_OVERLAY_KIND_UNKNOWN.
	_, _, err = ApplyOverlaysSeries([]types.OverlaySpec{{Kind: types.OverlayKindChiSqMatrix}}, host)
	ce = nil
	if !asCoded(err, &ce) || ce.Code != want.Code || ce.Details["shape"] != "series" {
		t.Errorf("SERIES dispatch: err = %v, want the wrong-shape refusal", err)
	}
	_, _, err = ApplyOverlaysWithExtensions([]types.OverlaySpec{{Kind: types.OverlayKindYoY}}, matrix, nil)
	ce = nil
	if !asCoded(err, &ce) || ce.Code != want.Code || ce.Details["shape"] != "matrix" {
		t.Errorf("MATRIX dispatch: err = %v, want the wrong-shape refusal", err)
	}
}

// TestRefuseWrongHostOverlays_Shape: over the authored Process shape, a
// kind that does not run on it is refused with the shared shape refusal
// (the slot refusal still wins for a non-Process kind); an ungrouped
// SCALAR host is not judged.
func TestRefuseWrongHostOverlays_Shape(t *testing.T) {
	series := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v"}},
		Overlays:     []types.OverlaySpec{{Kind: types.OverlayKindShareOfTotal}, {Kind: types.OverlayKindShareOfCol}}}
	matrix := &types.Request{Crosstab: &types.CrosstabSpec{},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindShareOfTotal}, {Kind: types.OverlayKindIndexVsPrior}}}
	for name, c := range map[string]struct {
		req  *types.Request
		want *errors.CodedError
	}{
		"series":  {series, overlayhost.ShapeRefusal(types.OverlayShapeSeries, types.OverlayKindShareOfCol, types.OverlayKindShareOfCol, 1)},
		"matrix":  {matrix, overlayhost.ShapeRefusal(types.OverlayShapeMatrix, types.OverlayKindIndexVsPrior, types.OverlayKindIndexVsPrior, 1)},
		"formula": {&types.Request{Groups: series.Groups, Aggregations: series.Aggregations, Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFormula}}}, overlayhost.ShapeRefusal(types.OverlayShapeSeries, types.OverlayKindFormula, types.OverlayKindFormula, 0)},
	} {
		if c.want == nil {
			t.Fatalf("%s: vacuous, no refusal expected", name)
		}
		err := refuseWrongHostOverlays(c.req, nil, nil)
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Message != c.want.Message || ce.Details["shape"] != c.want.Details["shape"] || ce.Details["index"] != c.want.Details["index"] {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	scalar := &types.Request{Aggregations: series.Aggregations, Overlays: []types.OverlaySpec{{Kind: types.OverlayKindShareOfCol}, {Kind: types.OverlayKindYoY}}}
	if err := refuseWrongHostOverlays(scalar, nil, nil); err != nil {
		t.Errorf("scalar host judged: %v", err)
	}
	// A streamable SERIES-only kind over a crosstab routes buffered,
	// whose hook raises the shape refusal.
	if s, _ := types.OverlayStreamable(types.OverlayKindIndexVsTotal); !s {
		t.Fatal("vacuous: OVERLAY_INDEX_VS_TOTAL is not streamable")
	}
	if canStreamOverlays(&types.Request{Crosstab: &types.CrosstabSpec{}, Overlays: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsTotal}}}, nil) {
		t.Error("a SERIES-only kind over a MATRIX host streams")
	}
}
