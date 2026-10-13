package processing

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// stdResidualCase is one std_residual.json case
// (scripts/reference/gen_std_residual.R). A nil entry is R's NaN.
type stdResidualCase struct {
	Name     string       `json:"name"`
	Observed [][]float64  `json:"observed"`
	Expected [][]*float64 `json:"expected"`
	StdRes   [][]*float64 `json:"stdres"`
	P        [][]*float64 `json:"p"`
	PRowHolm [][]*float64 `json:"p_row_holm"`
	PRowBH   [][]*float64 `json:"p_row_bh"`
}

// countHost is an unweighted crosstab host whose cells are table, with
// every cell present and margins summed from it.
func countHost(table [][]float64) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		CellLabel:    "n",
	}
	var grand float64
	colTotals := make([]float64, len(table[0]))
	for i, row := range table {
		mx.RowKeys = append(mx.RowKeys, types.AxisKey{string(rune('A' + i))})
		cells := make([]types.MatrixCell, len(row))
		var rt float64
		for j, v := range row {
			cells[j] = types.MatrixCell{Value: v, Present: true}
			rt += v
			colTotals[j] += v
		}
		grand += rt
		mx.Cells = append(mx.Cells, cells)
		mx.RowMargins = append(mx.RowMargins, types.MatrixCell{Value: rt, Present: true})
	}
	for j, v := range colTotals {
		mx.ColumnKeys = append(mx.ColumnKeys, types.AxisKey{string(rune('x' + j))})
		mx.ColumnMargins = append(mx.ColumnMargins, types.MatrixCell{Value: v, Present: true})
	}
	mx.GrandTotal = types.MatrixCell{Value: grand, Present: true}
	return NewCrosstabHostView(mx)
}

// assertMatrixMatches compares a payload matrix to an R matrix: nil
// (NaN in R) must be a present NaN cell, every other value within tol
// relative (absolute below 1).
func assertMatrixMatches(t *testing.T, where string, got *types.MatrixPayload, want [][]*float64, tol float64) {
	t.Helper()
	if got == nil || len(got.Cells) != len(want) {
		t.Fatalf("%s: matrix %+v, want %d rows", where, got, len(want))
	}
	for i := range want {
		if len(got.Cells[i]) != len(want[i]) {
			t.Fatalf("%s: row %d has %d cells, want %d", where, i, len(got.Cells[i]), len(want[i]))
		}
		for j, w := range want[i] {
			c := got.Cells[i][j]
			v, ok := c.Value.(float64)
			if !c.Present || !ok {
				t.Fatalf("%s[%d][%d]: cell %+v, want a present float64", where, i, j, c)
			}
			if w == nil {
				if !math.IsNaN(v) {
					t.Errorf("%s[%d][%d] = %v, want NaN (R null)", where, i, j, v)
				}
				continue
			}
			if d := math.Abs(v - *w); d > tol*math.Max(1, math.Abs(*w)) {
				t.Errorf("%s[%d][%d] = %.17g, want %.17g (R)", where, i, j, v, *w)
			}
		}
	}
}

func stdResidualSpec() *types.OverlaySpec {
	return &types.OverlaySpec{Kind: types.OverlayKindStdResidual, Scope: types.OverlayScopeCell}
}

// TestStdResidual_MatchesR: residuals, expected counts and p-values
// equal R's chisq.test()$stdres / $expected and 2·pnorm(−|z|) on every
// case — unweighted tables (including an all-zero row, whose cells R
// leaves NaN), the weighted fixture under kind frequency (the Σw table
// as is) and under kind probability (scaled to its Kish n_eff).
func TestStdResidual_MatchesR(t *testing.T) {
	doc := loadReference[stdResidualCase](t, "std_residual")
	hosts := map[string]func(c stdResidualCase) *CrosstabHostView{
		"weighted_frequency": func(stdResidualCase) *CrosstabHostView {
			return weightedCountHost(ctFixture, 2, 3, weighting.Frequency)
		},
		"weighted_probability": func(stdResidualCase) *CrosstabHostView {
			return weightedCountHost(ctFixture, 2, 3, weighting.Probability)
		},
	}
	seen := 0
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			build, ok := hosts[c.Name]
			if !ok {
				build = func(c stdResidualCase) *CrosstabHostView { return countHost(c.Observed) }
			}
			layer, _, err := applyStdResidual(stdResidualSpec(), build(c))
			if err != nil {
				t.Fatal(err)
			}
			assertMatrixMatches(t, "residual", layer.Payload.Matrix, c.StdRes, 1e-12)
			assertMatrixMatches(t, "expected", layer.Payload.Parts[StdResidualPartExpected].Matrix, c.Expected, 1e-12)
			assertMatrixMatches(t, "p", layer.Payload.Parts[StdResidualPartP].Matrix, c.P, 1e-10)
		})
		seen++
	}
	if seen < 5 {
		t.Fatalf("std_residual.json has %d cases, want at least 5", seen)
	}
}

// TestStdResidual_AgrestiWorkedExample: the published adjusted
// residuals of Agresti (2007, An Introduction to Categorical Data
// Analysis, Table 2.5 / Section 2.4.5) — party identification by
// gender, rounded to two decimals in the book.
func TestStdResidual_AgrestiWorkedExample(t *testing.T) {
	host := countHost([][]float64{{762, 327, 468}, {484, 239, 477}})
	layer, ws, err := applyStdResidual(stdResidualSpec(), host)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 0 {
		t.Fatalf("warnings %+v, want none (every expected count is large)", ws)
	}
	published := [][]float64{{4.50, 0.70, -5.32}, {-4.50, -0.70, 5.32}}
	for i := range published {
		for j, want := range published[i] {
			got := layer.Payload.Matrix.Cells[i][j].Value.(float64)
			if math.Abs(got-want) > 0.005 {
				t.Errorf("residual[%d][%d] = %.4f, published %.2f", i, j, got, want)
			}
		}
	}
}

// TestStdResidual_Layer pins the layer's structure: the primary MATRIX
// and both parts mirror the host coordinates; the summary reads the
// defined residuals with Baseline 0; the default name; parts are the
// only multi-output slot (no p_adjusted before a correction).
func TestStdResidual_Layer(t *testing.T) {
	host := countHost([][]float64{{10, 20, 30}, {20, 10, 5}})
	layer, _, err := applyStdResidual(stdResidualSpec(), host)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Name != "std_residual" || layer.Kind != types.OverlayKindStdResidual || layer.Scope != types.OverlayScopeCell {
		t.Fatalf("layer identity %q %q %q", layer.Name, layer.Kind, layer.Scope)
	}
	if layer.Payload.Shape != types.OverlayShapeMatrix || layer.Payload.PAdjusted != nil || len(layer.Payload.Parts) != 2 {
		t.Fatalf("payload shape %q, p_adjusted %v, parts %d", layer.Payload.Shape, layer.Payload.PAdjusted, len(layer.Payload.Parts))
	}
	hostMx := host.Payload()
	for name, m := range map[string]*types.MatrixPayload{
		"primary":  layer.Payload.Matrix,
		"expected": layer.Payload.Parts[StdResidualPartExpected].Matrix,
		"p":        layer.Payload.Parts[StdResidualPartP].Matrix,
	} {
		if len(m.RowKeys) != 2 || m.RowKeys[1][0] != hostMx.RowKeys[1][0] || len(m.ColumnKeys) != 3 || m.ColumnKeys[2][0] != hostMx.ColumnKeys[2][0] {
			t.Errorf("%s keys %v / %v do not mirror the host", name, m.RowKeys, m.ColumnKeys)
		}
	}
	for _, name := range []string{StdResidualPartExpected, StdResidualPartP} {
		if p := layer.Payload.Parts[name]; p.Shape != types.OverlayShapeMatrix || len(p.Parts) != 0 {
			t.Errorf("part %s shape %q nested %d", name, p.Shape, len(p.Parts))
		}
	}
	s := layer.Summary
	if s == nil || s.Baseline == nil || *s.Baseline != 0 || s.Count == nil || *s.Count != 6 || s.Min == nil || s.Max == nil || *s.Min >= 0 || *s.Max <= 0 {
		t.Fatalf("summary %+v", s)
	}
	if s.Parameters != nil {
		t.Fatalf("unweighted host stamped parameters %v", s.Parameters)
	}
}

// TestStdResidual_UndefinedCells: a zero row leaves E = 0 on its cells —
// residual and p NaN (null on the wire), expected 0, still present — and
// raises one PULSE_OVERLAY_EXPECTED_LOW counting the zero cells; a
// column holding the whole table leaves the variance term 0 on a cell
// with E > 0, also NaN.
func TestStdResidual_UndefinedCells(t *testing.T) {
	layer, ws, err := applyStdResidual(stdResidualSpec(), countHost([][]float64{{50, 30, 20}, {0, 0, 0}, {40, 60, 90}}))
	if err != nil {
		t.Fatal(err)
	}
	for j := 0; j < 3; j++ {
		z := layer.Payload.Matrix.Cells[1][j].Value.(float64)
		p := layer.Payload.Parts[StdResidualPartP].Matrix.Cells[1][j].Value.(float64)
		e := layer.Payload.Parts[StdResidualPartExpected].Matrix.Cells[1][j].Value.(float64)
		if !math.IsNaN(z) || !math.IsNaN(p) || e != 0 {
			t.Errorf("zero row cell %d: z %v p %v expected %v, want NaN NaN 0", j, z, p, e)
		}
	}
	if *layer.Summary.Count != 6 {
		t.Errorf("summary count %d, want the 6 defined residuals", *layer.Summary.Count)
	}
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_OVERLAY_EXPECTED_LOW) ||
		ws[0].Details["zero_expected_cells"] != 3 || ws[0].Details["low_expected_cells"] != 3 || ws[0].Details["expected_min"] != 0.0 {
		t.Fatalf("warnings %+v, want one EXPECTED_LOW with 3 zero / 3 low cells", ws)
	}

	// Every case in one column: that column's variance term is 0.
	layer, _, err = applyStdResidual(stdResidualSpec(), countHost([][]float64{{7, 0}, {9, 0}}))
	if err != nil {
		t.Fatal(err)
	}
	if z := layer.Payload.Matrix.Cells[0][0].Value.(float64); !math.IsNaN(z) {
		t.Errorf("whole-table column residual %v, want NaN", z)
	}
	if *layer.Summary.Count != 0 || layer.Summary.Min != nil {
		t.Errorf("summary %+v, want no defined residual", layer.Summary)
	}
}

// TestStdResidual_Refusals: a host below 2 × 2 or with a zero grand
// total is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, as
// OVERLAY_CHISQ_MATRIX refuses it.
func TestStdResidual_Refusals(t *testing.T) {
	for name, table := range map[string][][]float64{
		"one row":    {{1, 2, 3}},
		"one column": {{1}, {2}},
		"zero total": {{0, 0}, {0, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := applyStdResidual(stdResidualSpec(), countHost(table))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
				t.Fatalf("err %v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
			}
		})
	}
}

// TestStdResidual_WeightedParameters: the weighted host stamps the
// table's floor — sum_weights under frequency, plus n_eff under
// probability — and the probability residuals differ from the
// frequency ones (the Kish scaling moved them).
func TestStdResidual_WeightedParameters(t *testing.T) {
	ct := tallyCT(ctFixture, 2, 3)
	nEff := ct.table.sw * ct.table.sw / ct.table.sww
	freq, _, err := applyStdResidual(stdResidualSpec(), weightedCountHost(ctFixture, 2, 3, weighting.Frequency))
	if err != nil {
		t.Fatal(err)
	}
	prob, pws, err := applyStdResidual(stdResidualSpec(), weightedCountHost(ctFixture, 2, 3, weighting.Probability))
	if err != nil {
		t.Fatal(err)
	}
	assertParams(t, "frequency", freq.Summary, map[string]float64{"sum_weights": ct.table.sw})
	assertParams(t, "probability", prob.Summary, map[string]float64{"sum_weights": ct.table.sw, "n_eff": nEff})
	f00 := freq.Payload.Matrix.Cells[0][0].Value.(float64)
	p00 := prob.Payload.Matrix.Cells[0][0].Value.(float64)
	if math.Abs(f00-p00) < 1e-6*math.Abs(f00) {
		t.Fatalf("probability residual %v equals frequency %v: the Kish scaling did not run", p00, f00)
	}
	// The scaled table's expected counts fall below 5 (ctFixture's
	// n_eff is far below Σw): the warning reads the SCALED table.
	_, kishMin := pearsonRef(kishTable(ct))
	relClose(t, "probability expected_min (scaled)", lowExpectedMin(t, pws), kishMin, 1e-12)
}

// TestStdResidual_DispatchGates: through the MATRIX dispatcher the kind
// runs (one layer, parts intact past PartsRefusal) and the inferential
// Level / Within gate refuses a non-zero slot — the runtime twin of
// descriptor.ValidateOverlays.
func TestStdResidual_DispatchGates(t *testing.T) {
	host := countHost([][]float64{{10, 20}, {20, 10}})
	layers, _, err := applyOverlays([]types.OverlaySpec{*stdResidualSpec()}, host)
	if err != nil || len(layers) != 1 || len(layers[0].Payload.Parts) != 2 {
		t.Fatalf("dispatch: layers %+v err %v", layers, err)
	}
	for _, edit := range []func(*types.OverlaySpec){
		func(s *types.OverlaySpec) { s.Level = 1 },
		func(s *types.OverlaySpec) { s.Within = 1 },
	} {
		spec := *stdResidualSpec()
		edit(&spec)
		_, _, err := applyOverlays([]types.OverlaySpec{spec}, host)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE {
			t.Fatalf("level %d within %d: err %v, want PULSE_OVERLAY_LEVEL_OUT_OF_RANGE", spec.Level, spec.Within, err)
		}
	}
}
