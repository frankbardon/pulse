package exportoverlay

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pparquet "github.com/frankbardon/pulse/io/parquet"
)

// TestExportJob_Overlays_Parquet_RoundTrip drives the full ExportJob.Run
// dispatch with a Parquet writer carrying the three-shape overlay
// fixture. The Parquet adapter rides the same Arrow LIST<STRUCT> schema
// as the Arrow adapter (research/export-embedding-shape.md § 4.4 "Why
// Parquet shares the Arrow schema"); the round-trip claim mirrors the
// Arrow test exactly, swapping the writer/reader pair.
func TestExportJob_Overlays_Parquet_RoundTrip(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := pparquet.NewWriter(fs, "out.parquet")
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

	r := pparquet.NewReader(fs, "out.parquet")
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

// TestExportJob_Overlays_Parquet_OptOutDropsOverlays mirrors the Arrow
// opt-out test on the Parquet adapter.
func TestExportJob_Overlays_Parquet_OptOutDropsOverlays(t *testing.T) {
	fs := importThreeFieldCohort(t)

	w := pparquet.NewWriter(fs, "out.parquet")
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

	r := pparquet.NewReader(fs, "out.parquet")
	defer r.Close()
	got, err := r.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if got != nil {
		t.Errorf("ReadOverlays = %d layers, want nil", len(got))
	}
}
