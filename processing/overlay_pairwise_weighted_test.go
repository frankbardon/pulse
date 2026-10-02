package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z runtime tests.
//
// The oracle fixture is a 2 × 2 crosstab of (value, weight)
// observations, four per cell:
//
//	cell      observations (x, w)                     Σw  Σw²  mean    m2 = Σw(x−mean)²
//	(r0, c0)  (10,1) (14,2) (11,3) (13,1)              7   15   12      16
//	(r0, c1)  (12,2) (9,1)  (15,1) (13,2)              6   10   37/3    58/3
//	(r1, c0)  (12,1) (16,1) (11,2) (15,3)              7   15   95/7    194/7
//	(r1, c1)  (8,2)  (14,1) (11,1) (10,3)              7   15   71/7    174/7
//
// Expected p-values were derived OUTSIDE the engine by exact rational
// arithmetic (Python fractions.Fraction over the raw observations above,
// then one float sqrt and math.erfc): per leg
//
//	weights: var/n = (m2/(Σw−1)) / Σw
//	kish:    var/n = (m2/(Σw−Σw²/Σw)) / ((Σw)²/Σw²)
//
// se² = Σ legs, z = |mean_i − mean_j| / se, p = erfc(z/√2). The weights
// arm is statsmodels DescrStatsW(ddof=1) std_mean² summed over the legs
// (CompareMeans.ztest_ind, usevar="unequal"); statsmodels is not
// installed locally, so the closed form was evaluated exactly instead.
// Exact se² per pair:
//
//	weights  row  (r0,r1)@c0 51/49        (r0,r1)@c1 2726/2205
//	weights  col  (c0,c1)@r0 323/315      (c0,c1)@r1 184/147
//	kish     row  (r0,r1)@c0 135/49       (r0,r1)@c1 273470/97461
//	kish     col  (c0,c1)@r0 31295/13923  (c0,c1)@r1 2760/833
const (
	wtzWeightsRowC0 = 0.12348527214454894
	wtzWeightsRowC1 = 0.04883125804570395
	wtzWeightsColR0 = 0.7420200284873637
	wtzWeightsColR1 = 0.0021801703774041096
	wtzKishRowC0    = 0.34377675529835794
	wtzKishRowC1    = 0.19098427929325487
	wtzKishColR0    = 0.8240531699179632
	wtzKishColR1    = 0.059623517242732016
)

// wtzTolerance absorbs float summation-order / FMA divergence between the
// engine's streaming weighted recurrence and the exact oracle. The
// smallest oracle p-value is ~2e-3, so 1e-9 still separates every arm.
const wtzTolerance = 1e-9

func wtzSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	rowDict := encoding.NewDictionary()
	for _, r := range []string{"r0", "r1"} {
		if _, err := rowDict.Add(r); err != nil {
			t.Fatalf("row dict.Add: %v", err)
		}
	}
	colDict := encoding.NewDictionary()
	for _, c := range []string{"c0", "c1"} {
		if _, err := colDict.Add(c); err != nil {
			t.Fatalf("col dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "row", Type: encoding.FieldTypeCategoricalU8, Dictionary: rowDict},
			{Name: "col", Type: encoding.FieldTypeCategoricalU8, Dictionary: colDict},
			{Name: "value", Type: encoding.FieldTypeF64},
			{Name: "weight", Type: encoding.FieldTypeF64},
		},
	}
}

func wtzRecords(schema *encoding.Schema) []*Record {
	fixture := []struct {
		row, col      int
		value, weight float64
	}{
		{0, 0, 10, 1}, {0, 0, 14, 2}, {0, 0, 11, 3}, {0, 0, 13, 1},
		{0, 1, 12, 2}, {0, 1, 9, 1}, {0, 1, 15, 1}, {0, 1, 13, 2},
		{1, 0, 12, 1}, {1, 0, 16, 1}, {1, 0, 11, 2}, {1, 0, 15, 3},
		{1, 1, 8, 2}, {1, 1, 14, 1}, {1, 1, 11, 1}, {1, 1, 10, 3},
	}
	out := make([]*Record, 0, len(fixture))
	for _, o := range fixture {
		out = append(out, NewRecord(schema, map[string]float64{
			"row": float64(o.row), "col": float64(o.col),
			"value": o.value, "weight": o.weight,
		}))
	}
	return out
}

func wtzSpec(t *testing.T, scope types.OverlayScope, params types.PairwiseOverlayParams) types.OverlaySpec {
	t.Helper()
	return types.OverlaySpec{
		Name:   "wtz",
		Kind:   types.OverlayKindPairwiseWeightedTwoMeansZ,
		Scope:  scope,
		Params: mustParams(t, params),
	}
}

func wtzRun(t *testing.T, overlays []types.OverlaySpec, disableComponents bool) (*types.Response, error) {
	t.Helper()
	schema := wtzSchema(t)
	req := crosstabWeightedOverlayBaseRequest()
	req.Overlays = overlays
	return runBufferedCrosstabWithComponents(t, schema, req, wtzRecords(schema), disableComponents)
}

func wtzCell(t *testing.T, cell types.MatrixCell, label string) float64 {
	t.Helper()
	if !cell.Present {
		t.Fatalf("%s: p-value cell absent", label)
	}
	v, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("%s: p-value is %T, want float64", label, cell.Value)
	}
	return v
}

func wtzAssertP(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > wtzTolerance {
		t.Errorf("%s: p = %.17g, want %.17g (oracle)", label, got, want)
	}
}

// TestPairwiseWeightedTwoMeansZ_OracleRowAndColumn runs the real
// AGG_WEIGHTED_MEAN crosstab end to end, so the overlay reads the keys
// the aggregator actually emits, and checks every p-value against the
// exact-arithmetic oracle under both n_basis arms and both scopes.
func TestPairwiseWeightedTwoMeansZ_OracleRowAndColumn(t *testing.T) {
	cases := []struct {
		nBasis       string
		rowC0, rowC1 float64
		colR0, colR1 float64
	}{
		{types.PairwiseNBasisWeights, wtzWeightsRowC0, wtzWeightsRowC1, wtzWeightsColR0, wtzWeightsColR1},
		{types.PairwiseNBasisKish, wtzKishRowC0, wtzKishRowC1, wtzKishColR0, wtzKishColR1},
	}
	for _, tc := range cases {
		t.Run(tc.nBasis, func(t *testing.T) {
			params := types.PairwiseOverlayParams{NBasis: tc.nBasis}
			resp, err := wtzRun(t, []types.OverlaySpec{
				wtzSpec(t, types.OverlayScopeRow, params),
				wtzSpec(t, types.OverlayScopeColumn, params),
			}, false)
			if err != nil {
				t.Fatalf("RunCrosstab: %v", err)
			}
			if len(resp.Overlays) != 2 {
				t.Fatalf("got %d overlay layers, want 2", len(resp.Overlays))
			}
			if len(resp.Warnings) != 0 {
				t.Fatalf("unexpected warnings on a non-degenerate fixture: %+v", resp.Warnings)
			}

			// ROW scope: one pair (r0, r1) × opposite columns (c0, c1).
			row := resp.Overlays[0].Payload.Matrix
			if row == nil || len(row.RowKeys) != 1 || len(row.ColumnKeys) != 2 {
				t.Fatalf("row-scope layer shape wrong: %+v", row)
			}
			wtzAssertP(t, wtzCell(t, row.Cells[0][0], "row (r0,r1)@c0"), tc.rowC0, "row (r0,r1)@c0")
			wtzAssertP(t, wtzCell(t, row.Cells[0][1], "row (r0,r1)@c1"), tc.rowC1, "row (r0,r1)@c1")

			// COLUMN scope: opposite rows (r0, r1) × one pair (c0, c1).
			col := resp.Overlays[1].Payload.Matrix
			if col == nil || len(col.RowKeys) != 2 || len(col.ColumnKeys) != 1 {
				t.Fatalf("column-scope layer shape wrong: %+v", col)
			}
			wtzAssertP(t, wtzCell(t, col.Cells[0][0], "col (c0,c1)@r0"), tc.colR0, "col (c0,c1)@r0")
			wtzAssertP(t, wtzCell(t, col.Cells[1][0], "col (c0,c1)@r1"), tc.colR1, "col (c0,c1)@r1")
		})
	}
}

// TestPairwiseWeightedTwoMeansZ_BasePayloadUntouched pins the overlay
// contract: adding the layer must leave every other byte of the
// response — matrix, components, data, warnings — identical to the
// overlay-free run.
func TestPairwiseWeightedTwoMeansZ_BasePayloadUntouched(t *testing.T) {
	base, err := wtzRun(t, nil, false)
	if err != nil {
		t.Fatalf("baseline RunCrosstab: %v", err)
	}
	with, err := wtzRun(t, []types.OverlaySpec{
		wtzSpec(t, types.OverlayScopeRow, types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisKish}),
	}, false)
	if err != nil {
		t.Fatalf("overlay RunCrosstab: %v", err)
	}
	if len(with.Overlays) != 1 {
		t.Fatalf("got %d overlay layers, want 1", len(with.Overlays))
	}
	stripped := *with
	stripped.Overlays = nil
	if want, got := jsonOf(t, base), jsonOf(t, &stripped); want != got {
		t.Fatalf("base payload changed by the overlay:\nwithout: %s\nwith:    %s", want, got)
	}
}

// wtzHost builds a host view directly from weighted-moment components,
// for the shape / skip / pairing cases that need cells the aggregator
// fixture does not produce.
func wtzHost(rowKeys, colKeys []types.AxisKey, colFields []string, cells [][]map[string]any) *CrosstabHostView {
	colTypes := make([]string, len(colFields))
	for i := range colTypes {
		colTypes[i] = "GROUP_CATEGORY"
	}
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: colFields, Types: colTypes},
		RowKeys:      rowKeys,
		ColumnKeys:   colKeys,
	}
	mx.Cells = make([][]types.MatrixCell, len(rowKeys))
	for r := range rowKeys {
		mx.Cells[r] = make([]types.MatrixCell, len(colKeys))
		for c := range colKeys {
			if cells[r][c] != nil {
				mx.Cells[r][c] = types.MatrixCell{Value: cells[r][c]["weighted_mean"], Present: true}
			}
		}
	}
	return NewCrosstabHostViewWithComponents(mx, &types.CrosstabComponents{CellComponents: cells})
}

// wtzMoments is one weighted-mean cell's component map. The universal
// floor n is set deliberately WRONG (999): the overlay must size from
// the weight moments, never the floor.
func wtzMoments(mean, m2, sumW, sumWSq float64) map[string]any {
	return map[string]any{
		"n": 999, "n_null": 0,
		"weighted_mean": mean, "m2_weighted": m2,
		"sum_weights": sumW, "sum_weights_sq": sumWSq,
		"sum_weighted": mean * sumW,
	}
}

// TestPairwiseWeightedTwoMeansZ_PairAlongDim mirrors the Welford
// sibling: pair_along_dim=1 on a (wave, aud) column axis pairs auds
// within each wave only. Each in-wave pair is checked against the
// closed form, so a cross-wave pairing could not pass by luck.
func TestPairwiseWeightedTwoMeansZ_PairAlongDim(t *testing.T) {
	// Every cell: Σw = 5, Σw² = 9 (weights 1,2,2), m2 = 8, so under
	// n_basis=weights var/n = (8/4)/5 = 2/5 per leg and se² = 4/5.
	m := func(mean float64) map[string]any { return wtzMoments(mean, 8, 5, 9) }
	host := wtzHost(
		[]types.AxisKey{{"A"}},
		[]types.AxisKey{{"2021", "all"}, {"2021", "owner"}, {"2022", "all"}, {"2022", "owner"}},
		[]string{"wave", "aud"},
		[][]map[string]any{{m(10), m(11), m(20), m(23)}},
	)
	dim := 1
	layers, _, err := ApplyOverlays([]types.OverlaySpec{
		wtzSpec(t, types.OverlayScopeColumn, types.PairwiseOverlayParams{
			NBasis: types.PairwiseNBasisWeights, PairAlongDim: &dim,
		}),
	}, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	out := layers[0].Payload.Matrix
	if len(out.ColumnKeys) != 2 {
		t.Fatalf("expected 2 within-wave pairs, got %d: %v", len(out.ColumnKeys), out.ColumnKeys)
	}
	// z = |Δmean| / sqrt(4/5); p = erfc(z/√2).
	se := math.Sqrt(4.0 / 5.0)
	for i, delta := range []float64{1, 3} {
		want := math.Erfc(delta / se / math.Sqrt2)
		wtzAssertP(t, wtzCell(t, out.Cells[0][i], "within-wave pair"), want, "within-wave pair")
	}
	if k := out.ColumnKeys[0]; k[0] != "2021|all" || k[1] != "2021|owner" {
		t.Fatalf("pair[0] key = %v, want [2021|all 2021|owner]", k)
	}
}

// TestPairwiseWeightedTwoMeansZ_Skips covers the three degenerate legs.
// Each skips the pair-cell (absent on the payload) and reports exactly
// one aggregated PULSE_OVERLAY_REF_ZERO warning, while a healthy pair in
// the same layer still computes — so a skip that over-reaches fails.
func TestPairwiseWeightedTwoMeansZ_Skips(t *testing.T) {
	healthy := wtzMoments(10, 8, 5, 9)
	healthy2 := wtzMoments(12, 8, 5, 9)
	cases := []struct {
		name       string
		nBasis     string
		degenerate [2]map[string]any
		reason     string
	}{
		{
			// Σw = 1 exactly: m2/(Σw−1) has no denominator. Under kish the
			// same leg is fine (two rows, Σw² < (Σw)²), so the skip is
			// specific to the weights arm.
			name:       "weights_leg_sum_weights_le_1",
			nBasis:     types.PairwiseNBasisWeights,
			degenerate: [2]map[string]any{wtzMoments(10, 0.5, 1, 0.5), healthy2},
			reason:     "ORBIT_STATS_SKIP_N_TOO_SMALL",
		},
		{
			// All weight on one row: Σw = 5, Σw² = 25, so Σw − Σw²/Σw = 0.
			// Σw > 1, so the weights arm would NOT skip — the test is
			// specific to the kish denominator, not to n_eff (which is 1).
			name:       "kish_leg_all_weight_on_one_row",
			nBasis:     types.PairwiseNBasisKish,
			degenerate: [2]map[string]any{wtzMoments(10, 0, 5, 25), healthy2},
			reason:     "ORBIT_STATS_SKIP_N_TOO_SMALL",
		},
		{
			// Both legs have zero spread: se = 0.
			name:       "both_zero_variance",
			nBasis:     types.PairwiseNBasisWeights,
			degenerate: [2]map[string]any{wtzMoments(10, 0, 5, 9), wtzMoments(12, 0, 5, 9)},
			reason:     "ORBIT_STATS_SKIP_SE_ZERO",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Column c0 carries the degenerate pair, c1 a healthy one.
			host := wtzHost(
				[]types.AxisKey{{"A"}, {"B"}},
				[]types.AxisKey{{"c0"}, {"c1"}},
				[]string{"aud"},
				[][]map[string]any{
					{tc.degenerate[0], healthy},
					{tc.degenerate[1], healthy2},
				},
			)
			layers, warns, err := ApplyOverlays([]types.OverlaySpec{
				wtzSpec(t, types.OverlayScopeRow, types.PairwiseOverlayParams{NBasis: tc.nBasis}),
			}, host)
			if err != nil {
				t.Fatalf("ApplyOverlays: %v", err)
			}
			mx := layers[0].Payload.Matrix
			if mx.Cells[0][0].Present {
				t.Fatalf("degenerate pair-cell present (p=%v); want skipped", mx.Cells[0][0].Value)
			}
			if !mx.Cells[0][1].Present {
				t.Fatalf("healthy pair-cell skipped; the skip over-reached")
			}
			if len(warns) != 1 {
				t.Fatalf("got %d warnings, want 1 aggregated: %+v", len(warns), warns)
			}
			w := warns[0]
			if w.Code != string(errors.PULSE_OVERLAY_REF_ZERO) {
				t.Fatalf("warning code = %s, want PULSE_OVERLAY_REF_ZERO", w.Code)
			}
			if w.Details["reason"] != tc.reason || w.Details["skipped"] != 1 {
				t.Fatalf("warning details = %+v, want reason %s skipped 1", w.Details, tc.reason)
			}
		})
	}
}

// TestPairwiseWeightedTwoMeansZ_Refusals covers every hard refusal and
// pins each one's canonical code as the CodedError's own Code.
func TestPairwiseWeightedTwoMeansZ_Refusals(t *testing.T) {
	weightedHost := func() *CrosstabHostView {
		return wtzHost(
			[]types.AxisKey{{"A"}, {"B"}}, []types.AxisKey{{"x"}}, []string{"aud"},
			[][]map[string]any{{wtzMoments(10, 8, 5, 9)}, {wtzMoments(12, 8, 5, 9)}},
		)
	}
	welfordHost := func() *CrosstabHostView {
		mx := &types.MatrixPayload{
			RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
			ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
			RowKeys:      []types.AxisKey{{"A"}, {"B"}},
			ColumnKeys:   []types.AxisKey{{"x"}},
			Cells:        [][]types.MatrixCell{{{Value: 10.0, Present: true}}, {{Value: 12.0, Present: true}}},
		}
		return NewCrosstabHostViewWithComponents(mx, &types.CrosstabComponents{
			CellComponents: [][]map[string]any{
				{{"mean": 10.0, "variance": 4.0, "n": 50}},
				{{"mean": 12.0, "variance": 9.0, "n": 60}},
			},
		})
	}
	noComponentsHost := func() *CrosstabHostView {
		return NewCrosstabHostView(weightedHost().Payload())
	}
	cases := []struct {
		name   string
		host   func() *CrosstabHostView
		params types.PairwiseOverlayParams
		code   errors.Code
	}{
		{"welford_cell_host", welfordHost,
			types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisWeights},
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"components_disabled", noComponentsHost,
			types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisWeights},
			errors.PULSE_OVERLAY_COMPONENTS_REQUIRED},
		{"missing_n_basis", weightedHost,
			types.PairwiseOverlayParams{},
			errors.PULSE_OVERLAY_PARAM_MISSING},
		{"bad_n_basis", weightedHost,
			types.PairwiseOverlayParams{NBasis: "effective"},
			errors.PULSE_OVERLAY_PARAM_MISSING},
		{"n_source_present", weightedHost,
			types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisKish, NSource: types.PairwiseNSourceCellWeightSum},
			errors.PULSE_OVERLAY_PARAM_MISSING},
		{"p_source_present", weightedHost,
			types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisKish, PSource: types.PairwisePSourceCellValue},
			errors.PULSE_OVERLAY_PARAM_MISSING},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ApplyOverlays([]types.OverlaySpec{
				wtzSpec(t, types.OverlayScopeRow, tc.params),
			}, tc.host())
			if err == nil {
				t.Fatalf("expected %s, got nil", tc.code)
			}
			if !pairwiseErrHasCode(err, tc.code) {
				t.Fatalf("error %v does not carry %s", err, tc.code)
			}
		})
	}

	// Positive control: the weighted host with a valid n_basis runs, so
	// the refusals above are about their one changed input.
	if _, _, err := ApplyOverlays([]types.OverlaySpec{
		wtzSpec(t, types.OverlayScopeRow, types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisWeights}),
	}, weightedHost()); err != nil {
		t.Fatalf("control run failed: %v", err)
	}
}

// TestPairwiseWeightedTwoMeansZ_ComponentsDisabledEndToEnd drives the
// real crosstab with components off: the overlay must refuse with
// PULSE_OVERLAY_COMPONENTS_REQUIRED rather than skip every pair.
func TestPairwiseWeightedTwoMeansZ_ComponentsDisabledEndToEnd(t *testing.T) {
	_, err := wtzRun(t, []types.OverlaySpec{
		wtzSpec(t, types.OverlayScopeRow, types.PairwiseOverlayParams{NBasis: types.PairwiseNBasisWeights}),
	}, true)
	if err == nil {
		t.Fatalf("expected PULSE_OVERLAY_COMPONENTS_REQUIRED, got nil")
	}
	if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_COMPONENTS_REQUIRED) {
		t.Fatalf("error %v does not carry PULSE_OVERLAY_COMPONENTS_REQUIRED", err)
	}
}
