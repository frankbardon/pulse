package io

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Per-row source zone (ImportJob.SourceTZField / ConvertJob.SourceTZField).
// fieldZoneColumns: a naive datetime, a Z-stamped one, a second naive
// datetime, the zone column and a date column. Each row names its own
// zone: New York in summer and in winter (the same zone, both offsets),
// Berlin, Kolkata and a fixed offset.
var fieldZoneColumns = []string{"when", "stamped", "other", "zone", "day"}

var fieldZoneTuples = [][]string{
	{"2026-07-01 12:00", "2026-07-01T12:00:00Z", "2026-07-01 12:00", "America/New_York", "2026-07-01"},
	{"2026-01-15 08:00", "2026-07-01T12:00:00Z", "2026-07-01 12:00", "Europe/Berlin", "2026-01-15"},
	{"2026-07-01 12:00", "2026-07-01T12:00:00Z", "2026-07-01 12:00", "Asia/Kolkata", "2026-07-01"},
	{"2026-01-15 08:00", "2026-07-01T12:00:00Z", "2026-07-01 12:00", "+05:30", "2026-01-15"},
	{"2026-01-15 08:00", "2026-07-01T12:00:00Z", "2026-07-01 12:00", "America/New_York", "2026-01-15"},
}

// fieldZoneWhen / fieldZoneOther are the UTC instants each tuple's naive
// "when" / "other" cell names in that row's zone.
var (
	fieldZoneWhen  = []string{"2026-07-01T16:00:00Z", "2026-01-15T07:00:00Z", "2026-07-01T06:30:00Z", "2026-01-15T02:30:00Z", "2026-01-15T13:00:00Z"}
	fieldZoneOther = []string{"2026-07-01T16:00:00Z", "2026-07-01T10:00:00Z", "2026-07-01T06:30:00Z", "2026-07-01T06:30:00Z", "2026-07-01T16:00:00Z"}
)

func fieldZoneJob(fs afero.Fs, rows [][]string) *ImportJob {
	j := NewImportJob(newMockReader(fieldZoneColumns, rows), "z.pulse")
	j.FS = fs
	j.SourceTZField = "zone"
	return j
}

// TestImportSourceTZField_EachRowReadsInItsOwnZone: every naive datetime
// column reads in the row's zone (IANA with and without DST in force, a
// non-hour offset zone, a fixed ±HH:MM); a Z literal never moves; the date
// column and the zone column itself import as ordinary columns.
func TestImportSourceTZField_EachRowReadsInItsOwnZone(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := repeatRows(fieldZoneTuples, 60)
	rep, err := fieldZoneJob(fs, rows).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 0 || rep.RowsImported != len(rows) {
		t.Fatalf("imported %d, row errors %v", rep.RowsImported, rep.RowErrors)
	}
	if f := rep.Schema.Field("zone"); f == nil || !f.Type.IsCategorical() {
		t.Fatalf("zone column = %v, want an imported categorical", f)
	}
	when, stamped, other := exportColumn(t, fs, "z.pulse", 0), exportColumn(t, fs, "z.pulse", 1), exportColumn(t, fs, "z.pulse", 2)
	zone, day := exportColumn(t, fs, "z.pulse", 3), exportColumn(t, fs, "z.pulse", 4)
	for i := range rows {
		k := i % len(fieldZoneTuples)
		if when[i] != fieldZoneWhen[k] || other[i] != fieldZoneOther[k] {
			t.Fatalf("row %d (%s): when %v other %v, want %s / %s", i+1, fieldZoneTuples[k][3], when[i], other[i], fieldZoneWhen[k], fieldZoneOther[k])
		}
		if stamped[i] != "2026-07-01T12:00:00Z" || zone[i] != fieldZoneTuples[k][3] || day[i] != fieldZoneTuples[k][4] {
			t.Fatalf("row %d: stamped %v zone %v day %v moved", i+1, stamped[i], zone[i], day[i])
		}
	}
}

// TestImportSourceTZField_ColumnSourceTZWins: a ColumnSourceTZ entry fixes
// its column's zone; every other naive column still follows the row.
func TestImportSourceTZField_ColumnSourceTZWins(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := repeatRows(fieldZoneTuples, 60)
	j := fieldZoneJob(fs, rows)
	j.ColumnSourceTZ = map[string]string{"other": "Asia/Tokyo"}
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	when, other := exportColumn(t, fs, "z.pulse", 0), exportColumn(t, fs, "z.pulse", 2)
	for i := range rows {
		if other[i] != "2026-07-01T03:00:00Z" {
			t.Fatalf("row %d: other = %v, want the Tokyo reading", i+1, other[i])
		}
		if when[i] != fieldZoneWhen[i%len(fieldZoneTuples)] {
			t.Fatalf("row %d: when = %v", i+1, when[i])
		}
	}
	// A UTC entry keeps its column on the naive-UTC path.
	fs2 := afero.NewMemMapFs()
	j = fieldZoneJob(fs2, rows)
	j.ColumnSourceTZ = map[string]string{"other": "UTC"}
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := exportColumn(t, fs2, "z.pulse", 2)[0]; got != "2026-07-01T12:00:00Z" {
		t.Fatalf("UTC column override = %v", got)
	}
}

// TestImportSourceTZField_EmptyCell: an empty zone cell reads in SourceTZ
// when one is set; without one the row is a row error naming the row and
// the datetime column, and the rest imports.
func TestImportSourceTZField_EmptyCell(t *testing.T) {
	rows := repeatRows(fieldZoneTuples, 60)
	rows[5][3] = ""

	fs := afero.NewMemMapFs()
	j := fieldZoneJob(fs, rows)
	j.SourceTZ = "Europe/Berlin"
	rep, err := j.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 0 {
		t.Fatalf("fallback row errors: %v", rep.RowErrors)
	}
	// Row 6 is tuple 0 (a New York summer row) re-read in the Berlin
	// fallback; row 1, the same tuple with its zone, keeps New York.
	when := exportColumn(t, fs, "z.pulse", 0)
	if when[5] != "2026-07-01T10:00:00Z" || when[0] != fieldZoneWhen[0] {
		t.Fatalf("fallback when = %v, row 1 = %v", when[5], when[0])
	}
	if got := exportColumn(t, fs, "z.pulse", 2)[5]; got != "2026-07-01T10:00:00Z" {
		t.Fatalf("fallback other = %v, want the Berlin reading", got)
	}

	rep, err = fieldZoneJob(afero.NewMemMapFs(), rows).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 1 || rep.RowErrors[0].Row != 6 || rep.RowsImported != len(rows)-1 {
		t.Fatalf("no-fallback: imported %d, row errors %v", rep.RowsImported, rep.RowErrors)
	}
	ce := codedErr(t, rep.RowErrors[0].Err)
	if ce.Code != errors.PULSE_IMPORT_ROW_ERROR || ce.Details["row"] != 6 || ce.Details["column"] != "when" || ce.Details["zone_column"] != "zone" {
		t.Fatalf("empty-cell row error = %s %v", ce.Code, ce.Details)
	}
}

// TestImportSourceTZField_UnknownZoneIsRowCoded: an unknown zone name is a
// row error carrying PULSE_TIMEZONE_UNKNOWN and the row number; other rows
// import.
func TestImportSourceTZField_UnknownZoneIsRowCoded(t *testing.T) {
	rows := repeatRows(fieldZoneTuples, 60)
	rows[11][3] = "Mars/Olympus"
	rows[12][3] = "EST" // not Area/Location: refused by LoadImportZone
	rep, err := fieldZoneJob(afero.NewMemMapFs(), rows).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 2 || rep.RowsImported != len(rows)-2 {
		t.Fatalf("imported %d, row errors %v", rep.RowsImported, rep.RowErrors)
	}
	for k, want := range []struct {
		row  int
		zone string
	}{{12, "Mars/Olympus"}, {13, "EST"}} {
		re := rep.RowErrors[k]
		ce := codedErr(t, re.Err)
		if re.Row != want.row || ce.Code != errors.PULSE_TIMEZONE_UNKNOWN || ce.Details["row"] != want.row ||
			ce.Details["value"] != want.zone || ce.Details["zone_column"] != "zone" || ce.Details["column"] != "when" {
			t.Fatalf("row error %d = row %d %s %v", k, re.Row, ce.Code, ce.Details)
		}
	}
}

// TestImportSourceTZField_OffsetLiteralIgnoresBadZone: a row whose only
// datetime cells carry their own offset imports even when its zone cell
// is empty or unknown — the zone is never consulted.
func TestImportSourceTZField_OffsetLiteralIgnoresBadZone(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := repeatRows(fieldZoneTuples, 60)
	rows[2][0], rows[2][2], rows[2][3] = "2026-07-01T12:00:00+02:00", "", "Mars/Olympus"
	rows[3][0], rows[3][2], rows[3][3] = "2026-07-01T12:00:00Z", "", ""
	rep, err := fieldZoneJob(fs, rows).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RowErrors) != 0 {
		t.Fatalf("row errors: %v", rep.RowErrors)
	}
	when := exportColumn(t, fs, "z.pulse", 0)
	if when[2] != "2026-07-01T10:00:00Z" || when[3] != "2026-07-01T12:00:00Z" {
		t.Fatalf("offset literals = %v / %v", when[2], when[3])
	}
}

// TestImportSourceTZField_DSTPerRow: each row's DST transition is its own:
// a New York overlap / gap refuses at its row naming that row's zone,
// while the same wall clock in a Berlin row imports; a policy resolves and
// counts per row.
func TestImportSourceTZField_DSTPerRow(t *testing.T) {
	base := repeatRows(fieldZoneTuples, 60)
	berlin := func(rows [][]string, i int, wall string) {
		rows[i][0], rows[i][3] = wall, "Europe/Berlin"
	}
	for _, tc := range []struct {
		name string
		wall string
		code errors.Code
	}{
		{"overlap", "2026-11-01 01:30", errors.PULSE_IMPORT_DST_AMBIGUOUS},
		{"gap", "2026-03-08 02:30", errors.PULSE_IMPORT_DST_NONEXISTENT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := repeatRows(base, len(base))
			berlin(rows, 1, tc.wall) // unremarkable in Berlin
			rows[9][0] = tc.wall     // row 10 is New York (tuple 4)
			_, err := fieldZoneJob(afero.NewMemMapFs(), rows).Run(context.Background())
			ce := codedErr(t, err)
			if ce.Code != tc.code || ce.Details["row"] != 10 || ce.Details["zone"] != "America/New_York" || ce.Details["column"] != "when" {
				t.Fatalf("got %s %v", ce.Code, ce.Details)
			}

			fs := afero.NewMemMapFs()
			j := fieldZoneJob(fs, rows)
			j.DSTPolicy = DSTPolicyLater
			rep, err := j.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			n := "ambiguous_n"
			if tc.code == errors.PULSE_IMPORT_DST_NONEXISTENT {
				n = "nonexistent_n"
			}
			if len(rep.ZoneWarnings) != 1 || rep.ZoneWarnings[0].Details[n] != 1 {
				t.Fatalf("ZoneWarnings = %v", rep.ZoneWarnings)
			}
		})
	}
}

// TestImportSourceTZField_Refusals: a zone column the source does not
// carry, or one that is itself a datetime, is an option refusal before
// the row pass — on Run and Predict alike.
func TestImportSourceTZField_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
	}{
		{"missing column", "tz"},
		{"datetime column", "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, predict := range []bool{false, true} {
				fs := afero.NewMemMapFs()
				j := fieldZoneJob(fs, repeatRows(fieldZoneTuples, 60))
				j.SourceTZField = tc.field
				var err error
				if predict {
					_, err = j.Predict(context.Background())
				} else {
					_, err = j.Run(context.Background())
				}
				ce := codedErr(t, err)
				if ce.Code != errors.SERVICE_VALIDATION || ce.Details[DetailOption] != "source_tz_field" || ce.Details["column"] != tc.field {
					t.Fatalf("predict=%v: %s %v", predict, ce.Code, ce.Details)
				}
				if ok, _ := afero.Exists(fs, "z.pulse"); ok {
					t.Fatal("a refused import wrote a cohort")
				}
			}
		})
	}
}

// TestImportSourceTZField_PredictAgreesWithRun: the plain and the measured
// predict refuse at Run's row with Run's code and count the resolved
// values Run counts.
func TestImportSourceTZField_PredictAgreesWithRun(t *testing.T) {
	rows := repeatRows(fieldZoneTuples, 60)
	rows[9][0] = "2026-11-01 01:30"
	rows[19][0] = "2026-11-01 01:30"
	for _, measured := range []bool{false, true} {
		j := fieldZoneJob(afero.NewMemMapFs(), rows)
		j.ElideConstants = measured
		_, err := j.Predict(context.Background())
		ce := codedErr(t, err)
		if ce.Code != errors.PULSE_IMPORT_DST_AMBIGUOUS || ce.Details["row"] != 10 || ce.Details["zone"] != "America/New_York" {
			t.Fatalf("measured=%v: %s %v", measured, ce.Code, ce.Details)
		}
		j = fieldZoneJob(afero.NewMemMapFs(), rows)
		j.ElideConstants = measured
		j.DSTPolicy = DSTPolicyEarlier
		rep, err := j.Predict(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.ZoneWarnings) != 1 || rep.ZoneWarnings[0].Details["ambiguous_n"] != 2 {
			t.Fatalf("measured=%v: ZoneWarnings = %v", measured, rep.ZoneWarnings)
		}
	}
}

// TestImportSourceTZField_ZonesMemoisedPerName: a pass loads each distinct
// zone name once, however many rows name it.
func TestImportSourceTZField_ZonesMemoisedPerName(t *testing.T) {
	rows := repeatRows(fieldZoneTuples, 60)
	j := fieldZoneJob(afero.NewMemMapFs(), rows)
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := newMockReader(fieldZoneColumns, rows)
	res, err := InferSchemaWithOptions(src, InferOptions{SampleRows: 60})
	if err != nil {
		t.Fatal(err)
	}
	sz, err := j.resolveSourceZones(res.Schema)
	if err != nil {
		t.Fatal(err)
	}
	for n, row := range rows {
		sz.beginRow(row)
		if _, err := sz.parse(0, row[0], "when", n+1); err != nil {
			t.Fatal(err)
		}
	}
	if len(sz.memo) != 4 { // New York, Berlin, Kolkata, +05:30
		t.Fatalf("memo holds %d zones, want 4", len(sz.memo))
	}
}

// TestImportSourceTZField_UnsetIsByteIdentical: an empty SourceTZField
// writes exactly what a job that never heard of it writes, with or
// without a SourceTZ.
func TestImportSourceTZField_UnsetIsByteIdentical(t *testing.T) {
	rows := repeatRows(fieldZoneTuples, 60)
	run := func(field, tz string) []byte {
		fs := afero.NewMemMapFs()
		j := NewImportJob(newMockReader(fieldZoneColumns, rows), "z.pulse")
		j.FS, j.SourceTZField, j.SourceTZ = fs, field, tz
		if _, err := j.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		b, _ := afero.ReadFile(fs, "z.pulse")
		return b
	}
	if !bytes.Equal(run("", ""), run("", "UTC")) {
		t.Fatal("UTC source zone changed the bytes")
	}
	if bytes.Equal(run("", ""), run("zone", "")) {
		t.Fatal("SourceTZField changed nothing (fixture too weak)")
	}
}

// TestConvertSourceTZField_TargetCarriesRowInstants: convert writes each
// naive literal as the instant its row's zone names, a row whose zone
// cannot be resolved is dropped before the target (stage "source"), the
// kept cohort matches a plain import, and predict agrees with Run.
func TestConvertSourceTZField_TargetCarriesRowInstants(t *testing.T) {
	rows := repeatRows(fieldZoneTuples, 60)
	rows[4][3] = "Mars/Olympus"
	rep, target, kept := runConvert(t, newMockReader(fieldZoneColumns, rows), true, func(j *ConvertJob) {
		j.SourceTZField = "zone"
	})
	if len(target.rows) != len(rows)-1 || rep.RowsConverted != len(rows)-1 {
		t.Fatalf("target rows = %d, converted %d", len(target.rows), rep.RowsConverted)
	}
	if len(rep.RowErrors) != 1 || rep.RowErrors[0].Row != 5 || codedErr(t, rep.RowErrors[0].Err).Code != errors.PULSE_TIMEZONE_UNKNOWN {
		t.Fatalf("row errors = %v", rep.RowErrors)
	}
	var sourceDrop bool
	for _, w := range rep.DroppedWarnings {
		if w.Code == errors.PULSE_IMPORT_ROWS_DROPPED && w.Details["stage"] == DroppedStageSource {
			sourceDrop = true
		}
	}
	if !sourceDrop {
		t.Fatalf("DroppedWarnings = %v, want a stage=source entry", rep.DroppedWarnings)
	}
	for i, r := range target.rows {
		src := i
		if i >= 4 {
			src = i + 1
		}
		k := src % len(fieldZoneTuples)
		if r[0] != fieldZoneWhen[k] || r[2] != fieldZoneOther[k] || r[1] != "2026-07-01T12:00:00Z" || r[3] != fieldZoneTuples[k][3] {
			t.Fatalf("target row %d = %v", i+1, r)
		}
	}

	fs := afero.NewMemMapFs()
	ij := NewImportJob(newMockReader(fieldZoneColumns, rows), "z.pulse")
	ij.FS, ij.SourceTZField = fs, "zone"
	if _, err := ij.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "kept.pulse", kept, 0o644); err != nil {
		t.Fatal(err)
	}
	for col := 0; col < len(fieldZoneColumns); col++ {
		direct, viaConvert := exportColumn(t, fs, "z.pulse", col), exportColumn(t, fs, "kept.pulse", col)
		if len(direct) != len(viaConvert) {
			t.Fatalf("kept cohort has %d rows, plain import %d", len(viaConvert), len(direct))
		}
		for i := range direct {
			if direct[i] != viaConvert[i] {
				t.Fatalf("col %d row %d: kept %v, plain import %v", col, i+1, viaConvert[i], direct[i])
			}
		}
	}

	// Predict: the DST refusal Run raises, at Run's row.
	dst := repeatRows(fieldZoneTuples, 60)
	dst[9][0] = "2026-03-08 02:30"
	for _, predict := range []bool{false, true} {
		job := NewConvertJob(newMockReader(fieldZoneColumns, dst), &collectWriter{})
		job.FS, job.SourceTZField = afero.NewMemMapFs(), "zone"
		var err error
		if predict {
			_, err = job.Predict(context.Background())
		} else {
			_, err = job.Run(context.Background())
		}
		if ce := codedErr(t, err); ce.Code != errors.PULSE_IMPORT_DST_NONEXISTENT || ce.Details["row"] != 10 {
			t.Fatalf("predict=%v: %s %v", predict, ce.Code, ce.Details)
		}
	}
	// And the option refusal.
	job := NewConvertJob(newMockReader(fieldZoneColumns, rows), &collectWriter{})
	job.FS, job.SourceTZField = afero.NewMemMapFs(), "nope"
	if _, err := job.Predict(context.Background()); codedErr(t, err).Details[DetailOption] != "source_tz_field" {
		t.Fatalf("predict refusal = %v", err)
	}
}
