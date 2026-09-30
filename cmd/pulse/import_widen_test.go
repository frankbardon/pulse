package main

import (
	"bytes"
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

// TestCliConvertWidthPromotion: the same join-shaped source converts to
// csv, parquet and arrow instead of failing with the fatal
// PULSE_IMPORT_CATEGORICAL_OVERFLOW; the promotion rides the --json
// envelope's warnings, the csv target is the source text, and the
// --keep-pulse intermediate is byte-for-byte what `import csv` writes.
func TestCliConvertWidthPromotion(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("line_id,parent_name,qty\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "%d,parent-%04d,%d\n", i, i/10, i%9)
	}
	src := b.String()
	csvPath := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(csvPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(dir, "imported.pulse")
	if text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", imported); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	want, err := os.ReadFile(imported)
	if err != nil {
		t.Fatal(err)
	}

	for _, ext := range []string{"csv", "parquet", "arrow"} {
		t.Run(ext, func(t *testing.T) {
			out := filepath.Join(dir, "out."+ext)
			kept := filepath.Join(dir, "kept-"+ext+".pulse")
			text, err := runApp(t, "convert", "--json", "--keep-pulse", kept, csvPath, out)
			if err != nil {
				t.Fatalf("convert: %v\n%s", err, text)
			}
			var e struct {
				Data     map[string]any `json:"data"`
				Warnings []struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(text), &e); err != nil {
				t.Fatalf("decode envelope: %v\n%s", err, text)
			}
			if e.Data["RowsConverted"] != float64(3000) {
				t.Errorf("RowsConverted = %v, want 3000", e.Data["RowsConverted"])
			}
			found := false
			for _, w := range e.Warnings {
				found = found || w.Code == "PULSE_IMPORT_WIDTH_PROMOTED" && w.Details["field"] == "parent_name" &&
					w.Details["from"] == "categorical_u8" && w.Details["to"] == "categorical_u16"
			}
			if !found {
				t.Errorf("no parent_name width promotion in the envelope warnings:\n%s", text)
			}
			if ext == "csv" {
				if got, _ := os.ReadFile(out); string(got) != src {
					t.Error("csv target is not the source text")
				}
			}
			if got, _ := os.ReadFile(kept); !bytes.Equal(got, want) {
				t.Errorf("kept cohort (%d bytes) differs from the import (%d bytes)", len(got), len(want))
			}
		})
	}
}
