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
		if !floatClose(cell.Scalar(), want, 0.001) {
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
		got := matrix.RowMargins[i].Scalar()
		want := rowMargins[region]
		if !floatClose(got, want, 0.001) {
			t.Errorf("row margin (%s) = %v, want %v (standalone median)", region, got, want)
		}
	}

	// Independently: median-of-cells for north = median(20, 100) = 60.
	// Standalone row margin for north = median(10,20,30,100) = 25.
	// Verify they differ — guards against accidental cell-summation.
	if i, ok := indexOfKey(matrix.RowKeys, "north"); ok {
		got := matrix.RowMargins[i].Scalar()
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
	if !floatClose(matrix.GrandTotal.Scalar(), 20.0, 0.001) {
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
				sum += matrix.Cells[i][j].Scalar()
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
				sum += matrix.Cells[i][j].Scalar()
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
				sum += matrix.Cells[i][j].Scalar()
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
	if !cell.Present || !floatClose(cell.Scalar(), 3.0, 0.001) {
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
				sum += matrix.Cells[i][j].Scalar()
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

// TestCrosstab_Tier1TestSurfaces verifies a tier-1 test attached to a
// crosstab request fires and lands in Response.Tests — guards against
// the silent-drop regression where RunCrosstab forgot to invoke the
// tests slot.
func TestCrosstab_Tier1TestSurfaces(t *testing.T) {
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
		},
		Tests: []*types.Test{
			{Type: types.TEST_CHISQ, Rows: "region", Cols: "segment", Alpha: 0.05, Label: "indep"},
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("expected 1 tier-1 test result; got %d", len(resp.Tests))
	}
	if resp.Tests[0].Type != types.TEST_CHISQ {
		t.Errorf("test type = %s, want TEST_CHISQ", resp.Tests[0].Type)
	}
	if resp.Tests[0].Label != "indep" {
		t.Errorf("test label = %q, want %q", resp.Tests[0].Label, "indep")
	}
}

// TestCrosstab_Tier1TestEqualsStandalone verifies the test fold runs
// over the same filter-passing record set the cell aggregation sees,
// not a different sub-population. The TEST_CHISQ statistic from a
// crosstab+test pairing must equal the statistic from a standalone
// TEST_CHISQ request against the same cohort.
func TestCrosstab_Tier1TestEqualsStandalone(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	paired := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
		Tests: []*types.Test{
			{Type: types.TEST_CHISQ, Rows: "region", Cols: "segment", Alpha: 0.05},
		},
	}
	standalone := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "value", Label: "n"},
		},
		Tests: []*types.Test{
			{Type: types.TEST_CHISQ, Rows: "region", Cols: "segment", Alpha: 0.05},
		},
	}

	a, err := svc.Process(ctx, paired)
	if err != nil {
		t.Fatalf("paired: %v", err)
	}
	b, err := svc.Process(ctx, standalone)
	if err != nil {
		t.Fatalf("standalone: %v", err)
	}
	if len(a.Tests) != 1 || len(b.Tests) != 1 {
		t.Fatalf("expected one test result on each path")
	}
	if !floatClose(a.Tests[0].Statistic, b.Tests[0].Statistic, 1e-9) {
		t.Errorf("statistic divergence: paired=%v standalone=%v",
			a.Tests[0].Statistic, b.Tests[0].Statistic)
	}
	if !floatClose(a.Tests[0].PValue, b.Tests[0].PValue, 1e-9) {
		t.Errorf("p-value divergence: paired=%v standalone=%v",
			a.Tests[0].PValue, b.Tests[0].PValue)
	}
}

// TestCrosstab_Tier2PostTestSurfaces verifies a tier-2 post-test runs
// over the synthesised cell rows when shape=matrix. TEST_TREND on a
// monthly time-series crosstab is the canonical example.
func TestCrosstab_Tier2PostTestSurfaces(t *testing.T) {
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
		},
		PostTests: []*types.Test{
			{Type: types.TEST_TREND, Field: "n",
				OrderBy: []types.OrderKey{{Field: "region"}},
				Alpha:   0.05, Label: "trend"},
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.PostTests) != 1 {
		t.Fatalf("expected 1 tier-2 post-test result; got %d", len(resp.PostTests))
	}
	if resp.PostTests[0].Type != types.TEST_TREND {
		t.Errorf("post-test type = %s, want TEST_TREND", resp.PostTests[0].Type)
	}
}

// TestCrosstab_PartialColumnNormalize_SumsToOneWithinParent verifies
// that normalize=column with NormalizeLevel=0 on a 2-grouper column
// axis makes every cell + row pairing under a shared top-level column
// value sum to 1.0. The default (leaf) behavior would instead require
// cells in each leaf column to sum to 1.0; the partial behavior
// promotes the parent grouper to the 100% denominator.
func TestCrosstab_PartialColumnNormalize_SumsToOneWithinParent(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:      types.CrosstabNormalizeColumn,
			NormalizeLevel: &level,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	regionCols := map[string][]int{}
	for j, k := range matrix.ColumnKeys {
		region := k[0].(string)
		regionCols[region] = append(regionCols[region], j)
	}
	if len(regionCols) < 2 {
		t.Fatalf("expected >=2 distinct top-level regions; got %d", len(regionCols))
	}
	for region, cols := range regionCols {
		var sum float64
		for _, j := range cols {
			for i := range matrix.RowKeys {
				if matrix.Cells[i][j].Present {
					sum += matrix.Cells[i][j].Scalar()
				}
			}
		}
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("region %s partial column sum = %v, want 1.0", region, sum)
		}
	}
}

// TestCrosstab_PartialRowNormalize_SumsToOneWithinParent mirrors the
// column case for nested rows.
func TestCrosstab_PartialRowNormalize_SumsToOneWithinParent(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns:        []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:      types.CrosstabNormalizeRow,
			NormalizeLevel: &level,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix
	regionRows := map[string][]int{}
	for i, k := range matrix.RowKeys {
		region := k[0].(string)
		regionRows[region] = append(regionRows[region], i)
	}
	if len(regionRows) < 2 {
		t.Fatalf("expected >=2 distinct top-level row regions; got %d", len(regionRows))
	}
	for region, rows := range regionRows {
		var sum float64
		for _, i := range rows {
			for j := range matrix.ColumnKeys {
				if matrix.Cells[i][j].Present {
					sum += matrix.Cells[i][j].Scalar()
				}
			}
		}
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("region %s partial row sum = %v, want 1.0", region, sum)
		}
	}
}

// TestCrosstab_NormalizeLevelLeaf_ByteEqualToDefault confirms the
// short-circuit: setting NormalizeLevel to len(axis)-1 produces
// numerically identical cells to omitting the field entirely.
func TestCrosstab_NormalizeLevelLeaf_ByteEqualToDefault(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	mkSpec := func(level *int) *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:      types.CrosstabNormalizeColumn,
			NormalizeLevel: level,
		}
	}
	leaf := 1
	respA, err := svc.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "ct.pulse"}, Crosstab: mkSpec(nil)})
	if err != nil {
		t.Fatalf("Process A: %v", err)
	}
	respB, err := svc.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "ct.pulse"}, Crosstab: mkSpec(&leaf)})
	if err != nil {
		t.Fatalf("Process B: %v", err)
	}
	a := respA.Crosstab.Matrix
	b := respB.Crosstab.Matrix
	if len(a.Cells) != len(b.Cells) {
		t.Fatalf("row count diverged: %d vs %d", len(a.Cells), len(b.Cells))
	}
	for i := range a.Cells {
		if len(a.Cells[i]) != len(b.Cells[i]) {
			t.Fatalf("col count diverged at row %d: %d vs %d", i, len(a.Cells[i]), len(b.Cells[i]))
		}
		for j := range a.Cells[i] {
			if a.Cells[i][j].Present != b.Cells[i][j].Present || a.Cells[i][j].Value != b.Cells[i][j].Value {
				t.Errorf("cell [%d][%d] diverged: A=%+v B=%+v", i, j, a.Cells[i][j], b.Cells[i][j])
			}
		}
	}
}

// TestCrosstab_NormalizeLevelOutOfRangeRejected confirms the runtime
// gate rejects a depth value outside [0, len(axis)-1].
func TestCrosstab_NormalizeLevelOutOfRangeRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	bad := 5
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:      types.CrosstabNormalizeColumn,
			NormalizeLevel: &bad,
		},
	}
	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError; got %T (%v)", err, err)
	}
	if ce.Code != errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE {
		t.Errorf("code = %v, want PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE", ce.Code)
	}
}

// TestCrosstab_NormalizeLevelWithTotalRejected confirms NormalizeLevel
// is rejected when paired with Normalize=total (no axis to descend).
func TestCrosstab_NormalizeLevelWithTotalRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:           []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:        []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:      types.CrosstabNormalizeTotal,
			NormalizeLevel: &level,
		},
	}
	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError; got %T (%v)", err, err)
	}
	if ce.Code != errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE {
		t.Errorf("code = %v, want PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE", ce.Code)
	}
}

// TestCrosstab_NormalizeLevelLongShapeTagEmission verifies long-shape
// output carries both leaf (_margin=column) and partial
// (_margin=column_at_<depth>) margin rows when display margins +
// partial-depth normalization combine.
func TestCrosstab_NormalizeLevelLongShapeTagEmission(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:      types.CrosstabNormalizeColumn,
			NormalizeLevel: &level,
			Shape:          types.CrosstabShapeLong,
			Margins:        types.CrosstabMargins{Columns: true},
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	tags := map[string]int{}
	for _, row := range resp.Data {
		if t, ok := row["_margin"].(string); ok {
			tags[t]++
		}
	}
	if tags["column"] == 0 {
		t.Errorf("missing leaf _margin=column rows; tags=%v", tags)
	}
	if tags["column_at_0"] == 0 {
		t.Errorf("missing partial _margin=column_at_0 rows; tags=%v", tags)
	}
}

// TestCrosstab_PartialMarginMedianRecomputesFromRaw verifies that
// partial-depth normalization with a recompute-class aggregator
// (AGG_MEDIAN) derives the partial denominator from the raw row set
// for the truncated axis tuple — not from the leaf cell medians.
// Fixture composition (north column, leaves: retail + wholesale):
//
//	(north, retail)    raw values:    [10, 20, 30] → median 20
//	(north, wholesale) raw values:    [100]        → median 100
//	north top-level    raw values:    [10, 20, 30, 100] → median 25
//
// Cells live on rows keyed by segment. The (retail) row × (north, retail)
// cell is median([10,20,30]) = 20. Under partial-column normalization at
// level=0 the denominator is 25, so the normalized cell is 20/25 = 0.8.
// The median-of-cell-medians (20+100)/2 = 60 would give 0.333 — the test
// is failed by that path.
func TestCrosstab_PartialMarginMedianRecomputesFromRaw(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:           &types.Aggregation{Type: types.AGG_MEDIAN, Field: "value", Label: "m"},
			Normalize:      types.CrosstabNormalizeColumn,
			NormalizeLevel: &level,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	rowIdx := -1
	for i, k := range matrix.RowKeys {
		if k[0] == "retail" {
			rowIdx = i
			break
		}
	}
	colIdx := -1
	for j, k := range matrix.ColumnKeys {
		if k[0] == "north" && k[1] == "retail" {
			colIdx = j
			break
		}
	}
	if rowIdx < 0 || colIdx < 0 {
		t.Fatalf("missing axis tuple: rowIdx=%d colIdx=%d", rowIdx, colIdx)
	}
	got := matrix.Cells[rowIdx][colIdx]
	if !got.Present {
		t.Fatal("cell not present")
	}
	if floatClose(got.Scalar(), 20.0/60.0, 1e-9) {
		t.Errorf("normalization denominator was median-of-cell-medians (60); want partial recompute over raw rows (25)")
	}
	if !floatClose(got.Scalar(), 20.0/25.0, 1e-9) {
		t.Errorf("normalized median cell = %v, want 0.8 (20/25)", got.Value)
	}
}

// TestCrosstab_NormalizeWithin_RowFixesColPrefix exercises the user's
// canonical cross-axis scenario: Rows=[brand-like], Cols=[wavedate-like,
// xxx-like]. With normalize=row, normalize_within=0 the denominator for
// each cell is the count over the inner-column (xxx) leaf within the
// fixed (rowBrand, outerColWavedate) slab. Cells in each
// (rowBrand, outerColWavedate) row-slice must therefore sum to 1.0
// across the inner-column dimension.
//
// Fixture mapping onto crosstabRecords:
//   - rowAxis (brand)            ⇒ region
//   - outerCol (wavedate)        ⇒ segment
//   - innerCol (xxx — independent value bin) ⇒ GROUP_RANGE on value @ 50
//
// Per-slab denominators (from crosstabRecords):
//
//	(north, retail)    cells across value bins: bin[0,50)=3   denom=3
//	(north, wholesale) bin[100,150)=1                          denom=1
//	(south, retail)    bin[0,50)=2                              denom=2
//	(south, wholesale) bin[0,50)=1 + bin[50,100)=1              denom=2
//	(east, retail)     bin[0,50)=1                              denom=1
//	(east, wholesale)  (empty — slab dropped)
func TestCrosstab_NormalizeWithin_RowFixesColPrefix(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
				{Type: types.GROUP_RANGE, Field: "value", Interval: 50},
			},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeRow,
			NormalizeWithin: &within,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	type slab struct{ row, outerCol string }
	sums := map[slab]float64{}
	for i, rk := range matrix.RowKeys {
		for j, ck := range matrix.ColumnKeys {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				continue
			}
			key := slab{row: rk[0].(string), outerCol: ck[0].(string)}
			sums[key] += cell.Scalar()
		}
	}
	if len(sums) == 0 {
		t.Fatal("no present cells")
	}
	for s, sum := range sums {
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("slab (row=%s, outerCol=%s) sum = %v, want 1.0", s.row, s.outerCol, sum)
		}
	}
}

// TestCrosstab_NormalizeWithin_ColumnFixesRowPrefix is the symmetric
// twin: normalize=column with normalize_within=0 partitioning the row
// axis. Cells inside each (colKey, outerRowPrefix) column slice must
// sum to 1.0 across the inner-row dimension.
//
// Same fixture; axes swapped so segment is the outer row, the value bin
// is the inner row, and region is the column axis.
func TestCrosstab_NormalizeWithin_ColumnFixesRowPrefix(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
				{Type: types.GROUP_RANGE, Field: "value", Interval: 50},
			},
			Columns:         []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeColumn,
			NormalizeWithin: &within,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	type slab struct{ col, outerRow string }
	sums := map[slab]float64{}
	for i, rk := range matrix.RowKeys {
		for j, ck := range matrix.ColumnKeys {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				continue
			}
			key := slab{col: ck[0].(string), outerRow: rk[0].(string)}
			sums[key] += cell.Scalar()
		}
	}
	if len(sums) == 0 {
		t.Fatal("no present cells")
	}
	for s, sum := range sums {
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("slab (col=%s, outerRow=%s) sum = %v, want 1.0", s.col, s.outerRow, sum)
		}
	}
}

// TestCrosstab_NormalizeWithin_CombinedWithLevel exercises the composition
// of NormalizeLevel (same-axis truncation) and NormalizeWithin (cross-
// axis prefix). When normalize=row, NormalizeLevel=0 collapses deeper
// row groupers and NormalizeWithin=0 fixes the col-axis outer prefix.
//
// Fixture: Rows=[region, segment], Cols=[segment, GROUP_RANGE value@50],
// normalize=row, NormalizeLevel=0, NormalizeWithin=0.
//
// Denominator scope per cell: Σ records where region == cell.row[0]
// AND segment == cell.col[0]. The row prefix (region) is held across
// all `segment` row values; the col prefix (segment) is held across
// all value bins.
//
// For each (rowRegion, outerColSegment) slab, the matrix cells in
// every (region, *) row × (segment, *) column combination must sum to
// the per-region/segment record count divided by itself = 1.0.
func TestCrosstab_NormalizeWithin_CombinedWithLevel(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
				{Type: types.GROUP_RANGE, Field: "value", Interval: 50},
			},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeRow,
			NormalizeLevel:  &level,
			NormalizeWithin: &within,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	type slab struct{ rowPrefix, colPrefix string }
	sums := map[slab]float64{}
	for i, rk := range matrix.RowKeys {
		for j, ck := range matrix.ColumnKeys {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				continue
			}
			key := slab{rowPrefix: rk[0].(string), colPrefix: ck[0].(string)}
			sums[key] += cell.Scalar()
		}
	}
	if len(sums) == 0 {
		t.Fatal("no present cells")
	}
	for s, sum := range sums {
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("slab (rowPrefix=%s, colPrefix=%s) sum = %v, want 1.0",
				s.rowPrefix, s.colPrefix, sum)
		}
	}
}

// TestCrosstab_NormalizeWithin_ColumnCombinedWithLevel mirrors the
// row-side composition test for normalize=column. NormalizeLevel
// truncates the column axis to its top-level grouper; NormalizeWithin
// fixes a prefix of the row axis. Each (rowPrefix, colPrefix) slab
// must sum to 1.0.
//
// Fixture: Rows=[segment, GROUP_RANGE value@50], Cols=[region, segment],
// normalize=column, NormalizeLevel=0, NormalizeWithin=0.
func TestCrosstab_NormalizeWithin_ColumnCombinedWithLevel(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	level := 0
	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
				{Type: types.GROUP_RANGE, Field: "value", Interval: 50},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeColumn,
			NormalizeLevel:  &level,
			NormalizeWithin: &within,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	matrix := resp.Crosstab.Matrix

	type slab struct{ rowPrefix, colPrefix string }
	sums := map[slab]float64{}
	for i, rk := range matrix.RowKeys {
		for j, ck := range matrix.ColumnKeys {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				continue
			}
			key := slab{rowPrefix: rk[0].(string), colPrefix: ck[0].(string)}
			sums[key] += cell.Scalar()
		}
	}
	if len(sums) == 0 {
		t.Fatal("no present cells")
	}
	for s, sum := range sums {
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("slab (rowPrefix=%s, colPrefix=%s) sum = %v, want 1.0",
				s.rowPrefix, s.colPrefix, sum)
		}
	}
}

// TestCrosstab_NormalizeWithin_LongShape verifies long-shape emission
// carries the normalized cell values when normalize_within is active.
// The cross-axis partition does not introduce a new _margin tag (user-
// facing semantics deliberately reuse the existing per-cell row format);
// data rows must reach Response.Data with normalized values that obey
// the (rowKey, outerColPrefix) slab-sum invariant.
func TestCrosstab_NormalizeWithin_LongShape(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
				{Type: types.GROUP_RANGE, Field: "value", Interval: 50},
			},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeRow,
			NormalizeWithin: &within,
			Shape:           types.CrosstabShapeLong,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("Response.Data empty in long shape")
	}
	// No _margin rows (we did not request margins). Verify and sum.
	type slab struct{ row, outerCol string }
	sums := map[slab]float64{}
	for _, row := range resp.Data {
		if _, ok := row["_margin"]; ok {
			t.Errorf("unexpected _margin row in long-shape output without margins request: %v", row)
			continue
		}
		v, _ := row["n"].(float64)
		key := slab{row: row["region"].(string), outerCol: row["segment"].(string)}
		sums[key] += v
	}
	for s, sum := range sums {
		if !floatClose(sum, 1.0, 1e-9) {
			t.Errorf("long-shape slab (row=%s, outerCol=%s) sum = %v, want 1.0", s.row, s.outerCol, sum)
		}
	}
}

// TestCrosstab_NormalizeWithinOutOfRangeRejected confirms the runtime
// gate rejects a normalize_within value outside [0, len(other-axis)-1].
func TestCrosstab_NormalizeWithinOutOfRangeRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	bad := 5
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
				{Type: types.GROUP_RANGE, Field: "value", Interval: 50},
			},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeRow,
			NormalizeWithin: &bad,
		},
	}
	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError; got %T (%v)", err, err)
	}
	if ce.Code != errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE {
		t.Errorf("code = %v, want PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE", ce.Code)
	}
}

// TestCrosstab_NormalizeWithinOutOfRangeRejected_ColumnAxis covers the
// runtime gate's symmetric branch: normalize=column inspects the row
// axis length, so the out-of-range message names "rows".
func TestCrosstab_NormalizeWithinOutOfRangeRejected_ColumnAxis(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	bad := 5
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:         []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeColumn,
			NormalizeWithin: &bad,
		},
	}
	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError; got %T (%v)", err, err)
	}
	if ce.Code != errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE {
		t.Errorf("code = %v, want PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE", ce.Code)
	}
	if axis, _ := ce.Details["axis"].(string); axis != "rows" {
		t.Errorf("details.axis = %q, want \"rows\" (column-normalize branch inspects row axis)", axis)
	}
}

// TestCrosstab_NormalizeWithinWithTotalRejected confirms NormalizeWithin
// is rejected when paired with Normalize=total (no other axis to
// partition).
func TestCrosstab_NormalizeWithinWithTotalRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:         []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Normalize:       types.CrosstabNormalizeTotal,
			NormalizeWithin: &within,
		},
	}
	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError; got %T (%v)", err, err)
	}
	if ce.Code != errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE {
		t.Errorf("code = %v, want PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE", ce.Code)
	}
}

// TestCrosstab_NormalizeWithinWithoutNormalizeRejected confirms
// NormalizeWithin requires a normalize direction (row / column).
func TestCrosstab_NormalizeWithinWithoutNormalizeRejected(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	ctx := context.Background()

	within := 0
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:         []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			NormalizeWithin: &within,
		},
	}
	_, err := svc.Process(ctx, req)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError; got %T (%v)", err, err)
	}
	if ce.Code != errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS {
		t.Errorf("code = %v, want PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS", ce.Code)
	}
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
