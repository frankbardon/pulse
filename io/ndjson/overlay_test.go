package ndjson

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// helperFloat returns a pointer to f. Local helper mirrors the Arrow /
// Parquet / Excel test fixtures so the overlay catalog uses the same
// authoring shape across adapters.
func helperFloat(f float64) *float64 { return &f }

func helperInt(i int) *int { return &i }

// helperLayers returns a small fixture set covering all three
// OverlayPayload shapes (scalar / series / matrix). Mirrors the Arrow /
// Parquet / Excel adapter fixtures so the per-adapter round-trip claims
// share a reference shape.
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

// buildNdjsonBuffer constructs a tiny host-record NDJSON file and
// optionally calls SetOverlays on the writer. Returns the buffered
// bytes.
func buildNdjsonBuffer(t *testing.T, overlays []*types.OverlayLayer, callSet bool) []byte {
	t.Helper()
	w := NewWriterToBuffer()
	if callSet {
		w.SetOverlays(overlays)
	}
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
	return append([]byte(nil), w.Bytes()...)
}

// TestNdjsonWriter_OverlayRoundTrip verifies that a writer with overlays
// configured emits a single trailing `{"_overlays": [...]}` line after
// the last host-record line, that the reader walks the trailer back into
// []*types.OverlayLayer, and that the per-shape payload survives the
// round-trip byte-equivalently per § 6.4.
func TestNdjsonWriter_OverlayRoundTrip(t *testing.T) {
	w := NewWriterToBuffer()
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

	out := w.Bytes()
	lines := splitLines(out)
	// 2 host record lines + 1 trailing overlay line = 3 total.
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3; output:\n%s", len(lines), out)
	}
	// Host record lines stay untouched — no `_overlays` key.
	for i := 0; i < 2; i++ {
		if strings.Contains(lines[i], OverlayTrailerKey) {
			t.Errorf("host line %d carries trailer key: %q", i, lines[i])
		}
	}
	// Trailer is the LAST line.
	if !strings.HasPrefix(strings.TrimSpace(lines[2]), `{"`+OverlayTrailerKey+`":`) {
		t.Errorf("trailer line shape unexpected: %q", lines[2])
	}

	// Reader walks the trailer back.
	r := NewReaderFromBytes(out)
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d layers, want 3", len(got))
	}

	// Slot 0: scalar layer.
	if got[0].Name != "chisq_total" {
		t.Errorf("layer 0 name = %q, want chisq_total", got[0].Name)
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
// assertions. The JSON round-trip preserves the original any-typed
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

// TestNdjsonWriter_NoOverlaysByteIdenticalToBaseline verifies that a
// writer that never receives SetOverlays produces byte-identical output
// to writers that received SetOverlays(nil) and SetOverlays([]). Story
// acceptance: "When IncludeOverlays == false OR no overlays, byte-
// identical to baseline."
func TestNdjsonWriter_NoOverlaysByteIdenticalToBaseline(t *testing.T) {
	baseline := buildNdjsonBuffer(t, nil, false)
	nilOverlays := buildNdjsonBuffer(t, nil, true)
	emptyOverlays := buildNdjsonBuffer(t, []*types.OverlayLayer{}, true)

	if !bytes.Equal(baseline, nilOverlays) {
		t.Errorf("nil-overlay output differs from baseline\nbaseline: %q\nnil:      %q",
			baseline, nilOverlays)
	}
	if !bytes.Equal(baseline, emptyOverlays) {
		t.Errorf("empty-overlay output differs from baseline\nbaseline: %q\nempty:    %q",
			baseline, emptyOverlays)
	}

	// Verify no trailer line lands in any of them.
	for _, buf := range [][]byte{baseline, nilOverlays, emptyOverlays} {
		if bytes.Contains(buf, []byte(OverlayTrailerKey)) {
			t.Errorf("baseline buffer carries trailer key: %q", buf)
		}
	}
}

// TestNdjsonWriter_OverlayTrailerOmittedWhenEmpty asserts the
// invariants from § 6.2: the trailer is OMITTED (no extra line, no
// extra bytes) when overlays are absent. The output must be exactly the
// host record stream.
func TestNdjsonWriter_OverlayTrailerOmittedWhenEmpty(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{1}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := string(w.Bytes())
	if strings.Contains(out, OverlayTrailerKey) {
		t.Errorf("trailer key found in baseline output: %q", out)
	}
	// Exactly one host record line + trailing newline.
	if strings.Count(out, "\n") != 1 {
		t.Errorf("got %d newlines, want 1; output: %q",
			strings.Count(out, "\n"), out)
	}
}

// TestNdjsonWriter_OverlayTrailerExactlyOnce verifies the trailer is
// emitted exactly once per § 6.2 — repeat Close() calls do not
// duplicate the trailer line.
func TestNdjsonWriter_OverlayTrailerExactlyOnce(t *testing.T) {
	w := NewWriterToBuffer()
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
	first := append([]byte(nil), w.Bytes()...)
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !bytes.Equal(first, w.Bytes()) {
		t.Errorf("second Close() mutated the buffer\nfirst:  %q\nsecond: %q",
			first, w.Bytes())
	}
	trailerCount := bytes.Count(first, []byte(OverlayTrailerKey))
	if trailerCount != 1 {
		t.Errorf("trailer occurrence count = %d, want 1", trailerCount)
	}
}

// TestNdjsonWriter_OverlayTrailerIsLastLine verifies the spec-order
// requirement: the trailer must be the LAST line of the file, after
// every host-record line, with layer N at index N of the trailing
// `_overlays` array.
func TestNdjsonWriter_OverlayTrailerIsLastLine(t *testing.T) {
	w := NewWriterToBuffer()
	w.SetOverlays(helperLayers())
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := w.WriteRow([]any{i}); err != nil {
			t.Fatalf("WriteRow %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := splitLines(w.Bytes())
	// 5 record lines + 1 trailer = 6.
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6", len(lines))
	}
	for i := 0; i < 5; i++ {
		if strings.Contains(lines[i], OverlayTrailerKey) {
			t.Errorf("host line %d contains trailer key: %q", i, lines[i])
		}
	}
	// Trailer carries layers in spec-order.
	var trailer overlayTrailer
	if err := json.Unmarshal([]byte(lines[5]), &trailer); err != nil {
		t.Fatalf("unmarshal trailer: %v", err)
	}
	if len(trailer.Overlays) != 3 {
		t.Fatalf("trailer carries %d layers, want 3", len(trailer.Overlays))
	}
	wantNames := []string{"chisq_total", "index_vs_total", "index_vs_margin"}
	for i, want := range wantNames {
		if trailer.Overlays[i].Name != want {
			t.Errorf("layer %d name = %q, want %q", i, trailer.Overlays[i].Name, want)
		}
	}
}

// TestNdjsonWriter_HostStreamUnaffected verifies that the host record
// stream is BYTE-IDENTICAL between an overlay-bearing export and the
// same export with `IncludeOverlays=false` — overlays land sidecar,
// never inside the host stream.
func TestNdjsonWriter_HostStreamUnaffected(t *testing.T) {
	baseline := buildNdjsonBuffer(t, nil, false)
	withOverlays := buildNdjsonBuffer(t, helperLayers(), true)

	if bytes.Equal(baseline, withOverlays) {
		t.Fatalf("expected overlay-bearing output to differ from baseline")
	}
	// Baseline must be a strict prefix of the overlay-bearing output —
	// host stream byte-identical until the trailing line.
	if !bytes.HasPrefix(withOverlays, baseline) {
		t.Errorf("baseline NOT a prefix of overlay-bearing output\nbaseline:     %q\nwithOverlays: %q",
			baseline, withOverlays)
	}
}

// TestNdjsonReader_ReadOverlays_AbsentReturnsNil verifies the
// "absent means nil" contract per § 6.4 — a file with no trailer
// returns nil, not error.
func TestNdjsonReader_ReadOverlays_AbsentReturnsNil(t *testing.T) {
	data := buildNdjsonBuffer(t, nil, false)
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

// TestNdjsonReader_ReadOverlays_FromFS verifies that ReadOverlays can
// read the trailer from a fs-backed file (parallel to the byte-source
// path).
func TestNdjsonReader_ReadOverlays_FromFS(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.ndjson")
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

	r := NewReader(fs, "out.ndjson")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d layers, want 3", len(got))
	}
	if got[0].Kind != types.OverlayKindChiSqMatrix {
		t.Errorf("layer 0 kind = %s, want %s", got[0].Kind, types.OverlayKindChiSqMatrix)
	}
}

// TestNdjsonReader_ReadOverlays_EmptyFileReturnsNil verifies the
// edge case where the file carries no content — ReadOverlays returns
// nil without erroring.
func TestNdjsonReader_ReadOverlays_EmptyFileReturnsNil(t *testing.T) {
	r := NewReaderFromBytes([]byte(""))
	defer r.Close()

	layers, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if layers != nil {
		t.Errorf("got %d layers, want nil", len(layers))
	}
}

// TestNdjsonReader_ReadOverlays_HostRecordsIgnored verifies that
// host-record lines whose first key happens to contain other text are
// skipped — only lines whose FIRST key matches OverlayTrailerKey are
// treated as the trailer.
func TestNdjsonReader_ReadOverlays_HostRecordsIgnored(t *testing.T) {
	// Host lines contain "_overlay" substring but NOT the exact
	// trailer key as their first key — the reader skips them.
	data := `{"name":"alice","_overlay_hint":"unused"}
{"name":"bob","value":42}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	layers, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if layers != nil {
		t.Errorf("got %d layers, want nil (host records lack the exact key)", len(layers))
	}
}

func TestNdjsonWriter_SatisfiesOverlayAwareWriter(t *testing.T) {
	var _ pio.OverlayAwareWriter = (*Writer)(nil)
	w := NewWriterToBuffer()
	w.SetOverlays(nil)
	// Sanity: nil SetOverlays must not panic and must not alter
	// subsequent output.
	if err := w.WriteHeader([]string{"x"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{1}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if strings.Contains(string(w.Bytes()), OverlayTrailerKey) {
		t.Errorf("nil SetOverlays produced trailer: %q", w.Bytes())
	}
}

// splitLines splits b on newline byte and drops the trailing empty
// element (NDJSON always ends with `\n` so strings.Split(s, "\n") would
// otherwise produce a stray empty trailing entry).
func splitLines(b []byte) []string {
	parts := strings.Split(string(b), "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
