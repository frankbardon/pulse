package csv

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// datetimeCSV builds a CSV body with a datetime column, a plain date
// column and a numeric column, tiled to clear the inference sample
// floor so the schema is decided on a full window.
func datetimeCSV(tuples [][3]string, rows int) []byte {
	var b strings.Builder
	b.WriteString("stamp,day,n\n")
	for i := 0; i < rows; i++ {
		t := tuples[i%len(tuples)]
		b.WriteString(t[0])
		b.WriteByte(',')
		b.WriteString(t[1])
		b.WriteByte(',')
		b.WriteString(t[2])
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// TestCSV_DateTimeRoundTrip is the acceptance case: import a CSV
// carrying ISO-8601 datetimes, export it back to CSV, then re-import
// the exported CSV. The datetime column must survive both hops with
// its time-of-day intact, and the two .pulse payloads must be
// byte-identical — the strongest available statement that the canonical
// text form is lossless for `datetime` over the text adapters.
//
// The sibling date-only column is carried through the same pass on
// purpose: it must stay `date` across both imports, proving the
// datetime probe did not shadow date classification.
func TestCSV_DateTimeRoundTrip(t *testing.T) {
	tuples := [][3]string{
		{"2024-03-04T10:11:12Z", "2024-03-04", "10"},
		{"2024-03-05T23:59:59Z", "2024-03-05", "20"},
		{"2024-03-06T00:00:00Z", "2024-03-06", "30"},
		{"2024-02-29T06:30:45Z", "2024-02-29", "40"},
	}
	const rows = 60
	source := datetimeCSV(tuples, rows)

	fs := afero.NewMemMapFs()

	// Hop 1: CSV -> .pulse
	importJob := pio.NewImportJob(NewReaderFromBytes(source), "first.pulse")
	importJob.FS = fs
	firstReport, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}
	if len(firstReport.RowErrors) != 0 {
		t.Fatalf("import 1 RowErrors = %v, want none", firstReport.RowErrors)
	}
	assertRoundTripSchema(t, firstReport.Schema, "import 1")

	// Hop 2: .pulse -> CSV
	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("first.pulse", writer)
	exportJob.FS = fs
	exportReport, err := exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exportReport.RowsExported != rows {
		t.Fatalf("RowsExported = %d, want %d", exportReport.RowsExported, rows)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	exported := writer.Bytes()

	// The exported text must be the canonical datetime literal, not a
	// raw epoch integer and not a truncated date.
	exportedLines := strings.Split(strings.TrimRight(string(exported), "\n"), "\n")
	if len(exportedLines) != rows+1 {
		t.Fatalf("exported %d lines, want %d", len(exportedLines), rows+1)
	}
	for i := 1; i < len(exportedLines); i++ {
		want := tuples[(i-1)%len(tuples)]
		cells := strings.Split(exportedLines[i], ",")
		if len(cells) != 3 {
			t.Fatalf("line %d = %q, want 3 cells", i, exportedLines[i])
		}
		if cells[0] != want[0] {
			t.Fatalf("line %d stamp = %q, want %q", i, cells[0], want[0])
		}
		if cells[1] != want[1] {
			t.Fatalf("line %d day = %q, want %q", i, cells[1], want[1])
		}
	}

	// Hop 3: exported CSV -> .pulse again
	reimportJob := pio.NewImportJob(NewReaderFromBytes(exported), "second.pulse")
	reimportJob.FS = fs
	secondReport, err := reimportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if len(secondReport.RowErrors) != 0 {
		t.Fatalf("import 2 RowErrors = %v, want none", secondReport.RowErrors)
	}
	assertRoundTripSchema(t, secondReport.Schema, "import 2")

	first, err := afero.ReadFile(fs, "first.pulse")
	if err != nil {
		t.Fatalf("read first.pulse: %v", err)
	}
	second, err := afero.ReadFile(fs, "second.pulse")
	if err != nil {
		t.Fatalf("read second.pulse: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-imported cohort is not byte-identical to the original "+
			"(%d vs %d bytes); the CSV round trip lost information",
			len(first), len(second))
	}
}

func assertRoundTripSchema(t *testing.T, schema *encoding.Schema, stage string) {
	t.Helper()
	stamp := schema.Field("stamp")
	if stamp == nil {
		t.Fatalf("%s: stamp field missing", stage)
	}
	if stamp.Type != encoding.FieldTypeDateTime {
		t.Fatalf("%s: stamp.Type = %s, want datetime", stage, stamp.Type)
	}
	day := schema.Field("day")
	if day == nil {
		t.Fatalf("%s: day field missing", stage)
	}
	if day.Type != encoding.FieldTypeDate {
		t.Fatalf("%s: day.Type = %s, want date (a date-only column must not "+
			"be reclassified as datetime)", stage, day.Type)
	}
}

// TestCSV_DateTimeOffsetNormalisedToUTC pins the naive-UTC policy end
// to end: an offset-bearing literal imports to the same instant and
// exports as its UTC rendering, never as the original local wall clock.
func TestCSV_DateTimeOffsetNormalisedToUTC(t *testing.T) {
	tuples := [][3]string{
		{"2024-03-04T12:11:12+02:00", "2024-03-04", "10"},
		{"2024-03-04T05:11:12-05:00", "2024-03-04", "20"},
	}
	source := datetimeCSV(tuples, 60)

	fs := afero.NewMemMapFs()
	importJob := pio.NewImportJob(NewReaderFromBytes(source), "tz.pulse")
	importJob.FS = fs
	if _, err := importJob.Run(context.Background()); err != nil {
		t.Fatalf("import: %v", err)
	}

	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("tz.pulse", writer)
	exportJob.FS = fs
	if _, err := exportJob.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(writer.Bytes()), "\n"), "\n")
	// Both source literals denote 10:11:12 UTC.
	for i := 1; i < len(lines); i++ {
		cells := strings.Split(lines[i], ",")
		if cells[0] != "2024-03-04T10:11:12Z" {
			t.Fatalf("line %d stamp = %q, want %q", i, cells[0], "2024-03-04T10:11:12Z")
		}
	}
}
