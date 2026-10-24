package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestParams_APIProcessJSON (U58 E3-S1): `pulse api process --json`
// runs a FILTER_EXPRESSION reading params.from_wave, --echo-request
// echoes the params block with its exact integer, and an undeclared
// read is the coded envelope error predict reports.
func TestParams_APIProcessJSON(t *testing.T) {
	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "waves.csv", "wave,x\n1,10\n2,20\n3,30\n3,31\n4,40\n")
	c, err := json.Marshal(map[string]string{"filename": cohort})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, params, expr string) string {
		body := `{"cohort":` + string(c) + `,` + params +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"` + expr + `"}],` +
			`"aggregations":[{"type":"AGG_COUNT","field":"x","label":"c"}]}`
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	type envelope struct {
		Data struct {
			Data []map[string]any `json:"data"`
		} `json:"data"`
		Request json.RawMessage `json:"request"`
		Errors  []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	decode := func(out string) envelope {
		t.Helper()
		var env envelope
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("envelope: %v\n%s", err, out)
		}
		return env
	}

	ok := write("ok.json", `"params":{"from_wave":3,"big":9007199254740993},`, "wave == params.from_wave")
	out, err := runAPI(t, "process", "-r", ok, "--json", "--echo-request")
	if err != nil {
		t.Fatalf("process: %v\n%s", err, out)
	}
	env := decode(out)
	if len(env.Errors) != 0 || len(env.Data.Data) != 1 || env.Data.Data[0]["c"] != float64(2) {
		t.Fatalf("process: %s", out)
	}
	var echo struct {
		Params map[string]json.Number `json:"params"`
	}
	if err := json.Unmarshal(env.Request, &echo); err != nil || echo.Params["big"] != "9007199254740993" || echo.Params["from_wave"] != "3" {
		t.Fatalf("--echo-request lacks the exact params: %s", env.Request)
	}

	bad := write("bad.json", `"params":{"to_wave":3},`, "wave == params.from_wave")
	out, err = runAPI(t, "process", "-r", bad, "--json")
	wantEnvelopeFatal(t, err)
	env = decode(out)
	if len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_PARAMS_UNDECLARED" {
		t.Fatalf("undeclared: %s", out)
	}
	pout, _ := runAPI(t, "predict", "-r", bad, "--json")
	found := false
	for _, e := range decode(pout).Errors {
		found = found || (e.Code == env.Errors[0].Code && e.Message == env.Errors[0].Message)
	}
	if !found {
		t.Fatalf("predict lacks the run's %s %q: %s", env.Errors[0].Code, env.Errors[0].Message, pout)
	}
}
