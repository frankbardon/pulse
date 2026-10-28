package main

import (
	"encoding/json"
	"os"
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

// TestCliMaterialize_Predict: `pulse api materialize --predict` writes
// the predict envelope (the output columns, derived names and risks)
// and nothing at -o; a refused request is the envelope's coded error
// with exit status 1; without --predict, -o stays required (usage).
func TestCliMaterialize_Predict(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "series.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeMaterializeCSV(t, dir), "--output", src); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	cohort := map[string]any{"filename": src}
	output := map[string]any{"columns": []any{
		map[string]any{"from": "y", "as": "target", "nullable": false},
		map[string]any{"from": "LOG_x"},
	}}
	features := []any{map[string]any{"type": "FEAT_LOG", "field": "x"}}
	req := writeJSONFile(t, dir, "mat.json", map[string]any{"cohort": cohort, "features": features, "output": output})
	out := filepath.Join(dir, "never.pulse")

	status, text, _ := runExit(t, "api", "materialize", "-r", req, "-o", out, "--predict", "--json")
	var env struct {
		Data struct {
			Valid   bool `json:"valid"`
			Columns []struct {
				Name string `json:"name"`
				Type string `json:"type"`
				From string `json:"from"`
			} `json:"columns"`
			DerivedColumns []string `json:"derived_columns"`
			Risks          []struct {
				Column string `json:"column"`
				Code   string `json:"code"`
			} `json:"risks"`
		} `json:"data"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil || status != 0 || !env.Data.Valid || len(env.Errors) != 0 {
		t.Fatalf("predict (status %d, %v): %s", status, err, text)
	}
	if len(env.Data.Columns) != 2 || env.Data.Columns[0].Name != "target" || env.Data.Columns[1].From != "LOG_x" ||
		len(env.Data.DerivedColumns) != 1 || env.Data.DerivedColumns[0] != "LOG_x" {
		t.Errorf("predicted columns %+v, derived %v", env.Data.Columns, env.Data.DerivedColumns)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("--predict wrote %s (stat err %v)", out, err)
	}

	bad := writeJSONFile(t, dir, "bad.json", map[string]any{"cohort": cohort, "features": features,
		"output": map[string]any{"columns": []any{map[string]any{"from": "nope"}}}})
	status, text, _ = runExit(t, "api", "materialize", "-r", bad, "--predict", "--json")
	if err := json.Unmarshal([]byte(text), &env); err != nil || status != 1 || env.Data.Valid ||
		len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN" {
		t.Errorf("refused predict (status %d, %v): %s", status, err, text)
	}
	if status, _, _ := runExit(t, "api", "materialize", "-r", req); status != 2 {
		t.Errorf("materialize without -o or --predict exited %d, want 2 (usage)", status)
	}
}

// TestCliMaterialize_ShardArchiveOutput: an `output.shards` block makes
// `pulse api materialize` write a shard archive — the envelope names its
// shards, the text report says so — and --predict reports the container
// plan in both forms.
func TestCliMaterialize_ShardArchiveOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "series.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeMaterializeCSV(t, dir), "--output", src); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	output := map[string]any{"shards": map[string]any{"max_records": 5}, "elide_constants": true}
	req := writeJSONFile(t, dir, "mat.json", map[string]any{"cohort": map[string]any{"filename": src}, "output": output})

	text, err := runApp(t, "api", "materialize", "-r", req, "-o", filepath.Join(dir, "arch.pulse"), "--json")
	if err != nil {
		t.Fatalf("materialize: %v\n%s", err, text)
	}
	var env struct {
		Data struct {
			Records int64    `json:"records"`
			Shards  []string `json:"shards"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil || env.Data.Records != 12 || len(env.Data.Shards) != 3 {
		t.Fatalf("materialize envelope (%v): %s", err, text)
	}
	text, err = runApp(t, "api", "materialize", "-r", req, "-o", filepath.Join(dir, "arch2.pulse"))
	if err != nil || !strings.Contains(text, "Shard archive of 3 shard(s): part-00001.pulse, part-00002.pulse, part-00003.pulse") {
		t.Errorf("text report (%v):\n%s", err, text)
	}

	text, err = runApp(t, "api", "materialize", "-r", req, "--predict", "--json")
	if err != nil || !strings.Contains(text, `"kind": "shard_archive"`) || !strings.Contains(text, `"max_shards": 3`) {
		t.Errorf("predict envelope (%v):\n%s", err, text)
	}
	text, err = runApp(t, "api", "materialize", "-r", req, "--predict")
	if err != nil || !strings.Contains(text, "Container: shard archive, 5 row(s) per shard") || !strings.Contains(text, "Container: constants elided") {
		t.Errorf("predict text (%v):\n%s", err, text)
	}
}
