package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// writeGroupCSV writes a synthetic 240-row parent/child join: 20
// customers (cust_id → name, region, score) at 12 lines each and 6
// products (prod_id → category, price). Both blocks are wider than the
// 4-byte group index.
func writeGroupCSV(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("line_id,cust_id,cust_name,cust_region,cust_score,prod_id,prod_cat,prod_price,qty\n")
	for i := 0; i < 240; i++ {
		c, p := i/12, i%6
		fmt.Fprintf(&b, "%d,%d,cust-%02d,%s,%d.5,%d,cat-%d,%d.25,%d\n",
			i+1, 100+c, c, []string{"north", "south"}[c%2], 40+c, 900+p, p%3, 10+p, 1+i%5)
	}
	path := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCliImportGroup: repeatable `--group KEY:MEMBER,MEMBER` on an import
// leaf declares one parent group per flag — the commas inside a value
// are NOT split into separate flag values — and writes a 0x02 cohort
// whose groups hold one entry per distinct key. A malformed declaration
// is refused with its own code.
func TestCliImportGroup(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeGroupCSV(t, dir)
	out := filepath.Join(dir, "out.pulse")
	text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", out,
		"--group", "cust_id:cust_name,cust_region,cust_score", "--group", "prod_id:prod_cat,prod_price")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	schema, v, err := encoding.ReadPreamble(r)
	if err != nil || v != encoding.FormatVersionV2 {
		t.Fatalf("preamble: version 0x%02x, err %v", v, err)
	}
	if len(schema.Groups) != 2 || schema.GroupEntryCount(0) != 20 || schema.GroupEntryCount(1) != 6 ||
		len(schema.Groups[0].Members) != 4 || len(schema.Groups[1].Members) != 3 {
		t.Fatalf("groups = %+v, want [cust_id,cust_name,cust_region,cust_score]x20 and [prod_id,prod_cat,prod_price]x6", schema.Groups)
	}
	if !strings.Contains(text, "group 1 [key: cust_id]: 20 distinct tuples") {
		t.Fatalf("output does not report the groups:\n%s", text)
	}

	text, _ = runApp(t, "import", "csv", "--input", csvPath, "--output", filepath.Join(dir, "bad.pulse"),
		"--json", "--group", "cust_id:")
	if !strings.Contains(text, "PULSE_GROUP_DECLARATION_INVALID") {
		t.Fatalf("malformed --group not refused with PULSE_GROUP_DECLARATION_INVALID:\n%s", text)
	}
}

// TestCliImportGroupGate: the per-group viability gate on the CLI. A
// group no wider than its index is dropped with PULSE_GROUP_TOO_NARROW
// and a low-ratio group is written with PULSE_DEDUP_LOW_RATIO, both in
// the --json envelope's warnings; --strict turns the finding into
// errors[0] with its own code and writes nothing; --dedup-ratio-floor
// moves the floor.
func TestCliImportGroupGate(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeGroupCSV(t, dir)
	type entry struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	type envelope struct {
		FormatVersion string `json:"format_version"`
		Data          struct {
			Groups []struct {
				Label           string  `json:"label"`
				Verdict         string  `json:"verdict"`
				Ratio           float64 `json:"ratio"`
				DictionaryBytes int64   `json:"dictionary_bytes"`
			}
		} `json:"data"`
		Errors   []entry `json:"errors"`
		Warnings []entry `json:"warnings"`
	}
	run := func(out string, args ...string) envelope {
		t.Helper()
		text, _ := runApp(t, append([]string{"import", "csv", "--input", csvPath, "--output", out, "--json"}, args...)...)
		var env envelope
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("envelope: %v\n%s", err, text)
		}
		if env.FormatVersion != "1.1" || env.Errors == nil || env.Warnings == nil {
			t.Fatalf("envelope shape: %s", text)
		}
		return env
	}

	// Narrow (prod_id + prod_cat = 3 bytes) dropped; line_id is unique,
	// so its group has ratio 1 and is written with a warning.
	out := filepath.Join(dir, "gate.pulse")
	env := run(out, "--group", "cust_id:cust_name,cust_region,cust_score", "--group", "prod_id:prod_cat", "--group", "line_id:qty,prod_price")
	if len(env.Errors) != 0 {
		t.Fatalf("errors without --strict: %+v", env.Errors)
	}
	if len(env.Warnings) != 2 || env.Warnings[0].Code != "PULSE_GROUP_TOO_NARROW" || env.Warnings[1].Code != "PULSE_DEDUP_LOW_RATIO" {
		t.Fatalf("warnings %+v", env.Warnings)
	}
	if env.Warnings[1].Details["ratio"] != 1.0 || env.Warnings[1].Details["group_label"] != "group 3 [key: line_id]" {
		t.Fatalf("low-ratio details %v", env.Warnings[1].Details)
	}
	verdicts := []string{}
	for _, g := range env.Data.Groups {
		verdicts = append(verdicts, g.Verdict)
	}
	if strings.Join(verdicts, ",") != "admitted,dropped_too_narrow,low_ratio" || env.Data.Groups[0].DictionaryBytes <= 0 {
		t.Fatalf("groups %+v", env.Data.Groups)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("a low-ratio import must still write: %v", err)
	}

	// --strict: the same low-ratio group fails the import.
	strictOut := filepath.Join(dir, "strict.pulse")
	env = run(strictOut, "--strict", "--group", "line_id:qty,prod_price")
	if len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_DEDUP_LOW_RATIO" {
		t.Fatalf("strict errors %+v", env.Errors)
	}
	if _, err := os.Stat(strictOut); !os.IsNotExist(err) {
		t.Fatalf("strict refusal wrote a cohort (stat err %v)", err)
	}

	// --dedup-ratio-floor: customers repeat 12x; a floor of 13 flags them.
	env = run(filepath.Join(dir, "floor.pulse"), "--dedup-ratio-floor", "13", "--group", "cust_id:cust_name,cust_region,cust_score")
	if len(env.Warnings) != 1 || env.Warnings[0].Code != "PULSE_DEDUP_LOW_RATIO" || env.Warnings[0].Details["ratio_floor"] != 13.0 {
		t.Fatalf("floor 13 warnings %+v", env.Warnings)
	}
}

// TestCliCohortInspectGroups: the text inspect leaf reports a grouped
// cohort's physical layout and each group's realized figures, and marks
// member fields; a 0x01 cohort prints none of it.
func TestCliCohortInspectGroups(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeGroupCSV(t, dir)
	out := filepath.Join(dir, "out.pulse")
	if text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", out,
		"--group", "cust_id:cust_name,cust_region,cust_score"); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	text, err := runApp(t, "cohort", "inspect", out)
	if err != nil {
		t.Fatalf("cohort inspect: %v\n%s", err, text)
	}
	for _, want := range []string{
		"Records: 240",
		"Format: 0x02 (record stride",
		"Groups: 1",
		"group 1 [key: cust_id]  indexed  admitted",
		"fields: cust_id, cust_name, cust_region, cust_score",
		"dictionary: 20 entries x",
		"ratio: 12.00x",
		"stored in: group 1 (indexed, key)",
		"stored in: group 1 (indexed)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("grouped inspect text missing %q:\n%s", want, text)
		}
	}

	flat, err := runApp(t, "cohort", "inspect", createTestPulseFile(t, dir))
	if err != nil {
		t.Fatalf("cohort inspect flat: %v\n%s", err, flat)
	}
	for _, absent := range []string{"Format:", "Groups:", "stored in:"} {
		if strings.Contains(flat, absent) {
			t.Errorf("0x01 inspect text carries %q:\n%s", absent, flat)
		}
	}
}
