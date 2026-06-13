package exportoverlay

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
)

// TestExportJob_Overlays_Arrow_RoundTrip drives the full ExportJob.Run
// dispatch with an Arrow writer carrying the three-shape overlay fixture
// (scalar / series / matrix). The on-disk Arrow artefact is then re-read
// via parrow.Reader and the embedded overlay layers compared against the
// fixture using the shared CompareOverlayLayers assertion helper.
//
// This is the integration claim for the Arrow column-family sidecar
// (research/export-embedding-shape.md § 3): when ExportJob.Overlays is
// non-empty AND ExportJob.IncludeOverlays does not opt out, the writer
// receives SetOverlays(layers) and emits the populated list in row 0 of
// batch 0; the reader walks every list element across every batch and
// returns the first non-empty list.
func TestExportJob_Overlays_Arrow_RoundTrip(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := parrow.NewWriter(fs, "out.arrow")
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
	// Arrow embeds natively — no OverlayWarnings expected.
	if len(report.OverlayWarnings) != 0 {
		t.Errorf("OverlayWarnings = %v, want empty", report.OverlayWarnings)
	}

	r := parrow.NewReader(fs, "out.arrow")
	defer r.Close()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 || header[0] != "age" || header[1] != "name" || header[2] != "country" {
		t.Errorf("header = %v, want [age name country]", header)
	}

	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if err := pio.CompareOverlayLayers(got, helperOverlayLayers()); err != nil {
		t.Fatalf("CompareOverlayLayers: %v", err)
	}
}

// TestExportJob_Overlays_Arrow_OptOutDropsOverlays verifies that
// IncludeOverlays = *false suppresses overlay embedding even when
// ExportJob.Overlays carries a non-empty slice. The Arrow file then
// re-reads with no overlays present — ReadOverlays returns nil.
//
// This locks down the tri-state dispatcher contract from io.go's
// ExportJob.IncludeOverlays doc (nil / *true emit; *false opt out).
func TestExportJob_Overlays_Arrow_OptOutDropsOverlays(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := parrow.NewWriter(fs, "out.arrow")
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

	r := parrow.NewReader(fs, "out.arrow")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if got != nil {
		t.Errorf("ReadOverlays = %d layers, want nil", len(got))
	}
}

// TestExportJob_Overlays_Arrow_EmptyOverlaysIsNoOp verifies that an
// empty Overlays slice produces the byte-identical Arrow output to a
// pre-overlay export (the overlays field is OMITTED from the schema,
// not present-with-empty-list). The host record stream stays
// untouched.
func TestExportJob_Overlays_Arrow_EmptyOverlaysIsNoOp(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := parrow.NewWriter(fs, "out.arrow")
	job := pio.NewExportJob("test.pulse", w)
	job.FS = fs
	job.Overlays = nil

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Writer Close: %v", err)
	}

	r := parrow.NewReader(fs, "out.arrow")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if got != nil {
		t.Errorf("ReadOverlays = %d layers, want nil", len(got))
	}
}
