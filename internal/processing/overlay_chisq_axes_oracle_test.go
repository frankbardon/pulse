package processing

import (
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// The unweighted chi-square crosstab overlays against R (U64 E2-S2,
// #617): OVERLAY_CHISQ_MATRIX against chisq.test(table, correct =
// FALSE), and OVERLAY_CHISQ_ROW / OVERLAY_CHISQ_COL against each row's
// (column's) goodness of fit to the column (row) margin's distribution,
// chisq.test(table[i, ], p = colSums / N). The golden is written by
// scripts/reference/gen_overlay_chisq.R (`make reference`); the host is
// an AGG_COUNT crosstab payload of the case's table, and the three kinds
// run through applyOverlays, the dispatch a crosstab request's
// overlays[] take. Relative 1e-10 (oracleRelTol).

type chiSqAxisFigure struct {
	Statistic   float64 `json:"statistic"`
	DF          float64 `json:"df"`
	PValue      float64 `json:"p_value"`
	ExpectedMin float64 `json:"expected_min"`
}

// countHostPayload is an AGG_COUNT crosstab payload over table, rows
// r1.. and columns c1.., with its margins and grand total.
func countHostPayload(table [][]float64) *types.MatrixPayload {
	p := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		CellLabel:    "AGG_COUNT_id",
	}
	cols := len(table[0])
	colTotals := make([]float64, cols)
	var grand float64
	for j := range cols {
		p.ColumnKeys = append(p.ColumnKeys, types.AxisKey{"c" + strconv.Itoa(j+1)})
	}
	for i, row := range table {
		p.RowKeys = append(p.RowKeys, types.AxisKey{"r" + strconv.Itoa(i+1)})
		cells := make([]types.MatrixCell, cols)
		var rowTotal float64
		for j, v := range row {
			cells[j] = types.MatrixCell{Value: v, Present: true}
			rowTotal += v
			colTotals[j] += v
		}
		grand += rowTotal
		p.Cells = append(p.Cells, cells)
		p.RowMargins = append(p.RowMargins, types.MatrixCell{Value: rowTotal, Present: true})
	}
	for _, v := range colTotals {
		p.ColumnMargins = append(p.ColumnMargins, types.MatrixCell{Value: v, Present: true})
	}
	p.GrandTotal = types.MatrixCell{Value: grand, Present: true}
	return p
}

// TestReferenceOracle_OverlayChiSqAxes: statistic, df and p of every
// per-row and per-column entry and of the whole-matrix test, the matrix
// scalar payload, and the PULSE_OVERLAY_EXPECTED_LOW warnings exactly
// where R's smallest expected count is below 5.
func TestReferenceOracle_OverlayChiSqAxes(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name   string            `json:"name"`
		Table  [][]float64       `json:"table"`
		Matrix chiSqAxisFigure   `json:"matrix"`
		Rows   []chiSqAxisFigure `json:"rows"`
		Cols   []chiSqAxisFigure `json:"cols"`
	}](t, "overlay_chisq_axes")
	c := overlayOracleCheck(t, "OVERLAY_CHISQ_ROW / _COL / _MATRIX")
	for _, k := range doc.Cases {
		specs := []types.OverlaySpec{
			{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow},
			{Kind: types.OverlayKindChiSqCol, Scope: types.OverlayScopeColumn},
			{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
		}
		layers, warnings, err := applyOverlays(specs, NewCrosstabHostView(countHostPayload(k.Table)))
		if err != nil {
			t.Fatalf("%s: applyOverlays: %v", k.Name, err)
		}
		if len(layers) != 3 {
			t.Fatalf("%s: %d layers, want 3", k.Name, len(layers))
		}
		for li, axis := range []struct {
			kind string
			want []chiSqAxisFigure
		}{{"row", k.Rows}, {"col", k.Cols}} {
			series := layers[li].Payload.Series
			if series == nil || len(series.Entries) != len(axis.want) {
				t.Fatalf("%s %s: series %+v, want %d entries", k.Name, axis.kind, series, len(axis.want))
			}
			for i, w := range axis.want {
				s := series.Entries[i].Summary
				if s.Statistic == nil || s.PValue == nil {
					t.Fatalf("%s %s[%d]: summary %+v lacks the statistic / p", k.Name, axis.kind, i, s)
				}
				c.check(*s.Statistic, w.Statistic, "%s %s[%d] statistic", k.Name, axis.kind, i)
				c.check(*s.PValue, w.PValue, "%s %s[%d] p", k.Name, axis.kind, i)
				if s.Parameters["df"] != w.DF {
					t.Errorf("%s %s[%d]: df = %v, R %v", k.Name, axis.kind, i, s.Parameters["df"], w.DF)
				}
			}
		}
		m := layers[2]
		if m.Summary == nil || m.Summary.Statistic == nil || m.Summary.PValue == nil || m.Payload.Scalar == nil {
			t.Fatalf("%s matrix: layer %+v lacks the statistic / p", k.Name, m)
		}
		c.check(*m.Summary.Statistic, k.Matrix.Statistic, "%s matrix statistic", k.Name)
		c.check(*m.Payload.Scalar, k.Matrix.Statistic, "%s matrix scalar", k.Name)
		c.check(*m.Summary.PValue, k.Matrix.PValue, "%s matrix p", k.Name)
		if m.Summary.Parameters["df"] != k.Matrix.DF {
			t.Errorf("%s matrix: df = %v, R %v", k.Name, m.Summary.Parameters["df"], k.Matrix.DF)
		}

		// One PULSE_OVERLAY_EXPECTED_LOW per row / column whose smallest
		// expected count is below 5, and one for the matrix.
		low := map[string]bool{}
		for _, w := range warnings {
			if w.Code != "PULSE_OVERLAY_EXPECTED_LOW" {
				t.Errorf("%s: unexpected warning %s", k.Name, w.Code)
				continue
			}
			switch {
			case w.Details["row_index"] != nil:
				low["row"+strconv.Itoa(w.Details["row_index"].(int))] = true
			case w.Details["col_index"] != nil:
				low["col"+strconv.Itoa(w.Details["col_index"].(int))] = true
			default:
				low["matrix"] = true
			}
		}
		want := map[string]bool{}
		for i, r := range k.Rows {
			if r.ExpectedMin < 5 {
				want["row"+strconv.Itoa(i)] = true
			}
		}
		for j, col := range k.Cols {
			if col.ExpectedMin < 5 {
				want["col"+strconv.Itoa(j)] = true
			}
		}
		if k.Matrix.ExpectedMin < 5 {
			want["matrix"] = true
		}
		if len(low) != len(want) {
			t.Errorf("%s: low-expected warnings %v, R %v", k.Name, low, want)
		}
		for key := range want {
			if !low[key] {
				t.Errorf("%s: no low-expected warning for %s (R expected_min < 5)", k.Name, key)
			}
		}
	}
	c.done(len(doc.Cases))
}
