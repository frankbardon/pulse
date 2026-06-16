package service

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E3-S2 — Response.Components.Crosstab.CellCounts emission tests on both
// the buffered and fused crosstab paths. The cell builder records the
// per-(r, c) record count alongside each aggregator update; the matrix
// shape mirrors MatrixPayload.Cells coordinate-for-coordinate so
// consumers can dereference both via the same (rowKeys[i], colKeys[j])
// position. ExcludedRecords counts the rows that resolved no axis key
// (or where one axis key was null) and were therefore dropped from the
// cell-count matrix. IncludedRecords + ExcludedRecords == filtered row
// count by construction.

// cellCountsForRegionSegment returns the expected CellCounts matrix for
// the canonical crosstabRecords fixture (3 regions x 2 segments).
//
//	region order: north, south, east (the dict-insertion order kept by
//	the buffered + fused paths once sorted by composite axis key)
//	segment order: retail, wholesale
//
// counts (from crosstabRecords above):
//
//	(north, retail): 3, (north, wholesale): 1
//	(south, retail): 2, (south, wholesale): 2
//	(east,  retail): 1, (east,  wholesale): 0
//
// Sorted composite key order is alphabetical on the composite key
// string, so (east, ...) precedes (north, ...) precedes (south, ...).
// The helper looks up by region / segment name regardless of the sort
// order — we re-index using the live RowKeys / ColumnKeys returned by
// the matrix so the assertion stays stable across grouper-emission
// reordering.
func cellCountsForRegionSegment() map[[2]string]int {
	return map[[2]string]int{
		{"north", "retail"}:    3,
		{"north", "wholesale"}: 1,
		{"south", "retail"}:    2,
		{"south", "wholesale"}: 2,
		{"east", "retail"}:     1,
		// (east, wholesale) intentionally absent — zero cell on the
		// fixture.
	}
}

// TestCrosstabComponents_SmallFixture_CellCountsMatch verifies the basic
// happy path: a 3x2 fixture's CellCounts matrix matches the per-bucket
// record counts, and MatrixPayload.Cells is Present wherever the count
// is non-zero. Drives the buffered path explicitly (fusion disabled) so
// the buffered code path's wiring is exercised end-to-end.
func TestCrosstabComponents_SmallFixture_CellCountsMatch(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}

	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatalf("Response.Components.Crosstab not populated")
	}
	ct := resp.Components.Crosstab
	matrix := resp.Crosstab.Matrix
	if matrix == nil {
		t.Fatalf("Response.Crosstab.Matrix nil")
	}
	if len(ct.CellCounts) != len(matrix.RowKeys) {
		t.Fatalf("CellCounts rows = %d, want %d (len(RowKeys))", len(ct.CellCounts), len(matrix.RowKeys))
	}

	rowByName := map[string]int{}
	for i, k := range matrix.RowKeys {
		rowByName[k[0].(string)] = i
	}
	colByName := map[string]int{}
	for j, k := range matrix.ColumnKeys {
		colByName[k[0].(string)] = j
	}
	expect := cellCountsForRegionSegment()

	var totalCount int
	for i := range ct.CellCounts {
		if len(ct.CellCounts[i]) != len(matrix.ColumnKeys) {
			t.Fatalf("CellCounts[%d] cols = %d, want %d", i, len(ct.CellCounts[i]), len(matrix.ColumnKeys))
		}
		for j := range ct.CellCounts[i] {
			totalCount += ct.CellCounts[i][j]
			if ct.CellCounts[i][j] > 0 && !matrix.Cells[i][j].Present {
				t.Errorf("CellCounts[%d][%d]=%d but Matrix.Cells[%d][%d].Present=false",
					i, j, ct.CellCounts[i][j], i, j)
			}
		}
	}

	for pair, want := range expect {
		i, ok := rowByName[pair[0]]
		if !ok {
			t.Errorf("missing row key %q", pair[0])
			continue
		}
		j, ok := colByName[pair[1]]
		if !ok {
			t.Errorf("missing column key %q", pair[1])
			continue
		}
		if got := ct.CellCounts[i][j]; got != want {
			t.Errorf("CellCounts[%s][%s] = %d, want %d", pair[0], pair[1], got, want)
		}
	}

	// (east, wholesale) is the empty cell — must read zero.
	if i, ok := rowByName["east"]; ok {
		if j, ok := colByName["wholesale"]; ok {
			if got := ct.CellCounts[i][j]; got != 0 {
				t.Errorf("CellCounts[east][wholesale] = %d, want 0", got)
			}
		}
	}

	if totalCount != ct.IncludedRecords {
		t.Errorf("sum(CellCounts)=%d, IncludedRecords=%d (should match)",
			totalCount, ct.IncludedRecords)
	}
	if ct.IncludedRecords != int(resp.Metadata.FilteredRows) {
		t.Errorf("IncludedRecords=%d, FilteredRows=%d (no null axis rows expected)",
			ct.IncludedRecords, resp.Metadata.FilteredRows)
	}
	if ct.ExcludedRecords != 0 {
		t.Errorf("ExcludedRecords=%d, want 0 on a fixture with no null axis rows", ct.ExcludedRecords)
	}
}

// TestCrosstabComponents_BufferedVsFused_ParityBytEqual is the byte-equal
// parity gate: the same input must produce identical CellCounts on both
// paths. Drives the same Request through buffered and fused with
// SetDisableCrosstabFusion and asserts the matrices are reflect-equal.
func TestCrosstabComponents_BufferedVsFused_ParityBytEqual(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	svcBuf := New(cfg)
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}

	svcFused := New(cfg)
	// Fusion enabled by default.
	fusedResp, err := svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}

	if bufResp.Components == nil || bufResp.Components.Crosstab == nil {
		t.Fatalf("buffered Components.Crosstab nil")
	}
	if fusedResp.Components == nil || fusedResp.Components.Crosstab == nil {
		t.Fatalf("fused Components.Crosstab nil")
	}

	bufCT := bufResp.Components.Crosstab
	fusedCT := fusedResp.Components.Crosstab

	if !reflect.DeepEqual(bufCT.CellCounts, fusedCT.CellCounts) {
		t.Errorf("CellCounts differ:\n buffered=%v\n fused   =%v",
			bufCT.CellCounts, fusedCT.CellCounts)
	}
	if bufCT.IncludedRecords != fusedCT.IncludedRecords {
		t.Errorf("IncludedRecords differ: buffered=%d fused=%d",
			bufCT.IncludedRecords, fusedCT.IncludedRecords)
	}
	if bufCT.ExcludedRecords != fusedCT.ExcludedRecords {
		t.Errorf("ExcludedRecords differ: buffered=%d fused=%d",
			bufCT.ExcludedRecords, fusedCT.ExcludedRecords)
	}
}

// TestCrosstabComponents_EmptyCohort_AllZero verifies the boundary case:
// an empty cohort produces a CellCounts matrix with no rows / cols
// (no axis keys observed) and zero include / exclude counters. The
// Components.Crosstab slot is still populated so consumers can rely on
// the typed sub-struct existing whenever Crosstab ran.
func TestCrosstabComponents_EmptyCohort_AllZero(t *testing.T) {
	schema := crosstabSchema()
	// Empty records: a well-formed .pulse with header + schema but no
	// record bytes.
	cfg := setupTestFS(t, "ct.pulse", schema, nil)
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
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
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatalf("Components.Crosstab nil — slot must exist even on empty cohorts")
	}
	ct := resp.Components.Crosstab
	if len(ct.CellCounts) != 0 {
		t.Errorf("CellCounts non-empty on empty cohort: %v", ct.CellCounts)
	}
	if ct.IncludedRecords != 0 {
		t.Errorf("IncludedRecords = %d, want 0 on empty cohort", ct.IncludedRecords)
	}
	if ct.ExcludedRecords != 0 {
		t.Errorf("ExcludedRecords = %d, want 0 on empty cohort", ct.ExcludedRecords)
	}
}

// nullableCrosstabSchema returns a 3-field schema with a nullable
// categorical region column. The null bitmap allows individual records
// to mark region as null so the crosstab cell builder drops them from
// the row-axis partition.
func nullableCrosstabSchema() *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")

	segmentDict := encoding.NewDictionary()
	segmentDict.Add("retail")
	segmentDict.Add("wholesale")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict, Nullable: true},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: segmentDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}
}

// writeNullableCrosstabCohort builds a 5-row cohort where one row's
// region is null. The remaining 4 rows split evenly across (north,
// retail), (north, wholesale), (south, retail), (south, wholesale) so
// the cell-count map is [{north,retail}:1, {north,wholesale}:1,
// {south,retail}:1, {south,wholesale}:1] = 4 included records, with 1
// excluded (the null-region row).
func writeNullableCrosstabCohort(t *testing.T, path string) *fs.Config {
	t.Helper()
	schema := nullableCrosstabSchema()

	// (region, segment, value, region_is_null)
	type rec struct {
		region  uint64
		segment uint64
		value   uint64
		regNull bool
	}
	records := []rec{
		{0, 0, math.Float64bits(10), false}, // north, retail
		{0, 1, math.Float64bits(20), false}, // north, wholesale
		{1, 0, math.Float64bits(30), false}, // south, retail
		{1, 1, math.Float64bits(40), false}, // south, wholesale
		{0, 0, math.Float64bits(50), true},  // NULL region — placement byte arbitrary
	}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range records {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, r.region); err != nil {
			t.Fatalf("WriteFieldValue region: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, r.segment); err != nil {
			t.Fatalf("WriteFieldValue segment: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, r.value); err != nil {
			t.Fatalf("WriteFieldValue value: %v", err)
		}
		if bmSize := schema.BitmapByteSize(); bmSize > 0 {
			bm := make([]byte, bmSize)
			if r.regNull {
				encoding.BitmapSetNull(bm, 0) // region is field index 0
			}
			if err := encoding.WriteBitmap(&buf, bm); err != nil {
				t.Fatalf("WriteBitmap: %v", err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}

// TestCrosstabComponents_NullAxisRow_DropsFromCellCounts verifies that a
// row whose axis-key resolution returns null is excluded from
// CellCounts and contributes to ExcludedRecords. IncludedRecords +
// ExcludedRecords must equal the filter-passing row count
// (Metadata.FilteredRows here, since the request has no filters).
//
// Drives both buffered and fused paths to lock the same null-axis
// semantics on the new component matrix across paths.
func TestCrosstabComponents_NullAxisRow_DropsFromCellCounts(t *testing.T) {
	cfg := writeNullableCrosstabCohort(t, "ct.pulse")
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	cases := []struct {
		name        string
		disableFuse bool
	}{
		{"buffered", true},
		{"fused", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(tc.disableFuse)
			resp, err := svc.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if resp.Components == nil || resp.Components.Crosstab == nil {
				t.Fatalf("Components.Crosstab nil")
			}
			ct := resp.Components.Crosstab

			var sum int
			for i := range ct.CellCounts {
				for j := range ct.CellCounts[i] {
					sum += ct.CellCounts[i][j]
				}
			}
			if sum != ct.IncludedRecords {
				t.Errorf("sum(CellCounts)=%d, IncludedRecords=%d", sum, ct.IncludedRecords)
			}
			if ct.IncludedRecords != 4 {
				t.Errorf("IncludedRecords = %d, want 4 (4 valid axis rows)", ct.IncludedRecords)
			}
			if ct.ExcludedRecords != 1 {
				t.Errorf("ExcludedRecords = %d, want 1 (one null-region row)", ct.ExcludedRecords)
			}
			if got := ct.IncludedRecords + ct.ExcludedRecords; got != int(resp.Metadata.FilteredRows) {
				t.Errorf("IncludedRecords+ExcludedRecords=%d, FilteredRows=%d",
					got, resp.Metadata.FilteredRows)
			}
		})
	}
}

// TestCrosstabComponents_SumInvariant locks the invariant
// `sum(CellCounts) == IncludedRecords` on the canonical fixture across
// both paths. Cheap structural check that catches divergent paths where
// one increments IncludedRecords but skips the matrix slot (or vice
// versa).
func TestCrosstabComponents_SumInvariant(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	for _, tc := range []struct {
		name        string
		disableFuse bool
	}{
		{"buffered", true},
		{"fused", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(tc.disableFuse)
			resp, err := svc.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ct := resp.Components.Crosstab
			if ct == nil {
				t.Fatalf("Components.Crosstab nil")
			}
			var sum int
			for i := range ct.CellCounts {
				for j := range ct.CellCounts[i] {
					sum += ct.CellCounts[i][j]
				}
			}
			if sum != ct.IncludedRecords {
				t.Errorf("sum(CellCounts)=%d, IncludedRecords=%d", sum, ct.IncludedRecords)
			}
			// The canonical fixture has no null axis rows — so
			// IncludedRecords + ExcludedRecords == FilteredRows must
			// degenerate to IncludedRecords == FilteredRows with
			// ExcludedRecords == 0.
			if int(resp.Metadata.FilteredRows) != ct.IncludedRecords+ct.ExcludedRecords {
				t.Errorf("FilteredRows=%d, IncludedRecords+ExcludedRecords=%d",
					resp.Metadata.FilteredRows, ct.IncludedRecords+ct.ExcludedRecords)
			}
		})
	}
}

// --- E3-S3: CellComponents emission tests ---------------------------
//
// CellComponents[r][c] carries the per-cell aggregator components map:
// universal floor {n, n_null} merged with the cell aggregator's
// MetaAggregator.Components() output. Empty cells (Matrix.Cells[r][c]
// Present=false because no record landed) emit nil at the same
// coordinate so consumers can distinguish "no data" from "data with
// floor-only payload".

// crosstabCTOrFail dereferences resp.Components.Crosstab and t.Fatals
// when nil. Used by every CellComponents test to assert the shell
// exists before drilling into the matrix.
func crosstabCTOrFail(t *testing.T, resp *types.Response) *types.CrosstabComponents {
	t.Helper()
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatalf("Components.Crosstab nil")
	}
	return resp.Components.Crosstab
}

// rowColIndex looks up the matrix index for a categorical row / column
// key on the canonical (region, segment) fixture.
func rowColIndex(matrix *types.MatrixPayload) (map[string]int, map[string]int) {
	rowByName := map[string]int{}
	for i, k := range matrix.RowKeys {
		rowByName[k[0].(string)] = i
	}
	colByName := map[string]int{}
	for j, k := range matrix.ColumnKeys {
		colByName[k[0].(string)] = j
	}
	return rowByName, colByName
}

// TestCrosstabComponents_CellComponents_Scalar_AGG_SUM verifies the
// scalar family: AGG_SUM cell aggregator emits {sum} on each populated
// cell, plus the universal floor {n, n_null}. Empty cells emit nil.
//
// Drives the buffered path so the cell builder's runCellAggregation +
// buildCellComponentMap path is exercised end-to-end. Per-cell n and
// n_null come from the per-bucket walk inside runCellAggregation.
func TestCrosstabComponents_CellComponents_Scalar_AGG_SUM(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	if matrix == nil {
		t.Fatalf("Matrix nil")
	}
	rowByName, colByName := rowColIndex(matrix)

	// Shape: rows x cols
	if len(ct.CellComponents) != len(matrix.RowKeys) {
		t.Fatalf("CellComponents rows = %d, want %d", len(ct.CellComponents), len(matrix.RowKeys))
	}
	for i := range ct.CellComponents {
		if len(ct.CellComponents[i]) != len(matrix.ColumnKeys) {
			t.Fatalf("CellComponents[%d] cols = %d, want %d",
				i, len(ct.CellComponents[i]), len(matrix.ColumnKeys))
		}
	}

	// (north, retail): values [10, 20, 30] → sum=60, n=3, n_null=0
	i := rowByName["north"]
	j := colByName["retail"]
	cell := ct.CellComponents[i][j]
	if cell == nil {
		t.Fatalf("CellComponents[north][retail] = nil, want populated")
	}
	if got, want := cell["n"], 3; got != want {
		t.Errorf("n = %v, want %v", got, want)
	}
	if got, want := cell["n_null"], 0; got != want {
		t.Errorf("n_null = %v, want %v", got, want)
	}
	if got, want := cell["sum"], float64(60); got != want {
		t.Errorf("sum = %v, want %v", got, want)
	}
	// Keys should be exactly {n, n_null, sum} (floor + operator).
	if got, want := mapKeys(cell), []string{"n", "n_null", "sum"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cell keys = %v, want %v", got, want)
	}

	// (east, wholesale) is the empty cell — should emit nil.
	if ei, ok := rowByName["east"]; ok {
		if ej, ok := colByName["wholesale"]; ok {
			if ct.CellComponents[ei][ej] != nil {
				t.Errorf("CellComponents[east][wholesale] = %v, want nil (empty cell)",
					ct.CellComponents[ei][ej])
			}
		}
	}
}

// TestCrosstabComponents_CellComponents_Welford_AGG_VARIANCE verifies
// the Welford family: AGG_VARIANCE cell aggregator emits {mean, m2,
// variance} on each populated cell, plus the floor. Numerical match
// against the manual Welford pass over the (north, retail) bucket.
func TestCrosstabComponents_CellComponents_Welford_AGG_VARIANCE(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_VARIANCE, Field: "value", Label: "var"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	rowByName, colByName := rowColIndex(matrix)

	// (north, retail): values [10, 20, 30]
	//   mean   = 20
	//   m2     = (10-20)^2 + (20-20)^2 + (30-20)^2 = 200
	//   variance (population) = m2 / n = 200/3
	i := rowByName["north"]
	j := colByName["retail"]
	cell := ct.CellComponents[i][j]
	if cell == nil {
		t.Fatalf("CellComponents[north][retail] = nil")
	}
	if got, want := cell["n"], 3; got != want {
		t.Errorf("n = %v, want %v", got, want)
	}
	if got, want := cell["n_null"], 0; got != want {
		t.Errorf("n_null = %v, want %v", got, want)
	}
	if got, want := cell["mean"].(float64), 20.0; got != want {
		t.Errorf("mean = %v, want %v", got, want)
	}
	if got, want := cell["m2"].(float64), 200.0; got != want {
		t.Errorf("m2 = %v, want %v", got, want)
	}
	if got, want := cell["variance"].(float64), 200.0/3.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("variance = %v, want %v", got, want)
	}
	if got, want := mapKeys(cell), []string{"m2", "mean", "n", "n_null", "variance"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cell keys = %v, want %v", got, want)
	}
}

// TestCrosstabComponents_CellComponents_FloorOnly_AGG_COUNT verifies
// the floor-only family: AGG_COUNT's Components() returns nil, so the
// merged cell map carries the universal floor {n, n_null} alone.
func TestCrosstabComponents_CellComponents_FloorOnly_AGG_COUNT(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
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
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	rowByName, colByName := rowColIndex(matrix)

	i := rowByName["south"]
	j := colByName["wholesale"]
	cell := ct.CellComponents[i][j]
	if cell == nil {
		t.Fatalf("CellComponents[south][wholesale] = nil")
	}
	if got, want := cell["n"], 2; got != want {
		t.Errorf("n = %v, want %v", got, want)
	}
	if got, want := cell["n_null"], 0; got != want {
		t.Errorf("n_null = %v, want %v", got, want)
	}
	// AGG_COUNT is floor-only.
	if got, want := mapKeys(cell), []string{"n", "n_null"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cell keys = %v, want %v (floor-only)", got, want)
	}
}

// TestCrosstabComponents_CellComponents_EmptyCellsNil verifies the
// emission contract for empty cells: a (rowKey, colKey) pair that
// received no records emits nil at CellComponents[r][c]. Distinct from
// an all-null bucket which still emits a populated map with n_null > 0.
func TestCrosstabComponents_CellComponents_EmptyCellsNil(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	rowByName, colByName := rowColIndex(matrix)

	// (east, wholesale) is the canonical empty cell on the fixture.
	ei, ok := rowByName["east"]
	if !ok {
		t.Skip("no east row — skipping empty-cell check")
	}
	ej, ok := colByName["wholesale"]
	if !ok {
		t.Skip("no wholesale col — skipping empty-cell check")
	}
	if !matrix.Cells[ei][ej].Present == false {
		t.Errorf("Matrix.Cells[east][wholesale].Present = true, want false (sanity)")
	}
	if ct.CellComponents[ei][ej] != nil {
		t.Errorf("CellComponents[east][wholesale] = %v, want nil", ct.CellComponents[ei][ej])
	}
	// Sanity: non-empty cells must NOT be nil.
	i := rowByName["north"]
	j := colByName["retail"]
	if ct.CellComponents[i][j] == nil {
		t.Errorf("CellComponents[north][retail] = nil, want populated")
	}
}

// TestCrosstabComponents_CellComponents_BufferedVsFused_ParityByteEqual
// is the byte-equal parity gate for CellComponents emission. The same
// crosstab request must produce reflect-equal CellComponents across the
// buffered and fused paths. Drives both with SetDisableCrosstabFusion.
//
// Welford-cell sweep — these emit operator-specific maps via
// MetaAggregator that exercise the cellNNull tracker in the fused path
// and the per-bucket walk in the buffered path. A divergence here
// surfaces as a regression in the orchestrator's per-cell {n, n_null}
// floor or in the cell aggregator's Components() emission.
func TestCrosstabComponents_CellComponents_BufferedVsFused_ParityByteEqual(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	for _, cellAgg := range []types.AggregationType{
		types.AGG_SUM,
		types.AGG_AVERAGE,
		types.AGG_VARIANCE,
		types.AGG_STDDEV,
		types.AGG_WELFORD,
		types.AGG_COUNT,
		types.AGG_MIN,
		types.AGG_MAX,
		types.AGG_RANGE,
	} {
		t.Run(string(cellAgg), func(t *testing.T) {
			buildReq := func() *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: "ct.pulse"},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
						Cell:    &types.Aggregation{Type: cellAgg, Field: "value", Label: "cell"},
						Shape:   types.CrosstabShapeMatrix,
					},
				}
			}
			svcBuf := New(cfg)
			svcBuf.SetDisableCrosstabFusion(true)
			bufResp, err := svcBuf.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("buffered Process: %v", err)
			}
			svcFused := New(cfg)
			fusedResp, err := svcFused.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("fused Process: %v", err)
			}
			bufCT := crosstabCTOrFail(t, bufResp)
			fusedCT := crosstabCTOrFail(t, fusedResp)
			if !reflect.DeepEqual(bufCT.CellComponents, fusedCT.CellComponents) {
				t.Errorf("CellComponents differ for %s:\n buffered=%v\n fused   =%v",
					cellAgg, bufCT.CellComponents, fusedCT.CellComponents)
			}
		})
	}
}

// TestCrosstabComponents_CellComponents_NullInputTracking verifies the
// per-cell n_null floor counter. Uses the nullable crosstab cohort with
// a row whose region is null (excluded from cell counts) and asserts
// that valid cells emit {n, n_null} correctly. The dataset has no
// null VALUES (only null region keys), so n_null per cell stays 0.
//
// Drives both paths so the buffered runCellAggregation walk and the
// fused per-record NumericValue probe in Update both surface their
// floor counters identically.
func TestCrosstabComponents_CellComponents_NullInputTracking(t *testing.T) {
	cfg := writeNullableCrosstabCohort(t, "ct.pulse")
	ctx := context.Background()

	for _, tc := range []struct {
		name        string
		disableFuse bool
	}{
		{"buffered", true},
		{"fused", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(tc.disableFuse)
			req := &types.Request{
				Cohort: &types.Cohort{Filename: "ct.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
					Shape:   types.CrosstabShapeMatrix,
				},
			}
			resp, err := svc.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ct := crosstabCTOrFail(t, resp)
			// Every populated cell must carry both floor keys.
			for i := range ct.CellComponents {
				for j, cell := range ct.CellComponents[i] {
					if cell == nil {
						continue
					}
					if _, ok := cell["n"]; !ok {
						t.Errorf("[%d][%d] missing n", i, j)
					}
					if _, ok := cell["n_null"]; !ok {
						t.Errorf("[%d][%d] missing n_null", i, j)
					}
					// n + n_null must equal CellCounts on the matching coord.
					n, _ := cell["n"].(int)
					nNull, _ := cell["n_null"].(int)
					if n+nNull != ct.CellCounts[i][j] {
						t.Errorf("[%d][%d] n(%d)+n_null(%d) != CellCounts(%d)",
							i, j, n, nNull, ct.CellCounts[i][j])
					}
				}
			}
		})
	}
}

// TestCrosstabComponents_CellComponents_ManifestParity asserts that the
// operator-specific keys emitted per cell match the manifest's
// ComponentsSchemas.Aggregators[<op>].Keys (minus the universal floor
// {n, n_null}). Sweeps one cell aggregator per family represented in
// the built-in registry: scalar (AGG_SUM), Welford (AGG_VARIANCE),
// floor-only (AGG_COUNT). Map-state, composite, order-stat, and set-
// family aggregators are exercised by their own per-operator
// components tests under processing/; this test locks the cell-level
// parity between manifest declaration and runtime emission.
//
// A divergence here means either (a) the cell aggregator's
// Components() emits a key not declared in the manifest, or (b) the
// manifest declares a key the cell aggregator does not emit. Both
// surface the contract drift TestManifestComponentSchemasComplete is
// designed to catch — this is the cell-level companion test.
func TestCrosstabComponents_CellComponents_ManifestParity(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		op   types.AggregationType
	}{
		{"scalar/AGG_SUM", types.AGG_SUM},
		{"welford/AGG_VARIANCE", types.AGG_VARIANCE},
		{"welford-rich/AGG_WELFORD", types.AGG_WELFORD},
		{"floor/AGG_COUNT", types.AGG_COUNT},
		{"floor/AGG_NULL_COUNT", types.AGG_NULL_COUNT},
		{"order/AGG_MIN", types.AGG_MIN},
		{"order/AGG_MAX", types.AGG_MAX},
		{"order/AGG_RANGE", types.AGG_RANGE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(true)
			req := &types.Request{
				Cohort: &types.Cohort{Filename: "ct.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
					Cell:    &types.Aggregation{Type: tc.op, Field: "value", Label: "cell"},
					Shape:   types.CrosstabShapeMatrix,
				},
			}
			resp, err := svc.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ct := crosstabCTOrFail(t, resp)

			// Pick the first populated cell — the operator-key set is
			// the same across every populated cell for a uniform
			// aggregator. We compare the keys MINUS the floor against
			// the manifest's declared key set.
			var sampleCell map[string]any
			for i := range ct.CellComponents {
				for _, cell := range ct.CellComponents[i] {
					if cell != nil {
						sampleCell = cell
						break
					}
				}
				if sampleCell != nil {
					break
				}
			}
			if sampleCell == nil {
				t.Fatalf("no populated cell on fixture for %s", tc.op)
			}
			got := cellOperatorKeys(sampleCell)
			want := manifestAggOperatorKeysForCT(t, string(tc.op))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("operator keys for %s: cell=%v manifest=%v", tc.op, got, want)
			}
		})
	}
}

// mapKeys returns the sorted list of keys on a map[string]any. Used
// throughout the CellComponents tests so the assertions stay
// declarative.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cellOperatorKeys returns the sorted operator-key set on a cell map —
// the universal-floor keys {n, n_null} are stripped so the assertion
// matches the manifest's ComponentSchema.Keys projection.
func cellOperatorKeys(cell map[string]any) []string {
	out := make([]string, 0, len(cell))
	for k := range cell {
		if k == "n" || k == "n_null" {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// manifestAggOperatorKeysForCT returns the operator-key set for the
// named aggregator from the manifest's ComponentsSchemas projection,
// minus the universal floor. Mirrors the helper used by the ungrouped
// process-components tests (manifestAggOperatorKeys) but kept local so
// the crosstab test file is self-contained against helper-rename
// refactors. The descriptor.BuildManifest() projection is the same
// public surface LLM clients consume.
func manifestAggOperatorKeysForCT(t *testing.T, name string) []string {
	t.Helper()
	m := descriptor.BuildManifest()
	schema, ok := m.ComponentsSchemas.Aggregators[name]
	if !ok {
		t.Fatalf("manifest carries no components schema for %s", name)
	}
	out := make([]string, 0, len(schema.Keys))
	for _, k := range schema.Keys {
		if k.Name == "n" || k.Name == "n_null" {
			continue
		}
		out = append(out, k.Name)
	}
	sort.Strings(out)
	return out
}

// --- E3-S4: margin counts + margin components emission ----------------
//
// Margins (row / column / grand) emit their record-count vector +
// per-margin components map only when the matching display flag is set
// on CrosstabSpec.Margins. The components map mirrors the per-cell
// shape: universal floor {n, n_null} merged with the cell aggregator's
// MetaAggregator.Components() output. When MatrixPayload.RowMargins is
// nil (display flag off, even under normalize=row which computes the
// margin internally), the corresponding Components.Crosstab fields stay
// nil/empty (omitempty) so the additive byte-identity contract holds
// against the pre-margin-emission baseline.

// TestCrosstabComponents_MarginsPresent_AllSlotsPopulated checks the
// happy path: a crosstab with row + column + grand display flags emits
// non-nil RowMarginCounts / RowMarginComponents / ColumnMarginCounts /
// ColumnMarginComponents / GrandTotalCount / GrandTotalComponents.
// Drives the buffered path so the cell builder + recompute helpers are
// exercised end-to-end.
func TestCrosstabComponents_MarginsPresent_AllSlotsPopulated(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	if matrix == nil {
		t.Fatalf("Matrix nil")
	}
	if len(matrix.RowMargins) != len(matrix.RowKeys) {
		t.Fatalf("RowMargins=%d, RowKeys=%d", len(matrix.RowMargins), len(matrix.RowKeys))
	}
	if len(ct.RowMarginCounts) != len(matrix.RowKeys) {
		t.Errorf("RowMarginCounts=%d, want %d", len(ct.RowMarginCounts), len(matrix.RowKeys))
	}
	if len(ct.RowMarginComponents) != len(matrix.RowKeys) {
		t.Errorf("RowMarginComponents=%d, want %d", len(ct.RowMarginComponents), len(matrix.RowKeys))
	}
	if len(ct.ColumnMarginCounts) != len(matrix.ColumnKeys) {
		t.Errorf("ColumnMarginCounts=%d, want %d", len(ct.ColumnMarginCounts), len(matrix.ColumnKeys))
	}
	if len(ct.ColumnMarginComponents) != len(matrix.ColumnKeys) {
		t.Errorf("ColumnMarginComponents=%d, want %d", len(ct.ColumnMarginComponents), len(matrix.ColumnKeys))
	}
	if ct.GrandTotalCount == 0 {
		t.Errorf("GrandTotalCount = 0, want >0 (9 filtered rows on the fixture)")
	}
	if ct.GrandTotalComponents == nil {
		t.Errorf("GrandTotalComponents = nil, want populated map")
	}
}

// TestCrosstabComponents_MarginsAbsent_AllSlotsEmpty checks the gate:
// without display flags, the margin slots on Components.Crosstab stay
// nil even though the cell counts/components still emit. This is the
// `omitempty` byte-identity contract — a crosstab request without
// margins produces wire output indistinguishable from the pre-margin-
// emission baseline.
func TestCrosstabComponents_MarginsAbsent_AllSlotsEmpty(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
			// Margins display flags all false → no margin emission.
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	if ct.RowMarginCounts != nil {
		t.Errorf("RowMarginCounts = %v, want nil", ct.RowMarginCounts)
	}
	if ct.RowMarginComponents != nil {
		t.Errorf("RowMarginComponents = %v, want nil", ct.RowMarginComponents)
	}
	if ct.ColumnMarginCounts != nil {
		t.Errorf("ColumnMarginCounts = %v, want nil", ct.ColumnMarginCounts)
	}
	if ct.ColumnMarginComponents != nil {
		t.Errorf("ColumnMarginComponents = %v, want nil", ct.ColumnMarginComponents)
	}
	if ct.GrandTotalCount != 0 {
		t.Errorf("GrandTotalCount = %d, want 0", ct.GrandTotalCount)
	}
	if ct.GrandTotalComponents != nil {
		t.Errorf("GrandTotalComponents = %v, want nil", ct.GrandTotalComponents)
	}
}

// TestCrosstabComponents_MarginSumInvariants verifies the sum-of-row and
// sum-of-column invariants: sum(RowMarginCounts) == GrandTotalCount and
// sum(ColumnMarginCounts) == GrandTotalCount when all three margins are
// displayed. Locks the buffered path's recompute consistency against
// the orchestrator-tracked grand-total counter.
func TestCrosstabComponents_MarginSumInvariants(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "ct.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}

	for _, tc := range []struct {
		name        string
		disableFuse bool
	}{
		{"buffered", true},
		{"fused", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(tc.disableFuse)
			resp, err := svc.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ct := crosstabCTOrFail(t, resp)
			var rowSum int
			for _, n := range ct.RowMarginCounts {
				rowSum += n
			}
			if rowSum != ct.GrandTotalCount {
				t.Errorf("sum(RowMarginCounts)=%d, GrandTotalCount=%d", rowSum, ct.GrandTotalCount)
			}
			var colSum int
			for _, n := range ct.ColumnMarginCounts {
				colSum += n
			}
			if colSum != ct.GrandTotalCount {
				t.Errorf("sum(ColumnMarginCounts)=%d, GrandTotalCount=%d", colSum, ct.GrandTotalCount)
			}
			// The canonical fixture has no null axis rows, so the grand
			// total counter equals FilteredRows.
			if ct.GrandTotalCount != int(resp.Metadata.FilteredRows) {
				t.Errorf("GrandTotalCount=%d, FilteredRows=%d", ct.GrandTotalCount, resp.Metadata.FilteredRows)
			}
		})
	}
}

// TestCrosstabComponents_MarginComponents_WelfordFamily verifies the
// Welford cell aggregator emits {n, n_null, mean, m2, variance} on row
// + column + grand margins. The row margin for "north" aggregates all
// four (region=north) records — values [10, 20, 30, 100] — so the
// components map carries the orchestrator's universal floor (n=4,
// n_null=0) plus the cell aggregator's Welford output.
func TestCrosstabComponents_MarginComponents_WelfordFamily(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_VARIANCE, Field: "value", Label: "var"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	rowByName, colByName := rowColIndex(matrix)

	// north row margin: values [10, 20, 30, 100]
	//   mean = 40
	//   m2   = (10-40)^2 + (20-40)^2 + (30-40)^2 + (100-40)^2 = 5000
	//   variance (population) = m2 / n = 5000/4 = 1250
	nIdx := rowByName["north"]
	rowMarg := ct.RowMarginComponents[nIdx]
	if rowMarg == nil {
		t.Fatalf("RowMarginComponents[north] = nil")
	}
	if got, want := rowMarg["n"], 4; got != want {
		t.Errorf("row[north] n = %v, want %v", got, want)
	}
	if got, want := rowMarg["n_null"], 0; got != want {
		t.Errorf("row[north] n_null = %v, want %v", got, want)
	}
	if got, want := rowMarg["mean"].(float64), 40.0; got != want {
		t.Errorf("row[north] mean = %v, want %v", got, want)
	}
	if got, want := rowMarg["m2"].(float64), 5000.0; got != want {
		t.Errorf("row[north] m2 = %v, want %v", got, want)
	}
	if got, want := rowMarg["variance"].(float64), 1250.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("row[north] variance = %v, want %v", got, want)
	}
	if got, want := mapKeys(rowMarg), []string{"m2", "mean", "n", "n_null", "variance"}; !reflect.DeepEqual(got, want) {
		t.Errorf("row[north] keys = %v, want %v", got, want)
	}

	// column margin for retail: values [10, 20, 30, 5, 15, 1] = 6 records
	rIdx := colByName["retail"]
	colMarg := ct.ColumnMarginComponents[rIdx]
	if colMarg == nil {
		t.Fatalf("ColumnMarginComponents[retail] = nil")
	}
	if got, want := colMarg["n"], 6; got != want {
		t.Errorf("col[retail] n = %v, want %v", got, want)
	}
	keys := mapKeys(colMarg)
	want := []string{"m2", "mean", "n", "n_null", "variance"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("col[retail] keys = %v, want %v", keys, want)
	}

	// grand margin: 9 records total.
	if ct.GrandTotalCount != 9 {
		t.Errorf("GrandTotalCount=%d, want 9", ct.GrandTotalCount)
	}
	if ct.GrandTotalComponents == nil {
		t.Fatalf("GrandTotalComponents = nil")
	}
	if got, want := ct.GrandTotalComponents["n"], 9; got != want {
		t.Errorf("grand n = %v, want %v", got, want)
	}
	if got := mapKeys(ct.GrandTotalComponents); !reflect.DeepEqual(got, want) {
		t.Errorf("grand keys = %v, want %v", got, want)
	}
}

// TestCrosstabComponents_Normalized_MarginsConsistent verifies the
// normalization recompute path: a normalize=row request still emits
// margin counts + components consistent with the recomputed values when
// row margins are also displayed. The recompute path lowers to the
// same runCellAggregation walk the display path uses, so the
// orchestrator's per-margin (n, n_null) floor matches the recomputed
// margin's record count byte-for-byte.
func TestCrosstabComponents_Normalized_MarginsConsistent(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:     types.CrosstabShapeMatrix,
			Margins:   types.CrosstabMargins{Rows: true},
			Normalize: types.CrosstabNormalizeRow,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	if matrix.RowMargins == nil {
		t.Fatalf("MatrixPayload.RowMargins nil — Rows=true should emit")
	}
	if ct.RowMarginCounts == nil {
		t.Fatalf("RowMarginCounts nil — should mirror displayed RowMargins")
	}
	if ct.RowMarginComponents == nil {
		t.Fatalf("RowMarginComponents nil — should mirror displayed RowMargins")
	}
	// Cross-check: sum of cell counts in each row equals the row margin
	// count (single-grouper axes — no null axis records on the fixture).
	for i, rowCounts := range ct.CellCounts {
		var rowSum int
		for _, n := range rowCounts {
			rowSum += n
		}
		if rowSum != ct.RowMarginCounts[i] {
			t.Errorf("row[%d] sum(CellCounts)=%d, RowMarginCounts=%d",
				i, rowSum, ct.RowMarginCounts[i])
		}
	}
	// Column / grand display flags off → corresponding slots stay nil/zero.
	if ct.ColumnMarginCounts != nil {
		t.Errorf("ColumnMarginCounts populated under Margins.Columns=false")
	}
	if ct.GrandTotalCount != 0 {
		t.Errorf("GrandTotalCount=%d under Margins.Grand=false (normalize=row internal margin should not leak)",
			ct.GrandTotalCount)
	}
}

// TestCrosstabComponents_MarginCounts_BufferedVsFused_ParityByteEqual is
// the byte-equal parity gate for margin counts + components: same input
// must produce reflect-equal RowMarginCounts / RowMarginComponents /
// ColumnMarginCounts / ColumnMarginComponents / GrandTotalCount /
// GrandTotalComponents across the buffered and fused paths.
func TestCrosstabComponents_MarginCounts_BufferedVsFused_ParityByteEqual(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	for _, cellAgg := range []types.AggregationType{
		types.AGG_SUM,
		types.AGG_AVERAGE,
		types.AGG_VARIANCE,
		types.AGG_COUNT,
		types.AGG_MIN,
		types.AGG_MAX,
	} {
		t.Run(string(cellAgg), func(t *testing.T) {
			buildReq := func() *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: "ct.pulse"},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
						Cell:    &types.Aggregation{Type: cellAgg, Field: "value", Label: "cell"},
						Shape:   types.CrosstabShapeMatrix,
						Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					},
				}
			}
			svcBuf := New(cfg)
			svcBuf.SetDisableCrosstabFusion(true)
			bufResp, err := svcBuf.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("buffered Process: %v", err)
			}
			svcFused := New(cfg)
			fusedResp, err := svcFused.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("fused Process: %v", err)
			}
			bufCT := crosstabCTOrFail(t, bufResp)
			fusedCT := crosstabCTOrFail(t, fusedResp)
			if !reflect.DeepEqual(bufCT.RowMarginCounts, fusedCT.RowMarginCounts) {
				t.Errorf("RowMarginCounts differ for %s:\n buffered=%v\n fused   =%v",
					cellAgg, bufCT.RowMarginCounts, fusedCT.RowMarginCounts)
			}
			if !reflect.DeepEqual(bufCT.RowMarginComponents, fusedCT.RowMarginComponents) {
				t.Errorf("RowMarginComponents differ for %s:\n buffered=%v\n fused   =%v",
					cellAgg, bufCT.RowMarginComponents, fusedCT.RowMarginComponents)
			}
			if !reflect.DeepEqual(bufCT.ColumnMarginCounts, fusedCT.ColumnMarginCounts) {
				t.Errorf("ColumnMarginCounts differ for %s:\n buffered=%v\n fused   =%v",
					cellAgg, bufCT.ColumnMarginCounts, fusedCT.ColumnMarginCounts)
			}
			if !reflect.DeepEqual(bufCT.ColumnMarginComponents, fusedCT.ColumnMarginComponents) {
				t.Errorf("ColumnMarginComponents differ for %s:\n buffered=%v\n fused   =%v",
					cellAgg, bufCT.ColumnMarginComponents, fusedCT.ColumnMarginComponents)
			}
			if bufCT.GrandTotalCount != fusedCT.GrandTotalCount {
				t.Errorf("GrandTotalCount differ for %s: buffered=%d fused=%d",
					cellAgg, bufCT.GrandTotalCount, fusedCT.GrandTotalCount)
			}
			if !reflect.DeepEqual(bufCT.GrandTotalComponents, fusedCT.GrandTotalComponents) {
				t.Errorf("GrandTotalComponents differ for %s:\n buffered=%v\n fused   =%v",
					cellAgg, bufCT.GrandTotalComponents, fusedCT.GrandTotalComponents)
			}
		})
	}
}

// --- E3-S5: per-axis grouper components emission ---------------------
//
// RowKeyComponents[r] / ColumnKeyComponents[c] carry the per-axis grouper
// bucket emission for the matching axis key. Single-axis crosstabs surface
// the bucket map directly (so consumers can read e.g.
// `RowKeyComponents[r]["count"]` against a GROUP_CATEGORY axis). Multi-axis
// crosstabs wrap each axis position's bucket inside an `axes` slice keyed
// by the axis field name so consumers can identify which position
// contributed which bucket. Vector length matches
// MatrixPayload.RowKeys / ColumnKeys; buffered + fused paths emit
// byte-equal output.

// TestCrosstabComponents_RowKeyComponents_SingleAxis_BucketLayout verifies
// the single-axis common case: each RowKeyComponents[r] entry is the
// MetaGrouper bucket map (carrying {key, label, count} for GROUP_CATEGORY)
// for the matching row key. Length matches MatrixPayload.RowKeys.
func TestCrosstabComponents_RowKeyComponents_SingleAxis_BucketLayout(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	if matrix == nil {
		t.Fatalf("Matrix nil")
	}
	if len(ct.RowKeyComponents) != len(matrix.RowKeys) {
		t.Fatalf("RowKeyComponents len = %d, want %d (RowKeys)",
			len(ct.RowKeyComponents), len(matrix.RowKeys))
	}
	if len(ct.ColumnKeyComponents) != len(matrix.ColumnKeys) {
		t.Fatalf("ColumnKeyComponents len = %d, want %d (ColumnKeys)",
			len(ct.ColumnKeyComponents), len(matrix.ColumnKeys))
	}
	// Each row entry carries a GROUP_CATEGORY bucket: {key, label, count}.
	// Expected counts per region: north=4, south=4, east=1.
	wantCount := map[string]int{"north": 4, "south": 4, "east": 1}
	for i, k := range matrix.RowKeys {
		entry := ct.RowKeyComponents[i]
		if entry == nil {
			t.Errorf("RowKeyComponents[%d] = nil for key=%v", i, k)
			continue
		}
		// Single-axis: bucket map is the entry directly.
		gotKey, _ := entry["key"].(string)
		if gotKey == "" {
			t.Errorf("RowKeyComponents[%d] missing 'key' field: %v", i, entry)
			continue
		}
		wantKey, _ := k[0].(string)
		if gotKey != wantKey {
			t.Errorf("RowKeyComponents[%d].key = %q, want %q (axis tuple)", i, gotKey, wantKey)
		}
		if got, want := entry["count"], wantCount[gotKey]; got != want {
			t.Errorf("RowKeyComponents[%s].count = %v, want %v", gotKey, got, want)
		}
	}
	// Column buckets: retail=6, wholesale=3.
	wantColCount := map[string]int{"retail": 6, "wholesale": 3}
	for j, k := range matrix.ColumnKeys {
		entry := ct.ColumnKeyComponents[j]
		if entry == nil {
			t.Errorf("ColumnKeyComponents[%d] = nil", j)
			continue
		}
		gotKey, _ := entry["key"].(string)
		wantKey, _ := k[0].(string)
		if gotKey != wantKey {
			t.Errorf("ColumnKeyComponents[%d].key = %q, want %q", j, gotKey, wantKey)
		}
		if got, want := entry["count"], wantColCount[gotKey]; got != want {
			t.Errorf("ColumnKeyComponents[%s].count = %v, want %v", gotKey, got, want)
		}
	}
}

// TestCrosstabComponents_RowKeyComponents_OrderMatchesRowKeys locks the
// order invariant: ct.RowKeyComponents[r] addresses the same axis key as
// MatrixPayload.RowKeys[r] across the sorted emission order. Same for
// columns.
func TestCrosstabComponents_RowKeyComponents_OrderMatchesRowKeys(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
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
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix

	for i, tuple := range matrix.RowKeys {
		entry := ct.RowKeyComponents[i]
		if entry == nil {
			t.Errorf("RowKeyComponents[%d] = nil for tuple=%v", i, tuple)
			continue
		}
		gotKey, _ := entry["key"].(string)
		wantKey, _ := tuple[0].(string)
		if gotKey != wantKey {
			t.Errorf("RowKeyComponents[%d].key = %q, want %q (RowKeys[%d][0])",
				i, gotKey, wantKey, i)
		}
	}
	for j, tuple := range matrix.ColumnKeys {
		entry := ct.ColumnKeyComponents[j]
		if entry == nil {
			t.Errorf("ColumnKeyComponents[%d] = nil for tuple=%v", j, tuple)
			continue
		}
		gotKey, _ := entry["key"].(string)
		wantKey, _ := tuple[0].(string)
		if gotKey != wantKey {
			t.Errorf("ColumnKeyComponents[%d].key = %q, want %q (ColumnKeys[%d][0])",
				j, gotKey, wantKey, j)
		}
	}
}

// TestCrosstabComponents_RowKeyComponents_MultiAxis_CompositeLayout
// verifies the multi-axis composite layout: a 2-grouper row axis
// produces RowKeyComponents[r] entries shaped as {"axes": [{"field":
// ..., "bucket": ...}, ...]} carrying one entry per axis position.
// Per the suggested layout in the story, single-axis cases emit the
// bucket directly while multi-axis cases wrap each axis position so
// consumers can identify the contributing axis field.
func TestCrosstabComponents_RowKeyComponents_MultiAxis_CompositeLayout(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	svc := New(cfg)
	svc.SetDisableCrosstabFusion(true)
	ctx := context.Background()

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			// 2-axis rows = (region, segment); single-axis columns = (segment).
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:  &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape: types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ct := crosstabCTOrFail(t, resp)
	matrix := resp.Crosstab.Matrix
	if len(ct.RowKeyComponents) != len(matrix.RowKeys) {
		t.Fatalf("RowKeyComponents len = %d, want %d", len(ct.RowKeyComponents), len(matrix.RowKeys))
	}
	for i, tuple := range matrix.RowKeys {
		entry := ct.RowKeyComponents[i]
		if entry == nil {
			t.Errorf("RowKeyComponents[%d] = nil for tuple=%v", i, tuple)
			continue
		}
		axes, ok := entry["axes"].([]map[string]any)
		if !ok {
			t.Errorf("RowKeyComponents[%d] missing 'axes' slice: %v", i, entry)
			continue
		}
		if len(axes) != 2 {
			t.Errorf("RowKeyComponents[%d].axes len = %d, want 2 (one per axis position)",
				i, len(axes))
			continue
		}
		// Position 0 → field=region, bucket.key=tuple[0]
		// Position 1 → field=segment, bucket.key=tuple[1]
		wantFields := []string{"region", "segment"}
		for p, axisEntry := range axes {
			gotField, _ := axisEntry["field"].(string)
			if gotField != wantFields[p] {
				t.Errorf("RowKeyComponents[%d].axes[%d].field = %q, want %q",
					i, p, gotField, wantFields[p])
			}
			bucket, ok := axisEntry["bucket"].(map[string]any)
			if !ok {
				t.Errorf("RowKeyComponents[%d].axes[%d].bucket not a map: %v",
					i, p, axisEntry["bucket"])
				continue
			}
			wantKey, _ := tuple[p].(string)
			gotKey, _ := bucket["key"].(string)
			if gotKey != wantKey {
				t.Errorf("RowKeyComponents[%d].axes[%d].bucket.key = %q, want %q (tuple[%d])",
					i, p, gotKey, wantKey, p)
			}
		}
	}
	// Column axis is single-axis so the bucket map is emitted directly.
	for j, tuple := range matrix.ColumnKeys {
		entry := ct.ColumnKeyComponents[j]
		if entry == nil {
			t.Errorf("ColumnKeyComponents[%d] = nil for tuple=%v", j, tuple)
			continue
		}
		// Should NOT have an "axes" wrapper (single-axis case).
		if _, ok := entry["axes"]; ok {
			t.Errorf("ColumnKeyComponents[%d] has unexpected 'axes' wrapper (single-axis column should emit bucket directly): %v",
				j, entry)
		}
		gotKey, _ := entry["key"].(string)
		wantKey, _ := tuple[0].(string)
		if gotKey != wantKey {
			t.Errorf("ColumnKeyComponents[%d].key = %q, want %q", j, gotKey, wantKey)
		}
	}
}

// TestCrosstabComponents_RowKeyComponents_BufferedVsFused_ParityByteEqual
// is the byte-equal parity gate for axis-key component emission: the same
// crosstab request must produce reflect-equal RowKeyComponents +
// ColumnKeyComponents across the buffered and fused paths. Sweeps a
// single-axis case + a multi-axis (composite-layout) case so both shapes
// are exercised.
func TestCrosstabComponents_RowKeyComponents_BufferedVsFused_ParityByteEqual(t *testing.T) {
	schema := crosstabSchema()
	cfg := setupTestFS(t, "ct.pulse", schema, crosstabRecords())
	ctx := context.Background()

	cases := []struct {
		name string
		req  func() *types.Request
	}{
		{
			name: "single-axis",
			req: func() *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: "ct.pulse"},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
						Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
						Shape:   types.CrosstabShapeMatrix,
					},
				}
			},
		},
		{
			name: "multi-axis-rows",
			req: func() *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: "ct.pulse"},
					Crosstab: &types.CrosstabSpec{
						Rows: []*types.Group{
							{Type: types.GROUP_CATEGORY, Field: "region"},
							{Type: types.GROUP_CATEGORY, Field: "segment"},
						},
						Columns: []*types.Group{
							{Type: types.GROUP_CATEGORY, Field: "segment"},
						},
						Cell:  &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
						Shape: types.CrosstabShapeMatrix,
					},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svcBuf := New(cfg)
			svcBuf.SetDisableCrosstabFusion(true)
			bufResp, err := svcBuf.Process(ctx, tc.req())
			if err != nil {
				t.Fatalf("buffered Process: %v", err)
			}
			svcFused := New(cfg)
			fusedResp, err := svcFused.Process(ctx, tc.req())
			if err != nil {
				t.Fatalf("fused Process: %v", err)
			}
			bufCT := crosstabCTOrFail(t, bufResp)
			fusedCT := crosstabCTOrFail(t, fusedResp)
			if !reflect.DeepEqual(bufCT.RowKeyComponents, fusedCT.RowKeyComponents) {
				t.Errorf("RowKeyComponents differ:\n buffered=%v\n fused   =%v",
					bufCT.RowKeyComponents, fusedCT.RowKeyComponents)
			}
			if !reflect.DeepEqual(bufCT.ColumnKeyComponents, fusedCT.ColumnKeyComponents) {
				t.Errorf("ColumnKeyComponents differ:\n buffered=%v\n fused   =%v",
					bufCT.ColumnKeyComponents, fusedCT.ColumnKeyComponents)
			}
		})
	}
}

// --- E3-S10: consolidated parity sweep -------------------------------
//
// TestCrosstabComponents_BufferedVsFused_SweepParity is the single
// consolidated buffered-vs-fused parity gate. It exercises the matrix
// of (cell aggregator family) × (axis grouper pairing) the earlier per-
// slot parity tests left for the wrap-up story (E3-S10):
//
//   - Aggregator families on the cell:
//   - scalar:        AGG_SUM
//   - Welford:       AGG_VARIANCE
//   - map-state:     AGG_FREQUENCY  (fused-eligible: mergeable +
//     scalar margin + non-MapValued)
//   - order-stat:    AGG_MEDIAN     (non-mergeable → fused gate
//     rejects, both runs take the buffered path,
//     components emit only on terminal flush — this
//     locks the same-path invariant)
//   - Axis pairings on the (rows × columns) crosstab:
//   - GROUP_CATEGORY × GROUP_CATEGORY  (canonical)
//   - GROUP_RANGE    × GROUP_CATEGORY  (numeric row binning)
//   - GROUP_CATEGORY × GROUP_DATE      (date column binning)
//
// For every (aggregator, pairing) combination the test asserts:
//
//   - reflect.DeepEqual on every Components.Crosstab sub-slice
//     (CellCounts, CellComponents, RowKeyComponents, ColumnKeyComponents,
//     IncludedRecords, ExcludedRecords). Margin slots are off by default
//     here — margins have their own dedicated parity tests (above) so
//     this sweep stays focused on cell + axis-key shape parity.
//
// Empty / zero-row axes are guarded — every combo must produce at least
// one populated cell on the fixture so the parity diff is meaningful.
//
// AGG_SET_UNION + GROUP_SET_VALUE are NOT swept here. The set-family
// cell aggregator + set-axis grouper run on a distinct field type
// (FieldTypeSetU8) that the canonical (region/segment/value/date)
// fixture does not carry; their parity is covered by the dedicated
// processing/crosstab_set_test.go suite which exercises the buffered
// path directly via Processor.RunCrosstab.
func TestCrosstabComponents_BufferedVsFused_SweepParity(t *testing.T) {
	cfg := writeSweepCohort(t, "ct_sweep.pulse")
	ctx := context.Background()

	cellAggs := []struct {
		name string
		op   types.AggregationType
	}{
		{"scalar/AGG_SUM", types.AGG_SUM},
		{"welford/AGG_VARIANCE", types.AGG_VARIANCE},
		{"mapstate/AGG_FREQUENCY", types.AGG_FREQUENCY},
		// AGG_MEDIAN is non-mergeable → fused gate rejects (both runs
		// land on the buffered path, parity is trivially identical
		// but emission must still produce the BufferedComponents=true
		// per-cell map on terminal flush).
		{"orderstat/AGG_MEDIAN", types.AGG_MEDIAN},
	}

	type axisCase struct {
		name string
		rows []*types.Group
		cols []*types.Group
	}
	fiscalOffset := 0
	axisCases := []axisCase{
		{
			name: "CATEGORY_x_CATEGORY",
			rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			cols: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		},
		{
			name: "RANGE_x_CATEGORY",
			rows: []*types.Group{{Type: types.GROUP_RANGE, Field: "value", Interval: 25}},
			cols: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		},
		{
			name: "CATEGORY_x_DATE",
			rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			cols: []*types.Group{{
				Type:   types.GROUP_DATE,
				Field:  "wave_date",
				Params: dateParams("quarter", &fiscalOffset),
			}},
		},
	}

	for _, agg := range cellAggs {
		agg := agg
		for _, axes := range axisCases {
			axes := axes
			t.Run(agg.name+"/"+axes.name, func(t *testing.T) {
				buildReq := func() *types.Request {
					return &types.Request{
						Cohort: &types.Cohort{Filename: "ct_sweep.pulse"},
						Crosstab: &types.CrosstabSpec{
							Rows:    axes.rows,
							Columns: axes.cols,
							Cell:    &types.Aggregation{Type: agg.op, Field: "value", Label: "cell"},
							Shape:   types.CrosstabShapeMatrix,
						},
					}
				}

				svcBuf := New(cfg)
				svcBuf.SetDisableCrosstabFusion(true)
				bufResp, err := svcBuf.Process(ctx, buildReq())
				if err != nil {
					t.Fatalf("buffered Process: %v", err)
				}

				svcFused := New(cfg)
				// Fusion enabled by default; the dispatch falls back
				// to buffered internally if the gate rejects.
				fusedResp, err := svcFused.Process(ctx, buildReq())
				if err != nil {
					t.Fatalf("fused Process: %v", err)
				}

				bufCT := crosstabCTOrFail(t, bufResp)
				fusedCT := crosstabCTOrFail(t, fusedResp)

				// Sanity: at least one populated cell on the fixture
				// across every combo so the parity diff is meaningful.
				bufMatrix := bufResp.Crosstab.Matrix
				if bufMatrix == nil || len(bufMatrix.RowKeys) == 0 || len(bufMatrix.ColumnKeys) == 0 {
					t.Fatalf("buffered matrix degenerate for %s/%s: rows=%d cols=%d",
						agg.op, axes.name, len(bufMatrix.RowKeys), len(bufMatrix.ColumnKeys))
				}
				var populated int
				for _, row := range bufCT.CellCounts {
					for _, n := range row {
						if n > 0 {
							populated++
						}
					}
				}
				if populated == 0 {
					t.Fatalf("no populated cells for %s/%s — fixture coverage gap", agg.op, axes.name)
				}

				if !reflect.DeepEqual(bufCT.CellCounts, fusedCT.CellCounts) {
					t.Errorf("CellCounts differ:\n buffered=%v\n fused   =%v",
						bufCT.CellCounts, fusedCT.CellCounts)
				}
				if !reflect.DeepEqual(bufCT.CellComponents, fusedCT.CellComponents) {
					t.Errorf("CellComponents differ:\n buffered=%v\n fused   =%v",
						bufCT.CellComponents, fusedCT.CellComponents)
				}
				if !reflect.DeepEqual(bufCT.RowKeyComponents, fusedCT.RowKeyComponents) {
					t.Errorf("RowKeyComponents differ:\n buffered=%v\n fused   =%v",
						bufCT.RowKeyComponents, fusedCT.RowKeyComponents)
				}
				if !reflect.DeepEqual(bufCT.ColumnKeyComponents, fusedCT.ColumnKeyComponents) {
					t.Errorf("ColumnKeyComponents differ:\n buffered=%v\n fused   =%v",
						bufCT.ColumnKeyComponents, fusedCT.ColumnKeyComponents)
				}
				if bufCT.IncludedRecords != fusedCT.IncludedRecords {
					t.Errorf("IncludedRecords differ: buffered=%d fused=%d",
						bufCT.IncludedRecords, fusedCT.IncludedRecords)
				}
				if bufCT.ExcludedRecords != fusedCT.ExcludedRecords {
					t.Errorf("ExcludedRecords differ: buffered=%d fused=%d",
						bufCT.ExcludedRecords, fusedCT.ExcludedRecords)
				}
			})
		}
	}
}

// sweepCrosstabSchema mirrors crosstabSchema() but adds a wave_date date
// field so the parity sweep can pair CATEGORY × DATE without an extra
// fixture file. Field layout: region (cat_u8), segment (cat_u8), value
// (f64), wave_date (date).
func sweepCrosstabSchema() *encoding.Schema {
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
			{Name: "wave_date", Type: encoding.FieldTypeDate, ByteOffset: 10, CsvColumnIdx: 3},
		},
	}
}

// writeSweepCohort builds a 9-row cohort for the consolidated parity
// sweep. Rows mirror crosstabRecords() (so the CATEGORY × CATEGORY
// pairing produces the same shape the per-slot parity tests already
// lock) with deterministic wave_date values spread across two quarters
// of 2024 so the GROUP_DATE column axis produces ≥2 buckets.
func writeSweepCohort(t *testing.T, path string) *fs.Config {
	t.Helper()
	schema := sweepCrosstabSchema()

	// Date values: days since unix epoch (1970-01-01).
	//   Q1 2024 anchor: 2024-01-15 → 19737 days
	//   Q2 2024 anchor: 2024-05-15 → 19858 days
	const q1Day, q2Day = uint64(19737), uint64(19858)

	type rec struct {
		region, segment uint64
		value           float64
		day             uint64
	}
	recs := []rec{
		// (region, segment, value, day) — same shape as crosstabRecords()
		// for value, with dates split half-and-half across two quarters.
		{0, 0, 10, q1Day},  // north, retail, Q1
		{0, 0, 20, q1Day},  // north, retail, Q1
		{0, 0, 30, q2Day},  // north, retail, Q2
		{0, 1, 100, q1Day}, // north, wholesale, Q1
		{1, 0, 5, q1Day},   // south, retail, Q1
		{1, 0, 15, q2Day},  // south, retail, Q2
		{1, 1, 40, q2Day},  // south, wholesale, Q2
		{1, 1, 50, q2Day},  // south, wholesale, Q2
		{2, 0, 1, q1Day},   // east, retail, Q1
	}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range recs {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, r.region); err != nil {
			t.Fatalf("WriteFieldValue region: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, r.segment); err != nil {
			t.Fatalf("WriteFieldValue segment: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(r.value)); err != nil {
			t.Fatalf("WriteFieldValue value: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeDate, r.day); err != nil {
			t.Fatalf("WriteFieldValue wave_date: %v", err)
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}
