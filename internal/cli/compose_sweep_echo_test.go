package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAPICompose_EchoShowsExpandedSweep: `--echo-request` echoes the
// effective request the slots ran — a sweep's expanded `requests`
// (explicit first, then one per combination, labelled) and no `sweep`;
// a sweep-free request echoes as written.
func TestAPICompose_EchoShowsExpandedSweep(t *testing.T) {
	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "sw.csv", "n,cat\n1,a\n2,b\n3,a\n")
	slot := `{"cohort":{"filename":"` + cohort + `"},"aggregations":[{"type":"AGG_SUM","field":"n","label":"t"}]}`
	sweepBody := `{"cohort":{"filename":"` + cohort + `"},"aggregations":[{"type":"{{op}}","field":"n","label":"t"}]}`
	cases := map[string]struct {
		body   string
		labels []string
	}{
		"sweep":      {`{"requests":[` + slot + `],"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],"request":` + sweepBody + `}}`, []string{"", "op=AGG_SUM", "op=AGG_MAX"}},
		"sweep-free": {`{"requests":[` + slot + `]}`, []string{""}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runAPI(t, "compose", "--json", "--echo-request", "-r", path)
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			var env struct {
				Data struct {
					Responses []json.RawMessage `json:"responses"`
				} `json:"data"`
				Request map[string]json.RawMessage `json:"request"`
				Errors  []any                      `json:"errors"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Errors) != 0 {
				t.Fatalf("envelope: %v %v\n%s", err, env.Errors, out)
			}
			if _, ok := env.Request["sweep"]; ok {
				t.Fatalf("echo carries the sweep block: %s", env.Request["sweep"])
			}
			var reqs []struct {
				Label string `json:"label"`
			}
			if err := json.Unmarshal(env.Request["requests"], &reqs); err != nil {
				t.Fatal(err)
			}
			if len(reqs) != len(tc.labels) || len(env.Data.Responses) != len(tc.labels) {
				t.Fatalf("echoed %d requests, %d responses; want %d", len(reqs), len(env.Data.Responses), len(tc.labels))
			}
			for i, want := range tc.labels {
				if reqs[i].Label != want {
					t.Fatalf("echoed requests[%d].label = %q, want %q", i, reqs[i].Label, want)
				}
			}
		})
	}
}
