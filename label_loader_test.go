package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/imports"
	"github.com/frankbardon/pulse/io/spss"
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

// spssSidecarFixture is a realistic (abbreviated) SPSS metadata sidecar
// document. It is deliberately NOT parseable as a label table in either
// shape — no "rows" key, and object-valued members defeat the flat
// map[string]string arm — which is precisely the hazard being fixed.
const spssSidecarFixture = `{
  "format_version": 1,
  "kind": "spss",
  "fingerprint": {"sha256": "00", "source_size": 1, "source_mod_time": 2},
  "payload": {"variables": [{"short_name": "Q1"}], "derived": []}
}`

func TestLoadLabelTables_SkipsPulseSidecars(t *testing.T) {
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
			if err := os.WriteFile(filepath.Join(dir, "regions.json"),
				[]byte(`{"NA": "North America"}`), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.filename), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			opts := Options{LabelTablesDir: dir}
			if err := loadLabelTablesFromDir(&opts); err != nil {
				t.Fatalf("sidecar must not fail the load: %v", err)
			}
			if opts.Extensions.LabelTables["regions"].Rows["NA"] != "North America" {
				t.Fatalf("valid neighbour table not loaded: %+v", opts.Extensions.LabelTables)
			}
			if len(opts.Extensions.LabelTables) != 1 {
				t.Fatalf("skipped sidecar registered a table: %+v", opts.Extensions.LabelTables)
			}
			// Belt and braces: no table under the trimmed sidecar name.
			skipped := strings.TrimSuffix(tc.filename, ".json")
			if _, ok := opts.Extensions.LabelTables[skipped]; ok {
				t.Fatalf("sidecar registered under %q", skipped)
			}
		})
	}
}

// A skipped sidecar must not become a licence to swallow real breakage:
// a malformed label table still hard-fails, naming its path.
func TestLoadLabelTables_MalformedStillHardFails(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "truncated json", body: `{"NA": "North America"`},
		{name: "wrong value type", body: `{"NA": 5}`},
		{name: "empty object", body: `{}`},
		{name: "not json at all", body: `NA = North America`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "regions.json")
			if err := os.WriteFile(path, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			// A Pulse sidecar in the same directory must not soften this.
			if err := os.WriteFile(filepath.Join(dir, "cohort.pulse"+spss.SidecarSuffix),
				[]byte(spssSidecarFixture), 0644); err != nil {
				t.Fatal(err)
			}
			opts := Options{LabelTablesDir: dir}
			err := loadLabelTablesFromDir(&opts)
			if err == nil {
				t.Fatal("malformed label table must hard-fail")
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error must name the offending path %q; got %v", path, err)
			}
		})
	}
}

func TestIsPulseSidecarName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "cohort.pulse.spss.json", want: true},
		{name: "cohort.pulse.meta.json", want: true},
		{name: "a.b.c.pulse.spss.json", want: true},
		{name: "regions.json", want: false},
		{name: "spss.json", want: false},
		{name: "meta.json", want: false},
		{name: "country.json", want: false},
		{name: "cohort.pulse.9f3a.idx", want: false},
	}
	for _, tc := range cases {
		if got := isPulseSidecarName(tc.name); got != tc.want {
			t.Errorf("isPulseSidecarName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
