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
	// serial: t.Setenv mutates the process environment

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

// TestParams_APIComposeAndChainInherit (U58 E3-S2): `pulse api compose`
// and `pulse api process-chain` with a top-level params block — every
// slot / stage inherits it (a sub-request's own key wins), and
// --echo-request shows the merged params on each sub-request.
func TestParams_APIComposeAndChainInherit(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "waves.csv", "wave,x\n1,10\n2,20\n3,30\n3,31\n5,50\n")
	c, err := json.Marshal(map[string]string{"filename": cohort})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	body := func(own string) string {
		return own + `"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave"}],` +
			`"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}]}`
	}
	type echoed struct {
		Params map[string]json.Number `json:"params"`
	}

	compose := write("compose.json", `{"params":{"from_wave":3},"requests":[`+
		`{"cohort":`+string(c)+`,`+body("")+`,`+
		`{"cohort":`+string(c)+`,`+body(`"params":{"from_wave":5},`)+`]}`)
	out, err := runAPI(t, "compose", "--json", "--echo-request", "-r", compose)
	if err != nil {
		t.Fatalf("compose: %v\n%s", err, out)
	}
	var cenv struct {
		Data struct {
			Responses []struct {
				Data []map[string]any `json:"data"`
			} `json:"responses"`
		} `json:"data"`
		Request struct {
			Requests []echoed `json:"requests"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(out), &cenv); err != nil || len(cenv.Data.Responses) != 2 {
		t.Fatalf("compose envelope: %v\n%s", err, out)
	}
	if cenv.Data.Responses[0].Data[0]["s"] != float64(61) || cenv.Data.Responses[1].Data[0]["s"] != float64(50) {
		t.Fatalf("compose sums: %s", out)
	}
	if len(cenv.Request.Requests) != 2 || cenv.Request.Requests[0].Params["from_wave"] != "3" || cenv.Request.Requests[1].Params["from_wave"] != "5" {
		t.Fatalf("compose echo: %s", out)
	}

	chain := write("chain.json", `{"cohort":`+string(c)+`,"params":{"from_wave":3},"stages":[{"request":{`+body("")+`}]}`)
	out, err = runAPI(t, "process-chain", "--json", "--echo-request", "-r", chain)
	if err != nil {
		t.Fatalf("process-chain: %v\n%s", err, out)
	}
	var chenv struct {
		Data struct {
			Final struct {
				Data []map[string]any `json:"data"`
			} `json:"final"`
		} `json:"data"`
		Request struct {
			Stages []struct {
				Request echoed `json:"request"`
			} `json:"stages"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(out), &chenv); err != nil || len(chenv.Data.Final.Data) != 1 || chenv.Data.Final.Data[0]["s"] != float64(61) {
		t.Fatalf("process-chain: %v\n%s", err, out)
	}
	if len(chenv.Request.Stages) != 1 || chenv.Request.Stages[0].Request.Params["from_wave"] != "3" {
		t.Fatalf("process-chain echo: %s", out)
	}
}

// TestParams_APIFacetJSON (expression-wires E2-S4): `pulse api facet`
// with a FacetRequest params block — its FILTER_EXPRESSION reads
// params.from_wave — and an undeclared read is the coded envelope
// error `pulse api predict-facet` reports.
func TestParams_APIFacetJSON(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "waves.csv", "wave,x\n1,10\n2,20\n3,30\n3,31\n4,40\n")
	c, err := json.Marshal(map[string]string{"filename": cohort})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, params string) string {
		body := `{"cohort":` + string(c) + `,"fields":["x"],` + params +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave"}]}`
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	type envelope struct {
		Data struct {
			FilteredRecords int64 `json:"filtered_records"`
		} `json:"data"`
		Errors []struct {
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

	ok := write("ok.json", `"params":{"from_wave":3},`)
	out, err := runAPI(t, "facet", "-r", ok, "--json")
	if err != nil {
		t.Fatalf("facet: %v\n%s", err, out)
	}
	if env := decode(out); len(env.Errors) != 0 || env.Data.FilteredRecords != 2 {
		t.Fatalf("facet: %s", out)
	}
	pout, _ := runAPI(t, "predict-facet", "-r", ok, "--json")
	if env := decode(pout); len(env.Errors) != 0 {
		t.Fatalf("predict-facet ok: %s", pout)
	}

	bad := write("bad.json", `"params":{"to_wave":3},`)
	out, err = runAPI(t, "facet", "-r", bad, "--json")
	wantEnvelopeFatal(t, err)
	env := decode(out)
	if len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_PARAMS_UNDECLARED" {
		t.Fatalf("undeclared: %s", out)
	}
	pout, _ = runAPI(t, "predict-facet", "-r", bad, "--json")
	found := false
	for _, e := range decode(pout).Errors {
		found = found || (e.Code == env.Errors[0].Code && e.Message == env.Errors[0].Message)
	}
	if !found {
		t.Fatalf("predict-facet lacks the run's %s %q: %s", env.Errors[0].Code, env.Errors[0].Message, pout)
	}
}
