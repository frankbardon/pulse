package arrow

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// helperFloat returns a pointer to f. Local helper to keep the
// payload-construction noise out of every test body.
func helperFloat(f float64) *float64 { return &f }

// helperLayers returns a small fixture set covering all three
// OverlayPayload shapes (scalar / series / matrix) so the round-trip
// tests exercise every dispatch arm.
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
				Min: helperFloat(80.0),
				Max: helperFloat(125.0),
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

// TestArrowWriter_OverlayRoundTrip verifies that a writer with overlays
// configured emits a sidecar that the reader can rebuild
// byte-equivalent (modulo float64 JSON normalisation, which round-trips
// here because every fixture value has an exact float64 representation).
func TestArrowWriter_OverlayRoundTrip(t *testing.T) {
	w := NewWriterToBuffer()
	w.SetOverlays(helperLayers())
	if err := w.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"alice", "42"}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.WriteRow([]any{"bob", "99"}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReaderFromBytes(w.Bytes())
	defer r.Close()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	// Host header must NOT include the overlays sidecar field — it
	// must look identical to a non-overlay reader.
	if len(header) != 2 || header[0] != "name" || header[1] != "value" {
		t.Fatalf("header = %v, want [name value]", header)
	}

	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string{}, row...))
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0][0] != "alice" || rows[0][1] != "42" {
		t.Errorf("row 0 = %v", rows[0])
	}

	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d layers, want 3", len(got))
	}
	// Slot 0: scalar layer.
	if got[0].Name != "chisq_total" {
		t.Errorf("layer 0 name = %q", got[0].Name)
	}
	if got[0].Kind != types.OverlayKindChiSqMatrix {
		t.Errorf("layer 0 kind = %s", got[0].Kind)
	}
	if got[0].Payload.Shape != types.OverlayShapeScalar {
		t.Errorf("layer 0 shape = %s", got[0].Payload.Shape)
	}
	if got[0].Payload.Scalar == nil || *got[0].Payload.Scalar != 12.34 {
		t.Errorf("layer 0 scalar = %v", got[0].Payload.Scalar)
	}
	if got[0].Summary == nil || got[0].Summary.Statistic == nil || *got[0].Summary.Statistic != 12.34 {
		t.Errorf("layer 0 summary statistic = %+v", got[0].Summary)
	}
	if got[0].Summary.Parameters["df"] != 4 {
		t.Errorf("layer 0 summary df = %v", got[0].Summary.Parameters)
	}

	// Slot 1: series layer — entries preserve order.
	if got[1].Name != "index_vs_total" {
		t.Errorf("layer 1 name = %q", got[1].Name)
	}
	if got[1].Payload.Shape != types.OverlayShapeSeries {
		t.Errorf("layer 1 shape = %s", got[1].Payload.Shape)
	}
	series := got[1].Payload.Series
	if series == nil || len(series.Entries) != 2 {
		t.Fatalf("layer 1 series = %+v", series)
	}
	if got, want := stringFromAxisKey(series.Entries[0].Key), "NORTH"; got != want {
		t.Errorf("series entry 0 key = %q, want %q", got, want)
	}
	if series.Entries[0].Summary.Statistic == nil || *series.Entries[0].Summary.Statistic != 125.0 {
		t.Errorf("series entry 0 statistic = %+v", series.Entries[0].Summary)
	}

	// Slot 2: matrix layer.
	if got[2].Name != "index_vs_margin" {
		t.Errorf("layer 2 name = %q", got[2].Name)
	}
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
	// Ref round-trip.
	if got[2].Ref.Margin == nil || got[2].Ref.Margin.Axis != "row" {
		t.Errorf("layer 2 ref.margin = %+v", got[2].Ref.Margin)
	}
}

// stringFromAxisKey lifts the head of an axis key as a string for test
// assertions; the JSON round-trip preserves the original any-typed
// value (a string in this fixture).
func stringFromAxisKey(k types.AxisKey) string {
	if len(k) == 0 {
		return ""
	}
	if s, ok := k[0].(string); ok {
		return s
	}
	return ""
}

// TestArrowWriter_NoOverlaysByteIdenticalToBaseline verifies that a
// writer that never receives SetOverlays produces byte-identical output
// to a writer constructed the same way before overlay embedding
// existed. The story acceptance criterion: "When IncludeOverlays==false
// OR no overlays present, Arrow output byte-identical to pre-overlay
// baseline."
func TestArrowWriter_NoOverlaysByteIdenticalToBaseline(t *testing.T) {
	// Baseline writer — no SetOverlays call at all.
	baseline := NewWriterToBuffer()
	if err := baseline.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("baseline WriteHeader: %v", err)
	}
	if err := baseline.WriteRow([]any{"alice", "42"}); err != nil {
		t.Fatalf("baseline WriteRow: %v", err)
	}
	if err := baseline.WriteRow([]any{"bob", "99"}); err != nil {
		t.Fatalf("baseline WriteRow: %v", err)
	}
	if err := baseline.Close(); err != nil {
		t.Fatalf("baseline Close: %v", err)
	}

	// Companion writer — SetOverlays called with nil slice. nil
	// slice MUST behave the same as never calling SetOverlays.
	nilOverlays := NewWriterToBuffer()
	nilOverlays.SetOverlays(nil)
	if err := nilOverlays.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("nil-overlay WriteHeader: %v", err)
	}
	if err := nilOverlays.WriteRow([]any{"alice", "42"}); err != nil {
		t.Fatalf("nil-overlay WriteRow: %v", err)
	}
	if err := nilOverlays.WriteRow([]any{"bob", "99"}); err != nil {
		t.Fatalf("nil-overlay WriteRow: %v", err)
	}
	if err := nilOverlays.Close(); err != nil {
		t.Fatalf("nil-overlay Close: %v", err)
	}

	if !bytes.Equal(baseline.Bytes(), nilOverlays.Bytes()) {
		t.Errorf("nil-overlay output differs from baseline (len %d vs %d)",
			len(nilOverlays.Bytes()), len(baseline.Bytes()))
	}

	// Companion writer — SetOverlays called with empty slice.
	// Empty slice MUST also behave the same.
	emptyOverlays := NewWriterToBuffer()
	emptyOverlays.SetOverlays([]*types.OverlayLayer{})
	if err := emptyOverlays.WriteHeader([]string{"name", "value"}); err != nil {
		t.Fatalf("empty-overlay WriteHeader: %v", err)
	}
	if err := emptyOverlays.WriteRow([]any{"alice", "42"}); err != nil {
		t.Fatalf("empty-overlay WriteRow: %v", err)
	}
	if err := emptyOverlays.WriteRow([]any{"bob", "99"}); err != nil {
		t.Fatalf("empty-overlay WriteRow: %v", err)
	}
	if err := emptyOverlays.Close(); err != nil {
		t.Fatalf("empty-overlay Close: %v", err)
	}

	if !bytes.Equal(baseline.Bytes(), emptyOverlays.Bytes()) {
		t.Errorf("empty-overlay output differs from baseline (len %d vs %d)",
			len(emptyOverlays.Bytes()), len(baseline.Bytes()))
	}
}

// TestArrowReader_ReadOverlays_AbsentReturnsNil verifies that
// ReadOverlays returns nil (not error) on a baseline Arrow file with
// no overlays sidecar — the contract is "no overlays present" rather
// than "field missing is an error."
func TestArrowReader_ReadOverlays_AbsentReturnsNil(t *testing.T) {
	data := helperSimpleArrow(t)
	r := NewReaderFromBytes(data)
	defer r.Close()

	layers, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if layers != nil {
		t.Errorf("got %d layers, want nil", len(layers))
	}
}

// TestArrowWriter_OverlaysAcrossMultipleBatches verifies that when
// the writer flushes multiple record-batches (rows beyond the batch
// boundary) the populated overlay list is emitted ONCE in batch 0 row
// 0 per § 3.2 — the reader walks every list element in every batch
// and returns the first non-empty list it sees. The reader does NOT
// see duplicated layers across batches.
func TestArrowWriter_OverlaysAcrossMultipleBatches(t *testing.T) {
	w := NewWriterToBuffer()
	w.SetOverlays(helperLayers())
	if err := w.WriteHeader([]string{"id"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Spill across a batch boundary by writing more than
	// defaultRecordBatchSize rows.
	total := defaultRecordBatchSize + 10
	for i := 0; i < total; i++ {
		if err := w.WriteRow([]any{i}); err != nil {
			t.Fatalf("WriteRow %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r := NewReaderFromBytes(w.Bytes())
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 1 || header[0] != "id" {
		t.Fatalf("header = %v", header)
	}

	rowCount := 0
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if rowCount != total {
		t.Errorf("got %d rows, want %d", rowCount, total)
	}

	layers, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("got %d layers, want 3", len(layers))
	}
	if layers[0].Name != "chisq_total" {
		t.Errorf("layer 0 = %q", layers[0].Name)
	}
}

// TestArrowOverlay_OverlayAwareWriterInterface checks the Writer
// satisfies pio.OverlayAwareWriter — the optional interface that
// downstream ExportJob wiring will dispatch through. Compile-time
// assertion guards against accidental signature drift.
func TestArrowOverlay_OverlayAwareWriterInterface(t *testing.T) {
	// Compile-time check via the package's existing interface assertion
	// at the bottom of arrow.go. This test exists so a grep for the
	// interface name lands on a guarded use site.
	w := NewWriterToBuffer()
	w.SetOverlays(nil)
	// Sanity: nil SetOverlays must not panic and must not alter
	// behaviour.
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
