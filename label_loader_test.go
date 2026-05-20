package pulse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLabelTables_FlatMap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "country.json"),
		[]byte(`{"US": "United States", "CA": "Canada"}`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{LabelTablesDir: dir}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatalf("loadLabelTablesFromDir: %v", err)
	}
	tbl, ok := opts.Extensions.LabelTables["country"]
	if !ok {
		t.Fatalf("table 'country' not loaded; have %v", opts.Extensions.LabelTables)
	}
	if tbl.Rows["US"] != "United States" {
		t.Fatalf("expected US -> United States, got %q", tbl.Rows["US"])
	}
}

func TestLoadLabelTables_Wrapped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "region.json"),
		[]byte(`{"description": "ISO regions", "rows": {"NA": "North America"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{LabelTablesDir: dir}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	tbl := opts.Extensions.LabelTables["region"]
	if tbl.Description != "ISO regions" || tbl.Rows["NA"] != "North America" {
		t.Fatalf("wrapped table not loaded: %+v", tbl)
	}
}

func TestLoadLabelTables_EnvVar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.json"),
		[]byte(`{"a": "A"}`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envLabelTablesDir, dir)
	opts := Options{}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.Extensions.LabelTables["x"].Rows["a"] != "A" {
		t.Fatalf("env-var load failed: %+v", opts.Extensions.LabelTables)
	}
}

func TestLoadLabelTables_CollisionWithProgrammatic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "country.json"),
		[]byte(`{"US": "United States"}`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		LabelTablesDir: dir,
		Extensions: Extensions{
			LabelTables: map[string]LabelTable{"country": {Rows: map[string]string{"US": "US"}}},
		},
	}
	err := loadLabelTablesFromDir(&opts)
	if err == nil {
		t.Fatal("expected collision error")
	}
}

func TestLoadLabelTables_NoDirNoOp(t *testing.T) {
	opts := Options{}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatalf("no-op should not error: %v", err)
	}
	if opts.Extensions.LabelTables != nil {
		t.Fatalf("expected no tables loaded; got %v", opts.Extensions.LabelTables)
	}
}

func TestLoadLabelTables_NonexistentDir(t *testing.T) {
	opts := Options{LabelTablesDir: "/path/that/should/not/exist/__pulse__"}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatalf("nonexistent dir should be tolerated: %v", err)
	}
}
