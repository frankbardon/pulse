package spss

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E6-S1: `pulse export predict` against a `.sav` target.
//
// The claim under test is PARITY, not correctness in isolation. A predict
// that refuses is only worth having if the export refuses the same way, and
// a predict that passes is only safe if it never refuses an export that
// would have worked. Every test here therefore runs BOTH halves against the
// same cohort rather than asserting a code in the abstract.

// cohortFromRows builds a `.pulse` cohort with the given column names
// through the ordinary import path. There is no metadata sidecar, which is
// the point for the name tests: the SPSS names are SYNTHESISED from the
// cohort field names, so a name the format cannot carry reaches the policy.
func cohortFromRows(t *testing.T, columns []string, rows [][]string) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(&rowReader{columns: columns, rows: rows}, "c.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("building the fixture cohort: %v", err)
	}
	return fs, "c.pulse"
}

// predictSav runs `export predict` against a `.sav` target the way the CLI
// does — through pio.ExportJob, with the writer pointed at a filesystem of
// its own so nothing it might emit can reach the cohort's.
func predictSav(t *testing.T, fs afero.Fs, cohort string, opts WriterOptions) (*pio.PredictReport, error) {
	t.Helper()
	job := pio.NewExportJob(cohort, NewWriter(afero.NewMemMapFs(), "predict.out", opts))
	job.FS = fs
	return job.Predict(context.Background())
}

// runSav runs the real export against the same cohort.
func runSav(t *testing.T, fs afero.Fs, cohort string, opts WriterOptions) error {
	t.Helper()
	w := NewWriter(fs, "out.sav", opts)
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		return err
	}
	return w.Close()
}

// ---------------------------------------------------------------------------
// The interface
// ---------------------------------------------------------------------------

// TestWriter_SatisfiesCohortValidator. The dispatch is a type assertion, so
// a Writer that quietly stopped satisfying this would not fail to compile —
// `pulse export predict --format spss` would silently go back to answering
// from the source schema alone.
func TestWriter_SatisfiesCohortValidator(t *testing.T) {
	var w any = NewWriter(afero.NewMemMapFs(), "out.sav", WriterOptions{})
	if _, ok := w.(pio.CohortValidator); !ok {
		t.Error("the .sav Writer does not satisfy pio.CohortValidator; export predict would not consult it")
	}
}

// ---------------------------------------------------------------------------
// Refusal parity
// ---------------------------------------------------------------------------

// TestValidateCohort_NameRefusalMatchesTheExport is the acceptance case: a
// cohort whose field names are not legal SPSS variable names must be refused
// by predict with the code the export itself returns.
func TestValidateCohort_NameRefusalMatchesTheExport(t *testing.T) {
	fs, cohort := cohortFromRows(t,
		[]string{"household income", "age"},
		[][]string{{"10", "1"}, {"20", "2"}})

	_, perrPredict := predictSav(t, fs, cohort, WriterOptions{})
	if perrPredict == nil {
		t.Fatal("predict accepted a cohort with an illegal SPSS variable name")
	}
	if got := codeOf(t, perrPredict); got != perr.PULSE_SPSS_NAME_INVALID {
		t.Errorf("predict refused with %s, want PULSE_SPSS_NAME_INVALID", got)
	}

	errExport := runSav(t, fs, cohort, WriterOptions{})
	if errExport == nil {
		t.Fatal("the export accepted what predict refused; predict must not be stricter than the export")
	}
	if got, want := codeOf(t, errExport), codeOf(t, perrPredict); got != want {
		t.Errorf("export refused with %s, predict with %s; the two must agree", got, want)
	}
	// The diagnostic has to name the same column, or a caller acting on the
	// predict would rename the wrong field.
	if !strings.Contains(perrPredict.Error(), "household income") {
		t.Errorf("the predicted refusal does not name the offending field: %v", perrPredict)
	}
}

// TestValidateCohort_SanitiseNamesTurnsTheRefusalIntoAWarning is the
// never-refuse-what-the-export-accepts rule, exercised on the knob that
// moves the verdict. Predict is told about the same options the export will
// run under, so a --sanitise-names export predicts as a pass.
func TestValidateCohort_SanitiseNamesTurnsTheRefusalIntoAWarning(t *testing.T) {
	fs, cohort := cohortFromRows(t,
		[]string{"household income", "age"},
		[][]string{{"10", "1"}, {"20", "2"}})

	opts := WriterOptions{SanitiseNames: true}
	report, err := predictSav(t, fs, cohort, opts)
	if err != nil {
		t.Fatalf("predict refused a --sanitise-names export that the export accepts: %v", err)
	}
	if !hasCode(report.TargetWarnings, perr.PULSE_SPSS_NAME_SANITISED) {
		t.Errorf("predict raised no PULSE_SPSS_NAME_SANITISED; warnings = %v", codes(report.TargetWarnings))
	}
	if err := runSav(t, fs, cohort, opts); err != nil {
		t.Fatalf("the export failed after a passing predict: %v", err)
	}
}

// TestValidateCohort_RefusesTheRowStreamOptions. --include and --labels are
// refused by WriteCohort; a predict that stayed silent about them would send
// a caller to an export that fails on its first act.
func TestValidateCohort_RefusesTheRowStreamOptions(t *testing.T) {
	fs, cohort := cohortFromRows(t, []string{"age"}, [][]string{{"10"}, {"20"}})

	for _, tt := range []struct {
		name  string
		apply func(*pio.ExportJob)
	}{
		{"--include", func(j *pio.ExportJob) { j.Includes = []string{"age"} }},
		{"--labels", func(j *pio.ExportJob) { j.LabelResolver = noopResolver{} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			predict := pio.NewExportJob(cohort, NewWriter(afero.NewMemMapFs(), "p.out", WriterOptions{}))
			predict.FS = fs
			tt.apply(predict)
			_, err := predict.Predict(context.Background())
			if err == nil {
				t.Fatalf("predict accepted %s on a .sav export", tt.name)
			}
			if got := codeOf(t, err); got != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
				t.Errorf("predict refused with %s, want PULSE_SPSS_EXPORT_UNSUPPORTED", got)
			}

			run := pio.NewExportJob(cohort, NewWriter(fs, "out.sav", WriterOptions{}))
			run.FS = fs
			tt.apply(run)
			_, runErr := run.Run(context.Background())
			if runErr == nil {
				t.Fatalf("the export accepted %s that predict refused", tt.name)
			}
			if got, want := codeOf(t, runErr), codeOf(t, err); got != want {
				t.Errorf("export refused with %s, predict with %s", got, want)
			}
		})
	}
}

// TestValidateCohort_StaleSidecarRefusalMatchesTheExport covers the other
// half of the sidecar policy. An ABSENT sidecar is a warning; a STALE one is
// a refusal, and predict must report it as one.
func TestValidateCohort_StaleSidecarRefusalMatchesTheExport(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	// Move the cohort's bytes so the sidecar's size fingerprint no longer
	// matches — the same staleness the read path checks.
	raw, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading the cohort: %v", err)
	}
	if err := afero.WriteFile(fs, cohort, append(raw, raw...), 0644); err != nil {
		t.Fatalf("rewriting the cohort: %v", err)
	}

	_, predictErr := predictSav(t, fs, cohort, WriterOptions{})
	if predictErr == nil {
		t.Fatal("predict accepted a cohort whose metadata sidecar is stale")
	}
	if got := codeOf(t, predictErr); got != perr.PULSE_SPSS_SIDECAR_STALE {
		t.Errorf("predict refused with %s, want PULSE_SPSS_SIDECAR_STALE", got)
	}
	exportErr := runSav(t, fs, cohort, WriterOptions{})
	if exportErr == nil {
		t.Fatal("the export accepted the stale sidecar predict refused")
	}
	if got, want := codeOf(t, exportErr), codeOf(t, predictErr); got != want {
		t.Errorf("export refused with %s, predict with %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// Success parity
// ---------------------------------------------------------------------------

// TestValidateCohort_PassPredictsASuccessfulExport is the other side of the
// acceptance pair, over a cohort carrying a real SPSS metadata sidecar.
func TestValidateCohort_PassPredictsASuccessfulExport(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	report, err := predictSav(t, fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("predict refused a cohort the export accepts: %v", err)
	}
	if len(report.TargetWarnings) != 0 {
		t.Errorf("a sidecar-backed export predicted warnings %v, want none", codes(report.TargetWarnings))
	}
	if err := runSav(t, fs, cohort, WriterOptions{}); err != nil {
		t.Fatalf("the export failed after a passing predict: %v", err)
	}
}

// TestValidateCohort_WarnsWhereTheExportWarns. An absent sidecar means the
// dictionary is synthesised rather than reproduced — a caveat, not a
// refusal, and the caveat has to survive into the prediction or the caller
// cannot tell a faithful re-emission from a reconstruction.
func TestValidateCohort_WarnsWhereTheExportWarns(t *testing.T) {
	fs, cohort := cohortFromRows(t, []string{"age", "score"}, [][]string{{"10", "1"}, {"20", "2"}})

	report, err := predictSav(t, fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("predict refused an export with no sidecar; absent is a warning, not an error: %v", err)
	}
	if !hasCode(report.TargetWarnings, perr.PULSE_SPSS_SIDECAR_ABSENT) {
		t.Fatalf("predict raised no PULSE_SPSS_SIDECAR_ABSENT; warnings = %v", codes(report.TargetWarnings))
	}

	// And the export raises the same one, through its own channel.
	w := NewWriter(fs, "out.sav", WriterOptions{})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	exportReport, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("the export failed after a passing predict: %v", err)
	}
	if !hasCode(exportReport.TargetWarnings, perr.PULSE_SPSS_SIDECAR_ABSENT) {
		t.Errorf("the export's warnings %v do not include the one predict raised", codes(exportReport.TargetWarnings))
	}
}

// ---------------------------------------------------------------------------
// No side effects
// ---------------------------------------------------------------------------

// TestValidateCohort_WritesNothing. Predict declares no output path and must
// leave none: the filesystem it was handed must carry exactly what it
// carried before.
func TestValidateCohort_WritesNothing(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())
	before := listFS(t, fs)

	if _, err := predictSav(t, fs, cohort, WriterOptions{}); err != nil {
		t.Fatalf("predict: %v", err)
	}

	after := listFS(t, fs)
	if strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("predict changed the filesystem: %v → %v", before, after)
	}
}

// TestValidateCohort_LeavesTheWriterUsable. Validation is not a
// half-performed encode: nothing is recorded on the Writer, and the same
// Writer still produces the file it would have produced without the
// validation.
func TestValidateCohort_LeavesTheWriterUsable(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	w := NewWriter(fs, "out.sav", WriterOptions{})
	if _, err := w.ValidateCohort(context.Background(), pio.CohortSource{FS: fs, Path: cohort}); err != nil {
		t.Fatalf("ValidateCohort: %v", err)
	}
	if len(w.Bytes()) != 0 {
		t.Errorf("ValidateCohort left %d encoded byte(s) on the writer", len(w.Bytes()))
	}
	if len(w.Warnings()) != 0 {
		t.Errorf("ValidateCohort recorded %v on the writer's own warning channel", codes(w.Warnings()))
	}
	if len(w.Renames()) != 0 {
		t.Errorf("ValidateCohort recorded %d rename(s) on the writer", len(w.Renames()))
	}

	// The same writer still encodes, and encodes the same file the tested
	// encoder path produces.
	if _, err := w.WriteCohort(context.Background(), pio.CohortSource{FS: fs, Path: cohort}); err != nil {
		t.Fatalf("WriteCohort after ValidateCohort: %v", err)
	}
	want := exportCohort(t, fs, cohort, WriterOptions{})
	if string(w.Bytes()) != string(want) {
		t.Error("a validated-then-written cohort produced different bytes from a written one")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// listFS names every file on fs, so a predict that quietly emitted one is
// visible as a difference rather than as a passing test.
func listFS(t *testing.T, fs afero.Fs) []string {
	t.Helper()
	entries, err := afero.ReadDir(fs, "/")
	if err != nil {
		t.Fatalf("reading the fixture filesystem: %v", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name()+":"+strconv.FormatInt(e.Size(), 10))
	}
	sort.Strings(out)
	return out
}

// noopResolver is the minimum pio.LabelResolver a test needs to set
// CohortSource.Labelled. Nothing calls through it: both arms refuse before
// a cell is rendered.
type noopResolver struct{}

func (noopResolver) Has(string) bool                          { return false }
func (noopResolver) Mode(string) types.LabelMode              { return "" }
func (noopResolver) Apply(_, _ string) (string, string, bool) { return "", "", false }
func (noopResolver) FieldsWithAugment() []string              { return nil }
func (noopResolver) Warnings() []pio.LabelWarning             { return nil }
