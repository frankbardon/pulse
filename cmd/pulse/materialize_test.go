package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMaterializeCSV writes a weekly series where y = 2 * (running sum
// of x) + 1 exactly: a regression on the running-sum WINDOW column
// recovers slope 2 / intercept 1, and no source column can stand in.
func writeMaterializeCSV(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("week,x,y\n")
	cum := 0
	for w := 1; w <= 12; w++ {
		x := (w*7)%5 + 1
		cum += x
		fmt.Fprintf(&b, "%d,%d,%d.5\n", w, x, 2*cum+1)
	}
	path := filepath.Join(dir, "series.csv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeJSONFile(t *testing.T, dir, name string, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCliMaterialize_RegressionOnAWindowColumn is the end-to-end route
// the leaf exists for: `pulse api materialize` writes a window column as
// an ordinary field, and `pulse api process` then fits REG_OLS on it —
// a regression the source cohort cannot run (the window column does
// not exist there). The --json envelope carries the result; a refusal
// keeps its own code in errors[0].code; an existing --out needs
// --overwrite.
func TestCliMaterialize_RegressionOnAWindowColumn(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "series.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeMaterializeCSV(t, dir), "--output", src); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	matReq := writeJSONFile(t, dir, "mat.json", map[string]any{
		"cohort": map[string]any{"filename": src},
		"windows": []any{map[string]any{
			"type": "WIN_RUNNING_SUM", "field": "x", "label": "cum_x",
			"order_by": []any{map[string]any{"field": "week"}},
			"frame":    map[string]any{"mode": "rows", "preceding": nil, "following": 0},
		}},
	})
	out := filepath.Join(dir, "derived.pulse")
	text, err := runApp(t, "api", "materialize", "-r", matReq, "-o", out, "--json")
	if err != nil {
		t.Fatalf("materialize: %v\n%s", err, text)
	}
	var env struct {
		FormatVersion string `json:"format_version"`
		Data          struct {
			Target         string   `json:"target"`
			Records        int64    `json:"records"`
			DerivedColumns []string `json:"derived_columns"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, text)
	}
	if env.FormatVersion != "1.1" || env.Data.Records != 12 || len(env.Errors) != 0 ||
		len(env.Data.DerivedColumns) != 1 || env.Data.DerivedColumns[0] != "cum_x" {
		t.Fatalf("materialize envelope: %s", text)
	}

	reg := func(cohort string) string {
		return writeJSONFile(t, dir, "reg.json", map[string]any{
			"cohort": map[string]any{"filename": cohort},
			"regressions": []any{map[string]any{
				"type": "REG_OLS", "name": "on_window", "target": "y", "predictors": []any{"cum_x"},
			}},
		})
	}
	// The source cannot run it: the window column is not a field there.
	if text, _ := runApp(t, "api", "process", "-r", reg(src), "--json"); !strings.Contains(text, `"errors": [`) || strings.Contains(text, `"errors": []`) {
		t.Fatalf("regression on the source's window label should be refused:\n%s", text)
	}
	text, err = runApp(t, "api", "process", "-r", reg(out), "--json")
	if err != nil {
		t.Fatalf("process on materialized cohort: %v\n%s", err, text)
	}
	var resp struct {
		Data struct {
			Regressions []struct {
				Coefficients map[string]float64 `json:"coefficients"`
			} `json:"regressions"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("process envelope: %v\n%s", err, text)
	}
	if len(resp.Data.Regressions) != 1 {
		t.Fatalf("want one regression result:\n%s", text)
	}
	coef := resp.Data.Regressions[0].Coefficients
	if math.Abs(coef["cum_x"]-2) > 1e-9 {
		t.Fatalf("slope on the window column = %v, want 2 (coefficients %v)", coef["cum_x"], coef)
	}

	// An existing target is refused with its own code; nothing moves.
	before, _ := os.ReadFile(out)
	text, _ = runApp(t, "api", "materialize", "-r", matReq, "-o", out, "--json")
	if !strings.Contains(text, `"code": "SERVICE_VALIDATION"`) {
		t.Fatalf("existing --out not refused with SERVICE_VALIDATION:\n%s", text)
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(before, after) {
		t.Fatal("refused materialize changed the target")
	}
	// --overwrite replaces it.
	if text, err := runApp(t, "api", "materialize", "-r", matReq, "-o", out, "--overwrite"); err != nil || !strings.Contains(text, "Materialized 12 record(s)") {
		t.Fatalf("--overwrite: %v\n%s", err, text)
	}

	// A refused slot carries its own code, never the placeholder.
	bad := writeJSONFile(t, dir, "bad.json", map[string]any{
		"cohort":       map[string]any{"filename": src},
		"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "x"}},
	})
	text, _ = runApp(t, "api", "materialize", "-r", bad, "-o", filepath.Join(dir, "never.pulse"), "--json")
	if !strings.Contains(text, `"code": "PULSE_MATERIALIZE_SLOT_UNSUPPORTED"`) {
		t.Fatalf("aggregations not refused with PULSE_MATERIALIZE_SLOT_UNSUPPORTED:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "never.pulse")); !os.IsNotExist(err) {
		t.Fatalf("a refused materialize created its target (stat err %v)", err)
	}
}
