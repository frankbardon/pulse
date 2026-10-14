package overlayhost

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestScalarSpecRefusal_CoversProcessKinds: exactly the kinds that run on
// a MATRIX or SERIES Process host have a SCALAR rule, each needing the
// host it runs on (OVERLAY_SHARE_OF_TOTAL, the one kind with both,
// dispatching by the request's grouping); the MATRIX_RESULT kind is
// refused earlier by ShapeRefusal and has none.
func TestScalarSpecRefusal_CoversProcessKinds(t *testing.T) {
	for _, kind := range types.AllOverlayKinds() {
		var matrix, series bool
		for _, s := range Shapes(kind) {
			matrix = matrix || s == types.OverlayShapeMatrix
			series = series || s == types.OverlayShapeSeries
		}
		rule, ruled := scalarRules[kind]
		if ruled != (matrix || series) {
			t.Errorf("%s: runs on a MATRIX / SERIES host %v, has a SCALAR rule %v", kind, matrix || series, ruled)
			continue
		}
		if !ruled {
			continue
		}
		if rule.dual != (matrix && series) {
			t.Errorf("%s: dual %v, runs on both Process shapes %v", kind, rule.dual, matrix && series)
		}
		want := types.OverlayShapeSeries
		if matrix {
			// A MATRIX kind needs a crosstab; the dual kind's ungrouped
			// default is its MATRIX dispatch.
			want = types.OverlayShapeMatrix
		}
		if rule.host != want {
			t.Errorf("%s: needs %s, runs on %s", kind, rule.host, want)
		}
	}
}

// goodScalarSpec is a spec of kind that passes its host's Ref / Scope
// table (grouped selects OVERLAY_SHARE_OF_TOTAL's SERIES dispatch).
func goodScalarSpec(kind types.OverlayKind, grouped bool) *types.OverlaySpec {
	shape, _, _ := scalarHost(kind, grouped)
	if shape == types.OverlayShapeSeries {
		s := &types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeGroup}
		switch seriesRules[kind].arm {
		case seriesRollingMean:
			s.Ref.RollingMean = &types.OverlayRollingMeanRef{}
		case seriesYoY:
			s.Ref.YoY = &types.OverlayYoYRef{}
		case seriesBaseline:
			s.Ref.BaselineIndex = &types.OverlayBaselineIndexRef{}
		case seriesSibling:
			s.Ref.Sibling = &types.OverlaySiblingRef{Field: "g", Value: "a"}
		}
		return s
	}
	s := &types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeCell}
	if rule, ok := specRules[kind]; ok {
		s.Scope = rule.scopes[0]
		if rule.ref == refMargin {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisColumn}
		}
	}
	return s
}

// TestScalarSpecRefusal_Rules: every ruled kind is refused on a SCALAR
// host — a well-formed spec by HostRequiredRefusal (the host it needs;
// axis for an axis-margin kind; host "series" for SHARE_OF_TOTAL's
// SERIES dispatch), a bad scope by its host's table first. A kind
// without a rule (a Compose kind, the empty route of a hidden kind) is
// never refused here.
func TestScalarSpecRefusal_Rules(t *testing.T) {
	for _, kind := range ScalarSpecKinds() {
		for _, grouped := range []bool{false, true} {
			good := goodScalarSpec(kind, grouped)
			shape, dual, _ := scalarHost(kind, grouped)
			cerr := ScalarSpecRefusal(good, kind, grouped, 3)
			want := HostRequiredRefusal(good, kind, grouped, 3)
			if cerr == nil || want == nil || cerr.Message != want.Message || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
				t.Fatalf("%s (grouped %v): %v, want the host refusal %v", kind, grouped, cerr, want)
			}
			if cerr.Details["index"] != 3 || cerr.Details["kind"] != string(kind) {
				t.Fatalf("%s: details %v", kind, cerr.Details)
			}
			host := "requires a MATRIX host (Request.Crosstab); none present"
			if shape == types.OverlayShapeSeries {
				host = "requires a SERIES host (grouped Process result: Request.Groups and Request.Aggregations non-empty, Request.Crosstab nil)"
			}
			if !strings.HasSuffix(cerr.Message, host) {
				t.Fatalf("%s: message %q, want the %s host wording", kind, cerr.Message, shape)
			}
			_, axis := cerr.Details["axis"]
			if wantAxis := shape == types.OverlayShapeMatrix && specRules[kind].ref == refMargin; axis != wantAxis || (axis && cerr.Details["axis"] != "column") {
				t.Fatalf("%s: axis %v in %v, want %v", kind, axis, cerr.Details, wantAxis)
			}
			if got := cerr.Details["host"]; (got == "series") != (dual && shape == types.OverlayShapeSeries) {
				t.Fatalf("%s (grouped %v): host detail %v", kind, grouped, got)
			}
			if kind == types.OverlayKindFormula {
				continue
			}
			bad := *good
			bad.Scope = "nowhere"
			cerr = ScalarSpecRefusal(&bad, kind, grouped, 3)
			if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED || cerr.Details["scope"] != "nowhere" {
				t.Fatalf("%s (grouped %v): bad scope %v, want the table's scope refusal first", kind, grouped, cerr)
			}
		}
	}
	for _, route := range []types.OverlayKind{types.OverlayKindRank, types.OverlayKindMatrixGroupSummary, ""} {
		spec := &types.OverlaySpec{Kind: types.OverlayKindChiSqMatrix}
		if cerr := ScalarSpecRefusal(spec, route, false, 0); cerr != nil {
			t.Fatalf("route %q refused: %v", route, cerr)
		}
		if cerr := HostRequiredRefusal(spec, route, false, 0); cerr != nil {
			t.Fatalf("route %q host-refused: %v", route, cerr)
		}
	}
	// The message and details name the authored kind, the route decides.
	spec := &types.OverlaySpec{Kind: "OVERLAY_ACME_X", Scope: types.OverlayScopeMatrix}
	if cerr := ScalarSpecRefusal(spec, types.OverlayKindChiSqMatrix, false, 0); cerr == nil || cerr.Details["kind"] != "OVERLAY_ACME_X" || !strings.HasPrefix(cerr.Message, "overlay OVERLAY_ACME_X ") {
		t.Fatalf("authored kind: %v", cerr)
	}
}
