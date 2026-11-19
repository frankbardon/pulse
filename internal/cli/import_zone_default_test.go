package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// --zone-field on every leaf that writes a cohort from a source (import
// <format>, import predict, import auto, convert): the cohort's declared
// default per-row zone field (the ZONE schema section).

const zoneDefaultCSV = "local,zone\n" +
	"2026-07-01 12:00,America/New_York\n" +
	"2026-07-01 12:00,Asia/Tokyo\n" +
	"2026-07-01 12:00,Europe/Berlin\n"

func writeZoneDefaultCSV(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "zd.csv")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// cohortZoneField reads a written cohort's declared default zone field.
func cohortZoneField(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
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
		t.Fatal(err)
	}
	return s.ZoneField
}

// TestImportCSV_ZoneField: the flag writes the section; without it the
// cohort declares nothing; import predict judges the declaration.
func TestImportCSV_ZoneField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := writeZoneDefaultCSV(t, dir, zoneDefaultCSV)
	out := filepath.Join(dir, "out.pulse")
	if err := runImportCLI(t, "csv", "-i", in, "-o", out, "--zone-field", "zone"); err != nil {
		t.Fatal(err)
	}
	if got := cohortZoneField(t, out); got != "zone" {
		t.Fatalf("cohort zone field = %q, want zone", got)
	}
	plain := filepath.Join(dir, "plain.pulse")
	if err := runImportCLI(t, "csv", "-i", in, "-o", plain); err != nil {
		t.Fatal(err)
	}
	if got := cohortZoneField(t, plain); got != "" {
		t.Fatalf("undeclared cohort zone field = %q", got)
	}
	if err := runImportCLI(t, "predict", "-i", in, "--zone-field", "zone"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, runImportCLI(t, "predict", "-i", in, "--zone-field", "local"), perrors.SERVICE_VALIDATION)
}

// TestZoneFieldCLI_RefusalsMatchLibrary: every --zone-field refusal reaches
// the CLI exactly as the library ImportJob raises it — code, message and
// details (SERVICE_VALIDATION is not re-coded CLI_INPUT: it is the
// cohort default zone's own rule, the same on every surface) — from
// import <format>, import predict and import auto alike.
func TestZoneFieldCLI_RefusalsMatchLibrary(t *testing.T) {
	// serial: t.Setenv mutates the process environment (import auto)
	dir := withTempDataDir(t)
	in := writeZoneDefaultCSV(t, dir, zoneDefaultCSV+"2026-07-01 12:00,Mars/Olympus\n")
	for _, field := range []string{"tz", "local", "zone"} {
		r, err := pio.NewReader(pio.FormatCSV, afero.NewOsFs(), in, pio.ReaderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		job := pio.NewImportJob(r, "lib.pulse")
		job.FS, job.ZoneField = afero.NewMemMapFs(), field
		_, lerr := job.Run(context.Background())
		var lib *perrors.CodedError
		if !stderrors.As(lerr, &lib) {
			t.Fatalf("%s: library err = %v", field, lerr)
		}
		for name, err := range map[string]error{
			"import csv":     runImportCLI(t, "csv", "-i", in, "-o", filepath.Join(dir, field+".pulse"), "--zone-field", field),
			"import predict": runImportCLI(t, "predict", "-i", in, "--zone-field", field),
			"import auto":    runImportCLI(t, "auto", "--handle", "h_"+field, "--zone-field", field, "zd.csv"),
		} {
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != lib.Code || ce.Message != lib.Message || !reflect.DeepEqual(ce.Details, lib.Details) {
				t.Errorf("%s --zone-field %s = %v %v\nlibrary = %v %v", name, field, err, detailsOf(ce), lerr, lib.Details)
			}
		}
	}
}

func detailsOf(ce *perrors.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}

// TestImportAuto_ZoneField: the managed cohort declares the field and the
// sidecar records it.
func TestImportAuto_ZoneField(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	dir := withTempDataDir(t)
	writeZoneDefaultCSV(t, dir, zoneDefaultCSV)
	if err := runImportCLI(t, "auto", "--zone-field", "zone", "zd.csv"); err != nil {
		t.Fatal(err)
	}
	if got := cohortZoneField(t, filepath.Join(dir, "imports", "zd.pulse")); got != "zone" {
		t.Fatalf("managed cohort zone field = %q", got)
	}
	sc, err := os.ReadFile(filepath.Join(dir, "imports", "zd.pulse.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sc), `"zone_field": "zone"`) {
		t.Fatalf("sidecar does not record the zone field:\n%s", sc)
	}
}

// TestConvert_ZoneField: --zone-field declares the --keep-pulse cohort's
// default; without --keep-pulse it is refused (no cohort to ride).
func TestConvert_ZoneField(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := writeZoneDefaultCSV(t, dir, zoneDefaultCSV)
	keep := filepath.Join(dir, "kept.pulse")
	root := ConvertCommand()
	root.Writer = &bytes.Buffer{}
	if err := root.Run(context.Background(), []string{"convert", "--keep-pulse", keep, "--zone-field", "zone", in, filepath.Join(dir, "out.csv")}); err != nil {
		t.Fatal(err)
	}
	if got := cohortZoneField(t, keep); got != "zone" {
		t.Fatalf("kept cohort zone field = %q", got)
	}
	root = ConvertCommand()
	root.Writer = &bytes.Buffer{}
	err := root.Run(context.Background(), []string{"convert", "--zone-field", "zone", in, filepath.Join(dir, "out2.csv")})
	wantCode(t, err, perrors.SERVICE_VALIDATION)
	var ce *perrors.CodedError
	if stderrors.As(err, &ce); ce.Details["reason"] != "no_cohort" {
		t.Fatalf("no-cohort refusal details = %v", ce.Details)
	}
}
