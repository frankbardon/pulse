package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCliImportWidthPromotion: a parent-name column that outgrows the
// categorical_u8 its 500-row sample infers imports every row, and the
// promotion rides the --json envelope's warnings as
// PULSE_IMPORT_WIDTH_PROMOTED — for `import csv` and `import predict`
// with a measured pass alike.
func TestCliImportWidthPromotion(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("line_id,parent_name,qty\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "%d,parent-%04d,%d\n", i, i/10, i%9)
	}
	csvPath := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(csvPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	type env struct {
		Data     map[string]any `json:"data"`
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	check := func(t *testing.T, text string) env {
		t.Helper()
		var e env
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			t.Fatalf("decode envelope: %v\n%s", err, text)
		}
		for _, w := range e.Warnings {
			if w.Code == "PULSE_IMPORT_WIDTH_PROMOTED" && w.Details["field"] == "parent_name" &&
				w.Details["from"] == "categorical_u8" && w.Details["to"] == "categorical_u16" {
				return e
			}
		}
		t.Fatalf("no parent_name width promotion in the envelope warnings:\n%s", text)
		return e
	}

	text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", filepath.Join(dir, "out.pulse"), "--json")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	if e := check(t, text); e.Data["RowsImported"] != float64(3000) {
		t.Errorf("RowsImported = %v, want 3000", e.Data["RowsImported"])
	}

	text, err = runApp(t, "import", "predict", "--input", csvPath, "--elide-constants", "--json")
	if err != nil {
		t.Fatalf("predict: %v\n%s", err, text)
	}
	check(t, text)
}
