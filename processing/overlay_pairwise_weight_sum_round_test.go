package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// pairwiseWeightSumHost builds a 1x1 host whose only cell carries the
// given sum_weights figure, so pairwiseSampleSize reads it directly.
func pairwiseWeightSumHost(sumWeights any) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells:        [][]types.MatrixCell{{{Value: 0.5, Present: true}}},
	}
	cell := map[string]any{"n": 300}
	if sumWeights != nil {
		cell["sum_weights"] = sumWeights
	}
	comps := &types.CrosstabComponents{CellComponents: [][]map[string]any{{cell}}}
	return NewCrosstabHostViewWithComponents(mx, comps)
}

// TestPairwiseSampleSize_CellWeightSumRoundsToNearest pins n_source
// "cell_weight_sum": the weight sum rounds to the NEAREST integer (half
// away from zero), never truncates. A sum of 219.99999963 (floating-point
// noise below 220) used to become n=219 and lost a significance marker.
func TestPairwiseSampleSize_CellWeightSumRoundsToNearest(t *testing.T) {
	params := types.PairwiseOverlayParams{NSource: types.PairwiseNSourceCellWeightSum}
	for _, tc := range []struct {
		sum  float64
		want int
	}{
		{219.99999963, 220},
		{219.4, 219},
		{219.5, 220},
		{219.0, 219},
		{0.4, 0},
		{0.5, 1},
	} {
		got, ok := pairwiseSampleSize(pairwiseWeightSumHost(tc.sum), params, true, 0, 0)
		if !ok {
			t.Fatalf("sum_weights=%v: ok=false, want true", tc.sum)
		}
		if got != tc.want {
			t.Errorf("sum_weights=%v: n = %d, want %d (round to nearest)", tc.sum, got, tc.want)
		}
	}
}

// TestPairwiseSampleSize_CellWeightSumAbsentSkips keeps the !ok path: a
// cell with no sum_weights key yields (0, false) so the pair skips.
func TestPairwiseSampleSize_CellWeightSumAbsentSkips(t *testing.T) {
	params := types.PairwiseOverlayParams{NSource: types.PairwiseNSourceCellWeightSum}
	n, ok := pairwiseSampleSize(pairwiseWeightSumHost(nil), params, true, 0, 0)
	if ok || n != 0 {
		t.Errorf("absent sum_weights: got (%d, %v), want (0, false)", n, ok)
	}
}

// TestOverlayPairwise_PropZ_CellWeightSumNoiseMatchesInteger runs the
// full OVERLAY_PAIRWISE_PROP_Z path: a cell whose sum_weights carries
// floating-point noise just below 220 must produce the SAME p-value as
// one whose sum is exactly 220, and that p must equal twoProportionZ at
// n=220.
func TestOverlayPairwise_PropZ_CellWeightSumNoiseMatchesInteger(t *testing.T) {
	p := func(sumA float64) float64 {
		t.Helper()
		mx := &types.MatrixPayload{
			RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
			ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
			RowKeys:      []types.AxisKey{{"A"}, {"B"}},
			ColumnKeys:   []types.AxisKey{{"x"}},
			Cells: [][]types.MatrixCell{
				{{Value: 50.0, Present: true}},
				{{Value: 40.0, Present: true}},
			},
		}
		comps := &types.CrosstabComponents{CellComponents: [][]map[string]any{
			{{"n": 300, "sum_weights": sumA}},
			{{"n": 300, "sum_weights": 220.0}},
		}}
		specs := []types.OverlaySpec{{
			Name:   "pz",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  types.OverlayScopeRow,
			Params: mustParams(t, types.PairwiseOverlayParams{NSource: types.PairwiseNSourceCellWeightSum}),
		}}
		layers, _, err := ApplyOverlays(specs, NewCrosstabHostViewWithComponents(mx, comps))
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		cell := layers[0].Payload.Matrix.Cells[0][0]
		v, ok := cell.Value.(float64)
		if !cell.Present || !ok {
			t.Fatalf("pair cell absent or non-float: %+v", cell)
		}
		return v
	}
	want, ok := twoProportionZ(0.50*220, 220, 0.40*220, 220)
	if !ok {
		t.Fatal("twoProportionZ unexpectedly undefined")
	}
	if got := p(219.99999963); math.Abs(got-want) > 1e-12 {
		t.Errorf("sum_weights 219.99999963: p = %v, want %v (n=220)", got, want)
	}
	if got := p(220.0); math.Abs(got-want) > 1e-12 {
		t.Errorf("sum_weights 220: p = %v, want %v", got, want)
	}
}
