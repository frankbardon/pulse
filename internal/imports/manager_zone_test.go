package imports

import (
	"context"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// zoneFixtureCSV: a naive datetime column (row 3 is the New York fall-back
// overlap), a Z-stamped one and a date column.
const zoneFixtureCSV = "day,local,stamped\n" +
	"2026-07-01,2026-07-01 12:00,2026-07-01T12:00:00Z\n" +
	"2026-01-15,2026-01-15 08:00,2026-01-15T08:00:00Z\n" +
	"2026-11-01,2026-11-01 01:30,2026-11-01T01:30:00Z\n"

// columnText exports a cohort and returns column col's cell text.
func columnText(t *testing.T, afs afero.Fs, path string, col int) []any {
	t.Helper()
	w := &zoneSink{}
	ej := pio.NewExportJob(path, w)
	ej.FS = afs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := make([]any, len(w.rows))
	for i, r := range w.rows {
		out[i] = r[col]
	}
	return out
}

type zoneSink struct{ rows [][]any }

func (z *zoneSink) WriteHeader([]string) error { return nil }
func (z *zoneSink) WriteRow(v []any) error {
	z.rows = append(z.rows, append([]any(nil), v...))
	return nil
}
func (z *zoneSink) Close() error { return nil }

// TestManager_Open_SourceTZStoredAndPersisted: a managed import reads naive
// literals in the Spec's source zone (stored as the New York instant, not
// the naive-UTC reading), reports the resolved count, and persists zone,
// per-column map and policy onto the sidecar.
func TestManager_Open_SourceTZStoredAndPersisted(t *testing.T) {
	m, afs, _ := newTestManager(t)
	if err := afero.WriteFile(afs, "src.csv", []byte(zoneFixtureCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := Spec{SourcePath: "src.csv", Handle: "ny",
		SourceTZ: "America/New_York", ColumnSourceTZ: map[string]string{"stamped": "Europe/Berlin"},
		DSTPolicy: pio.DSTPolicyLater}
	res, err := m.Open(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"2026-07-01T16:00:00Z", "2026-01-15T13:00:00Z", "2026-11-01T06:30:00Z"}
	if got := columnText(t, afs, res.Path, 1); !reflect.DeepEqual(got, want) {
		t.Fatalf("local = %v, want %v (read in the source zone)", got, want)
	}
	if got := columnText(t, afs, res.Path, 2)[0]; got != "2026-07-01T12:00:00Z" {
		t.Fatalf("Z literal moved under its per-column zone: %v", got)
	}
	if len(res.ZoneWarnings) != 1 || res.ZoneWarnings[0].Code != perr.PULSE_IMPORT_DST_RESOLVED {
		t.Fatalf("ZoneWarnings = %v", res.ZoneWarnings)
	}

	sc, err := m.readSidecar(res.Path + SidecarSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if sc.SourceTZ != "America/New_York" || sc.DSTPolicy != "later" ||
		!reflect.DeepEqual(sc.ColumnSourceTZ, map[string]string{"stamped": "Europe/Berlin"}) {
		t.Fatalf("sidecar zone = %q %v %q", sc.SourceTZ, sc.ColumnSourceTZ, sc.DSTPolicy)
	}
	raw, err := afero.ReadFile(afs, res.Path+SidecarSuffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"source_tz"`, `"column_source_tz"`, `"dst_policy"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("sidecar JSON lacks %s:\n%s", key, raw)
		}
	}
}

// TestManager_Open_SourceTZNeverServedStale: a handle read in one zone is
// never handed back for another — without Overwrite the second Open is
// refused, with it the cohort is re-read in the new zone and the sidecar
// follows.
func TestManager_Open_SourceTZNeverServedStale(t *testing.T) {
	m, afs, _ := newTestManager(t)
	if err := afero.WriteFile(afs, "src.csv", []byte(zoneFixtureCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := m.Open(ctx, Spec{SourcePath: "src.csv", Handle: "h", SourceTZ: "America/New_York", DSTPolicy: pio.DSTPolicyLater}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Open(ctx, Spec{SourcePath: "src.csv", Handle: "h", SourceTZ: "Asia/Tokyo"})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_HANDLE_EXISTS {
		t.Fatalf("re-open in another zone = %v, want PULSE_IMPORT_HANDLE_EXISTS", err)
	}
	res, err := m.Open(ctx, Spec{SourcePath: "src.csv", Handle: "h", SourceTZ: "Asia/Tokyo", Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := columnText(t, afs, res.Path, 1)[0]; got != "2026-07-01T03:00:00Z" {
		t.Fatalf("overwritten handle local = %v, want the Tokyo reading", got)
	}
	sc, err := m.readSidecar(res.Path + SidecarSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if sc.SourceTZ != "Asia/Tokyo" || sc.DSTPolicy != "" {
		t.Fatalf("sidecar after overwrite = %q %q", sc.SourceTZ, sc.DSTPolicy)
	}
}

// TestManager_Open_NoSourceTZSidecarUnchanged: a UTC import's sidecar
// carries no zone key, and its refusals pass through with their codes.
func TestManager_Open_NoSourceTZSidecarUnchanged(t *testing.T) {
	m, afs, _ := newTestManager(t)
	if err := afero.WriteFile(afs, "src.csv", []byte(zoneFixtureCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := m.Open(context.Background(), Spec{SourcePath: "src.csv", Handle: "utc"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := afero.ReadFile(afs, res.Path+SidecarSuffix)
	if strings.Contains(string(raw), "source_tz") || strings.Contains(string(raw), "dst_policy") {
		t.Fatalf("UTC sidecar grew zone keys:\n%s", raw)
	}
	if got := columnText(t, afs, res.Path, 1)[0]; got != "2026-07-01T12:00:00Z" {
		t.Fatalf("naive-UTC reading changed: %v", got)
	}
	for _, tc := range []struct {
		spec Spec
		code perr.Code
	}{
		{Spec{SourcePath: "src.csv", Handle: "a", SourceTZ: "Mars/Olympus"}, perr.PULSE_TIMEZONE_UNKNOWN},
		{Spec{SourcePath: "src.csv", Handle: "b", ColumnSourceTZ: map[string]string{"day": "UTC"}}, perr.SERVICE_VALIDATION},
		{Spec{SourcePath: "src.csv", Handle: "c", SourceTZ: "America/New_York"}, perr.PULSE_IMPORT_DST_AMBIGUOUS},
	} {
		_, err := m.Open(context.Background(), tc.spec)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != tc.code {
			t.Errorf("%s: err = %v, want %s", tc.spec.Handle, err, tc.code)
		}
	}
}
