package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// TestCliDedup: `pulse dedup` rewrites a flat cohort into the same
// bytes `pulse import --group` writes from its source (one encode path),
// in place, reporting through the --json envelope; --out writes a new
// file and leaves the input alone; --suggest-groups alone writes
// nothing and returns candidates; --strict turns a gate finding into
// errors[0] with its own code; a shard archive is refused with a code.
func TestCliDedup(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeGroupCSV(t, dir)
	flat := filepath.Join(dir, "flat.pulse")
	grouped := filepath.Join(dir, "grouped.pulse")
	groups := []string{"--group", "cust_id:cust_name,cust_region,cust_score", "--group", "prod_id:prod_cat,prod_price"}
	if text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", flat); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	if text, err := runApp(t, append([]string{"import", "csv", "--input", csvPath, "--output", grouped}, groups...)...); err != nil {
		t.Fatalf("grouped import: %v\n%s", err, text)
	}
	flatRaw, _ := os.ReadFile(flat)
	want, _ := os.ReadFile(grouped)

	// --out: a new file, input untouched.
	outPath := filepath.Join(dir, "out.pulse")
	text, err := runApp(t, append([]string{"dedup", flat, "--out", outPath}, groups...)...)
	if err != nil {
		t.Fatalf("dedup --out: %v\n%s", err, text)
	}
	got, _ := os.ReadFile(outPath)
	now, _ := os.ReadFile(flat)
	if !bytes.Equal(got, want) || !bytes.Equal(now, flatRaw) {
		t.Fatalf("--out: target matches import %v, input untouched %v\n%s", bytes.Equal(got, want), bytes.Equal(now, flatRaw), text)
	}

	// --suggest-groups alone: read-only, candidates in the envelope.
	text, err = runApp(t, "dedup", flat, "--suggest-groups", "--json")
	if err != nil {
		t.Fatalf("suggest: %v\n%s", err, text)
	}
	var env struct {
		Data struct {
			Rewritten       bool `json:"rewritten"`
			GroupCandidates struct {
				Suggested []string `json:"suggested"`
			} `json:"group_candidates"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, text)
	}
	if env.Data.Rewritten || len(env.Data.GroupCandidates.Suggested) == 0 {
		t.Fatalf("suggest-only: %s", text)
	}

	// --strict with a low-ratio group: refused with its own code, input untouched.
	text, _ = runApp(t, "dedup", flat, "--json", "--strict", "--group", "line_id,cust_name,prod_price")
	if !strings.Contains(text, `"code": "PULSE_DEDUP_LOW_RATIO"`) {
		t.Fatalf("--strict not refused with PULSE_DEDUP_LOW_RATIO:\n%s", text)
	}
	if now, _ := os.ReadFile(flat); !bytes.Equal(now, flatRaw) {
		t.Fatal("refused dedup changed the input")
	}

	// In place.
	text, err = runApp(t, append([]string{"dedup", flat, "--json"}, groups...)...)
	if err != nil {
		t.Fatalf("dedup: %v\n%s", err, text)
	}
	got, _ = os.ReadFile(flat)
	if !bytes.Equal(got, want) || !strings.Contains(text, `"in_place": true`) || !strings.Contains(text, `"format_version_after": 2`) {
		t.Fatalf("in place: matches import %v\n%s", bytes.Equal(got, want), text)
	}
	if _, v, err := encx.ReadPreamble(bytes.NewReader(got)); err != nil || v != encoding.FormatVersionV2 {
		t.Fatalf("version 0x%02x err %v", v, err)
	}

	// No operation requested.
	text, _ = runApp(t, "dedup", flat, "--json")
	if !strings.Contains(text, `"code": "CLI_INPUT"`) {
		t.Fatalf("no-op dedup not refused with CLI_INPUT:\n%s", text)
	}
}

// TestCliDedup_ShardArchive: a shard archive is refused with
// SERVICE_VALIDATION, not rewritten.
func TestCliDedup_ShardArchive(t *testing.T) {
	dir := t.TempDir()
	csvPath := writeGroupCSV(t, dir)
	a, b := filepath.Join(dir, "a.pulse"), filepath.Join(dir, "b.pulse")
	for _, p := range []string{a, b} {
		if text, err := runApp(t, "import", "csv", "--input", csvPath, "--output", p); err != nil {
			t.Fatalf("import: %v\n%s", err, text)
		}
	}
	arch := filepath.Join(dir, "arch.pulse")
	if text, err := runApp(t, "shard", "create", arch, "--include", a, "--include", b); err != nil {
		t.Fatalf("shard create: %v\n%s", err, text)
	}
	before, _ := os.ReadFile(arch)
	text, _ := runApp(t, "dedup", arch, "--json", "--group", "cust_id:cust_name,cust_region,cust_score")
	if !strings.Contains(text, `"code": "SERVICE_VALIDATION"`) || !strings.Contains(text, "shard_archive") {
		t.Fatalf("shard archive not refused:\n%s", text)
	}
	if after, _ := os.ReadFile(arch); !bytes.Equal(before, after) {
		t.Fatal("refused dedup changed the archive")
	}
}
