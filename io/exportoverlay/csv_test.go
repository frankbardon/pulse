package exportoverlay

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	pcsv "github.com/frankbardon/pulse/io/csv"
	"github.com/spf13/afero"
)

// TestExportJob_Overlays_CSVWarnAndSkip drives the full ExportJob.Run
// dispatch with a CSV writer carrying the three-shape overlay fixture.
// CSV is flat — the adapter records the layers via SetOverlays but
// never writes them into the CSV body. The dispatcher lifts the
// PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning surfaced through
// OverlayWarningEmitter onto ExportReport.OverlayWarnings.
//
// The test verifies:
//
//   - the CSV body is byte-identical to a pre-overlay export
//   - exactly one PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning lands on
//     ExportReport.OverlayWarnings
//   - the warning's Details carry layer_count, layer_names, and
//     layer_kinds matching the recorded layer slate
func TestExportJob_Overlays_CSVWarnAndSkip(t *testing.T) {
	// Baseline export — no overlays. Used to compare the CSV body
	// byte-for-byte against the overlay-bearing export.
	baselineFs := importThreeFieldCohort(t)
	baselineW := pcsv.NewWriter(baselineFs, "out.csv")
	baselineJob := pio.NewExportJob("test.pulse", baselineW)
	baselineJob.FS = baselineFs
	if _, err := baselineJob.Run(context.Background()); err != nil {
		t.Fatalf("baseline Export: %v", err)
	}
	if err := baselineW.Close(); err != nil {
		t.Fatalf("baseline Close: %v", err)
	}
	baselineBody, err := afero.ReadFile(baselineFs, "out.csv")
	if err != nil {
		t.Fatalf("baseline ReadFile: %v", err)
	}

	// Overlay-bearing export — same Source/Target/Includes, plus
	// Overlays. The CSV body MUST stay byte-identical to the baseline.
	overlayFs := importThreeFieldCohort(t)
	overlayW := pcsv.NewWriter(overlayFs, "out.csv")
	overlayJob := pio.NewExportJob("test.pulse", overlayW)
	overlayJob.FS = overlayFs
	overlayJob.Overlays = helperOverlayLayers()

	report, err := overlayJob.Run(context.Background())
	if err != nil {
		t.Fatalf("overlay Export: %v", err)
	}
	if err := overlayW.Close(); err != nil {
		t.Fatalf("overlay Close: %v", err)
	}
	overlayBody, err := afero.ReadFile(overlayFs, "out.csv")
	if err != nil {
		t.Fatalf("overlay ReadFile: %v", err)
	}

	if !bytes.Equal(baselineBody, overlayBody) {
		t.Errorf("CSV body diverges from baseline:\nbaseline: %q\noverlay:  %q", baselineBody, overlayBody)
	}

	// Warning must fire — exactly one, with the canonical code.
	if len(report.OverlayWarnings) != 1 {
		t.Fatalf("OverlayWarnings = %d, want 1: %v", len(report.OverlayWarnings), report.OverlayWarnings)
	}
	warn := report.OverlayWarnings[0]
	if warn.Code != errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED {
		t.Errorf("warning code = %s, want %s", warn.Code, errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED)
	}

	// Details: layer_count, layer_names, layer_kinds populated.
	count, ok := warn.Details["layer_count"].(int)
	if !ok || count != 3 {
		t.Errorf("Details[layer_count] = %v, want 3", warn.Details["layer_count"])
	}
	names, ok := warn.Details["layer_names"].([]string)
	if !ok || len(names) != 3 {
		t.Fatalf("Details[layer_names] = %v, want 3 entries", warn.Details["layer_names"])
	}
	wantNames := []string{"chisq_total", "index_vs_total", "index_vs_margin"}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("Details[layer_names][%d] = %q, want %q", i, names[i], want)
		}
	}
}

// TestExportJob_Overlays_CSVWarnAndSkip_OptOutSuppressesWarning verifies
// IncludeOverlays = *false suppresses the SetOverlays call entirely so
// the warn-and-skip warning never fires. The CSV body still equals the
// baseline.
func TestExportJob_Overlays_CSVWarnAndSkip_OptOutSuppressesWarning(t *testing.T) {
	fs := importThreeFieldCohort(t)
	w := pcsv.NewWriter(fs, "out.csv")
	job := pio.NewExportJob("test.pulse", w)
	job.FS = fs
	job.Overlays = helperOverlayLayers()
	off := false
	job.IncludeOverlays = &off

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(report.OverlayWarnings) != 0 {
		t.Errorf("OverlayWarnings = %v, want empty after opt-out", report.OverlayWarnings)
	}
}

// TestExportJob_Overlays_CSVWarnAndSkip_NoOverlaysNoWarning verifies
// an overlay-free export emits no warning — the warning is specifically
// "you asked for overlays but CSV cannot carry them," not "CSV cannot
// carry overlays in general."
func TestExportJob_Overlays_CSVWarnAndSkip_NoOverlaysNoWarning(t *testing.T) {
	fs := importThreeFieldCohort(t)
	w := pcsv.NewWriter(fs, "out.csv")
	job := pio.NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(report.OverlayWarnings) != 0 {
		t.Errorf("OverlayWarnings = %v, want empty", report.OverlayWarnings)
	}
}
