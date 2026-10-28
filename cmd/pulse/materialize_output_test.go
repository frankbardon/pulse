package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestCliMaterialize_OutputSlot: `pulse api materialize` writes the
// cohort an `output` block lays out (a pure reshape), and every other
// api leaf taking a request refuses the slot with its own code in
// errors[0].code.
func TestCliMaterialize_OutputSlot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "series.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeMaterializeCSV(t, dir), "--output", src); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	cohort := map[string]any{"filename": src}
	output := map[string]any{"columns": []any{map[string]any{"from": "y", "as": "target"}, map[string]any{"from": "week"}}}
	matReq := writeJSONFile(t, dir, "mat.json", map[string]any{"cohort": cohort, "output": output})
	text, err := runApp(t, "api", "materialize", "-r", matReq, "-o", filepath.Join(dir, "reshaped.pulse"), "--json")
	if err != nil {
		t.Fatalf("materialize: %v\n%s", err, text)
	}
	var env struct {
		Data struct {
			Records        int64    `json:"records"`
			DerivedColumns []string `json:"derived_columns"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil || env.Data.Records != 12 || len(env.Errors) != 0 || len(env.Data.DerivedColumns) != 0 {
		t.Fatalf("materialize envelope (%v): %s", err, text)
	}

	agg := []any{map[string]any{"type": "AGG_COUNT", "field": "week", "label": "n"}}
	req := map[string]any{"cohort": cohort, "aggregations": agg, "output": output}
	leaves := []struct {
		leaf string
		body any
	}{
		{"process", req},
		{"predict", req},
		{"compose", map[string]any{"requests": []any{req}}},
		{"process-chain", map[string]any{"cohort": cohort, "stages": []any{map[string]any{"name": "a",
			"request": map[string]any{"aggregations": agg, "output": output}}}}},
	}
	for _, l := range leaves {
		path := writeJSONFile(t, dir, l.leaf+".json", l.body)
		text, _ := runApp(t, "api", l.leaf, "--request", path, "--json")
		if !strings.Contains(text, `"code": "PULSE_MATERIALIZE_OUTPUT_ONLY"`) {
			t.Errorf("api %s: output not refused with PULSE_MATERIALIZE_OUTPUT_ONLY:\n%s", l.leaf, text)
		}
	}
}
