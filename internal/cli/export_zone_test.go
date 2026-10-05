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

func runExportCLI(args ...string) error {
	root := ExportCommand()
	root.Writer = &bytes.Buffer{}
	return root.Run(context.Background(), append([]string{"export"}, args...))
}

// TestExport_TZFlag: --tz renders datetimes as local offset literals on
// a text export, leaves the default UTC output alone, is refused by the
// .sav writer and by export predict --format spss, and an unknown zone
// is PULSE_TIMEZONE_UNKNOWN on both leaves.
func TestExport_TZFlag(t *testing.T) {
	cohort, _, err := zoneImport(t)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(cohort)
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	plain, local := filepath.Join(dir, "plain.csv"), filepath.Join(dir, "local.csv")
	if err := runExportCLI("csv", "-i", cohort, "-o", plain); err != nil {
		t.Fatal(err)
	}
	if err := runExportCLI("csv", "-i", cohort, "-o", local, "--tz", "Asia/Kolkata"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(plain), "2026-07-01T12:00:00Z") {
		t.Fatalf("default export lost the UTC literal:\n%s", read(plain))
	}
	if !strings.Contains(read(local), "2026-07-01T17:30:00+05:30") || strings.Contains(read(local), "12:00:00Z") {
		t.Fatalf("--tz Asia/Kolkata did not render local offsets:\n%s", read(local))
	}
	if !strings.Contains(read(local), "2026-07-01,") {
		t.Fatalf("--tz moved a date column:\n%s", read(local))
	}

	err = runExportCLI("spss", "-i", cohort, "-o", filepath.Join(dir, "x.sav"), "--tz", "Europe/Berlin")
	wantCode(t, err, perrors.PULSE_SPSS_EXPORT_UNSUPPORTED)
	err = runExportCLI("predict", "-i", cohort, "--format", "spss", "--tz", "Europe/Berlin")
	wantCode(t, err, perrors.PULSE_SPSS_EXPORT_UNSUPPORTED)
	if err := runExportCLI("spss", "-i", cohort, "-o", filepath.Join(dir, "utc.sav"), "--tz", "UTC"); err != nil {
		t.Fatalf("--tz UTC on spss: %v", err)
	}

	err = runExportCLI("csv", "-i", cohort, "-o", filepath.Join(dir, "bad.csv"), "--tz", "+05:30")
	wantCode(t, err, perrors.PULSE_TIMEZONE_UNKNOWN)
	err = runExportCLI("predict", "-i", cohort, "--tz", "Mars/Olympus_Mons")
	wantCode(t, err, perrors.PULSE_TIMEZONE_UNKNOWN)
}
