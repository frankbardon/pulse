package io

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// zoneTuples is the source-zone fixture: a date column, a naive datetime, a
// Z-suffixed datetime and a second naive datetime. Row 3 (1-based) holds
// the America/New_York fall-back overlap (2026-11-01 01:30); every other
// naive value is unambiguous in both zones used below.
var zoneTuples = [][]string{
	{"2026-07-01", "2026-07-01 12:00", "2026-07-01T12:00:00Z", "2026-07-01 12:00"},
	{"2026-01-15", "2026-01-15T08:00:00", "2026-01-15T08:00:00+01:00", "2026-01-15 08:00"},
	{"2026-11-01", "2026-11-01 01:30", "2026-11-01T01:30:00Z", "2026-11-01 01:30"},
}

var zoneColumns = []string{"day", "local", "stamped", "other"}

func zoneJob(fs afero.Fs, rows [][]string) *ImportJob {
	j := NewImportJob(newMockReader(zoneColumns, rows), "z.pulse")
	j.FS = fs
	return j
}

// exportColumn re-reads a cohort and returns column col's exported text.
func exportColumn(t *testing.T, fs afero.Fs, path string, col int) []any {
	t.Helper()
	w := &collectWriter{}
	ej := NewExportJob(path, w)
	ej.FS = fs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	out := make([]any, len(w.rows))
	for i, r := range w.rows {
		out[i] = r[col]
	}
	return out
}

func codedErr(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *errors.CodedError", err, err)
	}
	return ce
}

// TestImportSourceTZ_DefaultPolicyRefusesAmbiguousRow: under the default
// policy the first ambiguous wall clock refuses the import, naming row,
// column, value and zone, and no cohort is written.
func TestImportSourceTZ_DefaultPolicyRefusesAmbiguousRow(t *testing.T) {
	fs := afero.NewMemMapFs()
	j := zoneJob(fs, repeatRows(zoneTuples, minSampleRows))
	j.SourceTZ = "America/New_York"
	_, err := j.Run(context.Background())
	ce := codedErr(t, err)
	if ce.Code != errors.PULSE_IMPORT_DST_AMBIGUOUS {
		t.Fatalf("code = %s, want PULSE_IMPORT_DST_AMBIGUOUS (%v)", ce.Code, err)
	}
	want := map[string]any{"row": 3, "column": "local", "value": "2026-11-01 01:30", "zone": "America/New_York"}
	for k, v := range want {
		if ce.Details[k] != v {
			t.Errorf("details[%s] = %v, want %v", k, ce.Details[k], v)
		}
	}
	if ok, _ := afero.Exists(fs, "z.pulse"); ok {
		t.Errorf("a refused import must write nothing")
	}
}

// TestImportSourceTZ_GapRefused: a spring-forward wall clock is
// PULSE_IMPORT_DST_NONEXISTENT under the default policy.
func TestImportSourceTZ_GapRefused(t *testing.T) {
	rows := repeatRows(zoneTuples[:2], minSampleRows)
	rows[9][1] = "2026-03-08 02:30"
	j := zoneJob(afero.NewMemMapFs(), rows)
	j.SourceTZ = "America/New_York"
	_, err := j.Run(context.Background())
	ce := codedErr(t, err)
	if ce.Code != errors.PULSE_IMPORT_DST_NONEXISTENT || ce.Details["row"] != 10 {
		t.Fatalf("got %s row %v, want PULSE_IMPORT_DST_NONEXISTENT at row 10", ce.Code, ce.Details["row"])
	}
}

// TestImportSourceTZ_LaterPolicyStoresInstantsAndWarns: --dst-policy later
// imports, stores every naive value as its New York instant (the overlap
// at its second occurrence), leaves Z / offset literals and the date
// column untouched, and reports the resolved count once.
func TestImportSourceTZ_LaterPolicyStoresInstantsAndWarns(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := repeatRows(zoneTuples, 60) // 20 overlap rows in each naive column
	j := zoneJob(fs, rows)
	j.SourceTZ = "America/New_York"
	j.DSTPolicy = DSTPolicyLater
	rep, err := j.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 0 {
		t.Fatalf("row errors: %v", rep.RowErrors)
	}
	if len(rep.ZoneWarnings) != 1 {
		t.Fatalf("ZoneWarnings = %v, want one", rep.ZoneWarnings)
	}
	w := rep.ZoneWarnings[0]
	if w.Code != errors.PULSE_IMPORT_DST_RESOLVED || w.Details["ambiguous_n"] != 40 || w.Details["nonexistent_n"] != 0 || w.Details["policy"] != "later" {
		t.Fatalf("warning = %s %v", w.Code, w.Details)
	}
	wantLocal := []string{"2026-07-01T16:00:00Z", "2026-01-15T13:00:00Z", "2026-11-01T06:30:00Z"}
	wantStamped := []string{"2026-07-01T12:00:00Z", "2026-01-15T07:00:00Z", "2026-11-01T01:30:00Z"}
	local, stamped, day := exportColumn(t, fs, "z.pulse", 1), exportColumn(t, fs, "z.pulse", 2), exportColumn(t, fs, "z.pulse", 0)
	for i := range rows {
		if local[i] != wantLocal[i%3] || stamped[i] != wantStamped[i%3] || day[i] != zoneTuples[i%3][0] {
			t.Fatalf("row %d: local %v stamped %v day %v", i+1, local[i], stamped[i], day[i])
		}
	}

	// earlier takes the first occurrence.
	fs2 := afero.NewMemMapFs()
	j2 := zoneJob(fs2, rows)
	j2.SourceTZ = "America/New_York"
	j2.DSTPolicy = DSTPolicyEarlier
	if _, err := j2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := exportColumn(t, fs2, "z.pulse", 1)[2]; got != "2026-11-01T05:30:00Z" {
		t.Fatalf("earlier overlap = %v, want 2026-11-01T05:30:00Z", got)
	}
}

// TestImportSourceTZ_PerColumnWins: a ColumnSourceTZ entry beats the
// global zone for its column only; a fixed offset is accepted.
func TestImportSourceTZ_PerColumnWins(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := repeatRows(zoneTuples[:2], minSampleRows)
	j := zoneJob(fs, rows)
	j.SourceTZ = "Europe/Berlin"
	j.ColumnSourceTZ = map[string]string{"other": "+05:30"}
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	local, other := exportColumn(t, fs, "z.pulse", 1), exportColumn(t, fs, "z.pulse", 3)
	if local[0] != "2026-07-01T10:00:00Z" || local[1] != "2026-01-15T07:00:00Z" {
		t.Errorf("global Berlin column = %v %v", local[0], local[1])
	}
	if other[0] != "2026-07-01T06:30:00Z" || other[1] != "2026-01-15T02:30:00Z" {
		t.Errorf("per-column +05:30 column = %v %v", other[0], other[1])
	}
}

// TestImportSourceTZ_Refusals: option errors fail before the row pass.
func TestImportSourceTZ_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		set    func(*ImportJob)
		code   errors.Code
		option string
	}{
		{"date column", func(j *ImportJob) { j.ColumnSourceTZ = map[string]string{"day": "Europe/Berlin"} }, errors.SERVICE_VALIDATION, "source_tz"},
		{"unknown column", func(j *ImportJob) { j.ColumnSourceTZ = map[string]string{"nope": "Europe/Berlin"} }, errors.SERVICE_VALIDATION, "source_tz"},
		{"unknown zone", func(j *ImportJob) { j.SourceTZ = "Europe/Nowhere" }, errors.PULSE_TIMEZONE_UNKNOWN, ""},
		{"bad offset", func(j *ImportJob) { j.SourceTZ = "+5:30" }, errors.PULSE_TIMEZONE_UNKNOWN, ""},
		{"bad per-column zone", func(j *ImportJob) { j.ColumnSourceTZ = map[string]string{"local": "EST"} }, errors.PULSE_TIMEZONE_UNKNOWN, ""},
		{"bad policy", func(j *ImportJob) { j.DSTPolicy = "first" }, errors.SERVICE_VALIDATION, "dst_policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, predict := range []bool{false, true} {
				j := zoneJob(afero.NewMemMapFs(), repeatRows(zoneTuples[:2], minSampleRows))
				tc.set(j)
				var err error
				if predict {
					_, err = j.Predict(context.Background())
				} else {
					_, err = j.Run(context.Background())
				}
				ce := codedErr(t, err)
				if ce.Code != tc.code {
					t.Fatalf("predict=%v code = %s, want %s (%v)", predict, ce.Code, tc.code, err)
				}
				if tc.option != "" && ce.Details[DetailOption] != tc.option {
					t.Errorf("details[option] = %v, want %s", ce.Details[DetailOption], tc.option)
				}
			}
		})
	}
}

// TestImportSourceTZ_UTCEquivalentIsByteIdentical: no zone, "UTC",
// "Etc/UTC" and "+00:00" write the same bytes as a job that never heard
// of source zones.
func TestImportSourceTZ_UTCEquivalentIsByteIdentical(t *testing.T) {
	rows := repeatRows(zoneTuples, minSampleRows)
	run := func(set func(*ImportJob)) []byte {
		fs := afero.NewMemMapFs()
		j := zoneJob(fs, rows)
		set(j)
		if _, err := j.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		b, _ := afero.ReadFile(fs, "z.pulse")
		return b
	}
	base := run(func(*ImportJob) {})
	for _, z := range []string{"UTC", "Etc/UTC", "+00:00"} {
		if got := run(func(j *ImportJob) { j.SourceTZ = z; j.DSTPolicy = DSTPolicyLater }); !bytes.Equal(got, base) {
			t.Errorf("SourceTZ %q changed the stored bytes", z)
		}
	}
}

// TestImportSourceTZ_ExplicitSchema: the zone applies to a caller-authored
// schema's datetime fields too.
func TestImportSourceTZ_ExplicitSchema(t *testing.T) {
	fs := afero.NewMemMapFs()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "local", Type: encoding.FieldTypeDateTime, CsvColumnIdx: 1},
	}}
	j := zoneJob(fs, repeatRows(zoneTuples[:2], 4))
	j.Schema = schema
	j.SourceTZ = "Asia/Kolkata"
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := exportColumn(t, fs, "z.pulse", 0)[0]; got != "2026-07-01T06:30:00Z" {
		t.Fatalf("got %v", got)
	}
}

// TestImportSourceTZ_PredictAgreesWithRun: the plain predict and the
// measured predict refuse at the same row with the same code, and report
// the same resolved counts, as Run.
func TestImportSourceTZ_PredictAgreesWithRun(t *testing.T) {
	rows := repeatRows(zoneTuples, 60)
	for _, measured := range []bool{false, true} {
		j := zoneJob(afero.NewMemMapFs(), rows)
		j.SourceTZ = "America/New_York"
		j.ElideConstants = measured
		_, err := j.Predict(context.Background())
		ce := codedErr(t, err)
		if ce.Code != errors.PULSE_IMPORT_DST_AMBIGUOUS || ce.Details["row"] != 3 || ce.Details["column"] != "local" {
			t.Fatalf("measured=%v: %s %v", measured, ce.Code, ce.Details)
		}

		j = zoneJob(afero.NewMemMapFs(), rows)
		j.SourceTZ = "America/New_York"
		j.DSTPolicy = DSTPolicyEarlier
		j.ElideConstants = measured
		rep, err := j.Predict(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.ZoneWarnings) != 1 || rep.ZoneWarnings[0].Details["ambiguous_n"] != 40 {
			t.Fatalf("measured=%v: ZoneWarnings = %v", measured, rep.ZoneWarnings)
		}
	}
}

// TestImportSourceTZ_GapInSampleStillInfersDatetime: the inference probe
// is zone-free, so a sampled gap value does not demote the column; the row
// pass refuses it loudly instead.
func TestImportSourceTZ_GapInSampleStillInfersDatetime(t *testing.T) {
	rows := repeatRows(zoneTuples[:2], minSampleRows)
	rows[0][1] = "2026-03-29 02:30"
	j := zoneJob(afero.NewMemMapFs(), rows)
	j.SourceTZ = "Europe/Berlin"
	_, err := j.Predict(context.Background())
	if ce := codedErr(t, err); ce.Code != errors.PULSE_IMPORT_DST_NONEXISTENT {
		t.Fatalf("code = %s", ce.Code)
	}
	j = zoneJob(afero.NewMemMapFs(), rows)
	j.SourceTZ = "Europe/Berlin"
	j.DSTPolicy = DSTPolicyLater
	r, err := j.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Schema.Field("local"); f == nil || f.Type != encoding.FieldTypeDateTime {
		t.Fatalf("local = %v, want datetime", f)
	}
	if r.ZoneWarnings[0].Details["nonexistent_n"] != 1 {
		t.Fatalf("warning = %v", r.ZoneWarnings[0].Details)
	}
}
