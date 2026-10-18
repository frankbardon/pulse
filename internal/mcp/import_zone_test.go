package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

const zoneImportCSV = "day,local,stamped\n" +
	"2026-07-01,2026-07-01 12:00,2026-07-01T12:00:00Z\n" +
	"2026-01-15,2026-01-15 08:00,2026-01-15T08:00:00Z\n" +
	"2026-11-01,2026-11-01 01:30,2026-11-01T01:30:00Z\n"

type zoneRowSink struct{ rows [][]any }

func (z *zoneRowSink) WriteHeader([]string) error { return nil }
func (z *zoneRowSink) WriteRow(v []any) error {
	z.rows = append(z.rows, append([]any(nil), v...))
	return nil
}
func (z *zoneRowSink) Close() error { return nil }

// TestPulseImport_SourceTZ: source_tz / column_source_tz / dst_policy
// reach the managed import — the naive literal is stored as the New York
// instant, zone_warnings carries the resolved count — and the refusals
// keep their codes.
func TestPulseImport_SourceTZ(t *testing.T) {
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "z.csv", []byte(zoneImportCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	out, err := invokeImport(t, p, `{"source":"z.csv","source_tz":"America/New_York","column_source_tz":{"stamped":"Europe/Berlin"},"dst_policy":"later"}`)
	if err != nil {
		t.Fatal(err)
	}
	zw, _ := out["zone_warnings"].([]any)
	if len(zw) != 1 || !strings.Contains(mustJSON(t, zw[0]), string(perr.PULSE_IMPORT_DST_RESOLVED)) {
		t.Fatalf("zone_warnings = %v", out["zone_warnings"])
	}
	sink := &zoneRowSink{}
	ej := pio.NewExportJob(out["path"].(string), sink)
	ej.FS = afs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sink.rows[0][1] != "2026-07-01T16:00:00Z" || sink.rows[2][1] != "2026-11-01T06:30:00Z" {
		t.Fatalf("stored local = %v / %v, want New York instants", sink.rows[0][1], sink.rows[2][1])
	}
	sc, _ := afero.ReadFile(afs, out["path"].(string)+".meta.json")
	if !bytes.Contains(sc, []byte(`"source_tz": "America/New_York"`)) {
		t.Fatalf("sidecar lacks the zone:\n%s", sc)
	}

	for _, tc := range []struct {
		args string
		code perr.Code
	}{
		{`{"source":"z.csv","handle":"a","source_tz":"America/New_York"}`, perr.PULSE_IMPORT_DST_AMBIGUOUS},
		{`{"source":"z.csv","handle":"b","source_tz":"Mars/Olympus"}`, perr.PULSE_TIMEZONE_UNKNOWN},
		{`{"source":"z.csv","handle":"c","column_source_tz":{"day":"UTC"}}`, perr.SERVICE_VALIDATION},
		{`{"source":"z.csv","handle":"d","dst_policy":"sideways"}`, perr.SERVICE_VALIDATION},
	} {
		_, err := invokeImport(t, p, tc.args)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != tc.code {
			t.Errorf("%s: err = %v, want %s", tc.args, err, tc.code)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPulseImport_SourceTZField: source_tz_field reaches the managed
// import — each row's naive literal stored as its own zone's instant, the
// empty cell in the source_tz fallback — the sidecar records it, and a
// zone column the source lacks is SERVICE_VALIDATION.
func TestPulseImport_SourceTZField(t *testing.T) {
	afs := afero.NewMemMapFs()
	csv := "local,zone\n2026-07-01 12:00,America/New_York\n2026-07-01 12:00,Asia/Tokyo\n2026-07-01 12:00,\n"
	if err := afero.WriteFile(afs, "zf.csv", []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	out, err := invokeImport(t, p, `{"source":"zf.csv","source_tz_field":"zone","source_tz":"Europe/Berlin"}`)
	if err != nil {
		t.Fatal(err)
	}
	sink := &zoneRowSink{}
	ej := pio.NewExportJob(out["path"].(string), sink)
	ej.FS = afs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"2026-07-01T16:00:00Z", "2026-07-01T03:00:00Z", "2026-07-01T10:00:00Z"} {
		if sink.rows[i][0] != want {
			t.Fatalf("row %d local = %v, want %s", i+1, sink.rows[i][0], want)
		}
	}
	sc, _ := afero.ReadFile(afs, out["path"].(string)+".meta.json")
	if !bytes.Contains(sc, []byte(`"source_tz_field": "zone"`)) {
		t.Fatalf("sidecar lacks the zone column:\n%s", sc)
	}
	_, err = invokeImport(t, p, `{"source":"zf.csv","handle":"bad","source_tz_field":"tz"}`)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION {
		t.Fatalf("missing zone column: err = %v", err)
	}
}
