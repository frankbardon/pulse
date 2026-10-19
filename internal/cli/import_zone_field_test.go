package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

// --source-tz-field on every leaf that reads a source zone: import <fmt>,
// import predict, import auto, convert and convert predict. Each row's
// naive literal lands as the instant its own zone cell names.

const zoneFieldCSV = "local,zone\n" +
	"2026-07-01 12:00,America/New_York\n" +
	"2026-07-01 12:00,Asia/Tokyo\n" +
	"2026-07-01 12:00,\n"

func writeZoneFieldCSV(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "zf.csv")
	if err := os.WriteFile(p, []byte(zoneFieldCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// wantZoneFieldInstants: New York, Tokyo, and the empty cell's Berlin
// fallback.
var wantZoneFieldInstants = []any{"2026-07-01T16:00:00Z", "2026-07-01T03:00:00Z", "2026-07-01T10:00:00Z"}

func TestImportCSV_SourceTZField(t *testing.T) {
	dir := t.TempDir()
	in := writeZoneFieldCSV(t, dir)
	out := filepath.Join(dir, "out.pulse")
	root := ImportCommand()
	root.Writer = &bytes.Buffer{}
	if err := root.Run(context.Background(), []string{"import", "csv", "-i", in, "-o", out,
		"--source-tz-field", "zone", "--source-tz", "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	got := exportedColumn(t, out, 0)
	for i, w := range wantZoneFieldInstants {
		if got[i] != w {
			t.Fatalf("row %d = %v, want %v", i+1, got[i], w)
		}
	}
	// A zone column the source does not carry is a flag mistake.
	root = ImportCommand()
	root.Writer = &bytes.Buffer{}
	err := root.Run(context.Background(), []string{"import", "csv", "-i", in, "-o", filepath.Join(dir, "x.pulse"), "--source-tz-field", "tz"})
	wantCode(t, err, perrors.CLI_INPUT)
	if !strings.Contains(err.Error(), "--source-tz-field") {
		t.Fatalf("refusal does not name the flag: %v", err)
	}

	// import predict judges it the same way.
	root = ImportCommand()
	root.Writer = &bytes.Buffer{}
	err = root.Run(context.Background(), []string{"import", "predict", "-i", in, "--source-tz-field", "local"})
	wantCode(t, err, perrors.CLI_INPUT)
	root = ImportCommand()
	root.Writer = &bytes.Buffer{}
	if err := root.Run(context.Background(), []string{"import", "predict", "-i", in, "--source-tz-field", "zone"}); err != nil {
		t.Fatal(err)
	}
}

func TestImportAuto_SourceTZField(t *testing.T) {
	dir := withTempDataDir(t)
	writeZoneFieldCSV(t, dir)
	root := ImportCommand()
	root.Writer = &bytes.Buffer{}
	if err := root.Run(context.Background(), []string{"import", "auto",
		"--source-tz-field", "zone", "--source-tz", "Europe/Berlin", "zf.csv"}); err != nil {
		t.Fatal(err)
	}
	if got := exportedColumn(t, filepath.Join(dir, "imports", "zf.pulse"), 0)[1]; got != wantZoneFieldInstants[1] {
		t.Fatalf("managed Tokyo row = %v", got)
	}
	sc, err := os.ReadFile(filepath.Join(dir, "imports", "zf.pulse.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sc), `"source_tz_field": "zone"`) {
		t.Fatalf("sidecar does not record the zone column:\n%s", sc)
	}
	err = runImportCLI(t, "auto", "--handle", "bad", "--source-tz-field", "tz", "zf.csv")
	wantCode(t, err, perrors.CLI_INPUT)
}

func TestConvert_SourceTZField(t *testing.T) {
	dir := t.TempDir()
	in := writeZoneFieldCSV(t, dir)
	out := filepath.Join(dir, "out.csv")
	root := ConvertCommand()
	root.Writer = &bytes.Buffer{}
	if err := root.Run(context.Background(), []string{"convert", "--source-tz-field", "zone",
		"--source-tz", "Europe/Berlin", in, out}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range wantZoneFieldInstants {
		if !strings.Contains(string(text), w.(string)) {
			t.Errorf("converted CSV lacks %s:\n%s", w, text)
		}
	}
	root = ConvertCommand()
	root.Writer = &bytes.Buffer{}
	err = root.Run(context.Background(), []string{"convert", "predict", "--source-tz-field", "tz", in, out})
	wantCode(t, err, perrors.CLI_INPUT)
}
