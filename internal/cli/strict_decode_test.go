package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAPI_StrictRequestDecode: every request-file leaf decodes through
// the shared strict decoder, so an unknown key — at the root
// (`filters`) or nested (`sort[0].direction`) — is the coded
// PULSE_REQUEST_UNKNOWN_FIELD envelope error with details.path and a
// suggestion, never a silently dropped slot. predict and process refuse
// the same request alike.
func TestAPI_StrictRequestDecode(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	const cohort = `"cohort":{"filename":"c.pulse"}`
	cases := []struct {
		name    string
		args    []string
		body    string
		path    string // "" = root
		unknown string
		suggest string
	}{
		{"process root", []string{"process"}, `{` + cohort + `,"filters":[{"type":"FILTER_INCLUDE","field":"a","values":["x"]}]}`, "", "filters", "filterers"},
		{"process nested", []string{"process"}, `{` + cohort + `,"sort":[{"field":"a","direction":"desc"}]}`, "sort[0].direction", "direction", ""},
		{"predict root", []string{"predict"}, `{` + cohort + `,"filters":[]}`, "", "filters", "filterers"},
		{"predict nested", []string{"predict"}, `{` + cohort + `,"sort":[{"field":"a","direction":"desc"}]}`, "sort[0].direction", "direction", ""},
		{"compose", []string{"compose"}, `{"requests":[{` + cohort + `,"groups":[{"type":"GROUP_RANGE","field":"a","interval":5,"label":"x"}]}]}`, "groups[0].label", "label", ""},
		{"process-chain", []string{"process-chain"}, `{"stages":[{"name":"s","request":{` + cohort + `,"sort":[{"feild":"a"}]}}]}`, "sort[0].feild", "feild", "field"},
		{"facet", []string{"facet", "--top-k", "3"}, `{` + cohort + `,"fields":["a"],"top_kk":1}`, "", "top_kk", ""},
		{"lookup", []string{"lookup"}, `{` + cohort + `,"keys":[{"field":"id","valu":"1"}]}`, "keys[0].valu", "valu", "value"},
		{"materialize", []string{"materialize", "-o", filepath.Join(dir, "out.pulse")}, `{` + cohort + `,"windos":[]}`, "", "windos", "windows"},
		{"predict-compose", []string{"predict-compose"}, `{"requests":[{` + cohort + `,"sort":[{"field":"a","direction":"desc"}]}]}`, "sort[0].direction", "direction", ""},
		{"predict-chain", []string{"predict-chain"}, `{"stages":[{"name":"s","request":{` + cohort + `,"aggregatoins":[]}}]}`, "", "aggregatoins", "aggregations"},
		{"predict-facet", []string{"predict-facet"}, `{` + cohort + `,"feilds":["a"]}`, "", "feilds", "fields"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqPath := filepath.Join(dir, "req.json")
			if err := os.WriteFile(reqPath, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, tc.args...), "-r", reqPath, "--json")
			out, _ := runAPI(t, args...)
			var env struct {
				Errors []struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"errors"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("not an envelope: %v\n%s", err, out)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_REQUEST_UNKNOWN_FIELD" {
				t.Fatalf("errors = %+v, want PULSE_REQUEST_UNKNOWN_FIELD\n%s", env.Errors, out)
			}
			d := env.Errors[0].Details
			if got, ok := d["path"]; tc.path == "" && ok || tc.path != "" && got != tc.path {
				t.Errorf("details.path = %v, want %q", got, tc.path)
			}
			if keys, _ := d["unknown_keys"].([]any); len(keys) != 1 || keys[0] != tc.unknown {
				t.Errorf("unknown_keys = %v, want [%s]", d["unknown_keys"], tc.unknown)
			}
			if tc.suggest != "" {
				if s, _ := d["suggestions"].(map[string]any); s[tc.unknown] != tc.suggest {
					t.Errorf("suggestions = %v, want %s -> %s", d["suggestions"], tc.unknown, tc.suggest)
				}
			}
		})
	}

	// An examples/ request file keeps its root `_meta` block: tolerated.
	reqPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(reqPath, []byte(`{"_meta":{"name":"x"},`+cohort+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if req, err := loadRequest(reqPath); err != nil || req.Cohort == nil {
		t.Fatalf("root _meta refused: %v", err)
	}
}
