package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
)

const zoneCSV = "day,local,stamped\n" +
	"2026-07-01,2026-07-01 12:00,2026-07-01T12:00:00Z\n" +
	"2026-01-15,2026-01-15 08:00,2026-01-15T08:00:00Z\n" +
	"2026-11-01,2026-11-01 01:30,2026-11-01T01:30:00Z\n"

type rowSink struct{ rows [][]any }

func (r *rowSink) WriteHeader([]string) error { return nil }
func (r *rowSink) WriteRow(v []any) error {
	r.rows = append(r.rows, append([]any(nil), v...))
	return nil
}
func (r *rowSink) Close() error { return nil }

func zoneImport(t *testing.T, extra ...string) (string, *bytes.Buffer, error) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(in, []byte(zoneCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pulse")
	root := ImportCommand()
	var buf bytes.Buffer
	root.Writer = &buf
	args := append([]string{"import", "csv", "-i", in, "-o", out}, extra...)
	return out, &buf, root.Run(context.Background(), args)
}

func exportedColumn(t *testing.T, path string, col int) []any {
	t.Helper()
	sink := &rowSink{}
	ej := pio.NewExportJob(path, sink)
	ej.FS = afero.NewOsFs()
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out []any
	for _, r := range sink.rows {
		out = append(out, r[col])
	}
	return out
}

func wantCode(t *testing.T, err error, code perrors.Code) {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

// TestImportCSV_SourceTZ_DefaultRefusesAmbiguous: the overlap row fails the
// import under the default --dst-policy, naming row and column.
func TestImportCSV_SourceTZ_DefaultRefusesAmbiguous(t *testing.T) {
	out, _, err := zoneImport(t, "--source-tz", "America/New_York")
	wantCode(t, err, perrors.PULSE_IMPORT_DST_AMBIGUOUS)
	if !strings.Contains(err.Error(), "row 3") || !strings.Contains(err.Error(), `"local"`) {
		t.Errorf("message does not name row and column: %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("refused import left a cohort behind")
	}
}

// TestImportCSV_SourceTZ_LaterStoresUTCAndWarns: --dst-policy later imports,
// stores New York wall clocks as UTC instants, keeps Z literals and reports
// the resolved count in the envelope warnings.
func TestImportCSV_SourceTZ_LaterStoresUTCAndWarns(t *testing.T) {
	out, buf, err := zoneImport(t, "--source-tz", "America/New_York", "--dst-policy", "later", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, buf.String())
	}
	found := false
	for _, w := range env.Warnings {
		if w.Code == string(perrors.PULSE_IMPORT_DST_RESOLVED) {
			found = w.Details["ambiguous_n"] == float64(1) && w.Details["nonexistent_n"] == float64(0)
		}
	}
	if !found {
		t.Fatalf("no PULSE_IMPORT_DST_RESOLVED {1,0} warning in %s", buf.String())
	}
	local := exportedColumn(t, out, 1)
	want := []any{"2026-07-01T16:00:00Z", "2026-01-15T13:00:00Z", "2026-11-01T06:30:00Z"}
	for i := range want {
		if local[i] != want[i] {
			t.Errorf("row %d local = %v, want %v", i+1, local[i], want[i])
		}
	}
	if got := exportedColumn(t, out, 2)[2]; got != "2026-11-01T01:30:00Z" {
		t.Errorf("Z literal moved: %v", got)
	}
	if got := exportedColumn(t, out, 0)[0]; got != "2026-07-01" {
		t.Errorf("date column moved: %v", got)
	}
}

// TestImportCSV_SourceTZ_PerColumn: col=Zone wins over the bare zone.
func TestImportCSV_SourceTZ_PerColumn(t *testing.T) {
	out, _, err := zoneImport(t, "--source-tz", "America/New_York", "--source-tz", "local=+05:30")
	if err != nil {
		t.Fatal(err)
	}
	if got := exportedColumn(t, out, 1)[0]; got != "2026-07-01T06:30:00Z" {
		t.Fatalf("local = %v, want the +05:30 reading", got)
	}
}

// TestImportCSV_SourceTZ_FlagRefusals: every flag mistake is CLI_INPUT; an
// unknown zone keeps its own code.
func TestImportCSV_SourceTZ_FlagRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code perrors.Code
	}{
		{"date column", []string{"--source-tz", "day=Europe/Berlin"}, perrors.CLI_INPUT},
		{"unknown column", []string{"--source-tz", "nope=Europe/Berlin"}, perrors.CLI_INPUT},
		{"two bare zones", []string{"--source-tz", "UTC", "--source-tz", "Europe/Berlin"}, perrors.CLI_INPUT},
		{"duplicate column", []string{"--source-tz", "local=UTC", "--source-tz", "local=Europe/Berlin"}, perrors.CLI_INPUT},
		{"empty column", []string{"--source-tz", "=Europe/Berlin"}, perrors.CLI_INPUT},
		{"bad policy", []string{"--source-tz", "UTC", "--dst-policy", "first"}, perrors.CLI_INPUT},
		{"unknown zone", []string{"--source-tz", "Mars/Olympus"}, perrors.PULSE_TIMEZONE_UNKNOWN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := zoneImport(t, tc.args...)
			wantCode(t, err, tc.code)
		})
	}
}

// TestImportCSV_NoSourceTZ_ByteIdentical: without --source-tz (and with a
// policy flag alone, which is inert) the cohort bytes are unchanged.
func TestImportCSV_NoSourceTZ_ByteIdentical(t *testing.T) {
	a, _, err := zoneImport(t)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := zoneImport(t, "--dst-policy", "later")
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ab, bb) {
		t.Fatal("--dst-policy without --source-tz changed the cohort")
	}
}
