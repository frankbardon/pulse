package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// sweepCLIFixture seeds one cohort and returns a writer for request
// files plus a three-value sweep body over it (one explicit slot, three
// sweep slots: four responses in all).
func sweepCLIFixture(t *testing.T) (dir string, write func(name, body string) string, composed string) {
	t.Helper()
	dir = withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "swcli.csv", "n,cat\n1,a\n2,b\n3,a\n")
	write = func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	slot := `{"cohort":{"filename":"` + cohort + `"},"groups":[{"type":"GROUP_CATEGORY","field":"cat"}],"aggregations":[{"type":"AGG_SUM","field":"n","label":"t"}]}`
	body := `{"cohort":{"filename":"` + cohort + `"},"groups":[{"type":"GROUP_CATEGORY","field":"cat"}],"aggregations":[{"type":"{{op}}","field":"n","label":"t"}]}`
	composed = `{"requests":[` + slot + `],"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX","AGG_MIN"]}],"request":` + body + `}}`
	return dir, write, composed
}

// TestAPICompose_SweepRunsEveryExpandedSlot: `pulse api compose` runs a
// sweep with no new flag — the explicit slot plus one response per
// combination, the same responses sequentially and with --parallel 0,
// and --stream emits rows tagged with every expanded slot's index.
func TestAPICompose_SweepRunsEveryExpandedSlot(t *testing.T) {
	_, write, composed := sweepCLIFixture(t)
	path := write("sweep.json", composed)

	responses := func(args ...string) []json.RawMessage {
		t.Helper()
		out, err := runAPI(t, append([]string{"compose", "--json", "-r", path}, args...)...)
		if err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, out)
		}
		var env struct {
			Data struct {
				Responses []json.RawMessage `json:"responses"`
			} `json:"data"`
			Errors []any `json:"errors"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Errors) != 0 {
			t.Fatalf("envelope %v: %v %v\n%s", args, err, env.Errors, out)
		}
		return env.Data.Responses
	}
	serial := responses()
	if len(serial) != 4 {
		t.Fatalf("sequential compose returned %d responses, want 4 (1 explicit + 3 sweep)", len(serial))
	}
	parallel := responses("--parallel", "0")
	if len(parallel) != len(serial) {
		t.Fatalf("--parallel 0 returned %d responses, want %d", len(parallel), len(serial))
	}
	for i := range serial {
		if dataOf(t, serial[i]) != dataOf(t, parallel[i]) {
			t.Fatalf("slot %d differs under --parallel 0:\n serial   %s\n parallel %s", i, serial[i], parallel[i])
		}
	}

	out, err := runAPI(t, "compose", "--stream", "-r", path)
	if err != nil {
		t.Fatalf("--stream: %v\n%s", err, out)
	}
	seen := map[int]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var row struct {
			Index *int            `json:"index"`
			Row   json.RawMessage `json:"row"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil || row.Index == nil || len(row.Row) == 0 {
			t.Fatalf("not an NDJSON row: %q (%v)", line, err)
		}
		seen[*row.Index]++
	}
	for i := range 4 {
		if seen[i] != 2 { // two categories per slot
			t.Fatalf("--stream emitted %d rows for slot %d, want 2 (rows by slot %v)", seen[i], i, seen)
		}
	}
}

// dataOf is one response's `data` rows as compact JSON text.
func dataOf(t *testing.T, resp json.RawMessage) string {
	t.Helper()
	var r struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	return string(r.Data)
}

// TestAPIPredictCompose_ShowsSweepSummary: `pulse api predict-compose`
// reports the sweep — axes, resolved mode, expanded count and labels.
func TestAPIPredictCompose_ShowsSweepSummary(t *testing.T) {
	_, write, composed := sweepCLIFixture(t)
	out, err := runAPI(t, "predict-compose", "--json", "-r", write("sweep.json", composed))
	if err != nil {
		t.Fatalf("predict-compose: %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Valid bool `json:"valid"`
			Sweep *struct {
				Axes []struct {
					Name  string `json:"name"`
					Count int    `json:"count"`
				} `json:"axes"`
				Mode          string   `json:"mode"`
				ExpandedCount int      `json:"expanded_count"`
				Labels        []string `json:"labels"`
			} `json:"sweep"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	s := env.Data.Sweep
	if !env.Data.Valid || s == nil {
		t.Fatalf("want a valid verdict with a sweep summary: %s", out)
	}
	if len(s.Axes) != 1 || s.Axes[0].Name != "op" || s.Axes[0].Count != 3 || s.Mode != "grid" || s.ExpandedCount != 3 {
		t.Fatalf("summary = %+v", *s)
	}
	if strings.Join(s.Labels, ",") != "op=AGG_SUM,op=AGG_MAX,op=AGG_MIN" {
		t.Fatalf("labels = %v", s.Labels)
	}
}

// TestAPICompose_SweepFaultsKeepTheirCodes: an invalid sweep and an
// over-limit sweep reach the --json envelope with their own code on
// compose (both entry points) and predict-compose — never the
// COMPOSE_ERROR / PREDICT_ERROR placeholder.
func TestAPICompose_SweepFaultsKeepTheirCodes(t *testing.T) {
	_, write, _ := sweepCLIFixture(t)
	// 32 x 32 = 1024 slots, over the default max_compose_slots of 1000.
	vals := make([]string, 32)
	for i := range vals {
		vals[i] = strconv.Itoa(i)
	}
	axis := "[" + strings.Join(vals, ",") + "]"
	cases := map[string]struct{ body, code string }{
		"invalid": {`{"requests":[],"sweep":{"axes":[],"request":{}}}`, "PULSE_SWEEP_INVALID"},
		"over-limit": {`{"requests":[],"sweep":{"axes":[{"name":"a","values":` + axis + `},{"name":"b","values":` + axis +
			`}],"label":"{{a}}_{{b}}","request":{"cohort":{"filename":"x.pulse"}}}}`, "PULSE_LIMIT_EXCEEDED"},
	}
	for name, tc := range cases {
		path := write(name+".json", tc.body)
		for _, args := range [][]string{
			{"compose"},
			{"compose", "--parallel", "0"},
			{"predict-compose"},
		} {
			t.Run(name+"/"+strings.Join(args, "_"), func(t *testing.T) {
				out, _ := runAPI(t, append(args, "--json", "-r", path)...)
				var env struct {
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if err := json.Unmarshal([]byte(out), &env); err != nil {
					t.Fatalf("not an envelope: %v\n%s", err, out)
				}
				if len(env.Errors) == 0 || env.Errors[0].Code != tc.code {
					t.Fatalf("errors = %+v, want %s first\n%s", env.Errors, tc.code, out)
				}
			})
		}
	}
}
