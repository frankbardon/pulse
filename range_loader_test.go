package pulse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRangeTables_FlatArray(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "quarters.json"),
		[]byte(`[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"},{"label":"Q2","start":"2024-04-01","end":"2024-06-30"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{RangeTablesDir: dir}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		t.Fatalf("loadRangeTablesFromDir: %v", err)
	}
	tbl, ok := opts.Extensions.RangeTables["quarters"]
	if !ok {
		t.Fatalf("table 'quarters' not loaded; have %v", opts.Extensions.RangeTables)
	}
	if len(tbl.Ranges) != 2 || tbl.Ranges[0].Label != "Q1" {
		t.Fatalf("unexpected ranges: %+v", tbl.Ranges)
	}
}

func TestLoadRangeTables_Wrapped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "phases.json"),
		[]byte(`{"description":"launch phases","ranges":[{"label":"pre","end":"2024-01-01"},{"label":"post","start":"2024-01-02"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{RangeTablesDir: dir}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	tbl := opts.Extensions.RangeTables["phases"]
	if tbl.Description != "launch phases" || len(tbl.Ranges) != 2 {
		t.Fatalf("wrapped table not loaded: %+v", tbl)
	}
}

func TestLoadRangeTables_EnvVar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.json"),
		[]byte(`[{"label":"all"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envRangeTablesDir, dir)
	opts := Options{}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if len(opts.Extensions.RangeTables["x"].Ranges) != 1 {
		t.Fatalf("env-var load failed: %+v", opts.Extensions.RangeTables)
	}
}

func TestLoadRangeTables_CollisionWithProgrammatic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "quarters.json"),
		[]byte(`[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		RangeTablesDir: dir,
		Extensions: Extensions{
			RangeTables: map[string]RangeTable{"quarters": {Ranges: []DateRangeSpec{{Label: "Q1"}}}},
		},
	}
	if err := loadRangeTablesFromDir(&opts); err == nil {
		t.Fatal("expected collision error")
	}
}

func TestLoadRangeTables_NoDirNoOp(t *testing.T) {
	opts := Options{}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		t.Fatalf("no-op should not error: %v", err)
	}
	if opts.Extensions.RangeTables != nil {
		t.Fatalf("expected no tables loaded; got %v", opts.Extensions.RangeTables)
	}
}

func TestLoadRangeTables_NonexistentDir(t *testing.T) {
	opts := Options{RangeTablesDir: "/path/that/should/not/exist/__pulse_range__"}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		t.Fatalf("nonexistent dir should be tolerated: %v", err)
	}
}
