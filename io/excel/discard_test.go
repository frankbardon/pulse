package excel

import (
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestWriter_DiscardReleasesWithoutEmitting covers the ONE resource
// concern on the io leaves' error return.
//
// Skipping Close on an error path is correct for the DATA: every adapter
// emits its file exactly once inside Close, so not closing writes nothing
// (TestExportTargets_EmitNothingBeforeClose in internal/cli pins that).
// Excel is the exception on RESOURCES, not on data. It drives an excelize
// StreamWriter, whose buffer spills to an os.CreateTemp file once it
// passes excelize.StreamChunkSize (16 MiB), and excelize.File.Close is the
// only thing that removes those temp files. A large export that errors
// mid-run therefore used to leave one behind.
//
// Discard is the release-without-emitting half of Close: it drops the
// workbook and its temp files and writes NOTHING to the target. Adding a
// plain `defer Close` instead would have written a zero-row workbook next
// to a hard error, which is the trap this whole family exists to avoid.
func TestWriter_DiscardReleasesWithoutEmitting(t *testing.T) {
	fs := afero.NewMemMapFs()
	const path = "out.xlsx"
	w := NewWriter(fs, path)

	if err := w.WriteHeader([]string{"n"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"1"}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if w.file == nil {
		t.Fatal("precondition: no workbook was built, so there is nothing to release")
	}

	if err := w.Discard(); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if w.file != nil {
		t.Error("Discard left the excelize workbook open; its temp files are never removed")
	}
	if ok, _ := afero.Exists(fs, path); ok {
		t.Error("Discard wrote the target; it must release only, never emit")
	}

	// Close after Discard stays a no-op — the error path must not be
	// able to resurrect a zero-row target.
	if err := w.Close(); err != nil {
		t.Fatalf("Close after Discard: %v", err)
	}
	if ok, _ := afero.Exists(fs, path); ok {
		t.Error("Close after Discard emitted the target")
	}
}

// TestWriter_SatisfiesDiscardableWriter pins the optional interface so the
// CLI's type assertion keeps reaching the excel adapter.
func TestWriter_SatisfiesDiscardableWriter(t *testing.T) {
	var w pio.Writer = NewWriter(afero.NewMemMapFs(), "out.xlsx")
	if _, ok := w.(pio.DiscardableWriter); !ok {
		t.Error("excel.Writer no longer satisfies pio.DiscardableWriter; the export leaves' error path stops releasing its temp files")
	}
}
