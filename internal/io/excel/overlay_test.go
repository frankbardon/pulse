package excel

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"
)

// helperFloat returns a pointer to f. Local helper mirrors the Arrow /
// Parquet test fixtures so the overlay catalog uses the same authoring
// shape across adapters.
func helperFloat(f float64) *float64 { return &f }

func helperInt(i int) *int { return &i }

// helperLayers returns a small fixture set covering all three
// OverlayPayload shapes (scalar / series / matrix). Mirrors the Arrow
// adapter's helperLayers so the per-adapter round-trip claims share a
// reference shape; the Excel best-effort round-trip rule from
// research/export-embedding-shape.md § 5.3 is exercised against this
// fixture.
func helperLayers() []*types.OverlayLayer {
	scalarVal := 12.34
	return []*types.OverlayLayer{
		{
			Name:  "chisq_total",
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
			Ref:   types.OverlayRef{},
			Payload: types.OverlayPayload{
				Shape:  types.OverlayShapeScalar,
				Scalar: &scalarVal,
			},
			Summary: &types.OverlaySummary{
				Statistic:  helperFloat(12.34),
				PValue:     helperFloat(0.00021),
				Parameters: map[string]float64{"df": 4},
			},
		},
		{
			Name:  "index_vs_total",
			Kind:  types.OverlayKindIndexVsTotal,
			Scope: types.OverlayScopeGroup,
			Ref:   types.OverlayRef{},
			Payload: types.OverlayPayload{
				Shape: types.OverlayShapeSeries,
				Series: &types.SeriesPayload{
					Entries: []types.SeriesEntry{
						{
							Key:     types.AxisKey{"NORTH"},
							Summary: types.OverlaySummary{Statistic: helperFloat(125.0)},
						},
						{
							Key:     types.AxisKey{"SOUTH"},
							Summary: types.OverlaySummary{Statistic: helperFloat(80.0)},
						},
					},
				},
			},
			Summary: &types.OverlaySummary{
				Min:   helperFloat(80.0),
				Max:   helperFloat(125.0),
				Count: helperInt(2),
			},
		},
		{
			Name:  "index_vs_margin",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: "row"},
			},
			Payload: types.OverlayPayload{
				Shape: types.OverlayShapeMatrix,
				Matrix: &types.MatrixPayload{
					RowHeader:    types.AxisHeader{Fields: []string{"region"}, Types: []string{"GROUP_CATEGORY"}},
					ColumnHeader: types.AxisHeader{Fields: []string{"channel"}, Types: []string{"GROUP_CATEGORY"}},
					RowKeys:      []types.AxisKey{{"NORTH"}, {"SOUTH"}},
					ColumnKeys:   []types.AxisKey{{"WEB"}, {"STORE"}},
					Cells: [][]types.MatrixCell{
						{
							{Value: 95.2, Present: true},
							{Value: 105.8, Present: true},
						},
						{
							{Value: 88.4, Present: true},
							{Value: 112.1, Present: true},
						},
					},
				},
			},
		},
	}
}

// TestExcelWriter_OverlayRoundTrip verifies that a writer with overlays
// configured emits one sheet per layer with the engine-managed
// "__overlay_<name>" prefix, that the reader walks the sheets back into
// []*types.OverlayLayer, and that the per-shape payload survives the
// round-trip (best-effort per § 5.3).
func TestExcelWriter_OverlayRoundTrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "overlays.xlsx")
	w.SetOverlays(helperLayers())
	if err := w.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"alice", 42}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.WriteRow([]any{"bob", 99}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReader(fs, "overlays.xlsx")
	defer r.Close()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	// Host header is unchanged; the host sheet is the default sheet.
	if len(header) != 2 || header[0] != "name" || header[1] != "value" {
		t.Fatalf("host header = %v, want [name value]", header)
	}

	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d layers, want 3", len(got))
	}

	// Slot 0: scalar layer.
	if got[0].Kind != types.OverlayKindChiSqMatrix {
		t.Errorf("layer 0 kind = %s", got[0].Kind)
	}
	if got[0].Payload.Shape != types.OverlayShapeScalar {
		t.Errorf("layer 0 shape = %s", got[0].Payload.Shape)
	}
	if got[0].Payload.Scalar == nil || *got[0].Payload.Scalar != 12.34 {
		t.Errorf("layer 0 scalar = %v", got[0].Payload.Scalar)
	}
	if got[0].Summary == nil {
		t.Fatalf("layer 0 summary nil")
	}
	if got[0].Summary.Statistic == nil || *got[0].Summary.Statistic != 12.34 {
		t.Errorf("layer 0 summary statistic = %+v", got[0].Summary.Statistic)
	}
	if got[0].Summary.PValue == nil || *got[0].Summary.PValue != 0.00021 {
		t.Errorf("layer 0 summary p_value = %+v", got[0].Summary.PValue)
	}
	if got[0].Summary.Parameters["df"] != 4 {
		t.Errorf("layer 0 summary df = %v", got[0].Summary.Parameters)
	}

	// Slot 1: series layer.
	if got[1].Payload.Shape != types.OverlayShapeSeries {
		t.Errorf("layer 1 shape = %s", got[1].Payload.Shape)
	}
	series := got[1].Payload.Series
	if series == nil || len(series.Entries) != 2 {
		t.Fatalf("layer 1 series = %+v", series)
	}
	if stringFromAxisKey(series.Entries[0].Key) != "NORTH" {
		t.Errorf("series entry 0 key = %q", stringFromAxisKey(series.Entries[0].Key))
	}
	if series.Entries[0].Summary.Statistic == nil || *series.Entries[0].Summary.Statistic != 125.0 {
		t.Errorf("series entry 0 statistic = %+v", series.Entries[0].Summary.Statistic)
	}
	if got[1].Summary == nil {
		t.Fatalf("layer 1 summary nil")
	}
	if got[1].Summary.Count == nil || *got[1].Summary.Count != 2 {
		t.Errorf("layer 1 summary count = %+v", got[1].Summary.Count)
	}

	// Slot 2: matrix layer.
	if got[2].Payload.Shape != types.OverlayShapeMatrix {
		t.Errorf("layer 2 shape = %s", got[2].Payload.Shape)
	}
	matrix := got[2].Payload.Matrix
	if matrix == nil {
		t.Fatalf("layer 2 matrix nil")
	}
	if len(matrix.RowKeys) != 2 || len(matrix.ColumnKeys) != 2 {
		t.Errorf("matrix axes = %d × %d", len(matrix.RowKeys), len(matrix.ColumnKeys))
	}
	if !matrix.Cells[0][0].Present || matrix.Cells[0][0].Scalar() != 95.2 {
		t.Errorf("matrix cell[0][0] = %+v", matrix.Cells[0][0])
	}
	if !matrix.Cells[1][1].Present || matrix.Cells[1][1].Scalar() != 112.1 {
		t.Errorf("matrix cell[1][1] = %+v", matrix.Cells[1][1])
	}
	if got[2].Ref.Margin == nil || got[2].Ref.Margin.Axis != "row" {
		t.Errorf("layer 2 ref.margin = %+v", got[2].Ref.Margin)
	}
}

// stringFromAxisKey lifts the head of an axis key as a string for
// assertions. The Excel reader rebuilds axis keys as single-element
// AxisKey{string} tuples (best-effort round-trip per § 5.3).
func stringFromAxisKey(k types.AxisKey) string {
	if len(k) == 0 {
		return ""
	}
	if s, ok := k[0].(string); ok {
		return s
	}
	return ""
}

// TestExcelWriter_NoOverlaysByteIdenticalToBaseline verifies that a
// writer that never receives SetOverlays produces the same workbook
// shape as one that received SetOverlays(nil) and SetOverlays([]) —
// the workbook sheet list is identical (only the host sheet) and the
// raw bytes are equal. Story acceptance: "When IncludeOverlays == false
// OR no overlays, Excel byte-identical to baseline."
func TestExcelWriter_NoOverlaysByteIdenticalToBaseline(t *testing.T) {
	baseline := buildExcelBuffer(t, nil, false)
	nilOverlays := buildExcelBuffer(t, nil, true) // SetOverlays(nil)
	emptyOverlays := buildExcelBuffer(t, []*types.OverlayLayer{}, true)

	if !bytes.Equal(baseline, nilOverlays) {
		t.Errorf("nil-overlay output differs from baseline (len %d vs %d)",
			len(nilOverlays), len(baseline))
	}
	if !bytes.Equal(baseline, emptyOverlays) {
		t.Errorf("empty-overlay output differs from baseline (len %d vs %d)",
			len(emptyOverlays), len(baseline))
	}

	// Verify no overlay sheets land on the workbook.
	for _, buf := range [][]byte{baseline, nilOverlays, emptyOverlays} {
		f, err := excelize.OpenReader(bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("OpenReader: %v", err)
		}
		for _, name := range f.GetSheetList() {
			if strings.HasPrefix(name, OverlaySheetPrefix) {
				t.Errorf("baseline workbook carries overlay sheet %q", name)
			}
		}
		f.Close()
	}
}

// buildExcelBuffer constructs an Excel workbook with a tiny host sheet
// and optionally calls SetOverlays. Returns the serialised bytes.
func buildExcelBuffer(t *testing.T, overlays []*types.OverlayLayer, callSet bool) []byte {
	t.Helper()
	fs := afero.NewMemMapFs()
	path := "out.xlsx"
	w := NewWriter(fs, path)
	if callSet {
		w.SetOverlays(overlays)
	}
	if err := w.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"alice", 42}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return data
}

// TestExcelReader_NoOverlaysReturnsNil verifies that ReadOverlays
// returns nil (not error) on a workbook with no overlay sheets — the
// contract is "no overlays present" rather than "field missing is an
// error" (mirrors the Arrow / Parquet contract).
func TestExcelReader_NoOverlaysReturnsNil(t *testing.T) {
	fs := afero.NewMemMapFs()
	helperCreateXLSX(t, fs, "plain.xlsx", "", []string{"name", "age"}, [][]string{
		{"alice", "30"},
	})
	r := NewReader(fs, "plain.xlsx")
	defer r.Close()

	layers, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if layers != nil {
		t.Errorf("got %d layers, want nil", len(layers))
	}
}

// TestExcelWriter_OverlaySheetNamePrefix verifies that every emitted
// overlay sheet's name starts with the engine-managed double-underscore
// prefix — readers can filter overlay sheets with HasPrefix.
func TestExcelWriter_OverlaySheetNamePrefix(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "overlays.xlsx")
	w.SetOverlays(helperLayers())
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{1}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, _ := afero.ReadFile(fs, "overlays.xlsx")
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()

	overlayCount := 0
	for _, name := range f.GetSheetList() {
		if strings.HasPrefix(name, OverlaySheetPrefix) {
			overlayCount++
		}
	}
	if overlayCount != 3 {
		t.Errorf("overlay sheet count = %d, want 3", overlayCount)
	}
}

// TestBuildOverlaySheetName_Truncation verifies the 31-char cap is
// honoured for long layer names and that the disambiguation suffix
// fires on collision.
func TestBuildOverlaySheetName_Truncation(t *testing.T) {
	existing := map[string]bool{}

	// Short name — composed name fits the cap.
	short := buildOverlaySheetName("short", existing)
	if short != "__overlay_short" {
		t.Errorf("short = %q, want __overlay_short", short)
	}
	existing[short] = true

	// Long name — truncated to 31 chars.
	long := buildOverlaySheetName("this_is_a_very_long_layer_name_that_exceeds_excel_limit", existing)
	if len(long) > excelSheetNameMaxLen {
		t.Errorf("long name length = %d, want <= 31: %q", len(long), long)
	}
	if !strings.HasPrefix(long, OverlaySheetPrefix) {
		t.Errorf("long name = %q, missing overlay prefix", long)
	}
	existing[long] = true

	// Collision — same long name passes again and gets a "_tN" suffix.
	long2 := buildOverlaySheetName("this_is_a_very_long_layer_name_that_exceeds_excel_limit", existing)
	if long2 == long {
		t.Errorf("collision should disambiguate; got identical %q", long2)
	}
	if len(long2) > excelSheetNameMaxLen {
		t.Errorf("disambiguated name length = %d, want <= 31: %q", len(long2), long2)
	}
	if !strings.HasSuffix(long2, "_t1") {
		t.Errorf("first collision should end with _t1; got %q", long2)
	}
}

// TestBuildOverlaySheetName_CollisionWithExisting verifies the engine
// disambiguates against ANY pre-existing sheet name on the workbook
// (e.g. the host sheet) so author-supplied names cannot clash with the
// overlay namespace.
func TestBuildOverlaySheetName_CollisionWithExisting(t *testing.T) {
	existing := map[string]bool{
		"__overlay_chisq": true, // an author-supplied sheet starting with the reserved prefix
	}
	got := buildOverlaySheetName("chisq", existing)
	if got == "__overlay_chisq" {
		t.Errorf("collision with existing sheet should disambiguate; got %q", got)
	}
	if !strings.HasSuffix(got, "_t1") {
		t.Errorf("first collision should end with _t1; got %q", got)
	}
}

// TestExcelWriter_OverlaySheetOrderMatchesSpec verifies overlay sheets
// land on the workbook in the same order as Response.Overlays slot
// order — the sheet ordinal after the host sheet matches the layer
// index. Story acceptance: "Stable spec-order emission (layer N →
// sheet ordinal N+1 after primary)."
func TestExcelWriter_OverlaySheetOrderMatchesSpec(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "ordered.xlsx")
	w.SetOverlays(helperLayers())
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{1}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, _ := afero.ReadFile(fs, "ordered.xlsx")
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	// First sheet is the host sheet; subsequent sheets are overlay
	// sheets in spec order.
	if len(sheets) != 4 {
		t.Fatalf("sheet count = %d, want 4 (1 host + 3 overlay)", len(sheets))
	}
	want := []string{
		"__overlay_chisq_total",
		"__overlay_index_vs_total",
		"__overlay_index_vs_margin",
	}
	for i, name := range want {
		if sheets[i+1] != name {
			t.Errorf("sheet[%d] = %q, want %q", i+1, sheets[i+1], name)
		}
	}
}

// TestExcelWriter_HostSheetUnchanged verifies the host data sheet is
// byte-identical when overlays are added vs not — the per-sheet
// invariant from research/export-embedding-shape.md § 2 rule 1 ("the
// host record stream is byte-identical between an overlay-bearing
// export and the same job with IncludeOverlays=false"). The whole-file
// bytes are NOT byte-identical (the new sheets land on the workbook)
// but the host sheet's rows are.
func TestExcelWriter_HostSheetUnchanged(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Baseline workbook — no overlays.
	wBase := NewWriter(fs, "base.xlsx")
	if err := wBase.WriteHeader([]string{"name", "age"}); err != nil {
		t.Fatalf("baseline WriteHeader: %v", err)
	}
	if err := wBase.WriteRow([]any{"alice", 30}); err != nil {
		t.Fatalf("baseline WriteRow: %v", err)
	}
	if err := wBase.WriteRow([]any{"bob", 25}); err != nil {
		t.Fatalf("baseline WriteRow: %v", err)
	}
	if err := wBase.Close(); err != nil {
		t.Fatalf("baseline Close: %v", err)
	}

	// Companion workbook — same data + overlays.
	wOver := NewWriter(fs, "overlay.xlsx")
	wOver.SetOverlays(helperLayers())
	if err := wOver.WriteHeader([]string{"name", "age"}); err != nil {
		t.Fatalf("overlay WriteHeader: %v", err)
	}
	if err := wOver.WriteRow([]any{"alice", 30}); err != nil {
		t.Fatalf("overlay WriteRow: %v", err)
	}
	if err := wOver.WriteRow([]any{"bob", 25}); err != nil {
		t.Fatalf("overlay WriteRow: %v", err)
	}
	if err := wOver.Close(); err != nil {
		t.Fatalf("overlay Close: %v", err)
	}

	baseReader := NewReader(fs, "base.xlsx")
	defer baseReader.Close()
	overReader := NewReader(fs, "overlay.xlsx")
	defer overReader.Close()

	hBase, _ := baseReader.ReadHeader()
	hOver, _ := overReader.ReadHeader()
	if len(hBase) != len(hOver) {
		t.Fatalf("header length differs: base=%v overlay=%v", hBase, hOver)
	}
	for i := range hBase {
		if hBase[i] != hOver[i] {
			t.Errorf("header[%d] differs: %q vs %q", i, hBase[i], hOver[i])
		}
	}

	var rowsBase [][]string
	if err := baseReader.ReadRows(context.Background(), func(row []string) error {
		rowsBase = append(rowsBase, append([]string{}, row...))
		return nil
	}); err != nil {
		t.Fatalf("baseline ReadRows: %v", err)
	}
	var rowsOver [][]string
	if err := overReader.ReadRows(context.Background(), func(row []string) error {
		rowsOver = append(rowsOver, append([]string{}, row...))
		return nil
	}); err != nil {
		t.Fatalf("overlay ReadRows: %v", err)
	}
	if len(rowsBase) != len(rowsOver) {
		t.Fatalf("row count differs: base=%d overlay=%d", len(rowsBase), len(rowsOver))
	}
	for r := range rowsBase {
		for c := range rowsBase[r] {
			if rowsBase[r][c] != rowsOver[r][c] {
				t.Errorf("row[%d][%d] differs: %q vs %q",
					r, c, rowsBase[r][c], rowsOver[r][c])
			}
		}
	}
}

// TestExcelOverlay_OverlayAwareWriterInterface asserts the Writer
// satisfies pio.OverlayAwareWriter — the optional interface that
// downstream ExportJob wiring dispatches through. Compile-time check.
func TestExcelOverlay_OverlayAwareWriterInterface(t *testing.T) {
	w := NewWriter(afero.NewMemMapFs(), "iface.xlsx")
	w.SetOverlays(nil)
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestExcelWriter_NilLayerSkipped verifies a nil OverlayLayer slot is
// skipped (no sheet emitted) — defensive against caller-side mistakes
// in the layer slice. The non-nil slots still emit one sheet each.
func TestExcelWriter_NilLayerSkipped(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "nil_layer.xlsx")
	layers := helperLayers()
	// Inject a nil between layers.
	withNil := []*types.OverlayLayer{layers[0], nil, layers[1]}
	w.SetOverlays(withNil)
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{1}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReader(fs, "nil_layer.xlsx")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d layers, want 2 (nil slot skipped)", len(got))
	}
}
