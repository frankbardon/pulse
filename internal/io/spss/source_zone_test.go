package spss

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// zSuffixReader replays the pre-zone rendering of a `.sav`: it wraps the
// real Reader (schema, dictionaries and all) and appends the `Z` the old
// formatDateTime emitted to every present cell of the datetime columns.
// Importing through it is "today's" import, the byte reference the naive
// rendering must reproduce when no source zone is set.
type zSuffixReader struct {
	*Reader
	cols map[int]bool
}

func (z *zSuffixReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	return z.Reader.ReadRows(ctx, func(row []string) error {
		for i := range row {
			if z.cols[i] && row[i] != "" {
				row[i] += "Z"
			}
		}
		return fn(row)
	})
}

func sourceZoneSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID"},
			{Name: "SEEN", Print: spsstest.Format{Type: spsstest.FormatDATETIME, Width: 20}},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(spssInstant(2024, time.March, 4, 10, 11, 12))},
			{spsstest.Num(2), spsstest.Num(spssInstant(2024, time.July, 1, 12, 0, 0))},
			{spsstest.Num(3), spsstest.Num(spssInstant(1969, time.December, 31, 23, 59, 59))},
			{spsstest.Num(4), spsstest.SysMis()},
		},
	}
}

// importSav imports src into a fresh in-memory filesystem and returns the
// cohort bytes plus the datetime column exported back as canonical text.
func importSavZoned(t *testing.T, src pio.Reader, sourceTZ string) ([]byte, []string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(src, "c.pulse")
	job.FS = fs
	job.SourceTZ = sourceTZ
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.RowErrors) != 0 {
		t.Fatalf("RowErrors = %v", report.RowErrors)
	}
	if got := report.Schema.Field("SEEN").Type; got != encoding.FieldTypeDateTime {
		t.Fatalf("SEEN.Type = %s, want datetime", got)
	}
	raw, err := afero.ReadFile(fs, "c.pulse")
	if err != nil {
		t.Fatal(err)
	}
	w := &rowCollector{}
	exp := pio.NewExportJob("c.pulse", w)
	exp.FS = fs
	if _, err := exp.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	var seen []string
	for _, row := range w.rows {
		seen = append(seen, toString(row[1]))
	}
	return raw, seen
}

// TestSourceZone_SavDateTimeIsNaive pins the two halves of the SPSS
// source-zone contract: a `.sav` DATETIME renders as a NAIVE literal, so
// with no source zone the stored cohort is byte-identical to the old
// `…Z` rendering (naive reads as UTC), and with a source zone the stored
// instant is the zone's wall clock — the `Z` used to beat the zone.
func TestSourceZone_SavDateTimeIsNaive(t *testing.T) {
	data := build(t, sourceZoneSpec())

	r := NewReaderFromBytes(data)
	rows := readAll(t, NewReaderFromBytes(data))
	if got := rows[0][1]; got != "2024-03-04T10:11:12" {
		t.Fatalf("rendered SEEN = %q, want the naive literal 2024-03-04T10:11:12", got)
	}

	today, todaySeen := importSavZoned(t, &zSuffixReader{Reader: NewReaderFromBytes(data), cols: map[int]bool{1: true}}, "")
	naive, naiveSeen := importSavZoned(t, r, "")
	if !bytes.Equal(today, naive) {
		t.Errorf("no source zone: cohort bytes differ from the `Z` rendering\nZ:     %v\nnaive: %v", todaySeen, naiveSeen)
	}

	_, berlin := importSavZoned(t, NewReaderFromBytes(data), "Europe/Berlin")
	want := []string{
		"2024-03-04T09:11:12Z", // CET, +01:00
		"2024-07-01T10:00:00Z", // CEST, +02:00
		"1969-12-31T22:59:59Z", // pre-1970, CET
		"",
	}
	for i := range want {
		if berlin[i] != want[i] {
			t.Errorf("Europe/Berlin row %d = %q, want %q", i, berlin[i], want[i])
		}
	}
}
