package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCliTransfer: `pulse export transfer` defaults its output to
// <input>.zst and reports through the --json envelope; `pulse import
// transfer` writes a byte-identical cohort with the same sha256; a
// second import onto the same output is refused with its own code
// until --overwrite; and `cohort inspect` on the artifact names
// PULSE_COHORT_COMPRESSED.
func TestCliTransfer(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeGroupCSV(t, dir)
	cohort := filepath.Join(dir, "c.pulse")
	if text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", cohort); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	text, err := runApp(t, "export", "transfer", "--input", cohort, "--level", "5", "--json")
	if err != nil {
		t.Fatalf("export transfer: %v\n%s", err, text)
	}
	type rep struct {
		Data struct {
			Output string  `json:"output"`
			Layout string  `json:"layout"`
			Level  int     `json:"level"`
			SHA256 string  `json:"sha256"`
			Ratio  float64 `json:"ratio"`
		} `json:"data"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	var out rep
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if out.Data.Output != cohort+".zst" || out.Data.Layout != "single_file" || out.Data.Level != 5 || out.Data.Ratio <= 1 {
		t.Fatalf("export envelope = %+v", out.Data)
	}

	rt := filepath.Join(dir, "rt.pulse")
	text, err = runApp(t, "import", "transfer", "--input", cohort+".zst", "--output", rt, "--json")
	if err != nil {
		t.Fatalf("import transfer: %v\n%s", err, text)
	}
	var in rep
	_ = json.Unmarshal([]byte(text), &in)
	a, _ := os.ReadFile(cohort)
	b, _ := os.ReadFile(rt)
	if !bytes.Equal(a, b) || in.Data.SHA256 != out.Data.SHA256 {
		t.Fatalf("round trip: identical %v, sha %s vs %s", bytes.Equal(a, b), in.Data.SHA256, out.Data.SHA256)
	}

	text, _ = runApp(t, "import", "transfer", "--input", cohort+".zst", "--output", rt, "--json")
	var refused rep
	_ = json.Unmarshal([]byte(text), &refused)
	if len(refused.Errors) == 0 || refused.Errors[0].Code != "PULSE_TRANSFER_INVALID" {
		t.Fatalf("existing output: %s", text)
	}
	if text, err := runApp(t, "import", "transfer", "--input", cohort+".zst", "--output", rt, "--overwrite"); err != nil || !strings.Contains(text, "sha256 "+out.Data.SHA256) {
		t.Fatalf("--overwrite: %v\n%s", err, text)
	}

	text, _ = runApp(t, "cohort", "inspect", cohort+".zst", "--json")
	if !strings.Contains(text, "PULSE_COHORT_COMPRESSED") {
		t.Fatalf("inspect of the artifact does not name PULSE_COHORT_COMPRESSED:\n%s", text)
	}
}
