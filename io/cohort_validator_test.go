package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The optional VALIDATE-side interface E6-S1 added, tested at the
// dispatcher rather than in an adapter.
//
// io/spss is the only implementer today and its own tests cover what it
// decides. What is checked here is the half that belongs to io/: that
// ExportJob.Predict consults a validating Target, that a refusal comes
// back as the error verbatim, that warnings land on the report — and,
// the load-bearing negative, that a Target which does not implement the
// interface (or is absent entirely) is predicted exactly as it was
// before the interface existed.

// validatingWriter is a Writer that also satisfies CohortValidator,
// recording what the dispatcher handed it and answering with whatever
// the test pre-loaded.
type validatingWriter struct {
	collectWriter
	src    CohortSource
	called int
	warns  []*errors.CodedError
	refuse error
}

func (w *validatingWriter) ValidateCohort(_ context.Context, src CohortSource) ([]*errors.CodedError, error) {
	w.called++
	w.src = src
	if w.refuse != nil {
		return nil, w.refuse
	}
	return w.warns, nil
}

// TestExportJob_Predict_ConsultsTheTarget is the story in one assertion:
// predict used to answer from the source schema alone no matter what the
// target was.
func TestExportJob_Predict_ConsultsTheTarget(t *testing.T) {
	fs := importThreeFieldFixture(t)

	w := &validatingWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if w.called != 1 {
		t.Errorf("ValidateCohort called %d time(s), want exactly 1", w.called)
	}
	// The source half of the report is unchanged by the consultation.
	if report.Schema == nil || len(report.Schema.Fields) != 3 {
		t.Errorf("PredictReport.Schema lost its fields: %+v", report.Schema)
	}
	if report.EstimatedRows != 3 {
		t.Errorf("EstimatedRows = %d, want 3", report.EstimatedRows)
	}
	// No write lifecycle is started on a writer predict will never Close.
	if len(w.collectWriter.header) != 0 {
		t.Errorf("WriteHeader was called during Predict (%v); validation starts no write", w.collectWriter.header)
	}
	if len(w.collectWriter.rows) != 0 {
		t.Errorf("WriteRow was called %d time(s) during Predict", len(w.collectWriter.rows))
	}
}

// TestExportJob_Predict_CarriesTheSourceAndTheRefusables pins what rides
// CohortSource into a validator. It has to be the same shape Run builds,
// or a validator would refuse a projection the export accepts — or worse,
// accept one the export refuses.
func TestExportJob_Predict_CarriesTheSourceAndTheRefusables(t *testing.T) {
	fs := importThreeFieldFixture(t)

	w := &validatingWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs
	job.Includes = []string{"age"}
	job.LabelResolver = stubResolver{}

	if _, err := job.Predict(context.Background()); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if w.src.Path != "test.pulse" {
		t.Errorf("CohortSource.Path = %q, want the cohort path — it is where a metadata sidecar rides", w.src.Path)
	}
	if w.src.FS == nil {
		t.Error("CohortSource.FS is nil; a validator reaching for the cohort through os would break fs.NewMemMap()")
	}
	if len(w.src.Includes) != 1 || w.src.Includes[0] != "age" {
		t.Errorf("CohortSource.Includes = %v, want the job's own slice", w.src.Includes)
	}
	if !w.src.Labelled {
		t.Error("CohortSource.Labelled = false with a resolver set; a validator could not refuse --labels")
	}
}

// TestExportJob_Predict_WarningsLandOnTheReport covers the reporting
// channel. A warning stranded inside the adapter is a warning nobody acts
// on.
func TestExportJob_Predict_WarningsLandOnTheReport(t *testing.T) {
	fs := importThreeFieldFixture(t)

	want := errors.NewCodedError(errors.PULSE_SPSS_SIDECAR_ABSENT, "synthesised")
	w := &validatingWriter{warns: []*errors.CodedError{want}}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if len(report.TargetWarnings) != 1 || report.TargetWarnings[0] != want {
		t.Errorf("PredictReport.TargetWarnings = %v, want the validator's own slice", report.TargetWarnings)
	}
	// SourceWarnings is a different channel and must not have absorbed it.
	if report.SourceWarnings != nil {
		t.Errorf("SourceWarnings = %v, want nil — a target diagnostic is not a source one", report.SourceWarnings)
	}
}

// TestExportJob_Predict_RefusalIsReturnedVerbatim is the parity claim the
// CLI depends on. `pulse errors lookup` is only usable if the code that
// reaches the envelope is the export's own, so Predict must not wrap.
func TestExportJob_Predict_RefusalIsReturnedVerbatim(t *testing.T) {
	fs := importThreeFieldFixture(t)

	want := errors.NewCodedError(errors.PULSE_SPSS_NAME_INVALID, "illegal name")
	w := &validatingWriter{refuse: want}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	report, err := job.Predict(context.Background())
	if err == nil {
		t.Fatal("Predict returned no error for a refusing target")
	}
	if err != error(want) {
		t.Errorf("Predict returned %v, want the validator's own coded error", err)
	}
	if report != nil {
		t.Errorf("Predict returned a report alongside a refusal (%+v); a refusal has no prediction", report)
	}
}

// TestExportJob_Predict_NonValidatingTargetUnchanged is the negative that
// makes the addition safe, and the promise CohortValidator's doc comment
// makes to every format that is not `.sav`: a Target that does not
// implement the interface — and a Target that is absent entirely, which
// is what `pulse export predict` built before E6-S1 — must produce the
// same report the function produced before the interface existed.
func TestExportJob_Predict_NonValidatingTargetUnchanged(t *testing.T) {
	fs := importThreeFieldFixture(t)

	for name, target := range map[string]Writer{
		"nil target":           nil,
		"non-validating write": &collectWriter{},
	} {
		t.Run(name, func(t *testing.T) {
			job := NewExportJob("test.pulse", target)
			job.FS = fs

			report, err := job.Predict(context.Background())
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if report.TargetWarnings != nil {
				t.Errorf("TargetWarnings = %v on a target that cannot validate, want nil", report.TargetWarnings)
			}
			if report.EstimatedRows != 3 || len(report.Schema.Fields) != 3 {
				t.Errorf("report moved: EstimatedRows=%d fields=%d, want 3 and 3",
					report.EstimatedRows, len(report.Schema.Fields))
			}
		})
	}
}

// stubResolver is the minimum LabelResolver a test needs to set
// CohortSource.Labelled. It never resolves anything: Predict does not
// read a record, so nothing calls through it.
type stubResolver struct{}

func (stubResolver) Has(string) bool                          { return false }
func (stubResolver) Mode(string) types.LabelMode              { return "" }
func (stubResolver) Apply(_, _ string) (string, string, bool) { return "", "", false }
func (stubResolver) FieldsWithAugment() []string              { return nil }
func (stubResolver) Warnings() []LabelWarning                 { return nil }
