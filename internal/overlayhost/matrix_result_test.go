package overlayhost

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

const mgs = types.OverlayKindMatrixGroupSummary

// TestJudgedShape: a request carrying matrices (no crosstab) offers the
// MATRIX_RESULT host to a MATRIX_RESULT kind and to every kind over a
// SCALAR authored shape; a crosstab or no-matrices request keeps its
// authored shape, and a SERIES kind keeps SERIES on a grouped request.
func TestJudgedShape(t *testing.T) {
	for _, c := range []struct {
		authored types.OverlayShape
		matrices bool
		kind     types.OverlayKind
		want     types.OverlayShape
	}{
		{types.OverlayShapeScalar, true, mgs, ShapeMatrixResult},
		{types.OverlayShapeSeries, true, mgs, ShapeMatrixResult},
		{types.OverlayShapeScalar, true, types.OverlayKindShareOfCol, ShapeMatrixResult},
		{types.OverlayShapeScalar, true, types.OverlayKindYoY, ShapeMatrixResult},
		{types.OverlayShapeSeries, true, types.OverlayKindYoY, types.OverlayShapeSeries},
		{types.OverlayShapeMatrix, true, mgs, types.OverlayShapeMatrix},
		{types.OverlayShapeScalar, false, mgs, types.OverlayShapeScalar},
		{types.OverlayShapeSeries, false, mgs, types.OverlayShapeSeries},
		{types.OverlayShapeScalar, false, types.OverlayKindShareOfCol, types.OverlayShapeScalar},
	} {
		if got := JudgedShape(c.authored, c.matrices, c.kind); got != c.want {
			t.Errorf("JudgedShape(%s, %v, %s) = %s, want %s", c.authored, c.matrices, c.kind, got, c.want)
		}
	}
}

// TestAcceptsShape_MatrixResult: the MATRIX_RESULT host runs exactly
// its kinds; a MATRIX_RESULT kind runs nowhere else — not even on the
// SCALAR host that accepts every other Process kind.
func TestAcceptsShape_MatrixResult(t *testing.T) {
	n := 0
	for _, k := range types.AllOverlayKinds() {
		if !Accepts(Request, k) {
			continue
		}
		mr := RunsOnMatrixResult(k)
		if mr {
			n++
		}
		if got := AcceptsShape(ShapeMatrixResult, k); got != mr {
			t.Errorf("%s on MATRIX_RESULT: %v, want %v", k, got, mr)
		}
		if got := AcceptsShape(types.OverlayShapeScalar, k); got == mr {
			t.Errorf("%s on SCALAR: %v, want %v", k, got, !mr)
		}
		if mr && (AcceptsShape(types.OverlayShapeMatrix, k) || AcceptsShape(types.OverlayShapeSeries, k)) {
			t.Errorf("%s runs on a crosstab or series host", k)
		}
	}
	if n != 1 || !RunsOnMatrixResult(mgs) {
		t.Errorf("%d MATRIX_RESULT kinds, want OVERLAY_MATRIX_GROUP_SUMMARY alone", n)
	}
	if !AcceptsShape(ShapeMatrixResult, "OVERLAY_NOT_IN_CATALOG") || !AcceptsShape(ShapeMatrixResult, "") {
		t.Error("a kind outside the catalog is refused on MATRIX_RESULT")
	}
}

func TestShapeRefusal_MatrixResult(t *testing.T) {
	ce := ShapeRefusal(types.OverlayShapeScalar, mgs, mgs, 2)
	want := map[string]any{"index": 2, "kind": string(mgs), "host": "request", "shape": "scalar", "shapes": []string{"matrix_result"}}
	if ce == nil || ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || !reflect.DeepEqual(ce.Details, want) ||
		ce.Message != "overlay OVERLAY_MATRIX_GROUP_SUMMARY does not run on a scalar-shape Request.Overlays host; it runs on a matrix-result host (Request.Matrices, no Request.Crosstab)" {
		t.Fatalf("got %+v", ce)
	}
	ce = ShapeRefusal(ShapeMatrixResult, types.OverlayKindShareOfCol, types.OverlayKindShareOfCol, 0)
	if ce == nil || ce.Details["shape"] != "matrix_result" {
		t.Fatalf("crosstab kind on MATRIX_RESULT: %+v", ce)
	}
}

func TestMatrixResultRefusal(t *testing.T) {
	ok := func() (*types.Request, *types.OverlaySpec) {
		req := &types.Request{
			Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}, {Name: "r", Type: types.MAT_CORRELATION, Vector: "v"}},
		}
		spec := &types.OverlaySpec{Kind: mgs, Scope: types.OverlayScopeMatrix,
			Ref: types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: "r"}}}
		return req, spec
	}
	for _, c := range []struct {
		name   string
		edit   func(*types.Request, *types.OverlaySpec)
		want   errors.Code
		detail map[string]any
	}{
		{"valid", func(*types.Request, *types.OverlaySpec) {}, "", nil},
		{"default name", func(_ *types.Request, s *types.OverlaySpec) { s.Ref.Matrix.Name = "MAT_COVARIANCE" }, "", nil},
		{"empty params object", func(_ *types.Request, s *types.OverlaySpec) { s.Params = json.RawMessage(` {} `) }, "", nil},
		{"null params", func(_ *types.Request, s *types.OverlaySpec) { s.Params = json.RawMessage(`null`) }, "", nil},
		{"no ref", func(_ *types.Request, s *types.OverlaySpec) { s.Ref = types.OverlayRef{} }, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, nil},
		{"empty name", func(_ *types.Request, s *types.OverlaySpec) { s.Ref.Matrix.Name = "" }, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, nil},
		{"second arm", func(_ *types.Request, s *types.OverlaySpec) {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
		}, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, nil},
		{"cell scope", func(_ *types.Request, s *types.OverlaySpec) { s.Scope = types.OverlayScopeCell }, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED, nil},
		{"level", func(_ *types.Request, s *types.OverlaySpec) { s.Level = 1 }, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE, nil},
		{"params", func(_ *types.Request, s *types.OverlaySpec) { s.Params = json.RawMessage(`{"weighted":true}`) }, errors.PULSE_OVERLAY_PARAM_MISSING, nil},
		{"unknown name", func(_ *types.Request, s *types.OverlaySpec) { s.Ref.Matrix.Name = "nope" }, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
			map[string]any{"index": 3, "kind": string(mgs), "which": "matrix", "name": "nope", "matrices": []string{"MAT_COVARIANCE", "r"}}},
		{"ungrouped", func(r *types.Request, _ *types.OverlaySpec) { r.Groups = nil }, errors.PULSE_OVERLAY_GROUPS_REQUIRED,
			map[string]any{"index": 3, "kind": string(mgs), "name": "r"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req, spec := ok()
			c.edit(req, spec)
			ce := MatrixResultRefusal(req, spec, mgs, 3)
			if c.want == "" {
				if ce != nil {
					t.Fatalf("refused: %+v", ce)
				}
				return
			}
			if ce == nil || ce.Code != c.want || ce.Details["index"] != 3 {
				t.Fatalf("got %+v, want %s at index 3", ce, c.want)
			}
			if c.detail != nil && !reflect.DeepEqual(ce.Details, c.detail) {
				t.Fatalf("details %v, want %v", ce.Details, c.detail)
			}
		})
	}
	req, spec := ok()
	spec.Ref = types.OverlayRef{}
	if MatrixResultRefusal(req, spec, types.OverlayKindShareOfCol, 0) != nil || MatrixResultRefusal(req, spec, "", 0) != nil {
		t.Error("a route off the MATRIX_RESULT host was judged")
	}
}

func TestStageRefusal(t *testing.T) {
	ce := StageRefusal(mgs, mgs, 1, 2)
	want := map[string]any{"index": 2, "kind": string(mgs), "host": "chain", "stage": 1}
	if ce == nil || ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || !reflect.DeepEqual(ce.Details, want) {
		t.Fatalf("got %+v", ce)
	}
	if StageRefusal(mgs, "", 0, 0) != nil || StageRefusal(types.OverlayKindShareOfCol, types.OverlayKindShareOfCol, 0, 0) != nil {
		t.Error("a hidden or non-MATRIX_RESULT route was refused")
	}
}
