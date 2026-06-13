package exportoverlay

import (
	"context"
	"strings"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pexcel "github.com/frankbardon/pulse/io/excel"
)

// TestExportJob_Overlays_Excel_Sheets drives the full ExportJob.Run
// dispatch with an Excel writer carrying the three-shape overlay
// fixture. The Excel adapter (research/export-embedding-shape.md § 5)
// emits one sheet per layer named "__overlay_<layer_name>" alongside
// the host data sheet. The test verifies the layer sheets exist, the
// reader can reconstruct the embedded layers, and the overlay payload
// shapes round-trip.
func TestExportJob_Overlays_Excel_Sheets(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := pexcel.NewWriter(fs, "out.xlsx")
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

	r := pexcel.NewReader(fs, "out.xlsx")
	defer r.Close()

	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ReadOverlays = %d layers, want 3", len(got))
	}

	// Verify the layer slate matches the fixture. Excel may drop the
	// summary parameters across the round-trip (the summary block uses
	// a JSON cell for the parameters map; the per-format reader
	// accepts a map[string]float64 only), and the kind / scope / ref
	// stays exact. The shared helper handles the per-shape payload
	// comparison.
	if err := pio.CompareOverlayLayers(got, helperOverlayLayers()); err != nil {
		t.Fatalf("CompareOverlayLayers: %v", err)
	}
}

// TestExportJob_Overlays_Excel_SheetNamePattern verifies the
// "__overlay_<name>" sheet-name pattern by introspecting the workbook
// produced by the export. This is the explicit acceptance criterion
// from the story — every sheet emitted for an overlay layer carries
// the OverlaySheetPrefix and includes the layer name.
func TestExportJob_Overlays_Excel_SheetNamePattern(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := pexcel.NewWriter(fs, "out.xlsx")
	job := pio.NewExportJob("test.pulse", w)
	job.FS = fs
	job.Overlays = helperOverlayLayers()

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Writer Close: %v", err)
	}

	// Open the workbook directly to introspect sheet names.
	r := pexcel.NewReader(fs, "out.xlsx")
	defer r.Close()

	sheetNames, err := r.SheetNames()
	if err != nil {
		t.Fatalf("SheetNames: %v", err)
	}

	var overlaySheets []string
	for _, s := range sheetNames {
		if strings.HasPrefix(s, pexcel.OverlaySheetPrefix) {
			overlaySheets = append(overlaySheets, s)
		}
	}
	if len(overlaySheets) != 3 {
		t.Fatalf("overlay sheets = %v, want 3 sheets with prefix %q", overlaySheets, pexcel.OverlaySheetPrefix)
	}

	wantNames := []string{"chisq_total", "index_vs_total", "index_vs_margin"}
	for i, want := range wantNames {
		got := strings.TrimPrefix(overlaySheets[i], pexcel.OverlaySheetPrefix)
		if got != want {
			t.Errorf("overlay sheet [%d] = %q, want %q (full: %q)", i, got, want, overlaySheets[i])
		}
	}
}

// TestExportJob_Overlays_Excel_OptOutNoOverlaySheets verifies the
// tri-state IncludeOverlays gate on Excel: *false suppresses the
// SetOverlays call so no overlay sheets land on the workbook.
func TestExportJob_Overlays_Excel_OptOutNoOverlaySheets(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := pexcel.NewWriter(fs, "out.xlsx")
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

	r := pexcel.NewReader(fs, "out.xlsx")
	defer r.Close()

	sheetNames, err := r.SheetNames()
	if err != nil {
		t.Fatalf("SheetNames: %v", err)
	}
	for _, s := range sheetNames {
		if strings.HasPrefix(s, pexcel.OverlaySheetPrefix) {
			t.Errorf("unexpected overlay sheet %q present after opt-out", s)
		}
	}
}
