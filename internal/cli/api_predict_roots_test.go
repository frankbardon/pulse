package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runAPI drives a fresh `pulse api` group and returns what it wrote
// and the error it returned.
func runAPI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := APICommand()
	root.Writer = &buf
	err := root.Run(context.Background(), append([]string{"api"}, args...))
	return buf.String(), err
}

// TestAPIPredictRoots: each non-Request predict leaf writes the facade's
// envelope whole under --json (data.valid, the echoed request), prints
// a Valid line in text mode, and a refused request carries its coded
// errors and exits non-zero in text mode.
func TestAPIPredictRoots(t *testing.T) {
	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "roots.csv", "n,cat\n1,a\n2,b\n3,a\n")
	c := `{"filename":"` + cohort + `"}`
	agg := `"aggregations":[{"type":"AGG_SUM","field":"n","label":"total"}]`
	cases := []struct {
		leaf, body string
		valid      bool
	}{
		{"predict-compose", `{"requests":[{"cohort":` + c + `,` + agg + `}]}`, true},
		{"predict-facet", `{"cohort":` + c + `,"fields":["cat"]}`, true},
		{"predict-chain", `{"cohort":` + c + `,"stages":[{"request":{"cohort":` + c + `,` + agg + `}}]}`, true},
		{"predict-facet", `{"cohort":` + c + `,"fields":["nope"]}`, false},
	}
	for i, tc := range cases {
		reqPath := filepath.Join(dir, tc.leaf+string(rune('a'+i))+".json")
		if err := os.WriteFile(reqPath, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runAPI(t, tc.leaf, "-r", reqPath, "--json")
		if err != nil {
			t.Fatalf("%s --json: %v", tc.leaf, err)
		}
		var env struct {
			FormatVersion string `json:"format_version"`
			Data          struct {
				Valid   bool            `json:"valid"`
				Request json.RawMessage `json:"request"`
			} `json:"data"`
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%s: not an envelope: %v\n%s", tc.leaf, err, out)
		}
		if env.FormatVersion == "" || env.Data.Valid != tc.valid || len(env.Data.Request) == 0 {
			t.Errorf("%s: envelope = %s, want valid %v with the echoed request", tc.leaf, out, tc.valid)
		}
		if tc.valid != (len(env.Errors) == 0) {
			t.Errorf("%s: errors %v disagree with valid %v", tc.leaf, env.Errors, tc.valid)
		}

		text, err := runAPI(t, tc.leaf, "-r", reqPath)
		if !strings.Contains(text, "Valid: ") {
			t.Errorf("%s: text output lacks the Valid line:\n%s", tc.leaf, text)
		}
		if (err == nil) != tc.valid {
			t.Errorf("%s: text-mode error = %v, want failure iff invalid", tc.leaf, err)
		}
	}
}

// TestAPIPredictRoots_CodedFacadeError: a facade fault (here a facet
// request with no cohort) is written with its own code, never the
// PREDICT_ERROR placeholder.
func TestAPIPredictRoots_CodedFacadeError(t *testing.T) {
	dir := withTempDataDir(t)
	reqPath := filepath.Join(dir, "nocohort.json")
	if err := os.WriteFile(reqPath, []byte(`{"fields":["a"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{"predict-facet", "predict-chain"} {
		out, _ := runAPI(t, leaf, "-r", reqPath, "--json")
		var env struct {
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%s: not an envelope: %v\n%s", leaf, err, out)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != "SERVICE_VALIDATION" {
			t.Errorf("%s: errors = %v, want SERVICE_VALIDATION first", leaf, env.Errors)
		}
	}
}
