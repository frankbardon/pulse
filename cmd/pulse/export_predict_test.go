package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// `pulse export predict --format <fmt>` — E6-S1.
//
// The leaf declared a --format flag and never read it, so it answered from
// the source cohort alone no matter what the export was going to write.
// These are the CLI-level halves of the parity claim: what predict SAYS and
// what the export then DOES, run as two real invocations of the real command
// tree.

// predictFixture writes a cohort whose column names are the caller's, and
// returns the directory and the cohort path. A cohort built this way carries
// no SPSS metadata sidecar, so its `.sav` variable names are synthesised from
// these field names — which is what puts an illegal name in front of the
// name policy.
func predictFixture(t *testing.T, header string, rows ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "source.csv")
	body := header + "\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(csvPath, []byte(body), 0644); err != nil {
		t.Fatalf("writing the fixture CSV: %v", err)
	}
	cohort := filepath.Join(dir, "c.pulse")
	if out, err := runApp(t, "import", "csv", "--input", csvPath, "--output", cohort); err != nil {
		t.Fatalf("importing the fixture: %v\noutput: %s", err, out)
	}
	return dir, cohort
}

func parseEnvelope(t *testing.T, out string) *descriptor.Envelope {
	t.Helper()
	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	return &env
}

func envCodes(entries []*descriptor.EnvelopeEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e != nil {
			out = append(out, e.Code)
		}
	}
	return out
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// TestCliExportPredict_SPSSRefusesIllegalNames is the acceptance case. The
// refusal must carry the export's own code — not the PREDICT_ERROR
// placeholder — or `pulse errors lookup` cannot be used on it.
func TestCliExportPredict_SPSSRefusesIllegalNames(t *testing.T) {
	_, cohort := predictFixture(t, "household income,age", "10,1", "20,2")

	out, err := runApp(t, "export", "predict", "--input", cohort, "--format", "spss", "--json")
	if err != nil {
		t.Fatalf("export predict: %v\noutput: %s", err, out)
	}
	env := parseEnvelope(t, out)
	if len(env.Errors) == 0 {
		t.Fatalf("predict reported no error for a cohort with an illegal SPSS variable name: %s", out)
	}
	if got := env.Errors[0].Code; got != "PULSE_SPSS_NAME_INVALID" {
		t.Fatalf("errors[0].code = %q, want PULSE_SPSS_NAME_INVALID (a placeholder is unusable with `pulse errors lookup`)", got)
	}

	// And the real export refuses the same way. This is the whole point: a
	// prediction nobody can act on is worse than no prediction.
	sav := filepath.Join(t.TempDir(), "out.sav")
	exportOut, err := runApp(t, "export", "spss", "--input", cohort, "--output", sav, "--json")
	if err != nil {
		t.Fatalf("export spss: %v\noutput: %s", err, exportOut)
	}
	exportEnv := parseEnvelope(t, exportOut)
	if len(exportEnv.Errors) == 0 {
		t.Fatalf("the export succeeded where predict refused: %s", exportOut)
	}
	if got, want := exportEnv.Errors[0].Code, env.Errors[0].Code; got != want {
		t.Errorf("export failed with %q, predict with %q; the two must agree", got, want)
	}
}

// TestCliExportPredict_SPSSWarnsWhereTheExportWarns. An absent metadata
// sidecar is a caveat, not a refusal — the dictionary is synthesised rather
// than reproduced — and the caveat must reach the envelope's `warnings`
// array rather than being stranded in the adapter.
func TestCliExportPredict_SPSSWarnsWhereTheExportWarns(t *testing.T) {
	dir, cohort := predictFixture(t, "age,score", "10,1", "20,2")

	out, err := runApp(t, "export", "predict", "--input", cohort, "--format", "spss", "--json")
	if err != nil {
		t.Fatalf("export predict: %v\noutput: %s", err, out)
	}
	env := parseEnvelope(t, out)
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused an export with no sidecar; absent is a warning: %v", envCodes(env.Errors))
	}
	if got := envCodes(env.Warnings); !contains(got, "PULSE_SPSS_SIDECAR_ABSENT") {
		t.Errorf("warnings = %v, want PULSE_SPSS_SIDECAR_ABSENT among them", got)
	}

	// A passing predict is followed by a succeeding export.
	sav := filepath.Join(dir, "out.sav")
	exportOut, err := runApp(t, "export", "spss", "--input", cohort, "--output", sav, "--json")
	if err != nil {
		t.Fatalf("export spss: %v\noutput: %s", err, exportOut)
	}
	if exportEnv := parseEnvelope(t, exportOut); len(exportEnv.Errors) != 0 {
		t.Fatalf("the export failed after a passing predict: %v", envCodes(exportEnv.Errors))
	}
	if st, err := os.Stat(sav); err != nil || st.Size() == 0 {
		t.Fatalf("the export wrote no .sav: %v", err)
	}
}

// TestCliExportPredict_CreatesNoOutputFile. The leaf declares no --output,
// and the throwaway writer it builds to ask the question must not leave one
// anywhere either.
func TestCliExportPredict_CreatesNoOutputFile(t *testing.T) {
	dir, cohort := predictFixture(t, "age,score", "10,1", "20,2")
	before := listDir(t, dir)

	if out, err := runApp(t, "export", "predict", "--input", cohort, "--format", "spss"); err != nil {
		t.Fatalf("export predict: %v\noutput: %s", err, out)
	}
	if after := listDir(t, dir); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Errorf("predict changed the directory: %v → %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, "predict.out")); !os.IsNotExist(err) {
		t.Error("predict left its throwaway target path on disk")
	}
}

// TestCliExportPredict_NoFormatIsUnchanged is the compatibility promise. With
// no --format there is no target, so nothing is validated and nothing can
// newly fail — including for a cohort a `.sav` export would refuse outright.
func TestCliExportPredict_NoFormatIsUnchanged(t *testing.T) {
	_, cohort := predictFixture(t, "household income,age", "10,1", "20,2")

	out, err := runApp(t, "export", "predict", "--input", cohort, "--json")
	if err != nil {
		t.Fatalf("export predict: %v\noutput: %s", err, out)
	}
	env := parseEnvelope(t, out)
	if len(env.Errors) != 0 {
		t.Errorf("a target-blind predict reported errors %v; with no target there is nothing to refuse", envCodes(env.Errors))
	}
	if len(env.Warnings) != 0 {
		t.Errorf("a target-blind predict reported warnings %v", envCodes(env.Warnings))
	}

	// The text path says so rather than implying a check that did not run.
	text, err := runApp(t, "export", "predict", "--input", cohort)
	if err != nil {
		t.Fatalf("export predict: %v\noutput: %s", err, text)
	}
	if !strings.Contains(text, "not checked") {
		t.Errorf("the text output does not say the target was unchecked:\n%s", text)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
