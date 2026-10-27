package types_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// partsFreeLayers are single-output layers of every payload shape, as
// the existing kinds build them.
func partsFreeLayers() map[string]types.OverlayLayer {
	v := 117.5
	stat, p := 6.5, 0.01
	return map[string]types.OverlayLayer{
		"scalar": {Name: "chi", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix,
			Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &v},
			Summary: &types.OverlaySummary{Statistic: &stat, PValue: &p, Parameters: map[string]float64{"df": 1}}},
		"series": {Name: "rows", Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow,
			Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{
				Entries: []types.SeriesEntry{{Key: types.AxisKey{"a"}, Summary: types.OverlaySummary{Statistic: &stat}}},
			}}},
		"matrix": {Name: "idx", Kind: types.OverlayKindIndexVsMargin, Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
				RowKeys: []types.AxisKey{{"r"}}, ColumnKeys: []types.AxisKey{{"c"}},
				Cells: [][]types.MatrixCell{{{Value: 1.5, Present: true}}},
			}}},
	}
}

// TestOverlayPayload_PartsFreeByteIdentical: a layer without parts —
// every existing kind's — writes no `parts` key, and an empty parts map
// writes the same bytes as a nil one. The scalar case is pinned to its
// literal pre-Parts wire form.
func TestOverlayPayload_PartsFreeByteIdentical(t *testing.T) {
	t.Parallel()

	const pinnedScalar = `{"name":"chi","kind":"OVERLAY_CHISQ_MATRIX","scope":"matrix","ref":{},"payload":{"shape":"scalar","scalar":117.5},"summary":{"statistic":6.5,"p_value":0.01,"parameters":{"df":1}}}`
	for name, layer := range partsFreeLayers() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nilParts, err := json.Marshal(layer)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(nilParts), `"parts"`) {
				t.Fatalf("a parts-free layer wrote a parts key: %s", nilParts)
			}
			layer.Payload.Parts = map[string]*types.OverlayPayload{}
			empty, err := json.Marshal(layer)
			if err != nil {
				t.Fatal(err)
			}
			if string(empty) != string(nilParts) {
				t.Errorf("empty parts map diverged from nil:\n got %s\nwant %s", empty, nilParts)
			}
			if name == "scalar" && string(nilParts) != pinnedScalar {
				t.Errorf("scalar layer bytes moved:\n got %s\nwant %s", nilParts, pinnedScalar)
			}
		})
	}
}

// TestOverlayPayload_PartsRoundTrip: parts ride beside the primary,
// every part shape survives a JSON round trip, and an undefined figure
// inside a part is null on the wire (never 0).
func TestOverlayPayload_PartsRoundTrip(t *testing.T) {
	t.Parallel()

	primary, total := 2.0, 9.0
	stat := 1.25
	layer := types.OverlayLayer{
		Name: "multi", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeCell,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeScalar, Scalar: &primary,
			Parts: map[string]*types.OverlayPayload{
				"total": {Shape: types.OverlayShapeScalar, Scalar: &total},
				"row_coordinates": {Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
					RowKeys:    []types.AxisKey{{"a"}, {"b"}},
					ColumnKeys: []types.AxisKey{{"dim_1"}, {"dim_2"}},
					Cells: [][]types.MatrixCell{
						{{Value: 0.5, Present: true}, {Value: -0.25, Present: true}},
						{{Value: -0.5, Present: true}, {Present: false}},
					},
				}},
				"steady_state": {Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{
					Entries: []types.SeriesEntry{{Key: types.AxisKey{"a"}, Summary: types.OverlaySummary{Statistic: &stat}}},
				}},
			},
		},
	}
	b, err := json.Marshal(layer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"payload":{"shape":"scalar","scalar":2,"parts":{"row_coordinates":`) {
		t.Errorf("parts do not ride beside the primary: %s", b)
	}
	var got types.OverlayLayer
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(layer, got) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got.Payload.Parts, layer.Payload.Parts)
	}

	nan := math.NaN()
	layer.Payload.Parts["total"].Scalar = &nan
	b, err = json.Marshal(layer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"total":{"shape":"scalar","scalar":null}`) {
		t.Errorf("an undefined part figure is not null on the wire: %s", b)
	}
}

// TestComposedResponse_PartsCarriedUnchanged: the Compose envelope
// carries a top-level layer's parts byte-for-byte as the layer alone
// writes them.
func TestComposedResponse_PartsCarriedUnchanged(t *testing.T) {
	t.Parallel()

	v, e := 1.0, 4.0
	layer := types.OverlayLayer{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix,
		Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &v,
			Parts: map[string]*types.OverlayPayload{"expected": {Shape: types.OverlayShapeScalar, Scalar: &e}}}}
	alone, err := json.Marshal(layer)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(types.ComposedResponse{Overlays: []types.OverlayLayer{layer}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"overlays":[`+string(alone)+`]`) {
		t.Errorf("compose overlays diverged from the layer's own bytes:\n got %s\nlayer %s", out, alone)
	}
}
