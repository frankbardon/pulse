package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// zoneFilterCSV is four rows whose UTC day and America/New_York day
// differ: to_day(ts) == d keeps ids 1 and 3 in UTC and ids 2, 3 and 4 in
// New York (row 1 is still 28 Feb there; rows 2 and 4 are not yet
// 1 Mar / 2 Mar in UTC terms of d).
const zoneFilterCSV = "id,ts,d\n" +
	"1,2026-03-01T03:00:00Z,2026-03-01\n" +
	"2,2026-03-01T03:00:00Z,2026-02-28\n" +
	"3,2026-03-01T15:00:00Z,2026-03-01\n" +
	"4,2026-03-02T02:00:00Z,2026-03-01\n"

const zoneFilterExpr = "to_day(ts) == d"

// TestCohortFilterCLI_TimeZone: --time-zone is the zone a to_day() in
// --filter reads, on both predicate shapes (expression alone, and
// include-set AND expression). Omitted, the leaf reads UTC exactly as
// before and writes the same bytes as an explicit UTC.
func TestCohortFilterCLI_TimeZone(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "zoned.csv", zoneFilterCSV)
	inc := writeInclude(t, dir, "ids.txt", "1\n2\n4\n")

	run := func(name string, extra ...string) string {
		t.Helper()
		dst := filepath.Join(dir, name)
		args := append([]string{"filter", "-i", src, "-o", dst, "--filter", zoneFilterExpr}, extra...)
		if err := runCohortCLI(t, args...); err != nil {
			t.Fatalf("cohort filter %v: %v", extra, err)
		}
		return dst
	}

	cases := []struct {
		name  string
		extra []string
		want  int64
	}{
		{"expr_none.pulse", nil, 2},
		{"expr_utc.pulse", []string{"--time-zone", "UTC"}, 2},
		{"expr_ny.pulse", []string{"--time-zone", "America/New_York"}, 3},
		{"set_none.pulse", []string{"--include-from", inc, "--include-field", "id"}, 1},
		{"set_ny.pulse", []string{"--include-from", inc, "--include-field", "id", "--time-zone", "America/New_York"}, 2},
	}
	for _, c := range cases {
		if n := inspectRecordCount(t, run(c.name, c.extra...)); n != c.want {
			t.Errorf("%s: rows = %d, want %d", c.name, n, c.want)
		}
	}

	// Omitted flag = today's output, byte for byte (an explicit UTC
	// names the same zone).
	none, err := os.ReadFile(filepath.Join(dir, "expr_none.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	utc, err := os.ReadFile(filepath.Join(dir, "expr_utc.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(none, utc) {
		t.Error("no --time-zone output differs from --time-zone UTC output")
	}
}

// TestCohortFilterCLI_TimeZoneUnknown: an unknown --time-zone is the
// PULSE_TIMEZONE_UNKNOWN envelope, exit 1, and nothing is written — on
// both predicate shapes.
func TestCohortFilterCLI_TimeZoneUnknown(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "zoned.csv", zoneFilterCSV)
	inc := writeInclude(t, dir, "ids.txt", "1\n")
	shapes := map[string][]string{
		"expr": nil,
		"set":  {"--include-from", inc, "--include-field", "id"},
	}
	for name, extra := range shapes {
		t.Run(name, func(t *testing.T) {
			dst := filepath.Join(dir, name+"_bad.pulse")
			args := append([]string{"filter", "--json", "-i", src, "-o", dst,
				"--filter", zoneFilterExpr, "--time-zone", "Mars/Olympus_Mons"}, extra...)
			var buf bytes.Buffer
			err := runCohortCLIOut(t, &buf, args...)
			out := buf.Bytes()
			if status, _ := ExitStatus(err); status != ExitFatal {
				t.Errorf("exit status = %d, want %d (err %v)", status, ExitFatal, err)
			}
			var env descriptor.Envelope
			if jerr := json.Unmarshal(out, &env); jerr != nil {
				t.Fatalf("envelope: %v\n%s", jerr, out)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_TIMEZONE_UNKNOWN" {
				t.Errorf("errors = %+v, want one PULSE_TIMEZONE_UNKNOWN", env.Errors)
			}
			if _, serr := os.Stat(dst); !os.IsNotExist(serr) {
				t.Errorf("output written despite the refusal: %v", serr)
			}
		})
	}
}
