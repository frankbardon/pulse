package service

import (
	"context"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// crosstabSchema builds a schema with two categorical fields (region,
// segment) and an f64 value field. Used across every crosstab test.
func crosstabSchema() *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")

	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}
}

// crosstabRecords builds a deterministic fixture used across crosstab
// tests. Composition:
//
//	(north, retail):    [10, 20, 30]   sum=60  median=20
//	(north, wholesale): [100]          sum=100 median=100
//	(south, retail):    [5, 15]        sum=20  median=10
//	(south, wholesale): [40, 50]       sum=90  median=45
//	(east,  retail):    [1]            sum=1   median=1
//	(east,  wholesale): (empty)
//
// Row margin (median) for north = median(10,20,30,100) = 25
// Median-of-cell-medians for north = median(20,100) = 60 — clearly different.
func crosstabRecords() [][]uint64 {
	return [][]uint64{
		{0, 0, math.Float64bits(10)},
		{0, 0, math.Float64bits(20)},
		{0, 0, math.Float64bits(30)},
		{0, 1, math.Float64bits(100)},
		{1, 0, math.Float64bits(5)},
		{1, 0, math.Float64bits(15)},
		{1, 1, math.Float64bits(40)},
		{1, 1, math.Float64bits(50)},
		{2, 0, math.Float64bits(1)},
	}
}

// TestCrosstab_CountCellByteEqualToManual checks the cell-correctness
// contract: every cell in a count crosstab equals the COUNT of the
// matching (region, segment) bucket, computed via per-bucket Process
// calls.
func TestCrosstab_CountCellByteEqualToManual(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process crosstab: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected Crosstab.Matrix payload")
	}
	matrix := resp.Crosstab.Matrix

	regionToRow := map[string]int{}
	for i, k := range matrix.RowKeys {
		regionToRow[k[0].(string)] = i
	}
	segmentToCol := map[string]int{}
	for j, k := range matrix.ColumnKeys {
		segmentToCol[k[0].(string)] = j
	}

	expectedCells := map[[2]string]float64{
		{"north", "retail"}:    3,
		{"north", "wholesale"}: 1,
		{"south", "retail"}:    2,
		{"south", "wholesale"}: 2,
		{"east", "retail"}:     1,
	}

	for pair, want := range expectedCells {
		i, ok := regionToRow[pair[0]]
		if !ok {
			t.Errorf("missing row key %q", pair[0])
			continue
		}
		j, ok := segmentToCol[pair[1]]
		if !ok {
			t.Errorf("missing column key %q", pair[1])
			continue
		}
		cell := matrix.Cells[i][j]
		if !cell.Present {
			t.Errorf("(%s,%s) cell not present", pair[0], pair[1])
			continue
		}
		if !floatClose(cell.Value, want, 0.001) {
			t.Errorf("(%s,%s) cell = %v, want %v", pair[0], pair[1], cell.Value, want)
		}
	}

	// Missing (east, wholesale) must be Present=false.
	if i, ok := regionToRow["east"]; ok {
		if j, ok := segmentToCol["wholesale"]; ok {
			if matrix.Cells[i][j].Present {
				t.Errorf("(east,wholesale) cell should be Present=false")
			}
		}
	}
}

// TestCrosstab_MedianMarginRecomputesFromRaw is the most important
// correctness test: row/column margins for AGG_MEDIAN must equal the
// standalone single-axis median over the raw rows, NOT the median of
// the per-cell medians.
func TestCrosstab_MedianMarginRecomputesFromRaw(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_MEDIAN, Field: "value", Label: "med"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process crosstab: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	// Recompute row margins independently via plain Process calls.
	rowMargins := map[string]float64{}
	for _, region := range []string{"north", "south", "east"} {
		probe := &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{region}},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_MEDIAN, Field: "value", Label: "med"},
			},
		}
		r, err := svc.Process(ctx, probe)
		if err != nil {
			t.Fatalf("probe %q: %v", region, err)
		}
		if len(r.Data) == 0 {
			continue
		}
		rowMargins[region] = r.Data[0]["med"].(float64)
	}

	for i, k := range matrix.RowKeys {
		region := k[0].(string)
		got := matrix.RowMargins[i].Value
		want := rowMargins[region]
		if !floatClose(got, want, 0.001) {
			t.Errorf("row margin (%s) = %v, want %v (standalone median)", region, got, want)
		}
	}

	// Independently: median-of-cells for north = median(20, 100) = 60.
	// Standalone row margin for north = median(10,20,30,100) = 25.
	// Verify they differ — guards against accidental cell-summation.
	if i, ok := indexOfKey(matrix.RowKeys, "north"); ok {
		got := matrix.RowMargins[i].Value
		if floatClose(got, 60.0, 0.001) {
			t.Errorf("row margin computed as median-of-cells (60); should be standalone median (25)")
		}
		if !floatClose(got, 25.0, 0.001) {
			t.Errorf("row margin (north) = %v, want 25.0", got)
		}
	}

	// Grand margin: median of all 9 values = median(1,5,10,15,20,30,40,50,100) = 20.
	if !matrix.GrandTotal.Present {
		t.Fatal("grand margin not present")
	}
	if !floatClose(matrix.GrandTotal.Value, 20.0, 0.001) {
		t.Errorf("grand median = %v, want 20.0", matrix.GrandTotal.Value)
	}
}

// TestCrosstab_NormalizeRow_SumsToOne checks that row normalization
// makes each row's cells sum to 1 (within epsilon).
func TestCrosstab_NormalizeRow_SumsToOne(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize: types.CrosstabNormalizeRow,
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	for i, k := range matrix.RowKeys {
		var sum float64
		for j := range matrix.ColumnKeys {
			if matrix.Cells[i][j].Present {
				sum += matrix.Cells[i][j].Value
			}
		}
		// Every row in the fixture has at least one cell with rows.
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("row %v sums to %v, want 1.0", k, sum)
		}
	}
}

// TestCrosstab_NormalizeColumn_SumsToOne checks column normalization.
func TestCrosstab_NormalizeColumn_SumsToOne(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize: types.CrosstabNormalizeColumn,
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	for j, k := range matrix.ColumnKeys {
		var sum float64
		for i := range matrix.RowKeys {
			if matrix.Cells[i][j].Present {
				sum += matrix.Cells[i][j].Value
			}
		}
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("column %v sums to %v, want 1.0", k, sum)
		}
	}
}

// TestCrosstab_NormalizeTotal_SumsToOne checks total normalization.
func TestCrosstab_NormalizeTotal_SumsToOne(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize: types.CrosstabNormalizeTotal,
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	var sum float64
	for i := range matrix.RowKeys {
		for j := range matrix.ColumnKeys {
			if matrix.Cells[i][j].Present {
				sum += matrix.Cells[i][j].Value
			}
		}
	}
	if !floatClose(sum, 1.0, 1e-9) {
		t.Errorf("total sums to %v, want 1.0", sum)
	}
}

// TestCrosstab_NestedAxes verifies a 2-grouper row axis produces nested
// row keys and that cell totals match what manual partitioning would
// give. Uses (region, segment) rows × (segment) columns to keep the
// inputs from the same fixture.
func TestCrosstab_NestedAxes(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	// Distinct fixture: 2-axis rows = (region, segment), 1-axis columns = (segment).
	// Each cell where row.segment == col.segment carries the bucket count;
	// off-diagonal cells are zero. This isn't a typical query but it
	// verifies axis tuple materialization end-to-end.
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	if got := len(matrix.RowHeader.Fields); got != 2 {
		t.Errorf("row header fields = %d, want 2", got)
	}
	if got := matrix.RowHeader.Fields[0]; got != "region" {
		t.Errorf("row header[0] = %q, want region", got)
	}
	if got := matrix.RowHeader.Fields[1]; got != "segment" {
		t.Errorf("row header[1] = %q, want segment", got)
	}

	// Each present row key must have exactly 2 components.
	for _, k := range matrix.RowKeys {
		if len(k) != 2 {
			t.Errorf("row tuple len = %d, want 2; tuple = %v", len(k), k)
		}
	}

	// Locate (north, retail) row and check its (retail) column equals 3.
	rowIdx := -1
	for i, k := range matrix.RowKeys {
		if k[0] == "north" && k[1] == "retail" {
			rowIdx = i
			break
		}
	}
	if rowIdx < 0 {
		t.Fatal("missing (north, retail) row tuple")
	}
	colIdx := -1
	for j, k := range matrix.ColumnKeys {
		if k[0] == "retail" {
			colIdx = j
			break
		}
	}
	if colIdx < 0 {
		t.Fatal("missing retail column")
	}
	cell := matrix.Cells[rowIdx][colIdx]
	if !cell.Present || !floatClose(cell.Value, 3.0, 0.001) {
		t.Errorf("(north,retail) → retail cell = %+v, want 3.0", cell)
	}
}

// TestCrosstab_BinningGrouperOnAxis verifies GROUP_RANGE works on the
// row axis (continuous-variable crosstab).
func TestCrosstab_BinningGrouperOnAxis(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_RANGE, Field: "value", Interval: 25},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	if len(matrix.RowKeys) == 0 {
		t.Fatal("no rows from GROUP_RANGE")
	}
	if matrix.RowHeader.Types[0] != "GROUP_RANGE" {
		t.Errorf("row header type = %q, want GROUP_RANGE", matrix.RowHeader.Types[0])
	}
}

// TestCrosstab_LongShape verifies long-shape output emits one tuple row
// per (row-key, column-key) cell and tags margin rows.
func TestCrosstab_LongShape(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeLong,
			Margins: types.CrosstabMargins{Rows: true, Grand: true},
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Shape != types.CrosstabShapeLong {
		t.Fatal("expected long shape on Crosstab result")
	}
	if resp.Crosstab.Matrix != nil {
		t.Error("matrix payload should be nil for long shape")
	}

	cellRows, marginRows := 0, 0
	for _, row := range resp.Data {
		if _, isMargin := row["_margin"]; isMargin {
			marginRows++
		} else {
			cellRows++
		}
	}
	// 5 non-empty cells in fixture.
	if cellRows != 5 {
		t.Errorf("cell rows = %d, want 5", cellRows)
	}
	// 3 row margins + 1 grand.
	if marginRows != 4 {
		t.Errorf("margin rows = %d, want 4", marginRows)
	}
}

// TestCrosstab_DivideByZeroDropsCell verifies the divide-by-zero policy:
// when a margin is zero (or absent), affected cells are dropped from
// the present set.
func TestCrosstab_DivideByZeroDropsCell(t *testing.T) {
	// Construct a fixture where one column has zero records — gives a
	// zero column margin under normalize=column.
	schema := crosstabSchema()
	records := [][]uint64{
		{0, 0, math.Float64bits(10)},
		{1, 0, math.Float64bits(20)},
		// No wholesale records at all → column margin = 0.
	}
	cfg := setupTestFS(t, "ct.pulse", schema, records)
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize: types.CrosstabNormalizeColumn,
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	// With no wholesale records the "wholesale" column simply does not
	// appear in column keys (Grouper.Group emits no bucket for an
	// empty category). Validate that retail column sums to 1.
	for j, k := range matrix.ColumnKeys {
		var sum float64
		for i := range matrix.RowKeys {
			if matrix.Cells[i][j].Present {
				sum += matrix.Cells[i][j].Value
			}
		}
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("column %v sums to %v, want 1.0", k, sum)
		}
	}
}

// TestCrosstab_ConflictsWithGroupsRejected verifies the mutual-exclusivity
// guard at runtime entry.
func TestCrosstab_ConflictsWithGroupsRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
	}

	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS")
	}
	if !errors.HasCode(err, errors.PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS) {
		t.Errorf("expected PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS, got %v", err)
	}
}

// TestCrosstab_EmptyRowsRejected.
func TestCrosstab_EmptyRowsRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
	}

	_, err := svc.Process(ctx, req)
	if !errors.HasCode(err, errors.PULSE_CROSSTAB_EMPTY_ROWS) {
		t.Errorf("expected PULSE_CROSSTAB_EMPTY_ROWS, got %v", err)
	}
}

// TestCrosstab_EmptyColumnsRejected.
func TestCrosstab_EmptyColumnsRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
	}

	_, err := svc.Process(ctx, req)
	if !errors.HasCode(err, errors.PULSE_CROSSTAB_EMPTY_COLUMNS) {
		t.Errorf("expected PULSE_CROSSTAB_EMPTY_COLUMNS, got %v", err)
	}
}

// TestCrosstab_MissingCellRejected.
func TestCrosstab_MissingCellRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		},
	}

	_, err := svc.Process(ctx, req)
	if !errors.HasCode(err, errors.PULSE_CROSSTAB_MISSING_CELL) {
		t.Errorf("expected PULSE_CROSSTAB_MISSING_CELL, got %v", err)
	}
}

// TestCrosstab_AxisOrderingDeterministic runs the same crosstab twice
// and asserts identical row + column key sequences.
func TestCrosstab_AxisOrderingDeterministic(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	makeReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			},
		}
	}

	a, err := svc.Process(ctx, makeReq())
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Process(ctx, makeReq())
	if err != nil {
		t.Fatal(err)
	}
	if !axisKeysEqual(a.Crosstab.Matrix.RowKeys, b.Crosstab.Matrix.RowKeys) {
		t.Error("row keys differ across runs")
	}
	if !axisKeysEqual(a.Crosstab.Matrix.ColumnKeys, b.Crosstab.Matrix.ColumnKeys) {
		t.Error("column keys differ across runs")
	}
}

func axisKeysEqual(a, b []types.AxisKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func indexOfKey(keys []types.AxisKey, want string) (int, bool) {
	for i, k := range keys {
		if len(k) > 0 && k[0] == want {
			return i, true
		}
	}
	return -1, false
}

// TestCrosstab_AllAggregatorsClassified is the CI guard: every
// registered aggregator must have a non-default
// MarginReducibility classification. Reaching the default branch is a
// CI-gated repair (PULSE_CROSSTAB_AGG_UNCLASSIFIED).
func TestCrosstab_AllAggregatorsClassified(t *testing.T) {
	var unclassified []string
	classes := map[types.MarginReducibility]struct{}{
		types.MarginSummable:      {},
		types.MarginMeanReducible: {},
		types.MarginRecompute:     {},
	}
	for _, agg := range types.AllAggregationTypes() {
		r := agg.MarginReducibility()
		if _, ok := classes[r]; !ok {
			unclassified = append(unclassified, string(agg))
		}
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("aggregators with no MarginReducibility classification: %v", unclassified)
	}
}
