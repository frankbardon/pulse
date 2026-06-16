package processing

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// fusedCrosstabSchema mirrors the (region, segment, value) shape used in
// service/crosstab_test.go but lives in the processing package so the
// fused-equivalence tests can drive the accumulator directly without
// crossing the service boundary.
func fusedCrosstabSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south", "east"} {
		if _, err := regionDict.Add(r); err != nil {
			t.Fatalf("region dict.Add: %v", err)
		}
	}
	segmentDict := encoding.NewDictionary()
	for _, s := range []string{"retail", "wholesale"} {
		if _, err := segmentDict.Add(s); err != nil {
			t.Fatalf("segment dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: regionDict},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8, Dictionary: segmentDict},
			{Name: "value", Type: encoding.FieldTypeF64},
		},
	}
}

// fusedCrosstabRecords builds the same canonical fixture the buffered
// tests use:
//
//	(north, retail):    [10, 20, 30]
//	(north, wholesale): [100]
//	(south, retail):    [5, 15]
//	(south, wholesale): [40, 50]
//	(east,  retail):    [1]
func fusedCrosstabRecords(schema *encoding.Schema) []*Record {
	mk := func(region, segment uint64, value float64) *Record {
		return NewRecord(schema, map[string]float64{
			"region":  float64(region),
			"segment": float64(segment),
			"value":   value,
		})
	}
	return []*Record{
		mk(0, 0, 10),
		mk(0, 0, 20),
		mk(0, 0, 30),
		mk(0, 1, 100),
		mk(1, 0, 5),
		mk(1, 0, 15),
		mk(1, 1, 40),
		mk(1, 1, 50),
		mk(2, 0, 1),
	}
}

// runFusedCrosstab drives the fused state with a record stream and
// returns the resulting Response.
func runFusedCrosstab(t *testing.T, schema *encoding.Schema, req *types.Request, recs []*Record) *types.Response {
	t.Helper()
	state, err := NewFusedCrosstabState(req.Crosstab, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	if err := state.AssertCanFuse(req); err != nil {
		t.Fatalf("AssertCanFuse: %v", err)
	}
	for _, r := range recs {
		state.AddTotalRow()
		if err := state.Update(r); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	resp, err := state.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return resp
}

// runBufferedCrosstab runs the legacy RunCrosstab path on the same
// record stream so the equivalence assertions can compare outputs
// element-wise.
func runBufferedCrosstab(t *testing.T, schema *encoding.Schema, req *types.Request, recs []*Record) *types.Response {
	t.Helper()
	p := NewProcessor(schema)
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	return resp
}

// assertMatrixEqual deep-compares two MatrixPayloads for byte-equal
// emission. Cells, margins, headers, normalize-applied, and cell label
// must all match. The comparison goes through reflect.DeepEqual after
// normalising the slice nil/empty distinction the JSON layer treats
// equivalently.
func assertMatrixEqual(t *testing.T, want, got *types.MatrixPayload) {
	t.Helper()
	if want == nil && got == nil {
		return
	}
	if want == nil || got == nil {
		t.Fatalf("matrix nil mismatch: want=%v got=%v", want, got)
	}
	if !reflect.DeepEqual(want.RowHeader, got.RowHeader) {
		t.Errorf("RowHeader: want %+v got %+v", want.RowHeader, got.RowHeader)
	}
	if !reflect.DeepEqual(want.ColumnHeader, got.ColumnHeader) {
		t.Errorf("ColumnHeader: want %+v got %+v", want.ColumnHeader, got.ColumnHeader)
	}
	if !reflect.DeepEqual(want.RowKeys, got.RowKeys) {
		t.Errorf("RowKeys: want %v got %v", want.RowKeys, got.RowKeys)
	}
	if !reflect.DeepEqual(want.ColumnKeys, got.ColumnKeys) {
		t.Errorf("ColumnKeys: want %v got %v", want.ColumnKeys, got.ColumnKeys)
	}
	if !reflect.DeepEqual(want.Cells, got.Cells) {
		t.Errorf("Cells: want %v got %v", want.Cells, got.Cells)
	}
	if !reflect.DeepEqual(want.RowMargins, got.RowMargins) {
		t.Errorf("RowMargins: want %v got %v", want.RowMargins, got.RowMargins)
	}
	if !reflect.DeepEqual(want.ColumnMargins, got.ColumnMargins) {
		t.Errorf("ColumnMargins: want %v got %v", want.ColumnMargins, got.ColumnMargins)
	}
	if !reflect.DeepEqual(want.GrandTotal, got.GrandTotal) {
		t.Errorf("GrandTotal: want %v got %v", want.GrandTotal, got.GrandTotal)
	}
	if want.CellLabel != got.CellLabel {
		t.Errorf("CellLabel: want %q got %q", want.CellLabel, got.CellLabel)
	}
	if want.NormalizeApplied != got.NormalizeApplied {
		t.Errorf("NormalizeApplied: want %q got %q", want.NormalizeApplied, got.NormalizeApplied)
	}
}

// assertMetadataEqual checks the response metadata block. TotalRows and
// FilteredRows must match — the orchestrator uses them downstream for
// observability + downstream tests.
func assertMetadataEqual(t *testing.T, want, got *types.ResponseMetadata) {
	t.Helper()
	if want == nil && got == nil {
		return
	}
	if want == nil || got == nil {
		t.Fatalf("metadata nil mismatch: want=%v got=%v", want, got)
	}
	if want.TotalRows != got.TotalRows {
		t.Errorf("TotalRows: want %d got %d", want.TotalRows, got.TotalRows)
	}
	if want.FilteredRows != got.FilteredRows {
		t.Errorf("FilteredRows: want %d got %d", want.FilteredRows, got.FilteredRows)
	}
}

// TestFusedCrosstab_BasicAggSumMatchesBuffered exercises the most
// common case: scalar AGG_SUM cell, single-grouper row + column axis,
// no margins, no normalization. Fused MatrixPayload must equal the
// buffered MatrixPayload byte-for-byte.
func TestFusedCrosstab_BasicAggSumMatchesBuffered(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecords(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)

	if want.Crosstab == nil || got.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: want=%v got=%v", want.Crosstab, got.Crosstab)
	}
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)
}

// TestFusedCrosstab_MarginsMatchBuffered exercises every margin combo:
// rows-only, columns-only, grand-only, all three, none. Each margin
// combination must produce byte-equal MatrixPayload (including the
// margin slices' Present flags) on the fused vs buffered paths.
func TestFusedCrosstab_MarginsMatchBuffered(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecords(schema)
	cases := []struct {
		name    string
		margins types.CrosstabMargins
	}{
		{"NoMargins", types.CrosstabMargins{}},
		{"RowsOnly", types.CrosstabMargins{Rows: true}},
		{"ColumnsOnly", types.CrosstabMargins{Columns: true}},
		{"GrandOnly", types.CrosstabMargins{Grand: true}},
		{"AllMargins", types.CrosstabMargins{Rows: true, Columns: true, Grand: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
					Shape:   types.CrosstabShapeMatrix,
					Margins: tc.margins,
				},
			}
			want := runBufferedCrosstab(t, schema, req, recs)
			got := runFusedCrosstab(t, schema, req, recs)
			assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
			assertMetadataEqual(t, want.Metadata, got.Metadata)
		})
	}
}

// TestFusedCrosstab_NormalizeWithinMatchesBuffered exercises the
// cross-axis denominator path: a 2-grouper column axis with
// Normalize=row + NormalizeWithin=0 fixes the outer column prefix. The
// canonical benchmark's shape, and the path most likely to drift
// between the two implementations.
func TestFusedCrosstab_NormalizeWithinMatchesBuffered(t *testing.T) {
	schema := normalizeWithinSchema(t)
	recs := normalizeWithinRecords(schema)
	zero := 0
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "brand"},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "wavedate"},
				{Type: types.GROUP_CATEGORY, Field: "xxx"},
			},
			Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:           types.CrosstabShapeMatrix,
			Normalize:       types.CrosstabNormalizeRow,
			NormalizeWithin: &zero,
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)

	// Cross-axis denominator semantics: cells in each (brand, wavedate)
	// slab of xxx must sum to 1.0 — the canonical shape of the
	// normalize_within contract. Lift the assertion out of the
	// reflect.DeepEqual umbrella so a future cell-shape change still
	// surfaces a legible failure.
	m := got.Crosstab.Matrix
	for i := range m.RowKeys {
		// Walk the column axis grouping cells by outer wavedate prefix.
		bySlab := map[string]float64{}
		for j, ck := range m.ColumnKeys {
			if !m.Cells[i][j].Present {
				continue
			}
			outer := ck[0].(string)
			bySlab[outer] += m.Cells[i][j].Scalar()
		}
		for slab, sum := range bySlab {
			if sum < 0.999999 || sum > 1.000001 {
				t.Errorf("brand=%v wavedate=%v slab sums to %v, want 1.0",
					m.RowKeys[i], slab, sum)
			}
		}
	}
}

// TestFusedCrosstab_NormalizeLevelMatchesBuffered exercises partial-
// depth normalization on a 2-grouper row axis. Normalize=row with
// NormalizeLevel=0 selects the parent (region) as the denominator;
// cells in each (region, segment) row tuple must sum to the same value
// as the buffered path emits.
func TestFusedCrosstab_NormalizeLevelMatchesBuffered(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecords(schema)
	zero := 0
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:          types.CrosstabShapeMatrix,
			Normalize:      types.CrosstabNormalizeRow,
			NormalizeLevel: &zero,
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)
}

// TestFusedCrosstab_LongShapeMatchesBuffered exercises the long-shape
// emission path: cell rows, then margin rows (rows / columns / grand /
// partial-depth) all in the same order the buffered emitter produces.
// Comparison normalises Response.Data row order before deep-equal —
// neither path guarantees a stable sort across runs, but both must
// produce the same multiset of rows.
func TestFusedCrosstab_LongShapeMatchesBuffered(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecords(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeLong,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)

	if want.Crosstab == nil || got.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: want=%v got=%v", want.Crosstab, got.Crosstab)
	}
	if want.Crosstab.Shape != got.Crosstab.Shape {
		t.Errorf("Shape: want %q got %q", want.Crosstab.Shape, got.Crosstab.Shape)
	}
	if want.Crosstab.Matrix != nil || got.Crosstab.Matrix != nil {
		t.Error("expected Matrix payload nil on long-shape result")
	}

	assertLongRowsEqual(t, want.Data, got.Data)
	assertMetadataEqual(t, want.Metadata, got.Metadata)
}

// assertLongRowsEqual compares two long-form row sequences after
// canonicalising each row to a deterministic key. The buffered emitter
// walks (row-axis-keys × col-axis-keys) in sorted composite-key order,
// then row-margin tuples in axis order, then column-margin tuples, then
// grand, then partial-depth margins. The fused emitter must produce the
// same shape — we don't enforce identical pointer equality (rows are
// fresh maps on both paths) but the canonical sort makes deep-equal
// robust to map-iteration nondeterminism that snuck into either side.
func assertLongRowsEqual(t *testing.T, want, got []map[string]any) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("long-shape row count: want %d got %d", len(want), len(got))
	}
	wantSorted := canonicaliseLongRows(want)
	gotSorted := canonicaliseLongRows(got)
	if !reflect.DeepEqual(wantSorted, gotSorted) {
		t.Errorf("long-shape rows mismatch:\nwant=%v\ngot=%v", wantSorted, gotSorted)
	}
}

// canonicaliseLongRows sorts the row sequence by every key's stringified
// value so a stable comparison is possible across runs. The crosstab
// emitter writes deterministic columns; we use that to derive each row's
// canonical sort key without inventing one.
func canonicaliseLongRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool {
		return canonicalKey(out[i]) < canonicalKey(out[j])
	})
	return out
}

func canonicalKey(row map[string]any) string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb sortableBuilder
	for _, k := range keys {
		sb.append(k)
		sb.append("=")
		sb.append(formatAny(row[k]))
		sb.append("|")
	}
	return sb.String()
}

type sortableBuilder struct{ parts []string }

func (b *sortableBuilder) append(s string) { b.parts = append(b.parts, s) }
func (b *sortableBuilder) String() string {
	total := 0
	for _, p := range b.parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range b.parts {
		out = append(out, p...)
	}
	return string(out)
}

func formatAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "<nil>"
	default:
		return formatFloat(coerceFloat64(v))
	}
}

func formatFloat(v float64) string {
	return formatRoundedNumeric(v)
}

// normalizeWithinSchema builds a (brand, wavedate, xxx, value) schema
// shaped after the canonical normalize_within benchmark from
// crosstab-guide.md. Each axis grouper carries a small dictionary so
// the equivalence tests run end-to-end without disk I/O.
func normalizeWithinSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	brandDict := encoding.NewDictionary()
	for _, b := range []string{"acme", "bigco"} {
		if _, err := brandDict.Add(b); err != nil {
			t.Fatalf("brand dict.Add: %v", err)
		}
	}
	waveDict := encoding.NewDictionary()
	for _, w := range []string{"2024Q1", "2024Q2"} {
		if _, err := waveDict.Add(w); err != nil {
			t.Fatalf("wave dict.Add: %v", err)
		}
	}
	xxxDict := encoding.NewDictionary()
	for _, x := range []string{"low", "mid", "high"} {
		if _, err := xxxDict.Add(x); err != nil {
			t.Fatalf("xxx dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brandDict},
			{Name: "wavedate", Type: encoding.FieldTypeCategoricalU8, Dictionary: waveDict},
			{Name: "xxx", Type: encoding.FieldTypeCategoricalU8, Dictionary: xxxDict},
			{Name: "value", Type: encoding.FieldTypeF64},
		},
	}
}

// fusedCrosstabRecordsWithNulls builds a (region, segment, value) record
// stream where some records have a null row key (region) and others have
// a null column key (segment). Used by the per-axis-null equivalence
// tests added in E4-S4Q. The buffered RunCrosstab path captures these
// records on the non-null axis margin (and the grand margin) regardless
// of the partner axis nullity; the fused path must do the same.
//
// Layout:
//
//	(north, retail):     [10, 20]           — placed cells
//	(north, NULL):       [50, 60]           — row margin contribution only
//	(NULL,  wholesale):  [100, 110]         — column margin contribution only
//	(NULL,  NULL):       [200]              — grand margin contribution only
//	(south, retail):     [5]                — placed cells
//	(east,  NULL):       [1, 2, 3]          — row margin only; entire row's
//	                                          col keys are null so this row
//	                                          appears in RowKeys with no
//	                                          placed cells
func fusedCrosstabRecordsWithNulls(schema *encoding.Schema) []*Record {
	mk := func(region, segment uint64, value float64) *Record {
		return NewRecord(schema, map[string]float64{
			"region":  float64(region),
			"segment": float64(segment),
			"value":   value,
		})
	}
	mkRowNull := func(segment uint64, value float64) *Record {
		r := NewRecord(schema, map[string]float64{
			"segment": float64(segment),
			"value":   value,
		})
		r.SetNull("region")
		return r
	}
	mkColNull := func(region uint64, value float64) *Record {
		r := NewRecord(schema, map[string]float64{
			"region": float64(region),
			"value":  value,
		})
		r.SetNull("segment")
		return r
	}
	mkBothNull := func(value float64) *Record {
		r := NewRecord(schema, map[string]float64{"value": value})
		r.SetNull("region")
		r.SetNull("segment")
		return r
	}
	return []*Record{
		mk(0, 0, 10), mk(0, 0, 20), // (north, retail)
		mkColNull(0, 50), mkColNull(0, 60), // (north, NULL)
		mkRowNull(1, 100), mkRowNull(1, 110), // (NULL, wholesale)
		mkBothNull(200), // (NULL, NULL)
		mk(1, 0, 5),     // (south, retail)
		mkColNull(2, 1), // (east, NULL)
		mkColNull(2, 2), // (east, NULL)
		mkColNull(2, 3), // (east, NULL)
	}
}

// TestFusedCrosstab_RowNullColValidStillFeedsColMargin: records whose
// row key is null but column key is valid must contribute to the column
// margin in the fused path, mirroring the buffered RunCrosstab path
// where PartitionByAxis(spec.Columns, filtered) groups records by column
// key regardless of row key nullity. Pre-E4-S4Q the fused path skipped
// the entire record on any axis nullity — those rows disappeared from
// the column margin.
func TestFusedCrosstab_RowNullColValidStillFeedsColMargin(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecordsWithNulls(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Columns: true, Grand: true},
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)

	if want.Crosstab == nil || got.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: want=%v got=%v", want.Crosstab, got.Crosstab)
	}
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)

	// Spot-check: the wholesale column margin must include the
	// (NULL, wholesale) records' value contribution (100 + 110 = 210).
	// We don't hard-code the expected sum here; the buffered/fused
	// equivalence above is the load-bearing assertion. This spot check
	// guards against silent regression in the buffered path itself.
	m := got.Crosstab.Matrix
	for i, ck := range m.ColumnKeys {
		if len(ck) > 0 && ck[0] == "wholesale" {
			if !m.ColumnMargins[i].Present {
				t.Errorf("wholesale column margin must be present; got absent")
			}
			if m.ColumnMargins[i].Scalar() < 210 {
				t.Errorf("wholesale column margin = %v; want >= 210 (null-region contributions)",
					m.ColumnMargins[i].Scalar())
			}
		}
	}
}

// TestFusedCrosstab_ColNullRowValidStillFeedsRowMargin: mirror of the
// above — records whose column key is null but row key is valid must
// contribute to the row margin in the fused path. Pre-E4-S4Q those rows
// were dropped entirely.
func TestFusedCrosstab_ColNullRowValidStillFeedsRowMargin(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecordsWithNulls(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Grand: true},
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)

	if want.Crosstab == nil || got.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: want=%v got=%v", want.Crosstab, got.Crosstab)
	}
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)

	// Spot-check: the north row margin must include the (north, NULL)
	// records' value contribution (10 + 20 + 50 + 60 = 140).
	m := got.Crosstab.Matrix
	for i, rk := range m.RowKeys {
		if len(rk) > 0 && rk[0] == "north" {
			if !m.RowMargins[i].Present {
				t.Errorf("north row margin must be present; got absent")
			}
			if m.RowMargins[i].Scalar() < 140 {
				t.Errorf("north row margin = %v; want >= 140 (null-segment contributions)",
					m.RowMargins[i].Scalar())
			}
		}
	}
}

// TestFusedCrosstab_RowKeysIncludeAllSeenBrands: the canonical real-world
// divergence — a row whose every record carries a null column key must
// still appear in RowKeys (matching buffered PartitionByAxis(spec.Rows,
// filtered) behaviour). Pre-E4-S4Q the fused path dropped these rows
// entirely because Update returned early on any axis nullity.
func TestFusedCrosstab_RowKeysIncludeAllSeenBrands(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecordsWithNulls(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true},
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)

	if want.Crosstab == nil || got.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: want=%v got=%v", want.Crosstab, got.Crosstab)
	}
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)

	// "east" appears only via records whose segment is null — assert it
	// shows up in the fused RowKeys.
	m := got.Crosstab.Matrix
	foundEast := false
	for _, rk := range m.RowKeys {
		if len(rk) > 0 && rk[0] == "east" {
			foundEast = true
			break
		}
	}
	if !foundEast {
		t.Errorf("expected 'east' in RowKeys; got %v (regression of pre-E4-S4Q axis-null bug)",
			m.RowKeys)
	}
}

// TestFusedCrosstab_GrandMarginCountsAllPostFilterRows: the grand margin
// must aggregate over every filter-passing record regardless of axis-key
// nullity. The buffered RunCrosstab applies the cell aggregator to the
// raw `filtered` slice; the fused path must drive grandMargin from every
// Update call.
func TestFusedCrosstab_GrandMarginCountsAllPostFilterRows(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	recs := fusedCrosstabRecordsWithNulls(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Grand: true},
		},
	}
	want := runBufferedCrosstab(t, schema, req, recs)
	got := runFusedCrosstab(t, schema, req, recs)

	if want.Crosstab == nil || got.Crosstab == nil {
		t.Fatalf("missing Crosstab on response: want=%v got=%v", want.Crosstab, got.Crosstab)
	}
	assertMatrixEqual(t, want.Crosstab.Matrix, got.Crosstab.Matrix)
	assertMetadataEqual(t, want.Metadata, got.Metadata)

	// Explicit assertion: the grand AGG_COUNT must equal the post-filter
	// record count regardless of axis nullity. fusedCrosstabRecordsWithNulls
	// returns 12 records; none are filtered out by this request.
	m := got.Crosstab.Matrix
	if !m.GrandTotal.Present {
		t.Fatalf("grand total must be present")
	}
	wantCount := float64(len(recs))
	if m.GrandTotal.Scalar() != wantCount {
		t.Errorf("grand AGG_COUNT = %v; want %v (one per post-filter record)",
			m.GrandTotal.Scalar(), wantCount)
	}
}

// normalizeWithinRecords populates the canonical normalize_within
// fixture: every (brand, wavedate, xxx) slab carries enough records to
// produce a non-trivial cell count, so the normalize-within denominator
// has variance and the equivalence assertions surface drift if either
// path miscalculates the slab denominator.
func normalizeWithinRecords(schema *encoding.Schema) []*Record {
	mk := func(brand, wave, xxx uint64, value float64) *Record {
		return NewRecord(schema, map[string]float64{
			"brand":    float64(brand),
			"wavedate": float64(wave),
			"xxx":      float64(xxx),
			"value":    value,
		})
	}
	return []*Record{
		// brand=acme, wave=2024Q1
		mk(0, 0, 0, 1), mk(0, 0, 0, 1), // low: 2
		mk(0, 0, 1, 1),                 // mid: 1
		mk(0, 0, 2, 1), mk(0, 0, 2, 1), // high: 2
		// brand=acme, wave=2024Q2
		mk(0, 1, 0, 1),                 // low: 1
		mk(0, 1, 1, 1), mk(0, 1, 1, 1), // mid: 2
		mk(0, 1, 2, 1), mk(0, 1, 2, 1), mk(0, 1, 2, 1), // high: 3
		// brand=bigco, wave=2024Q1
		mk(1, 0, 0, 1), mk(1, 0, 0, 1), mk(1, 0, 0, 1), // low: 3
		mk(1, 0, 1, 1), // mid: 1
		mk(1, 0, 2, 1), // high: 1
		// brand=bigco, wave=2024Q2
		mk(1, 1, 0, 1),                 // low: 1
		mk(1, 1, 1, 1), mk(1, 1, 1, 1), // mid: 2
		mk(1, 1, 2, 1), // high: 1
	}
}
