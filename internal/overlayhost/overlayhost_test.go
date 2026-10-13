package overlayhost

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestHostsCoverCatalog: every catalog kind runs on at least one slot,
// and the table names no kind outside the catalog.
func TestHostsCoverCatalog(t *testing.T) {
	catalog := map[types.OverlayKind]bool{}
	for _, k := range types.AllOverlayKinds() {
		catalog[k] = true
		if len(Hosts(k)) == 0 {
			t.Errorf("%s runs on no slot", k)
		}
	}
	for k := range hosts {
		if !catalog[k] {
			t.Errorf("%s is in the host table but not the catalog", k)
		}
	}
}

func TestRefusal(t *testing.T) {
	cases := []struct {
		name        string
		host        Host
		kind, route types.OverlayKind
		index       int
		wantMsg     string
		wantDetails map[string]any
	}{
		{"compose kind on a Process host", Request, types.OverlayKindTVsRef, types.OverlayKindTVsRef, 2,
			"overlay OVERLAY_T_VS_REF does not run on Request.Overlays; attach it to ComposedRequest.Overlays",
			map[string]any{"index": 2, "kind": "OVERLAY_T_VS_REF", "host": "request", "hosts": []string{"compose"}}},
		{"two-slot kind on a chain", Chain, types.OverlayKindFormula, types.OverlayKindFormula, 0,
			"overlay OVERLAY_FORMULA does not run on ChainRequest.Overlays; attach it to Request.Overlays or ComposedRequest.Overlays",
			map[string]any{"index": 0, "kind": "OVERLAY_FORMULA", "host": "chain", "hosts": []string{"request", "compose"}}},
		{"process kind on a facet", Facet, types.OverlayKindShareOfCol, types.OverlayKindShareOfCol, 1,
			"overlay OVERLAY_SHARE_OF_COL does not run on FacetRequest.Overlays; attach it to Request.Overlays",
			map[string]any{"index": 1, "kind": "OVERLAY_SHARE_OF_COL", "host": "facet", "hosts": []string{"request"}}},
		{"facet kind on a compose", Compose, types.OverlayKindKSVsPop, types.OverlayKindKSVsPop, 0,
			"overlay OVERLAY_KS_VS_POP does not run on ComposedRequest.Overlays; attach it to FacetRequest.Overlays",
			map[string]any{"index": 0, "kind": "OVERLAY_KS_VS_POP", "host": "compose", "hosts": []string{"facet"}}},
		{"chain kind on a Process host", Request, types.OverlayKindIndexVsStage, types.OverlayKindIndexVsStage, 0,
			"overlay OVERLAY_INDEX_VS_STAGE does not run on Request.Overlays; attach it to ChainRequest.Overlays",
			map[string]any{"index": 0, "kind": "OVERLAY_INDEX_VS_STAGE", "host": "request", "hosts": []string{"chain"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := Refusal(c.host, c.kind, c.route, c.index)
			if ce == nil {
				t.Fatal("not refused")
			}
			if ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
				t.Errorf("code = %s", ce.Code)
			}
			if ce.Message != c.wantMsg {
				t.Errorf("message = %q, want %q", ce.Message, c.wantMsg)
			}
			if !reflect.DeepEqual(ce.Details, c.wantDetails) {
				t.Errorf("details = %v, want %v", ce.Details, c.wantDetails)
			}
		})
	}
}

// TestRefusal_Accepted: a kind on its own slot, a kind outside the
// catalog and the empty route of a hidden kind are never refused.
func TestRefusal_Accepted(t *testing.T) {
	for _, c := range []struct {
		host Host
		kind types.OverlayKind
	}{
		{Request, types.OverlayKindShareOfTotal},
		{Request, types.OverlayKindFormula},
		{Compose, types.OverlayKindFormula},
		{Compose, types.OverlayKindTCell},
		{Chain, types.OverlayKindDeltaVsStage},
		{Facet, types.OverlayKindIndexVsPop},
		{Chain, "OVERLAY_NOT_IN_CATALOG"},
		{Facet, ""},
	} {
		if ce := Refusal(c.host, c.kind, c.kind, 0); ce != nil {
			t.Errorf("%s on %s refused: %v", c.kind, c.host, ce)
		}
	}
	// A hidden kind keys on the empty route: the authored kind is a
	// wrong-slot kind, but the slot's unknown-kind branch owns it.
	if ce := Refusal(Request, types.OverlayKindTVsRef, "", 0); ce != nil {
		t.Errorf("hidden kind refused: %v", ce)
	}
}

func TestSlotAndHostsCopy(t *testing.T) {
	for h, want := range map[Host]string{
		Request: "Request.Overlays", Compose: "ComposedRequest.Overlays",
		Chain: "ChainRequest.Overlays", Facet: "FacetRequest.Overlays", "other": "other",
	} {
		if got := h.Slot(); got != want {
			t.Errorf("%s.Slot() = %q, want %q", h, got, want)
		}
	}
	hs := Hosts(types.OverlayKindFormula)
	hs[0] = Chain
	if Hosts(types.OverlayKindFormula)[0] != Request {
		t.Error("Hosts returned the table's own slice")
	}
}

// TestShapesCoverRequestKinds: a kind declares Process shapes iff it
// runs on Request.Overlays.
func TestShapesCoverRequestKinds(t *testing.T) {
	for _, k := range types.AllOverlayKinds() {
		if got, want := len(Shapes(k)) > 0, Accepts(Request, k); got != want {
			t.Errorf("%s: declares shapes %v, runs on Request.Overlays %v", k, got, want)
		}
	}
	ss := Shapes(types.OverlayKindShareOfTotal)
	ss[0] = types.OverlayShapeScalar
	if Shapes(types.OverlayKindShareOfTotal)[0] != types.OverlayShapeMatrix {
		t.Error("Shapes returned the table's own slice")
	}
}

func TestShapeRefusal(t *testing.T) {
	cases := []struct {
		name        string
		shape       types.OverlayShape
		kind        types.OverlayKind
		index       int
		wantMsg     string
		wantDetails map[string]any
	}{
		{"matrix kind on a series host", types.OverlayShapeSeries, types.OverlayKindShareOfCol, 1,
			"overlay OVERLAY_SHARE_OF_COL does not run on a series-shape Request.Overlays host; it runs on a matrix host (Request.Crosstab)",
			map[string]any{"index": 1, "kind": "OVERLAY_SHARE_OF_COL", "host": "request", "shape": "series", "shapes": []string{"matrix"}}},
		{"series kind on a matrix host", types.OverlayShapeMatrix, types.OverlayKindYoY, 0,
			"overlay OVERLAY_YOY does not run on a matrix-shape Request.Overlays host; it runs on a series host (Request.Groups + Request.Aggregations, no Request.Crosstab)",
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "host": "request", "shape": "matrix", "shapes": []string{"series"}}},
		{"formula on a series host", types.OverlayShapeSeries, types.OverlayKindFormula, 2,
			"overlay OVERLAY_FORMULA does not run on a series-shape Request.Overlays host; it runs on a matrix host (Request.Crosstab)",
			map[string]any{"index": 2, "kind": "OVERLAY_FORMULA", "host": "request", "shape": "series", "shapes": []string{"matrix"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := ShapeRefusal(c.shape, c.kind, c.kind, c.index)
			if ce == nil {
				t.Fatal("not refused")
			}
			if ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || ce.Message != c.wantMsg ||
				!reflect.DeepEqual(ce.Details, c.wantDetails) {
				t.Errorf("got %s %q %v", ce.Code, ce.Message, ce.Details)
			}
		})
	}
	for _, c := range []struct {
		shape types.OverlayShape
		kind  types.OverlayKind
	}{
		{types.OverlayShapeMatrix, types.OverlayKindShareOfTotal},
		{types.OverlayShapeSeries, types.OverlayKindShareOfTotal},
		{types.OverlayShapeMatrix, types.OverlayKindFormula},
		{types.OverlayShapeSeries, types.OverlayKindIndexVsTotal},
		{types.OverlayShapeScalar, types.OverlayKindShareOfCol},
		{types.OverlayShapeSeries, types.OverlayKindTVsRef},
		{types.OverlayShapeMatrix, "OVERLAY_NOT_IN_CATALOG"},
		{types.OverlayShapeMatrix, ""},
	} {
		if ce := ShapeRefusal(c.shape, c.kind, c.kind, 0); ce != nil {
			t.Errorf("%s on %s refused: %v", c.kind, c.shape, ce)
		}
	}
}

// TestReadsCellValue: every MATRIX kind but the three moment pairwise
// kinds reads cell values; no SERIES-only, Compose, chain or FACET kind
// does (processing's TestOverlayReadsCellValue_MatchesHandlers pins the
// MATRIX half to the handlers).
func TestReadsCellValue(t *testing.T) {
	moments := map[types.OverlayKind]bool{
		types.OverlayKindPairwiseWelchT:            true,
		types.OverlayKindPairwiseTwoMeansZ:         true,
		types.OverlayKindPairwiseWeightedTwoMeansZ: true,
	}
	values := 0
	for _, k := range types.AllOverlayKinds() {
		matrix := false
		for _, s := range Shapes(k) {
			matrix = matrix || s == types.OverlayShapeMatrix
		}
		want := matrix && !moments[k]
		if got := ReadsCellValue(k); got != want {
			t.Errorf("%s: ReadsCellValue = %v, want %v", k, got, want)
		}
		if want {
			values++
		}
	}
	if values != 14 {
		t.Errorf("%d value-reading kinds, want 14", values)
	}
	for _, k := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindDeltaVsStage, types.OverlayKindYoY, "OVERLAY_NOT_IN_CATALOG", ""} {
		if ReadsCellValue(k) {
			t.Errorf("%s reads cell values", k)
		}
	}
}
