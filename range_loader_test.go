package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/imports"
	"github.com/frankbardon/pulse/io/spss"
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

// A range-tables dir aimed at a data directory sees the sidecars Pulse
// itself wrote beside each cohort. Skipping them by suffix is what keeps
// pulse.New from dying on a file Pulse produced, blaming a malformed
// range table. Mirrors TestLoadLabelTables_SkipsPulseSidecars.
func TestLoadRangeTables_SkipsPulseSidecars(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		body     string
	}{
		{
			name:     "spss metadata sidecar",
			filename: "cohort.pulse" + spss.SidecarSuffix,
			body:     spssSidecarFixture,
		},
		{
			name:     "managed import sidecar",
			filename: "cohort.pulse" + imports.SidecarSuffix,
			body:     `{"handle": "h1", "created_at": "2026-01-01T00:00:00Z", "ttl": "7d"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "quarters.json"),
				[]byte(`[{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}]`), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.filename), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			opts := Options{RangeTablesDir: dir}
			if err := loadRangeTablesFromDir(&opts); err != nil {
				t.Fatalf("sidecar must not fail the load: %v", err)
			}
			if len(opts.Extensions.RangeTables["quarters"].Ranges) != 1 {
				t.Fatalf("valid neighbour table not loaded: %+v", opts.Extensions.RangeTables)
			}
			if len(opts.Extensions.RangeTables) != 1 {
				t.Fatalf("skipped sidecar registered a table: %+v", opts.Extensions.RangeTables)
			}
			skipped := strings.TrimSuffix(tc.filename, ".json")
			if _, ok := opts.Extensions.RangeTables[skipped]; ok {
				t.Fatalf("sidecar registered under %q", skipped)
			}
		})
	}
}

// The skip is an exclusion list, not tolerance of unparseable JSON: a
// malformed range table still hard-fails with its path named, even with
// a sidecar sitting in the same directory.
func TestLoadRangeTables_MalformedStillHardFails(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "truncated json", body: `[{"label":"Q1","start":"2024-01-01"`},
		{name: "wrong shape", body: `{"quarters": 5}`},
		{name: "not json at all", body: `Q1 = 2024-01-01`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "cohort.pulse"+spss.SidecarSuffix),
				[]byte(spssSidecarFixture), 0644); err != nil {
				t.Fatal(err)
			}
			bad := filepath.Join(dir, "quarters.json")
			if err := os.WriteFile(bad, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			opts := Options{RangeTablesDir: dir}
			err := loadRangeTablesFromDir(&opts)
			if err == nil {
				t.Fatal("malformed range table must still hard-fail")
			}
			if !strings.Contains(err.Error(), bad) {
				t.Fatalf("error must name the offending path, got: %v", err)
			}
		})
	}
}
