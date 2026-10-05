package io_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	idescriptor "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/temporal"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// zoneExportCohort writes zone.pulse: a `datetime` ts at every quarter
// hour across both 2026 Europe/Berlin transitions, a `date` d (ts's UTC
// day) and an integer n. Returns the fs and the ts instants in row order.
func zoneExportCohort(t *testing.T) (afero.Fs, []int64) {
	t.Helper()
	var b strings.Builder
	b.WriteString("ts,d,n\n")
	var instants []int64
	i := 0
	for _, w := range [][2]string{{"2026-03-28T22:00:00Z", "2026-03-29T04:00:00Z"}, {"2026-10-24T22:00:00Z", "2026-10-25T04:00:00Z"}} {
		from, _ := time.Parse(time.RFC3339, w[0])
		to, _ := time.Parse(time.RFC3339, w[1])
		for at := from; !at.After(to); at = at.Add(15 * time.Minute) {
			fmt.Fprintf(&b, "%s,%s,%d\n", at.Format(time.RFC3339), at.Format("2006-01-02"), i)
			instants = append(instants, at.Unix())
			i++
		}
	}
	fs := afero.NewMemMapFs()
	importCSV(t, fs, []byte(b.String()), "zone.pulse", map[string]encoding.FieldType{"ts": encoding.FieldTypeDateTime, "d": encoding.FieldTypeDate})
	return fs, instants
}

// importCSV imports csv bytes into target and asserts the named types.
func importCSV(t *testing.T, fs afero.Fs, data []byte, target string, want map[string]encoding.FieldType) {
	t.Helper()
	importBytes(t, fs, pio.FormatCSV, data, target, want)
}

func importBytes(t *testing.T, fs afero.Fs, f pio.Format, data []byte, target string, want map[string]encoding.FieldType) {
	t.Helper()
	r, err := pio.NewReaderFromBytes(f, data, pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReaderFromBytes(%s): %v", f, err)
	}
	job := pio.NewImportJob(r, target)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("import %s: %v", f, err)
	}
	for name, ft := range want {
		if fld := rep.Schema.Field(name); fld == nil || fld.Type != ft {
			t.Fatalf("%s: field %s = %v, want %s", f, name, fld, ft)
		}
	}
}

// exportZone exports zone.pulse to f with ExportJob.TimeZone tz.
func exportZone(t *testing.T, fs afero.Fs, f pio.Format, tz string) []byte {
	t.Helper()
	w, err := pio.NewWriterToBuffer(f, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob("zone.pulse", w)
	job.FS = fs
	job.TimeZone = tz
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("export %s tz=%q: %v", f, tz, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.Bytes()
}

// columnOf reads data back through f's reader and returns column name.
func columnOf(t *testing.T, f pio.Format, data []byte, name string) []string {
	t.Helper()
	r, err := pio.NewReaderFromBytes(f, data, pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	header, rows := readRows(t, r)
	col := -1
	for i, h := range header {
		if h == name {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("%s: no column %s in %v", f, name, header)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row[col]
	}
	return out
}

func loc(t *testing.T, name string) *time.Location {
	t.Helper()
	z, err := temporal.LoadZone(name)
	if err != nil {
		t.Fatal(err)
	}
	return z.Location()
}

// TestExport_TimeZoneRendersLocalOffsets: a zoned export renders every
// datetime cell as time.In's RFC 3339 literal — +05:30 under Kolkata,
// +01:00 / +02:00 either side of each Berlin transition, the fall-back
// hour's two readings told apart by offset — and leaves `date` alone.
func TestExport_TimeZoneRendersLocalOffsets(t *testing.T) {
	fs, instants := zoneExportCohort(t)
	baseD := columnOf(t, pio.FormatCSV, exportZone(t, fs, pio.FormatCSV, ""), "d")
	for _, zone := range []string{"Asia/Kolkata", "Europe/Berlin"} {
		out := exportZone(t, fs, pio.FormatCSV, zone)
		ts := columnOf(t, pio.FormatCSV, out, "ts")
		offsets := map[string]bool{}
		seen := map[string]int{}
		for i, cell := range ts {
			want := time.Unix(instants[i], 0).In(loc(t, zone)).Format(time.RFC3339)
			if cell != want {
				t.Fatalf("%s row %d: %s, want %s", zone, i, cell, want)
			}
			offsets[cell[len(cell)-6:]] = true
			seen[cell[:len(cell)-6]]++
		}
		if d := columnOf(t, pio.FormatCSV, out, "d"); strings.Join(d, ",") != strings.Join(baseD, ",") {
			t.Fatalf("%s: date column moved under a zone", zone)
		}
		switch zone {
		case "Asia/Kolkata":
			if len(offsets) != 1 || !offsets["+05:30"] {
				t.Fatalf("Kolkata offsets = %v, want only +05:30", offsets)
			}
		case "Europe/Berlin":
			if !offsets["+01:00"] || !offsets["+02:00"] || len(offsets) != 2 {
				t.Fatalf("Berlin offsets = %v, want +01:00 and +02:00", offsets)
			}
			if seen["2026-10-25T02:30:00"] != 2 {
				t.Fatalf("fall-back 02:30 rendered %d times, want 2 (one per offset)", seen["2026-10-25T02:30:00"])
			}
			if seen["2026-03-29T02:30:00"] != 0 {
				t.Fatal("spring-forward gap 02:30 rendered; no instant reads it")
			}
		}
	}
}

// rowFormats is every format whose export goes through the rendered row
// stream (every format but spss).
func rowFormats() []pio.Format {
	var out []pio.Format
	for _, f := range pio.Formats() {
		if f != pio.FormatSPSS {
			out = append(out, f)
		}
	}
	return out
}

// TestExport_TimeZoneRoundTripsEveryFormat: a Berlin export through each
// row-stream format re-imports to the exact instants (the importer
// honours the offset), and differs from the zone-free export — the
// control that proves the zone reached the format.
func TestExport_TimeZoneRoundTripsEveryFormat(t *testing.T) {
	fs, instants := zoneExportCohort(t)
	for _, f := range rowFormats() {
		t.Run(f.String(), func(t *testing.T) {
			zoned := exportZone(t, fs, f, "Europe/Berlin")
			cells := columnOf(t, f, zoned, "ts")
			if !strings.HasSuffix(cells[0], "+01:00") {
				t.Fatalf("first cell %q carries no Berlin offset", cells[0])
			}
			if strings.Join(cells, ",") == strings.Join(columnOf(t, f, exportZone(t, fs, f, ""), "ts"), ",") {
				t.Fatal("zoned export reads back like the zone-free one")
			}
			rfs := afero.NewMemMapFs()
			importBytes(t, rfs, f, zoned, "re.pulse", map[string]encoding.FieldType{"ts": encoding.FieldTypeDateTime})
			w, _ := pio.NewWriterToBuffer(pio.FormatCSV, pio.WriterOptions{})
			job := pio.NewExportJob("re.pulse", w)
			job.FS = rfs
			if _, err := job.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			back := columnOf(t, pio.FormatCSV, w.Bytes(), "ts")
			for i, cell := range back {
				if want := time.Unix(instants[i], 0).UTC().Format(time.RFC3339); cell != want {
					t.Fatalf("row %d re-imported as %s, want %s", i, cell, want)
				}
			}
		})
	}
}

// TestExport_UTCTimeZoneIsByteIdentical: "UTC" and the UTC-equivalent
// "Etc/UTC" leave every format's export byte-identical to a zone-free
// one (Excel stamps a creation time, so it compares the cells instead).
func TestExport_UTCTimeZoneIsByteIdentical(t *testing.T) {
	fs, _ := zoneExportCohort(t)
	for _, f := range pio.Formats() {
		base := exportZone(t, fs, f, "")
		for _, tz := range []string{"UTC", "Etc/UTC"} {
			got := exportZone(t, fs, f, tz)
			if f == pio.FormatExcel {
				for _, col := range []string{"ts", "d", "n"} {
					if strings.Join(columnOf(t, f, got, col), ",") != strings.Join(columnOf(t, f, base, col), ",") {
						t.Errorf("%s tz=%s: column %s differs from the zone-free export", f, tz, col)
					}
				}
				continue
			}
			if !bytes.Equal(got, base) {
				t.Errorf("%s tz=%s: %d bytes differ from the zone-free export (%d)", f, tz, len(got), len(base))
			}
		}
	}
}

// TestExport_SPSSRefusesNonUTCZone: the .sav writer encodes raw storage
// into naive wall-clock DATETIMEs with no offset slot, so a non-UTC zone
// is refused — by Run and by Predict, with the same code.
func TestExport_SPSSRefusesNonUTCZone(t *testing.T) {
	fs, _ := zoneExportCohort(t)
	w, _ := pio.NewWriterToBuffer(pio.FormatSPSS, pio.WriterOptions{})
	job := pio.NewExportJob("zone.pulse", w)
	job.FS = fs
	job.TimeZone = "Asia/Kolkata"
	_, runErr := job.Run(context.Background())
	_, predErr := job.Predict(context.Background())
	for verb, err := range map[string]error{"Run": runErr, "Predict": predErr} {
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_SPSS_EXPORT_UNSUPPORTED {
			t.Fatalf("%s: err = %v, want PULSE_SPSS_EXPORT_UNSUPPORTED", verb, err)
		}
		if ce.Details[perrors.DetailTimeZone] != "Asia/Kolkata" || ce.Details["option"] != "--tz" {
			t.Fatalf("%s: details = %v", verb, ce.Details)
		}
	}
}

// TestExport_SPSSRowPathReparsesLocalLiterals: the .sav writer's ROW
// path (convert into .sav) re-parses each rendered cell, so offset-
// bearing literals from a zoned export land as the exact instants.
func TestExport_SPSSRowPathReparsesLocalLiterals(t *testing.T) {
	fs, instants := zoneExportCohort(t)
	csvOut := exportZone(t, fs, pio.FormatCSV, "Europe/Berlin")
	r, _ := pio.NewReaderFromBytes(pio.FormatCSV, csvOut, pio.ReaderOptions{})
	header, rows := readRows(t, r)
	w, _ := pio.NewWriterToBuffer(pio.FormatSPSS, pio.WriterOptions{})
	writeRows(t, w, header, rows)
	back := columnOf(t, pio.FormatSPSS, w.Bytes(), "ts")
	if len(back) != len(instants) {
		t.Fatalf("read %d rows back, want %d", len(back), len(instants))
	}
	for i, cell := range back {
		got, err := encoding.ParseDateTime(cell)
		if err != nil || int64(got) != instants[i] {
			t.Fatalf("row %d: .sav cell %q (from %q) = %d, want %d (%v)", i, cell, rows[i][0], int64(got), instants[i], err)
		}
	}
}

// TestExport_UnknownTimeZoneRefused: only IANA names (or "UTC") render;
// an offset string, an abbreviation or "Local" is PULSE_TIMEZONE_UNKNOWN
// from Run and Predict alike, before any row is written.
func TestExport_UnknownTimeZoneRefused(t *testing.T) {
	fs, _ := zoneExportCohort(t)
	for _, tz := range []string{"Mars/Olympus_Mons", "+05:30", "EST", "Local", "europe/berlin"} {
		w, _ := pio.NewWriterToBuffer(pio.FormatCSV, pio.WriterOptions{})
		job := pio.NewExportJob("zone.pulse", w)
		job.FS = fs
		job.TimeZone = tz
		_, runErr := job.Run(context.Background())
		_, predErr := job.Predict(context.Background())
		for verb, err := range map[string]error{"Run": runErr, "Predict": predErr} {
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_TIMEZONE_UNKNOWN {
				t.Fatalf("tz=%q %s: err = %v, want PULSE_TIMEZONE_UNKNOWN", tz, verb, err)
			}
		}
		_ = w.Close()
		if len(w.Bytes()) != 0 {
			t.Fatalf("tz=%q: refused export still wrote %d bytes", tz, len(w.Bytes()))
		}
	}
}

// TestManifestExportCapability_TimeZone pins the manifest's per-format
// time_zone label against what each real writer does with a Berlin
// export: "local_offset" formats read back offset literals, "refused"
// formats fail the export.
func TestManifestExportCapability_TimeZone(t *testing.T) {
	fs, _ := zoneExportCohort(t)
	m := idescriptor.BuildManifest()
	if len(m.Export.Formats) != len(pio.Formats()) {
		t.Fatalf("manifest lists %d export formats, registry %d", len(m.Export.Formats), len(pio.Formats()))
	}
	for _, fc := range m.Export.Formats {
		f := pio.Format(fc.Name)
		w, _ := pio.NewWriterToBuffer(f, pio.WriterOptions{})
		job := pio.NewExportJob("zone.pulse", w)
		job.FS = fs
		job.TimeZone = "Europe/Berlin"
		_, runErr := job.Run(context.Background())
		switch fc.TimeZone {
		case "local_offset":
			if runErr != nil {
				t.Fatalf("%s: %v", fc.Name, runErr)
			}
			_ = w.Close()
			if cell := columnOf(t, f, w.Bytes(), "ts")[0]; !strings.HasSuffix(cell, "+01:00") {
				t.Fatalf("%s: labelled local_offset but rendered %q", fc.Name, cell)
			}
		case "refused":
			if runErr == nil {
				t.Fatalf("%s: labelled refused but exported", fc.Name)
			}
		default:
			t.Fatalf("%s: unknown time_zone label %q", fc.Name, fc.TimeZone)
		}
	}
}
