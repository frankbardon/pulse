package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// repeatRows tiles the supplied per-row cell tuples until the sample
// floor is reached, so inference always sees a full sample window.
func repeatRows(tuples [][]string, n int) [][]string {
	out := make([][]string, 0, n)
	for i := 0; i < n; i++ {
		src := tuples[i%len(tuples)]
		row := make([]string, len(src))
		copy(row, src)
		out = append(out, row)
	}
	return out
}

// TestInferColumnType_DateTimeVsDate is the boundary pin. A column of
// full ISO-8601 datetime literals must classify as datetime; a column
// of date-only literals must STILL classify as date. The second half is
// the regression that matters — infer's own dateFormats list happily
// parses the T-forms, so probing datetime in the wrong order (or with a
// loose parser) would silently reclassify every existing date column.
func TestInferColumnType_DateTimeVsDate(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   encoding.FieldType
	}{
		{
			name:   "iso_datetime_utc",
			values: []string{"2024-03-04T10:11:12Z", "2024-03-05T23:00:00Z", "2024-03-06T00:00:01Z"},
			want:   encoding.FieldTypeDateTime,
		},
		{
			name:   "iso_datetime_naive",
			values: []string{"2024-03-04T10:11:12", "2024-03-05T23:00:00", "2024-03-06T00:00:01"},
			want:   encoding.FieldTypeDateTime,
		},
		{
			name:   "iso_datetime_offset",
			values: []string{"2024-03-04T10:11:12+02:00", "2024-03-05T23:00:00-05:00"},
			want:   encoding.FieldTypeDateTime,
		},
		{
			name:   "space_separated_datetime",
			values: []string{"2024-03-04 10:11:12", "2024-03-05 23:00:00"},
			want:   encoding.FieldTypeDateTime,
		},
		{
			name:   "datetime_at_midnight_is_still_datetime",
			values: []string{"2024-03-04T00:00:00Z", "2024-03-05T00:00:00Z"},
			want:   encoding.FieldTypeDateTime,
		},
		{
			name:   "date_only_iso_stays_date",
			values: []string{"2024-03-04", "2024-03-05", "2024-03-06"},
			want:   encoding.FieldTypeDate,
		},
		{
			name:   "date_only_slashed_stays_date",
			values: []string{"2024/03/04", "2024/03/05"},
			want:   encoding.FieldTypeDate,
		},
		{
			name:   "date_only_us_slash_stays_date",
			values: []string{"03/04/2024", "03/05/2024"},
			want:   encoding.FieldTypeDate,
		},
		{
			name:   "date_only_dmon_stays_date",
			values: []string{"04-Mar-2024", "05-Mar-2024"},
			want:   encoding.FieldTypeDate,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _, _, err := inferColumnTypeWithOpts("col", tc.values, defaultSetInferenceMinPct, 0)
			if err != nil {
				t.Fatalf("inferColumnTypeWithOpts: %v", err)
			}
			if got != tc.want {
				t.Errorf("inferred %s, want %s", got, tc.want)
			}
		})
	}
}

// TestInferColumnType_DateTimeNullable confirms null tokens interleaved
// with datetime literals still classify as datetime and flip nullable.
func TestInferColumnType_DateTimeNullable(t *testing.T) {
	values := []string{"2024-03-04T10:11:12Z", "", "2024-03-05T10:11:12Z", "N/A"}
	got, nullable, _, _, err := inferColumnTypeWithOpts("ts", values, defaultSetInferenceMinPct, 0)
	if err != nil {
		t.Fatalf("inferColumnTypeWithOpts: %v", err)
	}
	if got != encoding.FieldTypeDateTime {
		t.Errorf("inferred %s, want datetime", got)
	}
	if !nullable {
		t.Error("nullable = false, want true")
	}
}

// TestInferSchema_DateTimeColumnAlongsideDate exercises the full
// inference entry point so column ordering, byte offsets, and the
// date/datetime split are all checked together.
func TestInferSchema_DateTimeColumnAlongsideDate(t *testing.T) {
	rows := repeatRows([][]string{
		{"2024-03-04", "2024-03-04T10:11:12Z", "10"},
		{"2024-03-05", "2024-03-05T23:00:00Z", "20"},
	}, minSampleRows)

	reader := newMockReader([]string{"day", "stamp", "n"}, rows)
	schema, _, err := InferSchema(reader, minSampleRows)
	if err != nil {
		t.Fatalf("InferSchema: %v", err)
	}

	day := schema.Field("day")
	if day == nil || day.Type != encoding.FieldTypeDate {
		t.Fatalf("day.Type = %v, want date", day)
	}
	stamp := schema.Field("stamp")
	if stamp == nil || stamp.Type != encoding.FieldTypeDateTime {
		t.Fatalf("stamp.Type = %v, want datetime", stamp)
	}
	// date is 4 bytes, so datetime must start at offset 4 and the
	// following column at 4+8.
	if day.ByteOffset != 0 {
		t.Errorf("day.ByteOffset = %d, want 0", day.ByteOffset)
	}
	if stamp.ByteOffset != 4 {
		t.Errorf("stamp.ByteOffset = %d, want 4", stamp.ByteOffset)
	}
	if got := schema.Field("n").ByteOffset; got != 12 {
		t.Errorf("n.ByteOffset = %d, want 12", got)
	}
}

// TestConvertValue_DateTime pins the import-side literal → uint64
// conversion, including the seconds (not days) representation.
func TestConvertValue_DateTime(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    uint64
		wantErr bool
	}{
		{name: "utc", raw: "2024-03-04T10:11:12Z", want: 1709547072},
		{name: "naive", raw: "2024-03-04T10:11:12", want: 1709547072},
		{name: "offset_normalised", raw: "2024-03-04T12:11:12+02:00", want: 1709547072},
		{name: "space_separated", raw: "2024-03-04 10:11:12", want: 1709547072},
		{name: "date_only_rejected", raw: "2024-03-04", wantErr: true},
		{name: "ambiguous_slash_rejected", raw: "03/04/2024 10:11:12", wantErr: true},
		{name: "garbage_rejected", raw: "nope", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertValue(tc.raw, encoding.FieldTypeDateTime, nil, "")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("convertValue(%q) = %d, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("convertValue(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("convertValue(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestFormatFieldValue_DateTime pins the export-side canonical string.
func TestFormatFieldValue_DateTime(t *testing.T) {
	if got := formatFieldValue(encoding.FieldTypeDateTime, 1709547072, nil); got != "2024-03-04T10:11:12Z" {
		t.Errorf("formatFieldValue = %q, want %q", got, "2024-03-04T10:11:12Z")
	}
	if got := formatFieldValue(encoding.FieldTypeDateTime, 0, nil); got != "1970-01-01T00:00:00Z" {
		t.Errorf("formatFieldValue(0) = %q, want epoch", got)
	}
}

// TestConvertValue_DateTimeSurvivesFormatRoundTrip is the pure-function
// half of the round-trip guarantee: convert → format → convert must be
// a fixed point, time-of-day included.
func TestConvertValue_DateTimeSurvivesFormatRoundTrip(t *testing.T) {
	for _, lit := range []string{
		"2024-03-04T10:11:12Z",
		"2024-03-04 23:59:59",
		"2024-02-29T00:00:01Z",
		"1970-01-01T00:00:00Z",
	} {
		first, err := convertValue(lit, encoding.FieldTypeDateTime, nil, "")
		if err != nil {
			t.Fatalf("convertValue(%q): %v", lit, err)
		}
		text := formatFieldValue(encoding.FieldTypeDateTime, first, nil)
		second, err := convertValue(text, encoding.FieldTypeDateTime, nil, "")
		if err != nil {
			t.Fatalf("convertValue(%q) (exported form of %q): %v", text, lit, err)
		}
		if second != first {
			t.Errorf("%q: round trip drifted %d -> %q -> %d", lit, first, text, second)
		}
	}
}

// TestImportExport_DateTimeEndToEnd runs a real ImportJob → ExportJob
// pass over an in-memory filesystem: the datetime column must land as
// an 8-byte epoch-seconds field and come back out as the canonical
// literal with its time-of-day intact.
func TestImportExport_DateTimeEndToEnd(t *testing.T) {
	tuples := [][]string{
		{"2024-03-04T10:11:12Z", "10"},
		{"2024-03-05T23:59:59Z", "20"},
		{"2024-03-06T00:00:00Z", "30"},
	}
	rows := repeatRows(tuples, minSampleRows)

	fs := afero.NewMemMapFs()
	importJob := NewImportJob(newMockReader([]string{"stamp", "n"}, rows), "dt.pulse")
	importJob.FS = fs
	report, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v, want none", report.RowErrors)
	}
	stamp := report.Schema.Field("stamp")
	if stamp == nil || stamp.Type != encoding.FieldTypeDateTime {
		t.Fatalf("stamp.Type = %v, want datetime", stamp)
	}
	if got := stamp.Type.ByteSize(); got != 8 {
		t.Fatalf("datetime ByteSize = %d, want 8", got)
	}

	writer := &collectWriter{}
	exportJob := NewExportJob("dt.pulse", writer)
	exportJob.FS = fs
	if _, err := exportJob.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(writer.rows) != minSampleRows {
		t.Fatalf("exported %d rows, want %d", len(writer.rows), minSampleRows)
	}
	for i, row := range writer.rows {
		want := tuples[i%len(tuples)][0]
		if got := row[0]; got != want {
			t.Fatalf("row %d stamp = %v, want %q", i, got, want)
		}
	}
}
