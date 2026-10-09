package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// explainEnvelope is the slice of a `pulse explain --json` envelope the
// CLI test reads.
type explainEnvelope struct {
	Data *struct {
		Mode     string `json:"mode"`
		Root     string `json:"root"`
		Detail   string `json:"detail"`
		Summary  string `json:"summary"`
		Valid    *bool  `json:"valid"`
		Steps    []any  `json:"steps"`
		Findings []struct {
			Slot     string              `json:"slot"`
			Operator string              `json:"operator"`
			Verdict  string              `json:"verdict"`
			Numbers  map[string]*float64 `json:"numbers"`
		} `json:"findings"`
		Sentences []string `json:"sentences"`
		Caveats   []string `json:"caveats"`
	} `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

func decodeExplain(t *testing.T, text string) explainEnvelope {
	t.Helper()
	var env explainEnvelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decoding envelope: %v\n%s", err, text)
	}
	return env
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCliExplain: `pulse explain` is a thin adapter over pulse.Explain —
// a request file described before it runs (predict-checked over its
// cohort), a response file read into findings beside its request, a
// --json envelope unwrapped, another root through --root, and every
// refusal under its own code in errors[0].
func TestCliExplain(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "c.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeGroupCSV(t, dir), "--output", cohort); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	cohortJSON, _ := json.Marshal(cohort)
	reqPath := writeFile(t, dir, "req.json", `{"cohort":{"filename":`+string(cohortJSON)+`},
		"groups":[{"field":"cust_region"}],"aggregations":[{"type":"AGG_SUM","field":"qty","label":"total"}]}`)

	text, err := runApp(t, "explain", "--request", reqPath, "--json")
	if err != nil {
		t.Fatalf("request mode: %v\n%s", err, text)
	}
	env := decodeExplain(t, text)
	if env.Data == nil || env.Data.Mode != "request" || env.Data.Valid == nil || !*env.Data.Valid || len(env.Data.Steps) != 2 {
		t.Fatalf("request-mode envelope = %s", text)
	}

	// A process result as `pulse api process --json` writes it: an
	// envelope whose test came back undefined (null statistic and
	// p-value) beside a defined one.
	respPath := writeFile(t, dir, "resp.json", `{"format_version":"1.1","data":{"data":[{"cust_region":"north","total":3}],
		"tests":[{"type":"TEST_T","statistic":null,"df":0,"p_value":null,"alpha":0.05,"reject_null":false},
		         {"type":"TEST_T","statistic":2.4,"df":30,"p_value":0.02,"alpha":0.05,"reject_null":true}]},
		"errors":[],"warnings":[]}`)
	text, err = runApp(t, "explain", "--response", respPath, "--request", reqPath, "--detail", "full", "--json")
	if err != nil {
		t.Fatalf("response mode: %v\n%s", err, text)
	}
	env = decodeExplain(t, text)
	if env.Data == nil || env.Data.Mode != "response" || env.Data.Detail != "full" || len(env.Data.Sentences) == 0 {
		t.Fatalf("response-mode envelope = %s", text)
	}
	byslot := map[string]int{}
	for i, f := range env.Data.Findings {
		byslot[f.Slot] = i
	}
	undef := env.Data.Findings[byslot["tests[0]"]]
	if undef.Verdict != "not_computable" || undef.Numbers["statistic"] != nil || undef.Numbers["p_value"] != nil {
		t.Errorf("a null statistic was read as a figure: %+v", undef)
	}
	if def := env.Data.Findings[byslot["tests[1]"]]; def.Verdict != "evidence_of_difference" || def.Numbers["statistic"] == nil || *def.Numbers["statistic"] != 2.4 {
		t.Errorf("defined test = %+v", def)
	}
	if agg := env.Data.Findings[byslot["aggregations[0]"]]; agg.Operator != "AGG_SUM" {
		t.Errorf("aggregation not named from the companion: %+v", agg)
	}

	composed := writeFile(t, dir, "composed.json", `{"responses":[{"tests":[{"type":"TEST_T","statistic":1.1,"df":9,"p_value":0.3,"alpha":0.05}]}]}`)
	text, err = runApp(t, "explain", "--root", "composed", "--response", composed, "--json")
	if err != nil {
		t.Fatalf("composed: %v\n%s", err, text)
	}
	if env = decodeExplain(t, text); env.Data == nil || env.Data.Root != "composed_response" || len(env.Data.Findings) != 1 ||
		env.Data.Findings[0].Slot != "responses[0].tests[0]" {
		t.Fatalf("composed envelope = %s", text)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "CLI_INPUT"},
		{[]string{"--request", reqPath, "--root", "bogus"}, "CLI_INPUT"},
		{[]string{"--response", respPath, "--root", "sample"}, "CLI_INPUT"},
		{[]string{"--request", filepath.Join(dir, "missing.json")}, "CLI_INPUT"},
		{[]string{"--request", reqPath, "--detail", "verbose"}, "SERVICE_VALIDATION"},
		{[]string{"--request", writeFile(t, dir, "gone.json", `{"cohort":{"filename":"`+filepath.Join(dir, "gone.pulse")+`"}}`)}, "DATA_FILE"},
	} {
		text, _ := runApp(t, append(append([]string{"explain"}, tc.args...), "--json")...)
		if env := decodeExplain(t, text); len(env.Errors) == 0 || env.Errors[0].Code != tc.want {
			t.Errorf("explain %v: errors = %+v, want %s", tc.args, env.Errors, tc.want)
		}
	}

	text, err = runApp(t, "explain", "--response", respPath)
	if err != nil {
		t.Fatalf("text: %v\n%s", err, text)
	}
	if !strings.Contains(text, "This response reports") || !strings.Contains(text, "tests[0]") || !strings.Contains(text, "[not_computable]") {
		t.Errorf("text output = %s", text)
	}
}
