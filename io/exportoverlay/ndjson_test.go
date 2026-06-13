package exportoverlay

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pndjson "github.com/frankbardon/pulse/io/ndjson"
	"github.com/spf13/afero"
)

// TestExportJob_Overlays_NDJSON_TrailingBlock drives the full
// ExportJob.Run dispatch with an NDJSON writer carrying the three-shape
// overlay fixture. Per research/export-embedding-shape.md § 6 the
// adapter appends ONE trailing `{"_overlays":[...]}` line after the
// last host-record line. The test verifies:
//
//   - the trailing line is present exactly once
//   - record lines stay byte-identical to a pre-overlay export
//   - the layer slate round-trips via the reader's ReadOverlays
//   - layer spec order is preserved
func TestExportJob_Overlays_NDJSON_TrailingBlock(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := pndjson.NewWriter(fs, "out.ndjson")
	job := pio.NewExportJob("test.pulse", w)
	job.FS = fs
	job.Overlays = helperOverlayLayers()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Writer Close: %v", err)
	}
	if report.RowsExported != 3 {
		t.Errorf("RowsExported = %d, want 3", report.RowsExported)
	}
	if len(report.OverlayWarnings) != 0 {
		t.Errorf("OverlayWarnings = %v, want empty", report.OverlayWarnings)
	}

	data, err := afero.ReadFile(fs, "out.ndjson")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	// 3 record lines + 1 trailer = 4 lines total.
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4 (3 records + 1 trailer): got %q", len(lines), data)
	}

	// Count trailer lines (lines whose first key is "_overlays").
	trailerCount := 0
	for _, line := range lines {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if _, ok := probe[pndjson.OverlayTrailerKey]; ok {
			trailerCount++
		}
	}
	if trailerCount != 1 {
		t.Errorf("trailer lines = %d, want exactly 1", trailerCount)
	}

	// Verify the LAST line is the trailer (record lines before it).
	lastLine := lines[len(lines)-1]
	var lastProbe map[string]json.RawMessage
	if err := json.Unmarshal(lastLine, &lastProbe); err != nil {
		t.Fatalf("trailer unmarshal: %v", err)
	}
	if _, ok := lastProbe[pndjson.OverlayTrailerKey]; !ok {
		t.Errorf("last line is not the trailer: %q", lastLine)
	}

	// Round-trip via the reader's ReadOverlays.
	r := pndjson.NewReader(fs, "out.ndjson")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if err := pio.CompareOverlayLayers(got, helperOverlayLayers()); err != nil {
		t.Fatalf("CompareOverlayLayers: %v", err)
	}
}

// TestExportJob_Overlays_NDJSON_BodyByteIdenticalToBaseline verifies
// the host record stream stays byte-identical to a pre-overlay export
// until the trailer line. The trailer is the LAST line; the bytes
// before it match a baseline export with no overlays.
func TestExportJob_Overlays_NDJSON_BodyByteIdenticalToBaseline(t *testing.T) {
	fsBaseline := importThreeFieldCohort(t)
	wBaseline := pndjson.NewWriter(fsBaseline, "out.ndjson")
	baselineJob := pio.NewExportJob("test.pulse", wBaseline)
	baselineJob.FS = fsBaseline
	if _, err := baselineJob.Run(context.Background()); err != nil {
		t.Fatalf("baseline Export: %v", err)
	}
	if err := wBaseline.Close(); err != nil {
		t.Fatalf("baseline Close: %v", err)
	}
	baselineData, err := afero.ReadFile(fsBaseline, "out.ndjson")
	if err != nil {
		t.Fatalf("baseline ReadFile: %v", err)
	}

	fsOverlay := importThreeFieldCohort(t)
	wOverlay := pndjson.NewWriter(fsOverlay, "out.ndjson")
	overlayJob := pio.NewExportJob("test.pulse", wOverlay)
	overlayJob.FS = fsOverlay
	overlayJob.Overlays = helperOverlayLayers()
	if _, err := overlayJob.Run(context.Background()); err != nil {
		t.Fatalf("overlay Export: %v", err)
	}
	if err := wOverlay.Close(); err != nil {
		t.Fatalf("overlay Close: %v", err)
	}
	overlayData, err := afero.ReadFile(fsOverlay, "out.ndjson")
	if err != nil {
		t.Fatalf("overlay ReadFile: %v", err)
	}

	// The overlay-bearing output must be the baseline output + the
	// trailer line at the end. Strip the trailing newline from the
	// baseline and compare the prefix.
	if !bytes.HasPrefix(overlayData, baselineData) {
		t.Errorf("overlay NDJSON body diverges from baseline:\nbaseline: %q\noverlay:  %q", baselineData, overlayData)
	}
	// The overlay file must be strictly larger than the baseline (the
	// trailer line is at least `{"_overlays":[...]}\n`).
	if len(overlayData) <= len(baselineData) {
		t.Errorf("overlay NDJSON = %d bytes, baseline = %d, want overlay strictly larger", len(overlayData), len(baselineData))
	}
}

// TestExportJob_Overlays_NDJSON_OptOutSuppressesTrailer mirrors the
// Arrow opt-out test on the NDJSON adapter.
func TestExportJob_Overlays_NDJSON_OptOutSuppressesTrailer(t *testing.T) {
	fs := importThreeFieldCohort(t)
	w := pndjson.NewWriter(fs, "out.ndjson")
	job := pio.NewExportJob("test.pulse", w)
	job.FS = fs
	job.Overlays = helperOverlayLayers()
	off := false
	job.IncludeOverlays = &off

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Writer Close: %v", err)
	}

	r := pndjson.NewReader(fs, "out.ndjson")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if got != nil {
		t.Errorf("ReadOverlays = %d layers, want nil after opt-out", len(got))
	}
}
