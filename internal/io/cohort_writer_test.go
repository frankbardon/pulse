package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// The two optional WRITE-side interfaces E5-S6 added, tested at the
// dispatcher rather than in an adapter.
//
// io/spss is the only implementer today and its own tests cover what it
// does with them. What is checked here is the half that belongs to io/:
// that a CohortWriter REPLACES the row loop instead of running beside it,
// that it is handed the cohort plus the two row-stream transformations it
// must refuse, and — the load-bearing negative — that a writer
// implementing NEITHER interface produces exactly the report it produced
// before either existed.

// cohortOnlyWriter is a Writer that also satisfies CohortWriter and
// TargetWarningEmitter, recording what the dispatcher handed it.
type cohortOnlyWriter struct {
	collectWriter
	src    CohortSource
	called int
	rows   int
	warns  []*errors.CodedError
}

func (w *cohortOnlyWriter) WriteCohort(_ context.Context, src CohortSource) (int, error) {
	w.called++
	w.src = src
	return w.rows, nil
}

func (w *cohortOnlyWriter) Warnings() []*errors.CodedError { return w.warns }

// TestExportJob_CohortWriter_ReplacesTheRowLoop is the control-flow
// contract. A dispatcher that ran both would decode the cohort twice and
// hand the writer rendered text it exists precisely not to encode from.
func TestExportJob_CohortWriter_ReplacesTheRowLoop(t *testing.T) {
	fs := importThreeFieldFixture(t)

	w := &cohortOnlyWriter{rows: 3}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w.called != 1 {
		t.Errorf("WriteCohort called %d time(s), want exactly 1", w.called)
	}
	if len(w.collectWriter.rows) != 0 {
		t.Errorf("WriteRow was called %d time(s); a CohortWriter takes the place of the row loop", len(w.collectWriter.rows))
	}
	if len(w.collectWriter.header) == 0 {
		t.Error("WriteHeader was skipped; a CohortWriter still receives the projected column list")
	}
	if report.RowsExported != 3 {
		t.Errorf("RowsExported = %d, want the count WriteCohort returned (3)", report.RowsExported)
	}
}

// TestExportJob_CohortWriter_CarriesTheSourceAndTheRefusables pins what
// rides CohortSource. Path is where a format-specific metadata sidecar
// lives, and Includes / Labelled are the row-stream transformations a
// cohort writer must refuse rather than silently ignore.
func TestExportJob_CohortWriter_CarriesTheSourceAndTheRefusables(t *testing.T) {
	fs := importThreeFieldFixture(t)

	w := &cohortOnlyWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs
	job.Includes = []string{"age"}

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w.src.Path != "test.pulse" {
		t.Errorf("CohortSource.Path = %q, want the cohort path — it is where a metadata sidecar rides", w.src.Path)
	}
	if w.src.FS == nil {
		t.Error("CohortSource.FS is nil; a writer reaching for the cohort through os would break fs.NewMemMap()")
	}
	if len(w.src.Includes) != 1 || w.src.Includes[0] != "age" {
		t.Errorf("CohortSource.Includes = %v, want the job's own slice", w.src.Includes)
	}
	if w.src.Labelled {
		t.Error("CohortSource.Labelled = true with no resolver set")
	}
}

// TestExportJob_TargetWarnings_Lifted covers the reporting channel.
func TestExportJob_TargetWarnings_Lifted(t *testing.T) {
	fs := importThreeFieldFixture(t)

	want := errors.NewCodedError(errors.PULSE_SPSS_SIDECAR_ABSENT, "synthesised")
	w := &cohortOnlyWriter{warns: []*errors.CodedError{want}}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.TargetWarnings) != 1 || report.TargetWarnings[0] != want {
		t.Errorf("ExportReport.TargetWarnings = %v, want the writer's own slice", report.TargetWarnings)
	}
}

// TestExportJob_NoCohortWriter_ByteIdentical is the negative that makes
// the additions safe. Every adapter that shipped before these interfaces
// existed must take the same path and report the same shape.
func TestExportJob_NoCohortWriter_ByteIdentical(t *testing.T) {
	fs := importThreeFieldFixture(t)

	w := &collectWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(w.rows) != 3 {
		t.Errorf("WriteRow called %d time(s), want 3 — the row loop must still run", len(w.rows))
	}
	if report.TargetWarnings != nil {
		t.Errorf("TargetWarnings = %v on a writer that does not implement the interface, want nil", report.TargetWarnings)
	}
}

// TestConvertJob_TargetWarnings_Lifted mirrors the export lift on the
// other verb. Convert has no cohort, so a CohortWriter target takes the
// ordinary row path there — only the warning channel is shared.
func TestConvertJob_TargetWarnings_Lifted(t *testing.T) {
	want := errors.NewCodedError(errors.PULSE_SPSS_SIDECAR_ABSENT, "synthesised")
	w := &cohortOnlyWriter{warns: []*errors.CodedError{want}}

	job := NewConvertJob(newMockReader([]string{"age"}, [][]string{{"10"}, {"20"}}), w)
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.TargetWarnings) != 1 || report.TargetWarnings[0] != want {
		t.Errorf("ConvertReport.TargetWarnings = %v, want the writer's own slice", report.TargetWarnings)
	}

	plain := &collectWriter{}
	plainJob := NewConvertJob(newMockReader([]string{"age"}, [][]string{{"10"}}), plain)
	plainJob.FS = afero.NewMemMapFs()
	plainReport, err := plainJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if plainReport.TargetWarnings != nil {
		t.Errorf("TargetWarnings = %v on a writer that does not implement the interface, want nil", plainReport.TargetWarnings)
	}
}
