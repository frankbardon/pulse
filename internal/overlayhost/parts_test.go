package overlayhost

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func scalarPart(v float64) *types.OverlayPayload {
	return &types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &v}
}

// TestPartsRefusal: a well-formed parts map (or none) passes; a nested
// part, a nil part, an unnamed part and a shapeless part are each
// refused as PROCESSING_INTERNAL with the offending part named.
func TestPartsRefusal(t *testing.T) {
	nested := scalarPart(1)
	nested.Parts = map[string]*types.OverlayPayload{"inner": scalarPart(2)}
	cases := []struct {
		name     string
		parts    map[string]*types.OverlayPayload
		wantPart string // "" = accepted
		reason   string
	}{
		{name: "no parts", parts: nil},
		{name: "empty map", parts: map[string]*types.OverlayPayload{}},
		{name: "flat parts", parts: map[string]*types.OverlayPayload{
			"expected": {Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{}},
			"steady":   {Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{}},
			"total":    scalarPart(3),
		}},
		{name: "nested part", parts: map[string]*types.OverlayPayload{"a": scalarPart(1), "b": nested},
			wantPart: "b", reason: "parts never nest: a part cannot carry parts"},
		{name: "nil part", parts: map[string]*types.OverlayPayload{"p": nil},
			wantPart: "p", reason: "a part cannot be nil"},
		{name: "unnamed part", parts: map[string]*types.OverlayPayload{"": scalarPart(1)},
			wantPart: "", reason: "a part needs a name"},
		{name: "unknown shape", parts: map[string]*types.OverlayPayload{"x": {Shape: "cube"}},
			wantPart: "x", reason: "a part's shape must be scalar, series or matrix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			layer := &types.OverlayLayer{Kind: types.OverlayKindChiSqMatrix, Payload: types.OverlayPayload{
				Shape: types.OverlayShapeScalar, Scalar: new(float64), Parts: c.parts,
			}}
			got := PartsRefusal(layer, 3)
			if c.reason == "" {
				if got != nil {
					t.Fatalf("refused a well-formed layer: %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("accepted a malformed part (%s)", c.reason)
			}
			if got.Code != errors.PROCESSING_INTERNAL {
				t.Errorf("code = %s, want PROCESSING_INTERNAL", got.Code)
			}
			if got.Details["part"] != c.wantPart || got.Details["reason"] != c.reason || got.Details["index"] != 3 {
				t.Errorf("details = %v, want part %q reason %q index 3", got.Details, c.wantPart, c.reason)
			}
		})
	}
	if PartsRefusal(nil, 0) != nil {
		t.Error("nil layer refused")
	}
}

// TestDimAxisKeys pins the synthetic dimension-key convention: one
// single-element key per dimension, 1-based, "dim_" prefixed.
func TestDimAxisKeys(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{-1, `[]`},
		{0, `[]`},
		{1, `[["dim_1"]]`},
		{3, `[["dim_1"],["dim_2"],["dim_3"]]`},
	}
	for _, c := range cases {
		b, err := json.Marshal(DimAxisKeys(c.n))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("DimAxisKeys(%d) = %s, want %s", c.n, b, c.want)
		}
	}
}
