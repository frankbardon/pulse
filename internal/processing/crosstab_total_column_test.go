package processing

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A crosstab whose Columns is absent (or empty) has ONE implicit column,
// keyed types.CrosstabTotalColumnKey ("Total"), holding every record whose
// row key resolved (U47 #326 / #328). There is no constant field behind
// it: the column axis has no grouper, so every record resolves it, and the
// cell-admission rule therefore reduces to "the row key resolved".

// totalColumnSpec is a no-column crosstab over the fan-out fixture.
func totalColumnSpec(rows []*types.Group, cell *types.Aggregation, mode types.CrosstabNormalize) *types.CrosstabSpec {
	return &types.CrosstabSpec{
		Rows:      rows,
		Cell:      cell,
		Shape:     types.CrosstabShapeMatrix,
		Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		Normalize: mode,
	}
}

func totalSum() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"}
}

func totalCount() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"}
}

// totalColumnCases is the parity corpus: every normalize mode, a fanning
// row axis with null / empty sets, the partial-depth and cross-axis
// denominators (the cross-axis one with NO column prefix to fix), long
// shape, and a pairwise overlay pairing rows within the single column.
func totalColumnCases() map[string]*types.Request {
	zero := 0
	cases := map[string]*types.Request{}
	for _, mode := range []types.CrosstabNormalize{
		types.CrosstabNormalizeNone, types.CrosstabNormalizeRow,
		types.CrosstabNormalizeColumn, types.CrosstabNormalizeTotal,
	} {
		cases["category rows/"+string(mode)] = &types.Request{Crosstab: totalColumnSpec(
			[]*types.Group{catGroup("region")}, totalSum(), mode)}
		cases["fanning rows/"+string(mode)] = &types.Request{Crosstab: totalColumnSpec(
			[]*types.Group{setGroup("tags")}, totalCount(), mode)}
	}
	level := totalColumnSpec([]*types.Group{catGroup("region"), setGroup("tags")}, totalSum(), types.CrosstabNormalizeRow)
	level.NormalizeLevel = &zero
	cases["normalize_level row prefix"] = &types.Request{Crosstab: level}

	within := totalColumnSpec([]*types.Group{catGroup("region"), setGroup("tags")}, totalSum(), types.CrosstabNormalizeColumn)
	within.NormalizeWithin = &zero
	cases["normalize_within column mode"] = &types.Request{Crosstab: within}

	long := totalColumnSpec([]*types.Group{setGroup("tags")}, totalSum(), types.CrosstabNormalizeNone)
	long.Shape = types.CrosstabShapeLong
	cases["long shape margins"] = &types.Request{Crosstab: long}

	empty := totalColumnSpec([]*types.Group{catGroup("region")}, totalSum(), types.CrosstabNormalizeColumn)
	empty.Columns = []*types.Group{}
	cases["explicit empty columns"] = &types.Request{Crosstab: empty}

	cases["pairwise rows within the Total column"] = &types.Request{
		Crosstab: totalColumnSpec([]*types.Group{setGroup("tags")}, totalCount(), types.CrosstabNormalizeColumn),
		Overlays: []types.OverlaySpec{{
			Name:  "pw",
			Kind:  types.OverlayKindPairwisePropZ,
			Scope: types.OverlayScopeRow,
		}},
	}
	return cases
}

// TestCrosstab_TotalColumnFusedMatchesBuffered drives every no-column
// shape down both arms and diffs the whole wire form.
func TestCrosstab_TotalColumnFusedMatchesBuffered(t *testing.T) {
	for name, req := range totalColumnCases() {
		t.Run(name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := fanoutRecords(schema)
			assertFusedBufferedParity(t, schema, req, recs)
			// Parity alone passes when both arms drop every cell: pin that
			// the single column carries figures.
			resp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
			if err != nil {
				t.Fatal(err)
			}
			if !totalColumnHasFigure(resp) {
				t.Errorf("no present cell in the Total column: %s %s", jsonOf(t, resp.Crosstab), jsonOf(t, resp.Data))
			}
		})
	}
	t.Run("empty cohort", func(t *testing.T) {
		schema := fanoutCrosstabSchema(t)
		req := &types.Request{Crosstab: totalColumnSpec([]*types.Group{catGroup("region")}, totalSum(), types.CrosstabNormalizeNone)}
		assertFusedBufferedParity(t, schema, req, nil)
	})
}

// TestCrosstab_TotalColumnShape pins the implicit column on the wire, on
// both arms: one column keyed "Total" under a TOTAL header, the cell
// equal to the row's own aggregate over the records whose row key
// resolved, column % summing to 1 down the column, and the row margin
// equal to the single cell beside it.
func TestCrosstab_TotalColumnShape(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	recs := fanoutRecords(schema)
	arms := []struct {
		name string
		run  func(*types.Request) (*types.Response, error)
	}{
		{"buffered", func(r *types.Request) (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, r, recs, false)
		}},
		{"fused", func(r *types.Request) (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, r, recs, false)
		}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			raw, err := arm.run(&types.Request{Crosstab: totalColumnSpec(
				[]*types.Group{catGroup("region")}, totalSum(), types.CrosstabNormalizeNone)})
			if err != nil {
				t.Fatal(err)
			}
			m := raw.Crosstab.Matrix
			wantKeys := []types.AxisKey{{types.CrosstabTotalColumnKey}}
			if !reflect.DeepEqual(m.ColumnKeys, wantKeys) {
				t.Fatalf("ColumnKeys = %v, want %v", m.ColumnKeys, wantKeys)
			}
			wantHeader := types.AxisHeader{
				Fields: []string{types.CrosstabTotalColumnField},
				Types:  []string{types.CrosstabTotalColumnType},
			}
			if !reflect.DeepEqual(m.ColumnHeader, wantHeader) {
				t.Fatalf("ColumnHeader = %+v, want %+v", m.ColumnHeader, wantHeader)
			}
			// region north: 10 + 20 + 7 = 37; south: 30 + 40 + 50 + 60 = 180
			// (the null value contributes nothing to the sum).
			want := map[string]float64{"north": 37, "south": 180}
			for r, key := range m.RowKeys {
				got := m.Cells[r][0].Scalar()
				if got != want[key[0].(string)] {
					t.Errorf("cell[%v][Total] = %v, want %v", key, got, want[key[0].(string)])
				}
				if m.RowMargins[r].Scalar() != got {
					t.Errorf("row margin %v = %v, want the Total cell %v", key, m.RowMargins[r].Scalar(), got)
				}
			}
			if got := m.ColumnMargins[0].Scalar(); got != 217 {
				t.Errorf("Total column margin = %v, want 217", got)
			}
			if got := m.GrandTotal.Scalar(); got != 217 {
				t.Errorf("grand total = %v, want 217", got)
			}

			// Column %: the Total column sums to 1 (a single-response row
			// axis — a fanning one counts a multi-select record once per
			// selection, so its column % legitimately exceeds 1).
			norm, err := arm.run(&types.Request{Crosstab: totalColumnSpec(
				[]*types.Group{catGroup("region")}, totalCount(), types.CrosstabNormalizeColumn)})
			if err != nil {
				t.Fatal(err)
			}
			sum := 0.0
			for _, row := range norm.Crosstab.Matrix.Cells {
				if len(row) != 1 {
					t.Fatalf("row has %d columns, want 1", len(row))
				}
				sum += row[0].Scalar()
			}
			if math.Abs(sum-1) > 1e-12 {
				t.Errorf("column %% over the Total column sums to %v, want 1", sum)
			}
		})
	}
}

// TestCrosstab_TotalColumnPairwiseAlongRows pins that a row-scope
// pairwise overlay pairs the row keys within the single Total column:
// one p per row pair, the column axis echoing "Total".
func TestCrosstab_TotalColumnPairwiseAlongRows(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	req := totalColumnCases()["pairwise rows within the Total column"]
	resp, err := runBufferedCrosstabWithComponents(t, schema, req, fanoutRecords(schema), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
		t.Fatalf("overlays = %s, want one matrix layer", jsonOf(t, resp.Overlays))
	}
	pm := resp.Overlays[0].Payload.Matrix
	rows := len(resp.Crosstab.Matrix.RowKeys)
	if want := rows * (rows - 1) / 2; len(pm.RowKeys) != want {
		t.Errorf("pair rows = %d, want %d (every row pair)", len(pm.RowKeys), want)
	}
	if !reflect.DeepEqual(pm.ColumnKeys, []types.AxisKey{{types.CrosstabTotalColumnKey}}) {
		t.Errorf("pair matrix column keys = %v, want [[Total]]", pm.ColumnKeys)
	}
	present := 0
	for _, row := range pm.Cells {
		if row[0].Present {
			present++
		}
	}
	if present == 0 {
		t.Errorf("no pair p-value present: %s", jsonOf(t, pm))
	}
}

// TestCrosstab_TotalColumnNormalizeRefusals pins the two column-axis
// depth selectors on a crosstab with no column axis: refused out of range
// (axis_len 0) rather than read as a depth into nothing.
func TestCrosstab_TotalColumnNormalizeRefusals(t *testing.T) {
	zero := 0
	schema := fanoutCrosstabSchema(t)
	level := totalColumnSpec([]*types.Group{catGroup("region")}, totalSum(), types.CrosstabNormalizeColumn)
	level.NormalizeLevel = &zero
	within := totalColumnSpec([]*types.Group{catGroup("region")}, totalSum(), types.CrosstabNormalizeRow)
	within.NormalizeWithin = &zero
	for name, tc := range map[string]struct {
		spec *types.CrosstabSpec
		code errors.Code
	}{
		"normalize_level on the absent column axis":  {level, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE},
		"normalize_within on the absent column axis": {within, errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runBufferedCrosstabWithComponents(t, schema, &types.Request{Crosstab: tc.spec}, fanoutRecords(schema), false)
			if !errors.HasCode(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// totalColumnHasFigure reports whether a response carries at least one
// present cell (matrix) or one cell row (long shape).
func totalColumnHasFigure(resp *types.Response) bool {
	if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
		for _, row := range resp.Crosstab.Matrix.Cells {
			for _, c := range row {
				if c.Present {
					return true
				}
			}
		}
		return false
	}
	for _, row := range resp.Data {
		if _, margin := row["_margin"]; !margin {
			return true
		}
	}
	return false
}

// TestCrosstab_TotalColumnNormalizeWithinOracle pins the cross-axis
// denominator with no column prefix to fix: normalize=column within the
// region row prefix divides each cell by its region's total over the one
// column — exactly normalize=row at normalize_level 0, which reaches the
// same denominator through the partial-depth path. Both arms.
func TestCrosstab_TotalColumnNormalizeWithinOracle(t *testing.T) {
	zero := 0
	schema := fanoutCrosstabSchema(t)
	recs := fanoutRecords(schema)
	rows := func() []*types.Group { return []*types.Group{catGroup("region"), setGroup("tags")} }
	within := totalColumnSpec(rows(), totalSum(), types.CrosstabNormalizeColumn)
	within.NormalizeWithin = &zero
	level := totalColumnSpec(rows(), totalSum(), types.CrosstabNormalizeRow)
	level.NormalizeLevel = &zero
	for _, arm := range []struct {
		name string
		run  func(*types.Request) (*types.Response, error)
	}{
		{"buffered", func(r *types.Request) (*types.Response, error) {
			return runBufferedCrosstabWithComponents(t, schema, r, recs, false)
		}},
		{"fused", func(r *types.Request) (*types.Response, error) {
			return runFusedCrosstabViaRunner(t, schema, r, recs, false)
		}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			w, err := arm.run(&types.Request{Crosstab: within})
			if err != nil {
				t.Fatal(err)
			}
			l, err := arm.run(&types.Request{Crosstab: level})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := jsonOf(t, w.Crosstab.Matrix.Cells), jsonOf(t, l.Crosstab.Matrix.Cells); got != want {
				t.Errorf("within cells = %s\nwant (row level 0) %s", got, want)
			}
		})
	}
}
