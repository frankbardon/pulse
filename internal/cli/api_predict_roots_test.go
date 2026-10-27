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
	// serial: t.Setenv mutates the process environment

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
		// A refused request is a fatal envelope: exit 1, nothing extra.
		if tc.valid && err != nil {
			t.Fatalf("%s --json: %v", tc.leaf, err)
		}
		if !tc.valid {
			wantEnvelopeFatal(t, err)
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
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	for leaf, body := range map[string]string{"predict-facet": `{"fields":["a"]}`, "predict-chain": `{}`} {
		reqPath := filepath.Join(dir, leaf+"-nocohort.json")
		if err := os.WriteFile(reqPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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

// TestAPIPredictRoots_StrictAndEcho (#255): --strict promotes a root's
// warning into an error (valid false, exit 1 in text mode) and
// --echo-request publishes the root's normalized request on the
// envelope's `request`, absent without the flag.
func TestAPIPredictRoots_StrictAndEcho(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "roots.csv", "n,cat\n1,a\n2,b\n3,a\n")
	c := `{"filename":"` + cohort + `"}`
	// discrete_top_k under 5 is a facet warning.
	warnPath := filepath.Join(dir, "warn.json")
	if err := os.WriteFile(warnPath, []byte(`{"cohort":`+c+`,"fields":["cat"],"discrete_top_k":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	type envelope struct {
		Data struct {
			Valid bool `json:"valid"`
		} `json:"data"`
		Request  json.RawMessage         `json:"request"`
		Errors   []struct{ Code string } `json:"errors"`
		Warnings []struct{ Code string } `json:"warnings"`
	}
	decode := func(out string) envelope {
		var env envelope
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("not an envelope: %v\n%s", err, out)
		}
		return env
	}
	out, err := runAPI(t, "predict-facet", "-r", warnPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if env := decode(out); !env.Data.Valid || len(env.Warnings) != 1 || len(env.Errors) != 0 || env.Request != nil {
		t.Fatalf("lax: %s", out)
	}
	out, err = runAPI(t, "predict-facet", "-r", warnPath, "--json", "--strict")
	wantEnvelopeFatal(t, err) // the warning --strict escalated is fatal: exit 1
	if env := decode(out); env.Data.Valid || len(env.Errors) != 1 || len(env.Warnings) != 0 {
		t.Fatalf("--strict: want the warning as the one error, got %s", out)
	}
	if _, err := runAPI(t, "predict-facet", "-r", warnPath, "--strict"); err == nil {
		t.Error("--strict text mode: want a non-zero exit")
	}

	agg := `"aggregations":[{"field":"n"}]`
	for leaf, body := range map[string]string{
		"predict-compose": `{"requests":[{"cohort":` + c + `,` + agg + `}]}`,
		"predict-chain":   `{"cohort":` + c + `,"stages":[{"request":{"cohort":` + c + `,` + agg + `}}]}`,
		"predict-facet":   `{"cohort":` + c + `,"fields":["cat"]}`,
	} {
		reqPath := filepath.Join(dir, leaf+"-echo.json")
		if err := os.WriteFile(reqPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runAPI(t, leaf, "-r", reqPath, "--json", "--echo-request")
		if err != nil {
			t.Fatalf("%s: %v", leaf, err)
		}
		env := decode(out)
		if len(env.Request) == 0 {
			t.Errorf("%s --echo-request: no envelope request: %s", leaf, out)
		}
		// Compose and chain echo the defaults-resolved slot / stage.
		if leaf != "predict-facet" && !strings.Contains(string(env.Request), `AGG_SUM`) {
			t.Errorf("%s --echo-request: request not defaults-resolved: %s", leaf, env.Request)
		}
	}
}
