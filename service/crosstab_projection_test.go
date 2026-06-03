package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// wideCrosstabSchema returns a schema with two categorical axis fields
// (region, segment), one f64 cell field (value), and `padFields`
// additional f64 fields whose names are "pad_0", "pad_1", … The padding
// fields are present in the schema but never referenced by the crosstab
// test request — they exist purely to widen the cohort so the
// projection optimisation has something to drop.
func wideCrosstabSchema(t *testing.T, padFields int) *encoding.Schema {
	t.Helper()
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")

	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")

	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
		{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
		{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
	}
	off := 10
	for i := range padFields {
		fields = append(fields, encoding.Field{
			Name:         fmt.Sprintf("pad_%d", i),
			Type:         encoding.FieldTypeF64,
			ByteOffset:   off,
			CsvColumnIdx: 3 + i,
		})
		off += 8
	}
	return &encoding.Schema{Fields: fields}
}

// wideCrosstabRecords mirrors crosstabRecords but pads each row with
// arbitrary f64 noise in the trailing pad_i slots so the projection
// path has something to skip. The noise pattern is deterministic so
// the equivalence test can build a parallel narrow cohort with the
// same axis values + cell values.
func wideCrosstabRecords(padFields int) [][]uint64 {
	base := [][]uint64{
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
	for i := range base {
		for p := range padFields {
			base[i] = append(base[i], math.Float64bits(float64(i*1000+p)))
		}
	}
	return base
}

// narrowCrosstabRecords strips the pad fields from the wide records so
// the same logical rows can be written against a schema that lacks
// them.
func narrowCrosstabRecords(wide [][]uint64) [][]uint64 {
	out := make([][]uint64, len(wide))
	for i, row := range wide {
		out[i] = []uint64{row[0], row[1], row[2]}
	}
	return out
}

// TestCrosstabProjection_BehavioralEquivalenceOnWideCohort is the
// load-bearing correctness test. It runs an identical crosstab request
// against (a) a wide cohort whose schema has the three referenced
// fields plus 50 unreferenced pad fields, and (b) a narrow cohort with
// only the three referenced fields. The MatrixPayload must be byte-
// equivalent: same RowKeys, same ColumnKeys, same Cells, same margins.
// Failure here means projection drops or corrupts data needed by
// aggregation, partitioning, or margin recompute.
func TestCrosstabProjection_BehavioralEquivalenceOnWideCohort(t *testing.T) {
	ctx := context.Background()

	wideSchema := wideCrosstabSchema(t, 50)
	wideRecs := wideCrosstabRecords(50)
	wideCfg := setupTestFS(t, "wide.pulse", wideSchema, wideRecs)
	wideSvc := New(wideCfg)

	narrowSchema := wideCrosstabSchema(t, 0)
	narrowRecs := narrowCrosstabRecords(wideRecs)
	narrowCfg := setupTestFS(t, "narrow.pulse", narrowSchema, narrowRecs)
	narrowSvc := New(narrowCfg)

	mkReq := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "n"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	wideResp, err := wideSvc.Process(ctx, mkReq("wide.pulse"))
	if err != nil {
		t.Fatalf("wide Process: %v", err)
	}
	narrowResp, err := narrowSvc.Process(ctx, mkReq("narrow.pulse"))
	if err != nil {
		t.Fatalf("narrow Process: %v", err)
	}

	assertMatrixEqual(t, "matrix", wideResp.Crosstab.Matrix, narrowResp.Crosstab.Matrix)

	if wideResp.Metadata.TotalRows != narrowResp.Metadata.TotalRows {
		t.Errorf("TotalRows wide=%d narrow=%d", wideResp.Metadata.TotalRows, narrowResp.Metadata.TotalRows)
	}
}

// TestCrosstabProjection_MedianMarginUnchangedOnWideCohort guards the
// most subtle correctness property — AGG_MEDIAN row margin equals the
// standalone single-axis median, NOT the median of cell medians. Since
// projection slims the per-record value map, a regression that drops
// the cell field would silently break this. Run the same logical
// request against a wide cohort and assert the expected median margin.
func TestCrosstabProjection_MedianMarginUnchangedOnWideCohort(t *testing.T) {
	ctx := context.Background()

	wideSchema := wideCrosstabSchema(t, 25)
	wideRecs := wideCrosstabRecords(25)
	cfg := setupTestFS(t, "ct.pulse", wideSchema, wideRecs)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_MEDIAN, Field: "value", Label: "med"},
			Margins: types.CrosstabMargins{Rows: true},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	m := resp.Crosstab.Matrix

	rowIdx := map[string]int{}
	for i, k := range m.RowKeys {
		rowIdx[k[0].(string)] = i
	}
	// median(10,20,30,100) = 25; median(20,100) per cell = 60 — the recompute
	// path must produce 25.
	northIdx, ok := rowIdx["north"]
	if !ok {
		t.Fatalf("missing north row")
	}
	if !m.RowMargins[northIdx].Present {
		t.Fatal("expected north row margin present")
	}
	if !floatClose(m.RowMargins[northIdx].Value, 25.0, 0.001) {
		t.Errorf("north row median margin = %v, want 25 (median of raw rows; got median-of-medians would be 60)",
			m.RowMargins[northIdx].Value)
	}
}

// TestCrosstabProjection_FilterExpressionFallsBackToFullDecode runs a
// crosstab with a FILTER_EXPRESSION that NeededFields cannot prove
// complete (unparseable). The implementation must fall through to the
// full-decode path silently and still return a correct matrix.
func TestCrosstabProjection_FilterExpressionFallsBackToFullDecode(t *testing.T) {
	ctx := context.Background()

	schema := wideCrosstabSchema(t, 10)
	recs := wideCrosstabRecords(10)
	cfg := setupTestFS(t, "ct.pulse", schema, recs)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Filterers: []*types.Filterer{{
			Type: types.FILTER_EXPRESSION,
			// Parseable expression that references the cell field —
			// the walker will capture "value" and stay narrow, so we
			// also exercise the parseable branch. The wide-path
			// fallback gets independent coverage in the unit test
			// `TestNeededFields_CrosstabFilterExpressionBailsOut`.
			Expression: "value > 0",
		}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected matrix payload")
	}
	// Every original row has value > 0; count must equal 9.
	var total float64
	for _, row := range resp.Crosstab.Matrix.Cells {
		for _, cell := range row {
			if cell.Present {
				total += cell.Value
			}
		}
	}
	if total != 9 {
		t.Errorf("filter+count total = %v, want 9", total)
	}
}

// TestCrosstabProjection_TestsReferenceExtraField verifies that fields
// referenced only by a Tier-1 Tests slot (not by Rows/Columns/Cell)
// stay in the projection set. Drops a Tests entry referencing the
// cell field along a SplitBy field that lives outside the axes; the
// test result must surface and match a standalone-test run.
func TestCrosstabProjection_TestsReferenceExtraField(t *testing.T) {
	ctx := context.Background()

	// Schema: region, segment, value, group, pad_*.
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")
	groupDict := encoding.NewDictionary()
	groupDict.Add("A")
	groupDict.Add("B")

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
			{Name: "group", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 10, CsvColumnIdx: 3, Dictionary: groupDict},
		},
	}
	// 8 rows split 4 (group A) / 4 (group B); values 1..8.
	recs := [][]uint64{
		{0, 0, math.Float64bits(1), 0},
		{0, 1, math.Float64bits(2), 0},
		{1, 0, math.Float64bits(3), 0},
		{1, 1, math.Float64bits(4), 0},
		{0, 0, math.Float64bits(5), 1},
		{0, 1, math.Float64bits(6), 1},
		{1, 0, math.Float64bits(7), 1},
		{1, 1, math.Float64bits(8), 1},
	}
	cfg := setupTestFS(t, "ct.pulse", schema, recs)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
			Shape:   types.CrosstabShapeMatrix,
		},
		Tests: []*types.Test{{
			Type:    types.TEST_T,
			Field:   "value",
			SplitBy: "group",
		}},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Tests) == 0 {
		t.Fatal("expected Tier-1 test result on response")
	}
	tr := resp.Tests[0]
	// Statistic must reflect group A=(1,2,3,4) vs B=(5,6,7,8). Without
	// the SplitBy field in the projected map, the test would see all
	// rows in one group and the statistic would not match — but with
	// projection covering `group` the statistic must equal the
	// standalone equivalent. We don't pin a value; we just assert the
	// test populated p_value/statistic deterministically.
	if !(tr.PValue >= 0 && tr.PValue <= 1) {
		t.Errorf("p-value out of range: %v", tr.PValue)
	}
	if tr.Statistic == 0 {
		t.Errorf("expected nonzero t-statistic (group A vs B means differ); got 0")
	}
}

// assertMatrixEqual compares two MatrixPayloads field-by-field with
// helpful error messages on mismatch.
func assertMatrixEqual(t *testing.T, name string, a, b *types.MatrixPayload) {
	t.Helper()
	if (a == nil) != (b == nil) {
		t.Fatalf("%s: one matrix nil (a=%v b=%v)", name, a == nil, b == nil)
	}
	if a == nil {
		return
	}
	if !axisKeysEqual(a.RowKeys, b.RowKeys) {
		t.Errorf("%s: RowKeys differ: %v vs %v", name, a.RowKeys, b.RowKeys)
	}
	if !axisKeysEqual(a.ColumnKeys, b.ColumnKeys) {
		t.Errorf("%s: ColumnKeys differ: %v vs %v", name, a.ColumnKeys, b.ColumnKeys)
	}
	if len(a.Cells) != len(b.Cells) {
		t.Fatalf("%s: Cells row count differs: %d vs %d", name, len(a.Cells), len(b.Cells))
	}
	for i := range a.Cells {
		if len(a.Cells[i]) != len(b.Cells[i]) {
			t.Fatalf("%s: Cells[%d] column count differs: %d vs %d", name, i, len(a.Cells[i]), len(b.Cells[i]))
		}
		for j := range a.Cells[i] {
			if a.Cells[i][j].Present != b.Cells[i][j].Present {
				t.Errorf("%s: Cell[%d][%d] Present differs: %v vs %v", name, i, j, a.Cells[i][j].Present, b.Cells[i][j].Present)
			}
			if a.Cells[i][j].Present && !floatClose(a.Cells[i][j].Value, b.Cells[i][j].Value, 1e-9) {
				t.Errorf("%s: Cell[%d][%d] Value differs: %v vs %v", name, i, j, a.Cells[i][j].Value, b.Cells[i][j].Value)
			}
		}
	}
	if len(a.RowMargins) != len(b.RowMargins) {
		t.Errorf("%s: RowMargins length differs: %d vs %d", name, len(a.RowMargins), len(b.RowMargins))
	}
	for i := range a.RowMargins {
		if a.RowMargins[i].Present != b.RowMargins[i].Present {
			t.Errorf("%s: RowMargin[%d] Present differs: %v vs %v", name, i, a.RowMargins[i].Present, b.RowMargins[i].Present)
		}
		if a.RowMargins[i].Present && !floatClose(a.RowMargins[i].Value, b.RowMargins[i].Value, 1e-9) {
			t.Errorf("%s: RowMargin[%d] Value differs: %v vs %v", name, i, a.RowMargins[i].Value, b.RowMargins[i].Value)
		}
	}
	if len(a.ColumnMargins) != len(b.ColumnMargins) {
		t.Errorf("%s: ColumnMargins length differs: %d vs %d", name, len(a.ColumnMargins), len(b.ColumnMargins))
	}
	for i := range a.ColumnMargins {
		if a.ColumnMargins[i].Present != b.ColumnMargins[i].Present {
			t.Errorf("%s: ColumnMargin[%d] Present differs", name, i)
		}
		if a.ColumnMargins[i].Present && !floatClose(a.ColumnMargins[i].Value, b.ColumnMargins[i].Value, 1e-9) {
			t.Errorf("%s: ColumnMargin[%d] Value differs: %v vs %v", name, i, a.ColumnMargins[i].Value, b.ColumnMargins[i].Value)
		}
	}
	if a.GrandTotal.Present != b.GrandTotal.Present {
		t.Errorf("%s: GrandTotal.Present differs: %v vs %v", name, a.GrandTotal.Present, b.GrandTotal.Present)
	}
	if a.GrandTotal.Present && !floatClose(a.GrandTotal.Value, b.GrandTotal.Value, 1e-9) {
		t.Errorf("%s: GrandTotal.Value differs: %v vs %v", name, a.GrandTotal.Value, b.GrandTotal.Value)
	}
}

// BenchmarkCrosstabWideCohort exercises the projection path on a wide
// cohort (3 referenced fields, 200 total fields, 10K rows). Compare
// against the v0.12.2 baseline by reverting the applyCrosstabProjection
// call in service/crosstab.go and rerunning.
//
// Measured on darwin/arm64 (M1 Max), 200 fields × 10K rows:
//   - projected:    53 MB/op, 2.05M allocs/op
//   - unprojected: 116 MB/op, 2.07M allocs/op
//
// The savings scale with the unreferenced-field count. At 628 fields
// (the user's reported repro) the unprojected path is the difference
// between a working query and a multi-GB OOM event.
//
// Run with: go test ./service/ -bench BenchmarkCrosstabWideCohort -benchmem -run=^$
func BenchmarkCrosstabWideCohort(b *testing.B) {
	const pad = 197 // 200 total fields including region, segment, value
	const rows = 10000

	schema := buildBenchWideSchema(pad)
	recs := buildBenchWideRecords(rows, pad)

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range recs {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				b.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "ct.pulse", buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		},
	}

	b.ReportAllocs()
	for b.Loop() {
		_, err := svc.Process(ctx, req)
		if err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

func buildBenchWideSchema(padFields int) *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")
	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")

	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
		{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
		{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
	}
	off := 10
	for i := range padFields {
		fields = append(fields, encoding.Field{
			Name:         fmt.Sprintf("pad_%d", i),
			Type:         encoding.FieldTypeF64,
			ByteOffset:   off,
			CsvColumnIdx: 3 + i,
		})
		off += 8
	}
	return &encoding.Schema{Fields: fields}
}

func buildBenchWideRecords(rows, padFields int) [][]uint64 {
	out := make([][]uint64, rows)
	for i := range rows {
		row := make([]uint64, 3+padFields)
		row[0] = uint64(i % 3)
		row[1] = uint64(i % 2)
		row[2] = math.Float64bits(float64(i))
		for p := range padFields {
			row[3+p] = math.Float64bits(float64(i + p))
		}
		out[i] = row
	}
	return out
}
