package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rakeCase is one rake.json case (scripts/reference/gen_rake.R).
type rakeCase struct {
	Name       string             `json:"name"`
	Rows       []string           `json:"rows"`
	Columns    []string           `json:"columns"`
	Counts     [][]float64        `json:"counts"`
	RowTargets map[string]float64 `json:"row_targets"`
	ColTargets map[string]float64 `json:"col_targets"`
	TargetsAs  string             `json:"targets_as"`
	MaxIter    int                `json:"max_iter"`
	Capped     bool               `json:"capped"`
	RowMargins map[string]float64 `json:"row_margins"`
	ColMargins map[string]float64 `json:"col_margins"`
	Fitted     [][]float64        `json:"fitted"`
	Factors    [][]*float64       `json:"factors"`
}

// rakeHost builds a crosstab host over rows × columns whose columns run
// in colOrder (a permutation of the column indices) — targets align by
// key, so the permutation must not matter. counts is in rows × columns
// order; a zero count becomes an ABSENT cell (the crosstab emits no
// cell for a combination with no records).
func rakeHost(rows, cols []string, colOrder []int, counts [][]float64) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"age"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"region"}, Types: []string{"GROUP_CATEGORY"}},
		CellLabel:    "n",
	}
	for _, r := range rows {
		mx.RowKeys = append(mx.RowKeys, types.AxisKey{r})
	}
	for _, j := range colOrder {
		mx.ColumnKeys = append(mx.ColumnKeys, types.AxisKey{cols[j]})
	}
	for i := range rows {
		row := make([]types.MatrixCell, len(colOrder))
		for c, j := range colOrder {
			if counts[i][j] != 0 {
				row[c] = types.MatrixCell{Value: counts[i][j], Present: true}
			}
		}
		mx.Cells = append(mx.Cells, row)
	}
	return NewCrosstabHostView(mx)
}

func rakeSpec(t *testing.T, params map[string]any) *types.OverlaySpec {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return &types.OverlaySpec{Kind: types.OverlayKindRake, Scope: types.OverlayScopeMatrix, Params: raw}
}

func rakeCell(t *testing.T, where string, c types.MatrixCell) float64 {
	t.Helper()
	if !c.Present {
		t.Fatalf("%s: absent cell", where)
	}
	v, ok := c.Value.(float64)
	if !ok {
		t.Fatalf("%s: cell %+v, want a float64", where, c)
	}
	return v
}

// TestRake_MatchesR: the fitted table and factors equal stats::loglin's
// 2-D IPF (cross-checked against survey::rake in the generator) for
// share and count targets, structural zeros and a fit capped at two
// sweeps — with the host columns REVERSED against the targets' order,
// so a positional (not by-key) alignment would fail.
func TestRake_MatchesR(t *testing.T) {
	doc := loadReference[rakeCase](t, "rake")
	if len(doc.Cases) < 5 {
		t.Fatalf("rake.json has %d cases, want at least 5", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			nc := len(c.Columns)
			rev := make([]int, nc)
			for i := range rev {
				rev[i] = nc - 1 - i
			}
			params := map[string]any{
				"row_targets": c.RowTargets, "col_targets": c.ColTargets,
				"targets_as": c.TargetsAs, "max_iter": c.MaxIter, "tolerance": 1e-12,
			}
			layer, ws, err := applyRake(rakeSpec(t, params), rakeHost(c.Rows, c.Columns, rev, c.Counts))
			if err != nil {
				t.Fatal(err)
			}
			fitted, factors := layer.Payload.Matrix, layer.Payload.Parts[RakePartFactors]
			if layer.Payload.Shape != types.OverlayShapeMatrix || fitted == nil || factors == nil || factors.Matrix == nil {
				t.Fatalf("payload %+v", layer.Payload)
			}
			tol := 1e-9
			if c.Capped {
				tol = 1e-12 // the same arithmetic, sweep for sweep
			}
			for i := range c.Rows {
				for pos, j := range rev {
					where := "[" + c.Rows[i] + "][" + c.Columns[j] + "]"
					if fitted.ColumnKeys[pos][0] != c.Columns[j] || fitted.RowKeys[i][0] != c.Rows[i] {
						t.Fatalf("fitted keys %v / %v are not the host's", fitted.RowKeys, fitted.ColumnKeys)
					}
					mkClose(t, "fitted"+where, rakeCell(t, where, fitted.Cells[i][pos]), c.Fitted[i][j], tol)
					got := rakeCell(t, where, factors.Matrix.Cells[i][pos])
					if want := c.Factors[i][j]; want == nil {
						if !math.IsNaN(got) {
							t.Errorf("factor%s = %v, want null (observed 0)", where, got)
						}
						if v := rakeCell(t, where, fitted.Cells[i][pos]); v != 0 {
							t.Errorf("structural zero%s fitted to %v", where, v)
						}
					} else {
						mkClose(t, "factor"+where, got, *want, tol)
					}
				}
			}
			s := layer.Summary.Parameters
			notConverged := warningsWith(ws, errors.PULSE_OVERLAY_RAKE_NOT_CONVERGED, "")
			if c.Capped {
				if s["converged"] != 0 || s["iterations"] != float64(c.MaxIter) || !(s["residual"] > 1e-12) || len(notConverged) != 1 {
					t.Fatalf("capped fit: summary %v, warnings %+v", s, ws)
				}
				d := notConverged[0].Details
				if d["iterations"] != c.MaxIter || d["residual"] != s["residual"] || d["tolerance"] != 1e-12 {
					t.Fatalf("warning details %v", d)
				}
				return
			}
			if s["converged"] != 1 || !(s["residual"] <= 1e-12) || len(ws) != 0 {
				t.Fatalf("summary %v, warnings %+v", s, ws)
			}
		})
	}
}

// TestRake_DefaultsConverge: the default tolerance (1e-8) and max_iter
// (100) converge on the census case, and the fitted margins hit the
// scaled shares.
func TestRake_DefaultsConverge(t *testing.T) {
	rows, cols := []string{"young", "old"}, []string{"n", "s", "e"}
	counts := [][]float64{{40, 22, 31}, {35, 41, 28}}
	spec := rakeSpec(t, map[string]any{
		"row_targets": map[string]float64{"young": 1, "old": 3},
		"col_targets": map[string]float64{"n": 2, "s": 1, "e": 1},
	})
	layer, ws, err := applyRake(spec, rakeHost(rows, cols, []int{0, 1, 2}, counts))
	if err != nil || len(ws) != 0 {
		t.Fatalf("err %v, warnings %+v", err, ws)
	}
	s := layer.Summary.Parameters
	if s["converged"] != 1 || !(s["residual"] <= 1e-8) || s["iterations"] < 1 || s["iterations"] >= 100 {
		t.Fatalf("summary %v", s)
	}
	const grand = 197.0
	cells := layer.Payload.Matrix.Cells
	for i, want := range []float64{grand / 4, grand * 3 / 4} {
		var sum float64
		for j := range cols {
			sum += rakeCell(t, "row", cells[i][j])
		}
		mkClose(t, "row margin "+rows[i], sum, want, 1e-8)
	}
	for j, want := range []float64{grand / 2, grand / 4, grand / 4} {
		var sum float64
		for i := range rows {
			sum += rakeCell(t, "col", cells[i][j])
		}
		mkClose(t, "column margin "+cols[j], sum, want, 1e-8)
	}
}

// TestRake_UnreachableTargetWarnsAndShips: a row with no observed cell
// cannot reach a positive target — the fit stops at max_iter with a
// NOT_CONVERGED warning, still ships its payload (converged 0), and the
// empty row stays zero.
func TestRake_UnreachableTargetWarnsAndShips(t *testing.T) {
	rows, cols := []string{"a", "b", "c"}, []string{"x", "y"}
	counts := [][]float64{{10, 5}, {0, 0}, {4, 9}}
	spec := rakeSpec(t, map[string]any{
		"row_targets": map[string]float64{"a": 1, "b": 1, "c": 1},
		"col_targets": map[string]float64{"x": 1, "y": 1},
		"max_iter":    25,
	})
	layer, ws, err := applyRake(spec, rakeHost(rows, cols, []int{0, 1}, counts))
	if err != nil {
		t.Fatal(err)
	}
	s := layer.Summary.Parameters
	if s["converged"] != 0 || s["iterations"] != 25 || len(warningsWith(ws, errors.PULSE_OVERLAY_RAKE_NOT_CONVERGED, "")) != 1 {
		t.Fatalf("summary %v, warnings %+v", s, ws)
	}
	if r := s["residual"]; !(r > 0) || math.IsInf(r, 0) {
		t.Fatalf("residual %v, want a finite positive gap", r)
	}
	for j := range cols {
		if v := rakeCell(t, "b", layer.Payload.Matrix.Cells[1][j]); v != 0 {
			t.Fatalf("empty row fitted to %v", v)
		}
	}
	if _, err := json.Marshal(layer); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

// TestRake_RuntimeRefusals: every runtime refusal, with its own code
// and the spec index from the MATRIX dispatcher.
func TestRake_RuntimeRefusals(t *testing.T) {
	rows, cols := []string{"a", "b"}, []string{"x", "y"}
	counts := [][]float64{{3, 4}, {5, 6}}
	good := map[string]float64{"a": 1, "b": 1}
	goodCols := map[string]float64{"x": 1, "y": 1}
	for _, c := range []struct {
		name    string
		params  map[string]any
		edit    func(*CrosstabHostView)
		want    errors.Code
		details map[string]any
	}{
		{"host row key without a target", map[string]any{"row_targets": map[string]float64{"a": 1}, "col_targets": goodCols}, nil,
			errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH, map[string]any{"axis": "row", "host_only": []string{"b"}, "target_only": []string{}}},
		{"column target the host lacks", map[string]any{"row_targets": good, "col_targets": map[string]float64{"x": 1, "y": 1, "z": 1}}, nil,
			errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH, map[string]any{"axis": "column", "host_only": []string{}, "target_only": []string{"z"}}},
		{"both sides differ", map[string]any{"row_targets": good, "col_targets": map[string]float64{"x": 1, "w": 1}}, nil,
			errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH, map[string]any{"axis": "column", "host_only": []string{"y"}, "target_only": []string{"w"}}},
		{"counts totals differ", map[string]any{"row_targets": map[string]float64{"a": 10, "b": 10}, "col_targets": map[string]float64{"x": 10, "y": 11}, "targets_as": "counts"}, nil,
			errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH, map[string]any{"reason": "totals_differ"}},
		{"average cell", map[string]any{"row_targets": good, "col_targets": goodCols}, func(h *CrosstabHostView) { h.cellType = types.AGG_AVERAGE },
			errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, nil},
		{"negative cell", map[string]any{"row_targets": good, "col_targets": goodCols}, func(h *CrosstabHostView) { h.payload.Cells[0][1].Value = -1.0 },
			errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED, nil},
		{"zero total", map[string]any{"row_targets": good, "col_targets": goodCols}, func(h *CrosstabHostView) {
			for i := range h.payload.Cells {
				for j := range h.payload.Cells[i] {
					h.payload.Cells[i][j] = types.MatrixCell{}
				}
			}
		}, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, nil},
		{"missing targets", map[string]any{"col_targets": goodCols}, nil, errors.PULSE_OVERLAY_PARAM_MISSING, map[string]any{"param": "row_targets"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			host := rakeHost(rows, cols, []int{0, 1}, counts)
			if c.edit != nil {
				c.edit(host)
			}
			_, _, err := applyRakeAt(rakeSpec(t, c.params), host, 3)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.want {
				t.Fatalf("err %v, want %s", err, c.want)
			}
			if c.want != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || c.edit == nil || c.name == "average cell" {
				if c.want != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE && ce.Details["index"] != 3 {
					t.Fatalf("details %v lack the spec index", ce.Details)
				}
			}
			for k, want := range c.details {
				got, _ := json.Marshal(ce.Details[k])
				w, _ := json.Marshal(want)
				if string(got) != string(w) {
					t.Errorf("details[%s] = %s, want %s", k, got, w)
				}
			}
		})
	}
}

// TestRake_DoesNotMutateHost: the host payload is byte-identical after
// the overlay ran.
func TestRake_DoesNotMutateHost(t *testing.T) {
	rows, cols := []string{"a", "b", "c"}, []string{"x", "y"}
	host := rakeHost(rows, cols, []int{1, 0}, [][]float64{{10, 0}, {3, 7}, {8, 2}})
	before, _ := json.Marshal(host.Payload())
	spec := rakeSpec(t, map[string]any{
		"row_targets": map[string]float64{"a": 0.2, "b": 0.5, "c": 0.3},
		"col_targets": map[string]float64{"x": 0.4, "y": 0.6},
	})
	if _, _, err := applyRake(spec, host); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(host.Payload())
	if string(before) != string(after) {
		t.Fatalf("host payload moved:\n%s\n%s", before, after)
	}
}

// TestRake_ZeroTargetEmptiesTheRow: a zero target zeroes its row (and
// its factors) exactly, and the fit still converges.
func TestRake_ZeroTargetEmptiesTheRow(t *testing.T) {
	rows, cols := []string{"a", "b"}, []string{"x", "y"}
	spec := rakeSpec(t, map[string]any{
		"row_targets": map[string]float64{"a": 0, "b": 1},
		"col_targets": map[string]float64{"x": 1, "y": 1},
	})
	layer, ws, err := applyRake(spec, rakeHost(rows, cols, []int{0, 1}, [][]float64{{3, 4}, {5, 6}}))
	if err != nil || len(ws) != 0 || layer.Summary.Parameters["converged"] != 1 {
		t.Fatalf("err %v, warnings %+v, summary %v", err, ws, layer.Summary.Parameters)
	}
	for j := range cols {
		if v := rakeCell(t, "a"+strconv.Itoa(j), layer.Payload.Matrix.Cells[0][j]); v != 0 {
			t.Fatalf("zero-target row fitted to %v", v)
		}
		if f := rakeCell(t, "factor", layer.Payload.Parts[RakePartFactors].Matrix.Cells[0][j]); f != 0 {
			t.Fatalf("zero-target factor %v, want 0", f)
		}
	}
}
