package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

// The source-zone flags on every leaf past `import <fmt>`: import auto
// (managed pool), import predict, convert and convert predict. Each test
// proves a naive literal lands as the source-zone instant, not the naive
// UTC reading, and that a flag mistake is CLI_INPUT.

func writeZoneCSV(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "z.csv")
	if err := os.WriteFile(p, []byte(zoneCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// envelopeWarningCodes decodes a --json envelope's warning codes.
func envelopeWarningCodes(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var env struct {
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, buf.String())
	}
	var out []string
	for _, w := range env.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func hasZoneCode(codes []string, c perrors.Code) bool {
	for _, x := range codes {
		if x == string(c) {
			return true
		}
	}
	return false
}

// TestImportAuto_SourceTZ: the managed import stores the New York instant
// and reports the resolved count; a date column named per-column is
// CLI_INPUT.
func TestImportAuto_SourceTZ(t *testing.T) {
	dir := withTempDataDir(t)
	writeZoneCSV(t, dir)
	var buf bytes.Buffer
	root := ImportCommand()
	root.Writer = &buf
	err := root.Run(context.Background(), []string{"import", "auto", "--json",
		"--source-tz", "America/New_York", "--dst-policy", "later", "z.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasZoneCode(envelopeWarningCodes(t, &buf), perrors.PULSE_IMPORT_DST_RESOLVED) {
		t.Fatalf("no PULSE_IMPORT_DST_RESOLVED warning: %s", buf.String())
	}
	if got := exportedColumn(t, filepath.Join(dir, "imports", "z.pulse"), 1)[0]; got != "2026-07-01T16:00:00Z" {
		t.Fatalf("managed local = %v, want the New York instant", got)
	}
	sc, err := os.ReadFile(filepath.Join(dir, "imports", "z.pulse.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sc), `"source_tz": "America/New_York"`) || !strings.Contains(string(sc), `"dst_policy": "later"`) {
		t.Fatalf("sidecar does not record the zone:\n%s", sc)
	}

	err = runImportCLI(t, "auto", "--handle", "bad", "--source-tz", "day=UTC", "z.csv")
	wantCode(t, err, perrors.CLI_INPUT)
	err = runImportCLI(t, "auto", "--handle", "bad2", "--dst-policy", "first", "z.csv")
	wantCode(t, err, perrors.CLI_INPUT)
}

// TestImportPredict_SourceTZ: predict raises the refusal the import would
// and, under a policy, reports the resolved count in the envelope.
func TestImportPredict_SourceTZ(t *testing.T) {
	in := writeZoneCSV(t, t.TempDir())
	root := ImportCommand()
	root.Writer = &bytes.Buffer{}
	err := root.Run(context.Background(), []string{"import", "predict", "-i", in, "--source-tz", "America/New_York"})
	wantCode(t, err, perrors.PULSE_IMPORT_DST_AMBIGUOUS)

	var buf bytes.Buffer
	root = ImportCommand()
	root.Writer = &buf
	if err := root.Run(context.Background(), []string{"import", "predict", "-i", in, "--json",
		"--source-tz", "America/New_York", "--dst-policy", "earlier"}); err != nil {
		t.Fatal(err)
	}
	if !hasZoneCode(envelopeWarningCodes(t, &buf), perrors.PULSE_IMPORT_DST_RESOLVED) {
		t.Fatalf("no PULSE_IMPORT_DST_RESOLVED warning: %s", buf.String())
	}

	root = ImportCommand()
	root.Writer = &bytes.Buffer{}
	err = root.Run(context.Background(), []string{"import", "predict", "-i", in, "--source-tz", "nope=UTC"})
	wantCode(t, err, perrors.CLI_INPUT)
}

// TestConvert_SourceTZ: the target receives the UTC instant for each
// naive literal (Z literals verbatim), the --keep-pulse cohort stores the
// same instant, and convert predict refuses the overlap without a policy.
func TestConvert_SourceTZ(t *testing.T) {
	dir := t.TempDir()
	in := writeZoneCSV(t, dir)
	out := filepath.Join(dir, "out.csv")
	kept := filepath.Join(dir, "kept.pulse")
	root := ConvertCommand()
	root.Writer = &bytes.Buffer{}
	if err := root.Run(context.Background(), []string{"convert", "--source-tz", "America/New_York",
		"--dst-policy", "later", "--keep-pulse", kept, in, out}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-07-01T16:00:00Z", "2026-11-01T06:30:00Z", "2026-11-01T01:30:00Z"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("converted CSV lacks %s:\n%s", want, text)
		}
	}
	if strings.Contains(string(text), "2026-07-01 12:00") {
		t.Errorf("a naive literal passed through unconverted:\n%s", text)
	}
	if got := exportedColumn(t, kept, 1)[0]; got != "2026-07-01T16:00:00Z" {
		t.Fatalf("kept cohort local = %v, want the New York instant", got)
	}

	root = ConvertCommand()
	root.Writer = &bytes.Buffer{}
	err = root.Run(context.Background(), []string{"convert", "predict", "--source-tz", "America/New_York", in, out})
	wantCode(t, err, perrors.PULSE_IMPORT_DST_AMBIGUOUS)

	root = ConvertCommand()
	root.Writer = &bytes.Buffer{}
	err = root.Run(context.Background(), []string{"convert", "--source-tz", "day=UTC", in, filepath.Join(dir, "x.csv")})
	wantCode(t, err, perrors.CLI_INPUT)
}
