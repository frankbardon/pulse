package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// pairwiseWeightSumHost builds a 1x1 host whose only cell carries the
// given sum_weights figure, so pairwiseCountSampleSize reads it directly.
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
	return newCrosstabHostViewWithComponents(mx, comps)
}

// pairwiseCellValueHost builds a 1x1 host whose only cell VALUE is the
// given weighted count, the leg n_source "cell_value_weighted" reads.
func pairwiseCellValueHost(value float64) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells:        [][]types.MatrixCell{{{Value: value, Present: true}}},
	}
	return newCrosstabHostViewWithComponents(mx, nil)
}

// weightedNArms are the two n_source modes whose leg is a float weight
// base: both read n as the true base, truncated after absorbing float
// noise (⌊x + 1e-6⌋), never rounded.
var weightedNArms = []struct {
	name   string
	source string
	host   func(float64) *CrosstabHostView
}{
	{"cell_weight_sum", types.PairwiseNSourceCellWeightSum, func(f float64) *CrosstabHostView { return pairwiseWeightSumHost(f) }},
	{"cell_value_weighted", types.PairwiseNSourceCellValueWeight, pairwiseCellValueHost},
}

// TestPairwiseSampleSize_WeightedNTruncatesTrueBase pins the weighted n
// rule on both arms: float noise just below an integer reads as that
// integer, while a real fractional part truncates (49.55 is a base under
// 50, never rounded up to clear min_n 50).
func TestPairwiseSampleSize_WeightedNTruncatesTrueBase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   float64
		want int
	}{
		// Case 1: accumulation noise below an integral base.
		{"noise_220", 219.99999962747097, 220},
		{"noise_210", 210.26000098884106, 210},
		// Case 2: a real fractional part truncates, never rounds.
		{"frac_210.6", 210.6, 210},
		{"frac_49.55", 49.55, 49},
		{"frac_856.54", 856.54, 856},
		{"frac_1174.77", 1174.77, 1174},
		// Case 3: noise within the epsilon reads as the integer.
		{"eps_50", 49.9999999, 50},
		{"eps_136", 135.99999997, 136},
		{"eps_74", 73.99999994, 74},
		// Case 4: a gap wider than the epsilon is a real fraction.
		{"below_eps_49.999", 49.999, 49},
		// Integral bases are unchanged.
		{"exact_220", 220.0, 220},
		{"exact_0", 0.0, 0},
	}
	for _, arm := range weightedNArms {
		params := types.PairwiseOverlayParams{NSource: arm.source}
		for _, tc := range cases {
			t.Run(arm.name+"/"+tc.name, func(t *testing.T) {
				got, ok := pairwiseCountSampleSize(arm.host(tc.in), params, true, 0, 0)
				if !ok {
					t.Fatalf("%s=%v: ok=false, want true", arm.name, tc.in)
				}
				if got != tc.want {
					t.Errorf("%s=%v: n = %d, want %d (⌊x + 1e-6⌋)", arm.name, tc.in, got, tc.want)
				}
			})
		}
	}
}

// TestPairwiseSampleSize_CellWeightSumAbsentSkips keeps the !ok path: a
// cell with no sum_weights key yields (0, false) so the pair skips.
func TestPairwiseSampleSize_CellWeightSumAbsentSkips(t *testing.T) {
	t.Parallel()

	params := types.PairwiseOverlayParams{NSource: types.PairwiseNSourceCellWeightSum}
	n, ok := pairwiseCountSampleSize(pairwiseWeightSumHost(nil), params, true, 0, 0)
	if ok || n != 0 {
		t.Errorf("absent sum_weights: got (%d, %v), want (0, false)", n, ok)
	}
}

// Case 1 pair: two weighted bases whose sums carry float noise, with the
// proportions observed on them. At n = 220 / 210 the pair's p is
// ≈ 0.0997349, so it is marked at alpha 0.10; a one-off n flips it.
const (
	noiseSumA = 219.99999962747097
	noiseSumB = 210.26000098884106
	noisePA   = 0.33386363667158847
	noisePB   = 0.26120041794736454
	noiseWant = 0.0997349
	noiseAlph = 0.10
)

// TestPairwiseWeightedN_NoisePairMarkedAtAlpha checks case 1 end to end
// on both arms: the n each arm reads feeds the shared two-proportion
// kernel and yields p ≈ 0.0997349 < 0.10. The cell_weight_sum arm also
// runs the full OVERLAY_PAIRWISE_PROP_Z path through ApplyOverlays.
func TestPairwiseWeightedN_NoisePairMarkedAtAlpha(t *testing.T) {
	t.Parallel()

	for _, arm := range weightedNArms {
		t.Run(arm.name, func(t *testing.T) {
			params := types.PairwiseOverlayParams{NSource: arm.source}
			n1, ok1 := pairwiseCountSampleSize(arm.host(noiseSumA), params, true, 0, 0)
			n2, ok2 := pairwiseCountSampleSize(arm.host(noiseSumB), params, true, 0, 0)
			if !ok1 || !ok2 || n1 != 220 || n2 != 210 {
				t.Fatalf("n = (%d,%v) / (%d,%v), want 220 / 210", n1, ok1, n2, ok2)
			}
			p, ok := twoProportionZ(noisePA*float64(n1), float64(n1), noisePB*float64(n2), float64(n2))
			if !ok {
				t.Fatal("twoProportionZ undefined")
			}
			if math.Abs(p-noiseWant) > 1e-6 {
				t.Errorf("p = %v, want ≈ %v", p, noiseWant)
			}
			if p >= noiseAlph {
				t.Errorf("p = %v, want marked at alpha %v", p, noiseAlph)
			}
		})
	}

	t.Run("cell_weight_sum/apply_overlays", func(t *testing.T) {
		mx := &types.MatrixPayload{
			RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
			ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
			RowKeys:      []types.AxisKey{{"A"}, {"B"}},
			ColumnKeys:   []types.AxisKey{{"x"}},
			Cells: [][]types.MatrixCell{
				{{Value: noisePA, Present: true}},
				{{Value: noisePB, Present: true}},
			},
		}
		comps := &types.CrosstabComponents{CellComponents: [][]map[string]any{
			{{"n": 300, "sum_weights": noiseSumA}},
			{{"n": 300, "sum_weights": noiseSumB}},
		}}
		specs := []types.OverlaySpec{{
			Name:  "pz",
			Kind:  types.OverlayKindPairwisePropZ,
			Scope: types.OverlayScopeRow,
			Params: mustParams(t, types.PairwiseOverlayParams{
				NSource: types.PairwiseNSourceCellWeightSum,
				PSource: types.PairwisePSourceCellValue,
			}),
		}}
		layers, _, err := ApplyOverlaysWithExtensions(specs, newCrosstabHostViewWithComponents(mx, comps), nil)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		cell := layers[0].Payload.Matrix.Cells[0][0]
		p, ok := cell.Value.(float64)
		if !cell.Present || !ok {
			t.Fatalf("pair cell absent or non-float: %+v", cell)
		}
		want, ok := twoProportionZ(noisePA*220, 220, noisePB*210, 210)
		if !ok {
			t.Fatal("twoProportionZ unexpectedly undefined")
		}
		if math.Abs(p-want) > 1e-12 {
			t.Errorf("p = %v, want %v (n = 220 / 210)", p, want)
		}
		if math.Abs(p-noiseWant) > 1e-6 || p >= noiseAlph {
			t.Errorf("p = %v, want ≈ %v and marked at alpha %v", p, noiseWant, noiseAlph)
		}
	})
}
