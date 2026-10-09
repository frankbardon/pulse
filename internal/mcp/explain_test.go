package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// invokeExplain calls pulse_explain through the catalog's Invoke and
// returns the wire JSON an agent reads.
func invokeExplain(t *testing.T, p *pulse.Pulse, args string) (map[string]any, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolExplain {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m, nil
	}
	t.Fatal("pulse_explain not in the catalog")
	return nil, nil
}

// TestPulseExplain: the tool is pulse.Explain over the wire — a request
// described against the cohort it names, a response read beside its
// request with a null (undefined) statistic kept undefined rather than
// read as 0, and every refusal a coded error with its own code.
func TestPulseExplain(t *testing.T) {
	p, _, path := flatManagedCohort(t)
	req := `{"cohort":{"filename":"` + path + `"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"total"}]}`

	out, err := invokeExplain(t, p, `{"request":`+req+`}`)
	if err != nil {
		t.Fatalf("request mode: %v", err)
	}
	if out["mode"] != "request" || out["valid"] != true || len(out["steps"].([]any)) != 1 {
		t.Fatalf("request-mode result = %v", out)
	}

	resp := `{"data":[{"total":5}],"tests":[{"type":"TEST_T","statistic":null,"df":0,"p_value":null,"alpha":0.05,"reject_null":false}],
		"regressions":[{"type":"REG_OLS","target":"amount","n_obs":20,"r2":0.4,"coefficients":{"x":2},"std_errors":{"x":null},"p_values":{"x":null}}]}`
	out, err = invokeExplain(t, p, `{"response":`+resp+`,"request":`+req+`,"detail":"full"}`)
	if err != nil {
		t.Fatalf("response mode: %v", err)
	}
	if out["mode"] != "response" || out["detail"] != "full" {
		t.Fatalf("response-mode result = %v", out)
	}
	seen := map[string]bool{}
	for _, f := range out["findings"].([]any) {
		f := f.(map[string]any)
		nums := f["numbers"].(map[string]any)
		switch f["slot"] {
		case "tests[0]", "regressions[0].coefficients.x":
			seen[f["slot"].(string)] = true
			if f["verdict"] != "not_computable" || nums["p_value"] != nil {
				t.Errorf("%s: a null figure was read as a value: %v", f["slot"], f)
			}
			if f["slot"] == "tests[0]" && nums["statistic"] != nil {
				t.Errorf("null statistic reported as %v", nums["statistic"])
			}
		case "aggregations[0]":
			seen["aggregations[0]"] = true
			if f["operator"] != "AGG_SUM" {
				t.Errorf("aggregation not named from the companion: %v", f)
			}
		}
	}
	if len(seen) != 3 {
		t.Errorf("findings = %v", out["findings"])
	}

	out, err = invokeExplain(t, p, `{"composed_response":{"responses":[{"tests":[{"type":"TEST_T","statistic":1,"df":9,"p_value":0.3,"alpha":0.05}]}]}}`)
	if err != nil || out["root"] != "composed_response" {
		t.Fatalf("composed: %v %v", out, err)
	}

	for args, want := range map[string]perr.Code{
		`{}`: perr.SERVICE_VALIDATION,
		`{"request":` + req + `,"detail":"verbose"}`:                       perr.SERVICE_VALIDATION,
		`{"request":` + req + `,"sample":{"n":1}}`:                         perr.SERVICE_VALIDATION,
		`{"response":{"tests":[{"p_value":"x"}]}}`:                         perr.SERVICE_VALIDATION,
		`{"request":{"cohort":{"filename":"no.pulse"},"aggregations":[]}}`: perr.DATA_FILE,
		`{"chain_response":{"stages":[]},"composed":{"requests":[]}}`:      perr.SERVICE_VALIDATION,
	} {
		if _, err := invokeExplain(t, p, args); !perr.HasCode(err, want) {
			t.Errorf("%s: err = %v, want %s", args, err, want)
		}
	}
}

// TestPulseExplain_InputSchema: the input contract names exactly the
// explain roots plus detail, each root an open object.
func TestPulseExplain_InputSchema(t *testing.T) {
	ts, ok := SchemaFor(toolmeta.ToolExplain)
	if !ok {
		t.Fatal("no schema for pulse_explain")
	}
	var s struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(ts.InputSchema, &s); err != nil {
		t.Fatal(err)
	}
	roots := []string{"request", "composed", "chain", "facet", "sample", "response", "composed_response", "chain_response", "facet_result"}
	for _, k := range roots {
		if s.Properties[k].Type != "object" {
			t.Errorf("%s type = %v, want object", k, s.Properties[k].Type)
		}
	}
	if s.Properties["detail"].Type != "string" || len(s.Properties) != len(roots)+1 {
		t.Errorf("input schema properties = %v", s.Properties)
	}
}
