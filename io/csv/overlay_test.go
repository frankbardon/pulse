package csv

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// helperFloat returns a pointer to f. Local helper mirrors the Arrow /
// Parquet / Excel / NDJSON test fixtures so the overlay catalog uses
// the same authoring shape across adapters.
func helperFloat(f float64) *float64 { return &f }

// helperLayers returns a small fixture set covering all three
// OverlayPayload shapes (scalar / series / matrix). Mirrors the
// overlay-aware adapters' fixtures so the CSV warn-and-skip claim
// shares the reference layer slate.
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
				Statistic: helperFloat(12.34),
				PValue:    helperFloat(0.00021),
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
					},
				},
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
					RowKeys:    []types.AxisKey{{"NORTH"}},
					ColumnKeys: []types.AxisKey{{"WEB"}},
					Cells: [][]types.MatrixCell{
						{{Value: 95.2, Present: true}},
					},
				},
			},
		},
	}
}

func TestCsvWriter_OverlayInterface(t *testing.T) {
	var w pio.OverlayAwareWriter = NewWriterToBuffer()
	// Nil and empty slices must be safe to pass — the dispatcher will
	// pass nil when Response.Overlays is empty and an empty slice
	// when the caller explicitly hands `[]*OverlayLayer{}`.
	w.SetOverlays(nil)
	w.SetOverlays([]*types.OverlayLayer{})
}

// TestCsvWriter_NoOverlaysByteIdenticalToBaseline asserts that a writer
// that never receives SetOverlays produces byte-identical output to a
// writer that receives SetOverlays(nil) AND a writer that receives
// SetOverlays([]). This is the "byte-identical to a non-overlay
// export" acceptance criterion (research/export-embedding-shape.md
// § 7.1, story acceptance "Host CSV byte-identical to non-overlay
// export").
func TestCsvWriter_NoOverlaysByteIdenticalToBaseline(t *testing.T) {
	header := []string{"name", "value"}
	rows := [][]any{
		{"alice", 42},
		{"bob", 99},
	}

	// Baseline writer — no SetOverlays call at all.
	baseline := NewWriterToBuffer()
	if err := baseline.WriteHeader(header); err != nil {
		t.Fatalf("baseline WriteHeader: %v", err)
	}
	for _, r := range rows {
		if err := baseline.WriteRow(r); err != nil {
			t.Fatalf("baseline WriteRow: %v", err)
		}
	}
	if err := baseline.Close(); err != nil {
		t.Fatalf("baseline Close: %v", err)
	}

	// Companion writer — SetOverlays(nil).
	nilOverlays := NewWriterToBuffer()
	nilOverlays.SetOverlays(nil)
	if err := nilOverlays.WriteHeader(header); err != nil {
		t.Fatalf("nil WriteHeader: %v", err)
	}
	for _, r := range rows {
		if err := nilOverlays.WriteRow(r); err != nil {
			t.Fatalf("nil WriteRow: %v", err)
		}
	}
	if err := nilOverlays.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
	if !bytes.Equal(baseline.Bytes(), nilOverlays.Bytes()) {
		t.Errorf("nil-overlay output differs from baseline:\nbaseline: %q\nnil:      %q",
			baseline.Bytes(), nilOverlays.Bytes())
	}

	// Companion writer — SetOverlays([]).
	emptyOverlays := NewWriterToBuffer()
	emptyOverlays.SetOverlays([]*types.OverlayLayer{})
	if err := emptyOverlays.WriteHeader(header); err != nil {
		t.Fatalf("empty WriteHeader: %v", err)
	}
	for _, r := range rows {
		if err := emptyOverlays.WriteRow(r); err != nil {
			t.Fatalf("empty WriteRow: %v", err)
		}
	}
	if err := emptyOverlays.Close(); err != nil {
		t.Fatalf("empty Close: %v", err)
	}
	if !bytes.Equal(baseline.Bytes(), emptyOverlays.Bytes()) {
		t.Errorf("empty-overlay output differs from baseline:\nbaseline: %q\nempty:    %q",
			baseline.Bytes(), emptyOverlays.Bytes())
	}
}

// TestCsvWriter_OverlaysBodyByteIdenticalToBaseline asserts that even
// when SetOverlays is called with a populated layer slice the CSV body
// stays byte-identical to a writer that never received any overlays.
// This is the WARN-AND-SKIP acceptance criterion: the body is the
// host CSV verbatim; the warning is surfaced separately via
// OverlayWarnings(). Research § 7.1 calls this out as the explicit
// "accepted risk".
func TestCsvWriter_OverlaysBodyByteIdenticalToBaseline(t *testing.T) {
	header := []string{"name", "value"}
	rows := [][]any{
		{"alice", 42},
		{"bob", 99},
	}

	baseline := NewWriterToBuffer()
	if err := baseline.WriteHeader(header); err != nil {
		t.Fatalf("baseline WriteHeader: %v", err)
	}
	for _, r := range rows {
		if err := baseline.WriteRow(r); err != nil {
			t.Fatalf("baseline WriteRow: %v", err)
		}
	}
	if err := baseline.Close(); err != nil {
		t.Fatalf("baseline Close: %v", err)
	}

	withOverlays := NewWriterToBuffer()
	withOverlays.SetOverlays(helperLayers())
	if err := withOverlays.WriteHeader(header); err != nil {
		t.Fatalf("overlays WriteHeader: %v", err)
	}
	for _, r := range rows {
		if err := withOverlays.WriteRow(r); err != nil {
			t.Fatalf("overlays WriteRow: %v", err)
		}
	}
	if err := withOverlays.Close(); err != nil {
		t.Fatalf("overlays Close: %v", err)
	}

	if !bytes.Equal(baseline.Bytes(), withOverlays.Bytes()) {
		t.Errorf("overlay-bearing CSV body diverges from baseline:\nbaseline: %q\noverlays: %q",
			baseline.Bytes(), withOverlays.Bytes())
	}
}

// TestCsvWriter_OverlayWarnings_FiresOnNonEmptyOverlays asserts that
// OverlayWarnings() returns exactly one
// PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning when the writer
// recorded a non-empty layer slice, and that the warning's Details
// carry layer_count, layer_names, and layer_kinds matching the
// recorded layers.
func TestCsvWriter_OverlayWarnings_FiresOnNonEmptyOverlays(t *testing.T) {
	w := NewWriterToBuffer()
	w.SetOverlays(helperLayers())

	warnings := w.OverlayWarnings()
	if len(warnings) != 1 {
		t.Fatalf("OverlayWarnings = %d, want 1", len(warnings))
	}
	got := warnings[0]
	if got.Code != errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED {
		t.Errorf("warning code = %s, want %s", got.Code, errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED)
	}
	// Message must include the dropped-layer count.
	if got.Message == "" {
		t.Errorf("warning message empty")
	}
	// Details: layer_count == 3, layer_names + layer_kinds populated.
	count, ok := got.Details["layer_count"].(int)
	if !ok || count != 3 {
		t.Errorf("Details[layer_count] = %v, want 3", got.Details["layer_count"])
	}
	names, ok := got.Details["layer_names"].([]string)
	if !ok || len(names) != 3 {
		t.Fatalf("Details[layer_names] = %v, want 3 entries", got.Details["layer_names"])
	}
	wantNames := []string{"chisq_total", "index_vs_total", "index_vs_margin"}
	for i, n := range wantNames {
		if names[i] != n {
			t.Errorf("Details[layer_names][%d] = %q, want %q", i, names[i], n)
		}
	}
	kinds, ok := got.Details["layer_kinds"].([]string)
	if !ok || len(kinds) != 3 {
		t.Fatalf("Details[layer_kinds] = %v, want 3 entries", got.Details["layer_kinds"])
	}
	wantKinds := []string{
		string(types.OverlayKindChiSqMatrix),
		string(types.OverlayKindIndexVsTotal),
		string(types.OverlayKindIndexVsMargin),
	}
	for i, k := range wantKinds {
		if kinds[i] != k {
			t.Errorf("Details[layer_kinds][%d] = %q, want %q", i, kinds[i], k)
		}
	}
}

// TestCsvWriter_OverlayWarnings_EmptyWhenNoOverlays asserts that
// OverlayWarnings() returns nil when SetOverlays was never called,
// called with nil, or called with an empty slice. The warning is
// specifically "you asked for overlays but CSV cannot carry them,"
// not "CSV cannot carry overlays in general" — so an overlay-free
// export emits no warning.
func TestCsvWriter_OverlayWarnings_EmptyWhenNoOverlays(t *testing.T) {
	cases := []struct {
		name string
		set  func(*Writer)
	}{
		{"never called", func(*Writer) {}},
		{"nil slice", func(w *Writer) { w.SetOverlays(nil) }},
		{"empty slice", func(w *Writer) { w.SetOverlays([]*types.OverlayLayer{}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWriterToBuffer()
			tc.set(w)
			if got := w.OverlayWarnings(); got != nil {
				t.Errorf("OverlayWarnings = %v, want nil", got)
			}
		})
	}
}

// TestCsvWriter_OverlayWarning_SkipsNilLayers verifies that nil
// entries inside an otherwise-non-empty layer slice are skipped from
// the layer_names / layer_kinds detail arrays but still counted in
// layer_count. Defensive — the dispatcher should never hand the
// writer a slice with nil entries, but the warn-and-skip surface
// must not panic if it does.
func TestCsvWriter_OverlayWarning_SkipsNilLayers(t *testing.T) {
	w := NewWriterToBuffer()
	w.SetOverlays([]*types.OverlayLayer{
		{Name: "first", Kind: types.OverlayKindIndexVsMargin},
		nil,
		{Name: "third", Kind: types.OverlayKindChiSqMatrix},
	})

	warnings := w.OverlayWarnings()
	if len(warnings) != 1 {
		t.Fatalf("OverlayWarnings = %d, want 1", len(warnings))
	}
	got := warnings[0]
	count, _ := got.Details["layer_count"].(int)
	if count != 3 {
		t.Errorf("layer_count = %d, want 3", count)
	}
	names, _ := got.Details["layer_names"].([]string)
	if len(names) != 2 || names[0] != "first" || names[1] != "third" {
		t.Errorf("layer_names = %v, want [first third]", names)
	}
}
