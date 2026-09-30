package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestCliImportGroup: repeatable `--group KEY:MEMBER,MEMBER` on an import
// leaf declares one parent group per flag — the commas inside a value
// are NOT split into separate flag values — and writes a 0x02 cohort
// whose groups hold one entry per distinct key. A malformed declaration
// is refused with its own code.
func TestCliImportGroup(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("line_id,cust_id,cust_name,cust_region,prod_id,prod_cat,qty\n")
	for i := 0; i < 240; i++ {
		c, p := i/12, i%6
		fmt.Fprintf(&b, "%d,%d,cust-%02d,%s,%d,cat-%d,%d\n", i+1, 100+c, c, []string{"north", "south"}[c%2], 900+p, p%3, 1+i%5)
	}
	csvPath := filepath.Join(dir, "in.csv")
	if err := os.WriteFile(csvPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pulse")
	text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", out,
		"--group", "cust_id:cust_name,cust_region", "--group", "prod_id:prod_cat")
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
		len(schema.Groups[0].Members) != 3 || len(schema.Groups[1].Members) != 2 {
		t.Fatalf("groups = %+v, want [cust_id,cust_name,cust_region]x20 and [prod_id,prod_cat]x6", schema.Groups)
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
