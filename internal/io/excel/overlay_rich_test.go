package excel

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/xuri/excelize/v2"
)

// roundTripLayers writes layers onto a one-row workbook and reads them
// back.
func roundTripLayers(t *testing.T, layers []*types.OverlayLayer) ([]*types.OverlayLayer, *excelize.File) {
	t.Helper()
	w := NewWriterToBuffer()
	w.SetOverlays(layers)
	if err := w.WriteHeader([]string{"h"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]any{1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := NewReaderFromBytes(w.Bytes())
	t.Cleanup(func() { _ = r.Close() })
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	return got, r.file
}

// TestExcelOverlay_MatrixMarginsAndAbsentCells: a matrix overlay with
// row margins, column margins and a grand total round-trips every one of
// them; an absent cell or margin stays absent (a blank cell, never 0);
// a composite axis key is joined with "|".
func TestExcelOverlay_MatrixMarginsAndAbsentCells(t *testing.T) {
	t.Parallel()

	layer := &types.OverlayLayer{
		Name: "margins", Kind: types.OverlayKindIndexVsMargin, Scope: types.OverlayScopeCell,
		Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
			RowKeys:       []types.AxisKey{{"NORTH", 1}, {"SOUTH", 2}},
			ColumnKeys:    []types.AxisKey{{"WEB"}, {"STORE"}},
			Cells:         [][]types.MatrixCell{{{Value: 1.5, Present: true}, {}}, {{Value: 3.0, Present: true}, {Value: 4.25, Present: true}}},
			RowMargins:    []types.MatrixCell{{Value: 10.0, Present: true}, {}},
			ColumnMargins: []types.MatrixCell{{}, {Value: 20.0, Present: true}},
			GrandTotal:    types.MatrixCell{Value: 99.0, Present: true},
		}},
		Summary: &types.OverlaySummary{Baseline: helperFloat(100), Min: helperFloat(1.5)},
	}
	got, _ := roundTripLayers(t, []*types.OverlayLayer{layer})
	if len(got) != 1 || got[0].Payload.Matrix == nil {
		t.Fatalf("layers = %+v", got)
	}
	m := got[0].Payload.Matrix
	if stringFromAxisKey(m.RowKeys[0]) != "NORTH|1" || stringFromAxisKey(m.ColumnKeys[1]) != "STORE" {
		t.Errorf("axis keys = %v / %v, want NORTH|1 and STORE", m.RowKeys, m.ColumnKeys)
	}
	if !m.Cells[0][0].Present || m.Cells[0][0].Scalar() != 1.5 || m.Cells[0][1].Present {
		t.Errorf("row 0 cells = %+v, want [1.5, absent]", m.Cells[0])
	}
	if len(m.RowMargins) != 2 || !m.RowMargins[0].Present || m.RowMargins[0].Scalar() != 10 || m.RowMargins[1].Present {
		t.Errorf("row margins = %+v, want [10, absent]", m.RowMargins)
	}
	if len(m.ColumnMargins) != 2 || m.ColumnMargins[0].Present || m.ColumnMargins[1].Scalar() != 20 {
		t.Errorf("column margins = %+v, want [absent, 20]", m.ColumnMargins)
	}
	if !m.GrandTotal.Present || m.GrandTotal.Scalar() != 99 {
		t.Errorf("grand total = %+v, want 99", m.GrandTotal)
	}
	if s := got[0].Summary; s == nil || s.Baseline == nil || *s.Baseline != 100 || s.Min == nil || *s.Min != 1.5 {
		t.Errorf("summary = %+v, want baseline 100, min 1.5", got[0].Summary)
	}
}

// TestExcelOverlay_SeriesEntryFields: per-entry p-values and parameters
// round-trip; an entry with no statistic writes present=false and reads
// back with no statistic (never 0); a keyless entry stays keyless.
func TestExcelOverlay_SeriesEntryFields(t *testing.T) {
	t.Parallel()

	layer := &types.OverlayLayer{
		Name: "series", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup,
		Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: []types.SeriesEntry{
			{Key: types.AxisKey{"A"}, Summary: types.OverlaySummary{Statistic: helperFloat(2.5), PValue: helperFloat(0.04), Parameters: map[string]float64{"df": 3}}},
			{Key: types.AxisKey{"B"}, Summary: types.OverlaySummary{PValue: helperFloat(0.5)}},
			{Summary: types.OverlaySummary{Statistic: helperFloat(-1)}},
		}}},
		Summary: &types.OverlaySummary{Statistic: helperFloat(7), Max: helperFloat(2.5), Count: helperInt(3)},
	}
	got, f := roundTripLayers(t, []*types.OverlayLayer{layer})
	if len(got) != 1 || got[0].Payload.Series == nil || len(got[0].Payload.Series.Entries) != 3 {
		t.Fatalf("layers = %+v", got)
	}
	e := got[0].Payload.Series.Entries
	if e0 := e[0].Summary; e0.Statistic == nil || *e0.Statistic != 2.5 || e0.PValue == nil || *e0.PValue != 0.04 || e0.Parameters["df"] != 3 {
		t.Errorf("entry 0 = %+v", e[0].Summary)
	}
	if e[1].Summary.Statistic != nil || e[1].Summary.PValue == nil || *e[1].Summary.PValue != 0.5 {
		t.Errorf("entry 1 = %+v, want no statistic, p 0.5", e[1].Summary)
	}
	if present, _ := f.GetCellValue(OverlaySheetPrefix+"series", "C7"); present != "FALSE" {
		t.Errorf("entry 1 present cell = %q, want FALSE", present)
	}
	if len(e[2].Key) != 0 || e[2].Summary.Statistic == nil || *e[2].Summary.Statistic != -1 {
		t.Errorf("entry 2 = %+v, want keyless with statistic -1", e[2])
	}
	if s := got[0].Summary; s == nil || s.Statistic == nil || *s.Statistic != 7 || s.Max == nil || *s.Max != 2.5 || s.Count == nil || *s.Count != 3 {
		t.Errorf("summary = %+v", got[0].Summary)
	}
}

// TestExcelOverlay_DegenerateShapes: an unknown payload shape is echoed
// on the sheet (never silently dropped), and a series / matrix layer
// whose payload pointer is nil writes its header block and reads back
// with the shape it declared.
func TestExcelOverlay_DegenerateShapes(t *testing.T) {
	t.Parallel()

	layers := []*types.OverlayLayer{
		{Name: "odd", Kind: types.OverlayKindIndexVsTotal, Payload: types.OverlayPayload{Shape: "TENSOR"}},
		{Name: "noseries", Kind: types.OverlayKindIndexVsTotal, Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries}},
		{Name: "nomatrix", Kind: types.OverlayKindIndexVsMargin, Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix}},
	}
	got, f := roundTripLayers(t, layers)
	if v, _ := f.GetCellValue(OverlaySheetPrefix+"odd", "B5"); v != "TENSOR" {
		t.Errorf("unknown shape echo = %q, want TENSOR", v)
	}
	byName := map[string]*types.OverlayLayer{}
	for _, l := range got {
		byName[l.Name] = l
	}
	if l := byName["noseries"]; l == nil || l.Payload.Shape != types.OverlayShapeSeries || len(l.Payload.Series.Entries) != 0 {
		t.Errorf("nil series layer = %+v", l)
	}
	if l := byName["nomatrix"]; l == nil || l.Payload.Matrix != nil {
		t.Errorf("nil matrix layer = %+v, want no matrix payload", l)
	}
}

// TestExcelWriter_DecimalCells: a decimal128 column renders numbers at
// the field's scale with a "0.00…" format (a string at another scale is
// rescaled half-to-even), and anything that cannot be a decimal number
// — nil, "", unparseable text, a value that overflows the rescale, a
// foreign type — is written as text rather than dropped or zeroed.
func TestExcelWriter_DecimalCells(t *testing.T) {
	t.Parallel()

	w := NewWriterToBuffer()
	w.SetPulseSchema(&encoding.Schema{Fields: []encoding.Field{
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 2},
		{Name: "whole", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 0},
	}})
	if err := w.WriteHeader([]string{"amt", "whole"}); err != nil {
		t.Fatal(err)
	}
	big37 := strings.Repeat("9", 37)
	d := encoding.NewDecimal128FromInt(-1234) // -12.34 at scale 2
	rows := [][]any{
		{"1.5", "12"},
		{"1.235", d},
		{nil, ""},
		{"n/a", 7},
		{big37, "3"},
		{d, "4"},
	}
	for _, r := range rows {
		if err := w.WriteRow(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(strings.NewReader(string(w.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	want := map[string]string{
		"A2": "1.50", "B2": "12",
		"A3": "1.24", // 1.235 → half-to-even at scale 2
		"A4": "", "B4": "",
		"A5": "n/a", "B5": "7",
		"A6": big37, // 10^37-1 at scale 2 overflows: kept as text
		"A7": "-12.34",
	}
	for cell, w := range want {
		if got, _ := f.GetCellValue(sheet, cell); got != w {
			t.Errorf("%s = %q, want %q", cell, got, w)
		}
	}
	// The number cells carry the scale's format; the text fallbacks none.
	for cell, fmtWant := range map[string]string{"A2": "0.00", "B2": "0"} {
		id, _ := f.GetCellStyle(sheet, cell)
		st, err := f.GetStyle(id)
		if err != nil || st.CustomNumFmt == nil || *st.CustomNumFmt != fmtWant {
			t.Errorf("%s number format = %v (%v), want %q", cell, st, err, fmtWant)
		}
	}
	if raw, _ := f.GetCellValue(sheet, "A2", excelize.Options{RawCellValue: true}); raw != "1.5" {
		t.Errorf("A2 raw = %q, want the number 1.5", raw)
	}
}

// TestExcelReader_EmptyAndCorruptWorkbooks: a workbook with no rows has
// no header and no rows (not an error); bytes that are not a workbook
// are refused by every read entry point, never read as empty.
func TestExcelReader_EmptyAndCorruptWorkbooks(t *testing.T) {
	t.Parallel()

	empty := excelize.NewFile()
	buf, err := empty.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	r := NewReaderFromBytes(buf.Bytes())
	if h, err := r.ReadHeader(); h != nil || err != nil {
		t.Errorf("empty workbook header = (%v, %v), want (nil, nil)", h, err)
	}
	var n int
	if err := r.ReadRows(t.Context(), func([]string) error { n++; return nil }); err != nil || n != 0 {
		t.Errorf("empty workbook rows = (%d, %v), want none", n, err)
	}
	_ = r.Close()

	bad := []byte("PK\x03\x04 but not a workbook")
	if _, err := NewReaderFromBytes(bad).ReadHeader(); err == nil {
		t.Error("ReadHeader accepted a corrupt workbook")
	}
	if _, err := NewReaderFromBytes(bad).SheetNames(); err == nil {
		t.Error("SheetNames accepted a corrupt workbook")
	}
	if _, err := NewReaderFromBytes(bad).ReadOverlays(); err == nil {
		t.Error("ReadOverlays accepted a corrupt workbook")
	}
	if err := NewReaderFromBytes(bad).ReadRows(t.Context(), func([]string) error { return nil }); err == nil {
		t.Error("ReadRows accepted a corrupt workbook")
	}
}

// TestExcelOverlay_OneSidedMargins: a matrix overlay carrying only row
// margins keeps them through the round trip, and one carrying only
// column margins reads back with no row margins (not a column of absent
// ones).
func TestExcelOverlay_OneSidedMargins(t *testing.T) {
	t.Parallel()

	base := func() *types.MatrixPayload {
		return &types.MatrixPayload{
			RowKeys:    []types.AxisKey{{"N"}, {"S"}},
			ColumnKeys: []types.AxisKey{{"W"}},
			Cells:      [][]types.MatrixCell{{{Value: 1.0, Present: true}}, {{Value: 2.0, Present: true}}},
		}
	}
	rowOnly := base()
	rowOnly.RowMargins = []types.MatrixCell{{Value: 10.0, Present: true}, {}}
	colOnly := base()
	colOnly.ColumnMargins = []types.MatrixCell{{Value: 3.0, Present: true}}
	layers := []*types.OverlayLayer{
		{Name: "rows", Kind: types.OverlayKindIndexVsMargin, Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: rowOnly}},
		{Name: "cols", Kind: types.OverlayKindIndexVsMargin, Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: colOnly}},
	}
	got, _ := roundTripLayers(t, layers)
	if len(got) != 2 {
		t.Fatalf("layers = %d", len(got))
	}
	byName := map[string]*types.MatrixPayload{got[0].Name: got[0].Payload.Matrix, got[1].Name: got[1].Payload.Matrix}
	r := byName["rows"]
	if len(r.RowMargins) != 2 || !r.RowMargins[0].Present || r.RowMargins[0].Scalar() != 10 || r.RowMargins[1].Present || len(r.ColumnMargins) != 0 {
		t.Errorf("row-margin-only = rows %+v cols %+v, want rows [10, absent], no cols", r.RowMargins, r.ColumnMargins)
	}
	c := byName["cols"]
	if len(c.RowMargins) != 0 || len(c.ColumnMargins) != 1 || c.ColumnMargins[0].Scalar() != 3 {
		t.Errorf("column-margin-only = rows %+v cols %+v, want no rows, cols [3]", c.RowMargins, c.ColumnMargins)
	}
}
