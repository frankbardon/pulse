package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// PULSE_IMPORT_ROWS_DROPPED at the job level: the partial-import warning
// (#304), its StrictRows refusal, the convert target stage, and the
// measured Predict's parity with Run. The every-entry-point table lives
// in cmd/pulse (import_rows_dropped_test.go).

func droppedFixture() (*stringRowsReader, *encoding.Schema) {
	return &stringRowsReader{header: []string{"id", "n"}, rows: [][]string{{"1", "1"}, {"2", "abc"}, {"3", "3"}, {"4", ""}}},
		&encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 1},
		}}
}

func wantDroppedDetails(t *testing.T, w *perrors.CodedError, dropped, imported, row int, column, stage string) {
	t.Helper()
	if w == nil || w.Code != perrors.PULSE_IMPORT_ROWS_DROPPED {
		t.Fatalf("warning = %v, want PULSE_IMPORT_ROWS_DROPPED", w)
	}
	d := w.Details
	ff, _ := d["first_failure"].(map[string]any)
	if d["dropped"] != dropped || d["imported"] != imported || ff == nil || ff["row"] != row || ff["column"] != column || ff["reason"] == "" {
		t.Errorf("details = %v, want dropped %d imported %d first_failure row %d column %q", d, dropped, imported, row, column)
	}
	if got, _ := d["stage"].(string); got != stage {
		t.Errorf("stage = %q, want %q", got, stage)
	}
}

func TestImportJob_RowsDropped_Warning(t *testing.T) {
	src, schema := droppedFixture()
	job := &ImportJob{Source: src, Target: "p.pulse", Schema: schema, FS: afero.NewMemMapFs()}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.RowErrors) != 2 || rep.RowsImported != 2 {
		t.Fatalf("RowErrors %d, RowsImported %d; want 2, 2", len(rep.RowErrors), rep.RowsImported)
	}
	if len(rep.DroppedWarnings) != 1 {
		t.Fatalf("DroppedWarnings = %v, want one", rep.DroppedWarnings)
	}
	w := rep.DroppedWarnings[0]
	wantDroppedDetails(t, w, 2, 2, 2, "n", "")
	if ff := w.Details["first_failure"].(map[string]any); ff["reason"] == rep.RowErrors[0].Err.Error() {
		t.Errorf("reason %q repeats the row error's prefixed text; want the bare reason", ff["reason"])
	}
}

func TestImportJob_RowsDropped_CleanImportHasNone(t *testing.T) {
	_, schema := droppedFixture()
	job := &ImportJob{Source: &stringRowsReader{header: []string{"id", "n"}, rows: [][]string{{"1", "1"}}}, Target: "p.pulse", Schema: schema, FS: afero.NewMemMapFs()}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.DroppedWarnings != nil {
		t.Errorf("DroppedWarnings = %v on a clean import, want nil", rep.DroppedWarnings)
	}
}

func TestImportJob_RowsDropped_StrictWritesNothing(t *testing.T) {
	src, schema := droppedFixture()
	fs := afero.NewMemMapFs()
	job := &ImportJob{Source: src, Target: "p.pulse", Schema: schema, FS: fs, StrictRows: true}
	rep, err := job.Run(context.Background())
	if rep != nil || err == nil {
		t.Fatalf("Run = (%v, %v), want a nil report and the refusal", rep, err)
	}
	wantDroppedDetails(t, codedFrom(t, err), 2, 2, 2, "n", "")
	if ok, _ := afero.Exists(fs, "p.pulse"); ok {
		t.Error("StrictRows refusal left a cohort behind")
	}
}

// TestImportJob_RowsDropped_PredictParity: the measured Predict reaches
// Run's verdict on the same rows — the warning, and under StrictRows the
// same refusal.
func TestImportJob_RowsDropped_PredictParity(t *testing.T) {
	for _, strict := range []bool{false, true} {
		src, schema := droppedFixture()
		job := &ImportJob{Source: src, Target: "p.pulse", Schema: schema, FS: afero.NewMemMapFs(), ElideConstants: true, StrictRows: strict}
		pred, perr := job.Predict(context.Background())
		src.pos = 0
		_, rerr := job.Run(context.Background())
		if strict {
			if !perrors.HasCode(perr, perrors.PULSE_IMPORT_ROWS_DROPPED) || !perrors.HasCode(rerr, perrors.PULSE_IMPORT_ROWS_DROPPED) {
				t.Fatalf("strict: predict %v, run %v; want both PULSE_IMPORT_ROWS_DROPPED", perr, rerr)
			}
			wantDroppedDetails(t, codedFrom(t, perr), 2, 2, 2, "n", "")
			continue
		}
		if perr != nil || rerr != nil {
			t.Fatalf("predict %v, run %v", perr, rerr)
		}
		if len(pred.DroppedWarnings) != 1 {
			t.Fatalf("predict DroppedWarnings = %v, want one", pred.DroppedWarnings)
		}
		wantDroppedDetails(t, pred.DroppedWarnings[0], 2, 2, 2, "n", "")
	}
}

func TestConvertJob_RowsDropped_TargetStage(t *testing.T) {
	for _, strict := range []bool{false, true} {
		job := &ConvertJob{
			Source: &stringRowsReader{header: []string{"n"}, rows: [][]string{{"1"}, {"2"}, {"3"}}},
			Target: &failingWriter{failOn: map[int]bool{2: true}},
			Schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			}},
			FS:         afero.NewMemMapFs(),
			StrictRows: strict,
		}
		rep, err := job.Run(context.Background())
		if strict {
			if rep != nil {
				t.Fatalf("strict: report %v, want nil", rep)
			}
			wantDroppedDetails(t, codedFrom(t, err), 1, 2, 2, "", DroppedStageTarget)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.DroppedWarnings) != 1 {
			t.Fatalf("DroppedWarnings = %v, want one", rep.DroppedWarnings)
		}
		wantDroppedDetails(t, rep.DroppedWarnings[0], 1, 2, 2, "", DroppedStageTarget)
	}
}
