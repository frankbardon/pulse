package service

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"testing"

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
		region   uint64
		segment  uint64
		value    uint64
		regNull  bool
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
