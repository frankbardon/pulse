package exportoverlay

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	pparquet "github.com/frankbardon/pulse/io/parquet"
	"github.com/spf13/afero"
)

// TestConvertJob_Overlays_PreservedAcrossFormats verifies that overlay
// layers handed to ConvertJob.Overlays land in the target adapter's
// format-native sidecar — and survive a round-trip back through the
// target's Reader. The Arrow → Parquet pairing is the canonical case
// (both adapters share the Arrow LIST<STRUCT> schema per
// research/export-embedding-shape.md § 4.4); this test exercises the
// dispatch wiring end-to-end.
//
// The intermediate ConvertJob does NOT need to read overlays from the
// source Reader — overlays are an export-side concern; the convert
// surface accepts them as an explicit input on ConvertJob.Overlays
// just like ExportJob.Overlays. Higher-level facades (pulse.Convert
// against a streamable Response) lift the response's Overlays slot onto
// the convert input before dispatch.
func TestConvertJob_Overlays_PreservedAcrossFormats(t *testing.T) {
	// Step 1: build an Arrow source file with three host rows. The
	// source Reader carries no embedded overlays — ConvertJob.Overlays
	// is the engine input, not the source.
	fs := afero.NewMemMapFs()

	srcW := parrow.NewWriter(fs, "src.arrow")
	if err := srcW.WriteHeader([]string{"age", "name", "country"}); err != nil {
		t.Fatalf("source WriteHeader: %v", err)
	}
	srcRows := [][]any{
		{"10", "alice", "US"},
		{"20", "bob", "GB"},
		{"30", "carol", "US"},
	}
	for _, r := range srcRows {
		if err := srcW.WriteRow(r); err != nil {
			t.Fatalf("source WriteRow: %v", err)
		}
	}
	if err := srcW.Close(); err != nil {
		t.Fatalf("source Close: %v", err)
	}

	// Step 2: convert Arrow → Parquet with overlays.
	srcR := parrow.NewReader(fs, "src.arrow")
	defer srcR.Close()

	dstW := pparquet.NewWriter(fs, "dst.parquet")
	job := pio.NewConvertJob(srcR, dstW)
	job.FS = fs
	job.Overlays = helperOverlayLayers()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if err := dstW.Close(); err != nil {
		t.Fatalf("dst Close: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("RowsConverted = %d, want 3", report.RowsConverted)
	}
	if len(report.OverlayWarnings) != 0 {
		t.Errorf("OverlayWarnings = %v, want empty", report.OverlayWarnings)
	}

	// Step 3: round-trip via the Parquet reader.
	dstR := pparquet.NewReader(fs, "dst.parquet")
	defer dstR.Close()
	got, err := dstR.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if err := pio.CompareOverlayLayers(got, helperOverlayLayers()); err != nil {
		t.Fatalf("CompareOverlayLayers: %v", err)
	}
}

// TestConvertJob_Overlays_OptOutDropsOverlays mirrors the ExportJob
// opt-out test on the ConvertJob path: IncludeOverlays = *false
// suppresses the SetOverlays call so the destination carries no
// overlays.
func TestConvertJob_Overlays_OptOutDropsOverlays(t *testing.T) {
	fs := afero.NewMemMapFs()

	srcW := parrow.NewWriter(fs, "src.arrow")
	if err := srcW.WriteHeader([]string{"age", "name", "country"}); err != nil {
		t.Fatalf("source WriteHeader: %v", err)
	}
	if err := srcW.WriteRow([]any{"10", "alice", "US"}); err != nil {
		t.Fatalf("source WriteRow: %v", err)
	}
	if err := srcW.Close(); err != nil {
		t.Fatalf("source Close: %v", err)
	}

	srcR := parrow.NewReader(fs, "src.arrow")
	defer srcR.Close()

	dstW := pparquet.NewWriter(fs, "dst.parquet")
	job := pio.NewConvertJob(srcR, dstW)
	job.FS = fs
	job.Overlays = helperOverlayLayers()
	off := false
	job.IncludeOverlays = &off

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if err := dstW.Close(); err != nil {
		t.Fatalf("dst Close: %v", err)
	}

	dstR := pparquet.NewReader(fs, "dst.parquet")
	defer dstR.Close()
	got, err := dstR.ReadOverlays()
	if err != nil {
		t.Fatalf("ReadOverlays: %v", err)
	}
	if got != nil {
		t.Errorf("ReadOverlays = %d layers, want nil after opt-out", len(got))
	}
}
