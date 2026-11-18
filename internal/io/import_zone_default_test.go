package io

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// The cohort DEFAULT zone field an import or convert declares
// (ImportJob.ZoneField / ConvertJob.ZoneField): written as the 0x02 ZONE
// section, refused before the row pass when it names no categorical
// field (SERVICE_VALIDATION, option "zone_field") and before anything is
// written when an entry is not a zone (PULSE_TIMEZONE_UNKNOWN), with
// predict reaching every verdict Run does.

// declaredZoneTuples: an id, a naive local time and the zone column —
// every entry a valid IANA zone.
var (
	declaredZoneColumns = []string{"id", "local", "zone"}
	declaredZoneTuples  = [][]string{
		{"1", "2026-07-01 12:00", "America/New_York"},
		{"2", "2026-07-01 12:00", "Europe/Berlin"},
		{"3", "2026-07-01 12:00", "Asia/Tokyo"},
	}
)

func declaredZoneJob(fs afero.Fs, rows [][]string, field string) *ImportJob {
	j := NewImportJob(newMockReader(declaredZoneColumns, rows), "z.pulse")
	j.FS = fs
	j.ZoneField = field
	return j
}

// readCohortSchema reads a written cohort's header + schema back.
func readCohortSchema(t *testing.T, fs afero.Fs, path string) (byte, *encoding.Schema) {
	t.Helper()
	b, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(b)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return v, s
}

// TestImportZoneField_WritesSection: a declared zone field rides the
// written cohort (format 0x02, Schema.ZoneField read back) and the
// report's schema; the rows are the undeclared import's, and an
// undeclared import stays a 0x01 cohort without the field.
func TestImportZoneField_WritesSection(t *testing.T) {
	t.Parallel()

	rows := repeatRows(declaredZoneTuples, 120)
	fs := afero.NewMemMapFs()
	rep, err := declaredZoneJob(fs, rows, "zone").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Schema.ZoneField != "zone" {
		t.Fatalf("report schema ZoneField = %q, want zone", rep.Schema.ZoneField)
	}
	v, s := readCohortSchema(t, fs, "z.pulse")
	if v != encoding.FormatVersionV2 || s.ZoneField != "zone" {
		t.Fatalf("written cohort: version %#x ZoneField %q, want 0x02 / zone", v, s.ZoneField)
	}

	plain := afero.NewMemMapFs()
	if _, err := declaredZoneJob(plain, rows, "").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	pv, ps := readCohortSchema(t, plain, "z.pulse")
	if pv != encoding.FormatVersionV1 || ps.ZoneField != "" {
		t.Fatalf("undeclared cohort: version %#x ZoneField %q, want 0x01 / none", pv, ps.ZoneField)
	}
	for col := range declaredZoneColumns {
		if got, want := exportColumn(t, fs, "z.pulse", col), exportColumn(t, plain, "z.pulse", col); !reflect.DeepEqual(got, want) {
			t.Fatalf("column %d differs from the undeclared import: %v vs %v", col, got, want)
		}
	}
}

// TestImportZoneField_ComposesWithGroupsAndElision: the section survives
// the grouped write (a declared group holding the zone field, and the
// constant group elision builds over a one-zone column).
func TestImportZoneField_ComposesWithGroupsAndElision(t *testing.T) {
	t.Parallel()

	rows := repeatRows(declaredZoneTuples, 120)
	fs := afero.NewMemMapFs()
	j := declaredZoneJob(fs, rows, "zone")
	j.Groups = []GroupDecl{{Members: []string{"local", "zone"}}}
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, s := readCohortSchema(t, fs, "z.pulse"); s.ZoneField != "zone" || !s.HasGroups() {
		t.Fatalf("grouped cohort: ZoneField %q groups %v", s.ZoneField, s.HasGroups())
	}

	one := make([][]string, 0, 120)
	for i := 0; i < 120; i++ {
		one = append(one, []string{"1", "2026-07-01 12:00", "Europe/Berlin"})
		one[i][0] = string(rune('0' + i%10))
	}
	efs := afero.NewMemMapFs()
	e := declaredZoneJob(efs, one, "zone")
	e.ElideConstants = true
	rep, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ElidedConstants) == 0 {
		t.Fatalf("expected the constant columns elided, report %v", rep.ElidedConstants)
	}
	if _, s := readCohortSchema(t, efs, "z.pulse"); s.ZoneField != "zone" {
		t.Fatalf("elided cohort ZoneField = %q, want zone", s.ZoneField)
	}
	for i, z := range exportColumn(t, efs, "z.pulse", 2) {
		if z != "Europe/Berlin" {
			t.Fatalf("row %d zone = %v", i+1, z)
		}
	}
}

// TestImportZoneField_RefusalsPredictEqualsRun: every refusal is raised
// by Run and Predict with the same code and details, and a refused Run
// writes nothing. A missing or non-categorical field is the option
// refusal; an entry that is not a zone keeps PULSE_TIMEZONE_UNKNOWN with
// its dictionary code.
func TestImportZoneField_RefusalsPredictEqualsRun(t *testing.T) {
	t.Parallel()

	good := repeatRows(declaredZoneTuples, 120)
	bad := repeatRows(declaredZoneTuples, 120)
	bad[70][2] = "+05:30" // a source-zone spelling, never a cohort zone
	cases := []struct {
		name    string
		rows    [][]string
		field   string
		code    errors.Code
		details map[string]any
	}{
		{"missing", good, "tz", errors.SERVICE_VALIDATION,
			map[string]any{DetailOption: "zone_field", encoding.DetailZoneField: "tz"}},
		{"not_categorical", good, "local", errors.SERVICE_VALIDATION,
			map[string]any{DetailOption: "zone_field", encoding.DetailZoneField: "local", "type": "datetime"}},
		{"bad_entry", bad, "zone", errors.PULSE_TIMEZONE_UNKNOWN,
			map[string]any{encoding.DetailZoneField: "zone", "dictionary_index": 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := afero.NewMemMapFs()
			_, err := declaredZoneJob(fs, tc.rows, tc.field).Run(context.Background())
			run := codedErr(t, err)
			if run.Code != tc.code {
				t.Fatalf("Run code = %s, want %s (%v)", run.Code, tc.code, err)
			}
			for k, v := range tc.details {
				if run.Details[k] != v {
					t.Errorf("Run details[%s] = %v, want %v (%v)", k, run.Details[k], v, run.Details)
				}
			}
			if ok, _ := afero.Exists(fs, "z.pulse"); ok {
				t.Error("a refused import must write nothing")
			}
			_, perr := declaredZoneJob(afero.NewMemMapFs(), tc.rows, tc.field).Predict(context.Background())
			pred := codedErr(t, perr)
			if pred.Code != run.Code || pred.Message != run.Message || !reflect.DeepEqual(pred.Details, run.Details) {
				t.Fatalf("Predict = %s %q %v\nRun     = %s %q %v", pred.Code, pred.Message, pred.Details, run.Code, run.Message, run.Details)
			}
		})
	}
}

// TestImportZoneField_PredictAccepts: a valid declaration predicts clean,
// sized with the ZONE section (the measured pass Run's write matches).
func TestImportZoneField_PredictAccepts(t *testing.T) {
	t.Parallel()

	rows := repeatRows(declaredZoneTuples, 120)
	rep, err := declaredZoneJob(afero.NewMemMapFs(), rows, "zone").Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fs := afero.NewMemMapFs()
	if _, err := declaredZoneJob(fs, rows, "zone").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := afero.ReadFile(fs, "z.pulse")
	if rep.Projection == nil || rep.Projection.ProjectedFileBytes != int64(len(b)) {
		t.Fatalf("projection %+v, want %d bytes (Run's file)", rep.Projection, len(b))
	}
}

// TestConvertZoneField: the KeepPulseAt intermediate carries the
// declared default; the target is unchanged; without KeepPulseAt the
// declaration is refused (no cohort to ride); a bad entry is refused by
// Run and Predict alike before the intermediate is written.
func TestConvertZoneField(t *testing.T) {
	t.Parallel()

	rows := repeatRows(declaredZoneTuples, 120)
	_, target, kept := runConvert(t, newMockReader(declaredZoneColumns, rows), true, func(j *ConvertJob) { j.ZoneField = "zone" })
	_, plainTarget, plainKept := runConvert(t, newMockReader(declaredZoneColumns, rows), true)
	if !reflect.DeepEqual(target.rows, plainTarget.rows) {
		t.Fatal("the zone field moved the convert target")
	}
	r := bytes.NewReader(kept)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil || s.ZoneField != "zone" {
		t.Fatalf("kept cohort ZoneField = %v (%v), want zone", s, err)
	}
	if bytes.Equal(kept, plainKept) {
		t.Fatal("the kept cohort did not change")
	}

	for _, keep := range []string{"", "kept.pulse"} {
		bad := repeatRows(declaredZoneTuples, 120)
		if keep != "" {
			bad[70][2] = "Mars/Olympus"
		}
		fs := afero.NewMemMapFs()
		job := NewConvertJob(newMockReader(declaredZoneColumns, bad), &collectWriter{})
		job.FS, job.KeepPulseAt, job.ZoneField = fs, keep, "zone"
		_, err := job.Run(context.Background())
		run := codedErr(t, err)
		want := errors.SERVICE_VALIDATION
		if keep != "" {
			want = errors.PULSE_TIMEZONE_UNKNOWN
		}
		if run.Code != want || run.Details[encoding.DetailZoneField] != "zone" {
			t.Fatalf("keep %q: Run = %s %v, want %s naming the field", keep, run.Code, run.Details, want)
		}
		if keep == "" && (run.Details[DetailOption] != "zone_field" || run.Details["reason"] != "no_cohort") {
			t.Fatalf("no-cohort refusal details = %v", run.Details)
		}
		if keep != "" && run.Details["dictionary_index"] != 3 {
			t.Fatalf("bad entry index = %v, want 3", run.Details["dictionary_index"])
		}
		if ok, _ := afero.Exists(fs, "kept.pulse"); ok {
			t.Fatalf("keep %q: a refused convert wrote the intermediate", keep)
		}
		pj := NewConvertJob(newMockReader(declaredZoneColumns, bad), &collectWriter{})
		pj.FS, pj.KeepPulseAt, pj.ZoneField = afero.NewMemMapFs(), keep, "zone"
		_, perr := pj.Predict(context.Background())
		pred := codedErr(t, perr)
		if pred.Code != run.Code || pred.Message != run.Message || !reflect.DeepEqual(pred.Details, run.Details) {
			t.Fatalf("keep %q: Predict = %s %q %v\nRun     = %s %q %v", keep, pred.Code, pred.Message, pred.Details, run.Code, run.Message, run.Details)
		}
	}
}
