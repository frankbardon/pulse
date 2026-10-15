package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runtimeWarningEnvelope is the slice of a --json envelope these tests
// read: the top-level warnings and errors, and data's own warnings.
type runtimeWarningEnvelope struct {
	Data     json.RawMessage `json:"data"`
	Errors   []envelopeEntry `json:"errors"`
	Warnings []envelopeEntry `json:"warnings"`
}

type envelopeEntry struct {
	Code    string         `json:"code"`
	Details map[string]any `json:"details"`
}

func entriesWithCode(es []envelopeEntry, code string) []envelopeEntry {
	var out []envelopeEntry
	for _, e := range es {
		if e.Code == code {
			out = append(out, e)
		}
	}
	return out
}

// runtimeWarningFixture seeds a cohort with a categorical g and a
// numeric x and writes the request files the tests run.
func runtimeWarningFixture(t *testing.T) func(name string) string {
	t.Helper()
	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "rw.csv", "g,x,y\na,1,2\nb,2,1\na,3,5\nc,4,4\nd,5,3\n")
	c := `{"filename":"` + cohort + `"}`
	count := `"aggregations":[{"type":"AGG_COUNT","field":"x"}]`
	leak := `"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x"}}],` + count
	// A declared vector no slot references: PULSE_VECTOR_UNREFERENCED,
	// a runtime warning a chain stage can carry.
	unref := `"vectors":[{"name":"v","fields":["x","y"]}],` + count
	files := map[string]string{
		"process.json": `{"cohort":` + c + `,` + leak + `}`,
		"compose.json": `{"requests":[{"cohort":` + c + `,` + count + `},{"cohort":` + c + `,` + leak + `}]}`,
		"chain.json":   `{"cohort":` + c + `,"stages":[{"request":{` + unref + `}}]}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return func(name string) string {
		if name == "cohort" {
			return cohort
		}
		return filepath.Join(dir, name)
	}
}

func decodeRuntimeEnvelope(t *testing.T, out string) runtimeWarningEnvelope {
	t.Helper()
	var env runtimeWarningEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	return env
}

// TestAPI_RuntimeWarningsReachEnvelope: a runtime warning a result
// carries (Response.Warnings, FacetResult.Warnings) is promoted into the
// --json envelope's top-level warnings on every api leaf that returns
// one, located by details.request (compose slot) / details.stage
// (chain stage), and stays in data too.
func TestAPI_RuntimeWarningsReachEnvelope(t *testing.T) {
	f := runtimeWarningFixture(t)
	const leak, unref, trunc = "PULSE_FEAT_TARGET_LEAKAGE_RISK", "PULSE_VECTOR_UNREFERENCED", "PULSE_FACET_VALUES_TRUNCATED"
	cases := []struct {
		name, code string
		args       []string
		locate     string
		at         float64
	}{
		{"process", leak, []string{"process", "-r", f("process.json"), "--json"}, "", 0},
		{"compose", leak, []string{"compose", "-r", f("compose.json"), "--json"}, "request", 1},
		{"compose_parallel", leak, []string{"compose", "-r", f("compose.json"), "--json", "--parallel", "2"}, "request", 1},
		{"chain", unref, []string{"process-chain", "-r", f("chain.json"), "--json"}, "stage", 0},
		{"facet", trunc, []string{"facet", "-i", f("cohort"), "-f", "g", "--top-k", "2", "--json"}, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runAPI(t, tc.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			env := decodeRuntimeEnvelope(t, out)
			got := entriesWithCode(env.Warnings, tc.code)
			if len(got) != 1 {
				t.Fatalf("envelope warnings %v: want exactly one %s", env.Warnings, tc.code)
			}
			if tc.locate != "" && got[0].Details[tc.locate] != tc.at {
				t.Errorf("details.%s = %v, want %v", tc.locate, got[0].Details[tc.locate], tc.at)
			}
			if !strings.Contains(string(env.Data), tc.code) {
				t.Errorf("data no longer carries %s", tc.code)
			}
		})
	}
}

// TestAPI_StrictRuntimeWarningExitsFatal: under --strict the same
// runtime warning is the run's coded error and the leaf exits 1.
func TestAPI_StrictRuntimeWarningExitsFatal(t *testing.T) {
	f := runtimeWarningFixture(t)
	out, err := runAPI(t, "process", "-r", f("process.json"), "--json", "--strict")
	if status, _ := ExitStatus(err); status != ExitFatal {
		t.Fatalf("exit status %d, want %d\n%s", status, ExitFatal, out)
	}
	env := decodeRuntimeEnvelope(t, out)
	if len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_FEAT_TARGET_LEAKAGE_RISK" {
		t.Errorf("errors %v, want PULSE_FEAT_TARGET_LEAKAGE_RISK first", env.Errors)
	}
}

// TestAPI_StreamEndsWithWarningsRecord: a streamed run that warned ends
// its NDJSON with one {"warnings":[...]} record (compose: located by
// details.request); a warning-free stream has none.
func TestAPI_StreamEndsWithWarningsRecord(t *testing.T) {
	f := runtimeWarningFixture(t)
	for _, tc := range []struct {
		name string
		args []string
		at   any
	}{
		{"process", []string{"process", "-r", f("process.json"), "--stream"}, nil},
		{"compose", []string{"compose", "-r", f("compose.json"), "--stream"}, float64(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runAPI(t, tc.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var lines []map[string]json.RawMessage
			sc := bufio.NewScanner(strings.NewReader(out))
			for sc.Scan() {
				var m map[string]json.RawMessage
				if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
					t.Fatalf("not NDJSON: %v: %s", err, sc.Text())
				}
				lines = append(lines, m)
			}
			if len(lines) < 2 {
				t.Fatalf("want rows then a warnings record, got %d lines:\n%s", len(lines), out)
			}
			last := lines[len(lines)-1]
			var ws []envelopeEntry
			if err := json.Unmarshal(last["warnings"], &ws); err != nil || len(last) != 1 {
				t.Fatalf("last line %v is not a lone warnings record", last)
			}
			got := entriesWithCode(ws, "PULSE_FEAT_TARGET_LEAKAGE_RISK")
			if len(got) != 1 {
				t.Fatalf("stream warnings %v, want one leakage entry", ws)
			}
			if tc.at != nil && got[0].Details["request"] != tc.at {
				t.Errorf("details.request = %v, want %v", got[0].Details["request"], tc.at)
			}
			for _, l := range lines[:len(lines)-1] {
				if _, ok := l["warnings"]; ok {
					t.Error("a warnings record before the end of the stream")
				}
			}
		})
	}
	// Warning-free: no trailing record.
	clean := filepath.Join(filepath.Dir(f("process.json")), "clean.json")
	if err := os.WriteFile(clean, []byte(`{"cohort":{"filename":"`+f("cohort")+`"},"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runAPI(t, "process", "-r", clean, "--stream")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"warnings"`) {
		t.Errorf("warning-free stream carries a warnings record:\n%s", out)
	}
}
