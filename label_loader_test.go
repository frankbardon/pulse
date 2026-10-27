package pulse

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/imports"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
)

func TestLoadLabelTables_FlatMap(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// TestLoadLabelTables_KeepTheFileKeyOrder: both file shapes record their
// rows' key order as LabelTable.Order — the code order of a `.sav` export
// bound to the table — and a repeated key keeps its first position.
func TestLoadLabelTables_KeepTheFileKeyOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := map[string]string{
		"flat.json":    `{"NE": "Northeast", "MW": "Midwest", "SO": "South", "WE": "West"}`,
		"wrapped.json": `{"description": "census regions", "rows": {"WE": "West", "NE": "Northeast", "SO": "South"}}`,
		"repeat.json":  `{"b": "B", "a": "A", "b": "B2"}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{LabelTablesDir: dir}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]string{
		"flat":    {"NE", "MW", "SO", "WE"},
		"wrapped": {"WE", "NE", "SO"},
		"repeat":  {"b", "a"},
	} {
		if got := opts.Extensions.LabelTables[name].Order; !reflect.DeepEqual(got, want) {
			t.Errorf("%s Order = %q, want %q", name, got, want)
		}
	}
}

func TestLoadLabelTables_EnvVar(t *testing.T) {
	// serial: t.Setenv mutates the process environment
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
	t.Parallel()

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
	assertDirCollision(t, loadLabelTablesFromDir(&opts), "label_table", "country", filepath.Join(dir, "country.json"))
	// pulse.New raises the same coded refusal.
	_, err := New(Options{FS: afero.NewMemMapFs(), LabelTablesDir: dir, Extensions: opts.Extensions})
	assertDirCollision(t, err, "label_table", "country", filepath.Join(dir, "country.json"))
}

// assertDirCollision: err is the documented directory-loader collision
// — PULSE_EXTENSION_DUPLICATE naming the category, name and file.
func assertDirCollision(t *testing.T, err error, category, name, path string) {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_EXTENSION_DUPLICATE {
		t.Fatalf("collision: got %v, want PULSE_EXTENSION_DUPLICATE", err)
	}
	want := map[string]any{"category": category, "name": name, "path": path}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Fatalf("collision details %+v, want %+v", ce.Details, want)
	}
}

func TestLoadLabelTables_NoDirNoOp(t *testing.T) {
	t.Parallel()

	opts := Options{}
	if err := loadLabelTablesFromDir(&opts); err != nil {
		t.Fatalf("no-op should not error: %v", err)
	}
	if opts.Extensions.LabelTables != nil {
		t.Fatalf("expected no tables loaded; got %v", opts.Extensions.LabelTables)
	}
}

func TestLoadLabelTables_NonexistentDir(t *testing.T) {
	t.Parallel()

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

// modelFileFixture is a saved regression model file (model_format_version
// 1). Neither a label table nor a range table in any shape — which is
// why both table-directory loaders must skip it by suffix.
const modelFileFixture = `{
  "model_format_version": 1,
  "name": "mmm",
  "type": "REG_OLS",
  "target": "y",
  "predictors": ["x"],
  "terms": ["x"],
  "coefficients": {"(intercept)": 1.5, "x": -0.25},
  "std_errors": {"(intercept)": 0.5, "x": 0.125},
  "n_obs": 10,
  "provenance": {"cohort": "c.pulse"}
}`

// populationFileFixture is a saved population file (a Compose vector
// fit's save). Neither a label table nor a range table in any shape —
// both table-directory loaders must skip it by suffix.
const populationFileFixture = `{
  "population_format_version": 1,
  "name": "brand",
  "operator": "MAT_COVARIANCE",
  "labels": ["q1", "q2"],
  "members": ["q1", "q2"],
  "basis": "unweighted",
  "n": 3,
  "n_mass": 3,
  "n_weight_invalid": 0,
  "sum_weights": 3,
  "sum_weights_sq": 3,
  "mean": [1, 2],
  "m2": [[2, 1], [1, 2]],
  "provenance": {"cohort": "c.pulse"}
}`

// semFitFileFixture is a minimal saved SEM fit file (U59 E3-S3): one
// factor over three ratings, ungrouped.
const semFitFileFixture = `{
  "sem_format_version": 1,
  "name": "brand",
  "operator": "MAT_SEM",
  "model": "f =~ q1 + q2 + q3",
  "labels": ["q1", "q2", "q3"],
  "members": ["q1", "q2", "q3"],
  "observed": ["q1", "q2", "q3"],
  "inner": ["f"],
  "latents": ["f"],
  "estimator": "ml",
  "conventions": "semopy",
  "meanstructure": false,
  "solver": "slsqp",
  "information": "dense",
  "basis": "unweighted",
  "n": 100,
  "sum_weights": 100,
  "dof": 0,
  "n_params": 6,
  "converged": true,
  "status": "converged",
  "iterations": 12,
  "groups": [{
    "level": "", "n": 100, "sum_weights": 100,
    "lambda": [[1], [0.8], [0.7]], "beta": [[0]], "psi": [[0.5]],
    "theta": [[0.3, 0, 0], [0, 0.4, 0], [0, 0, 0.5]],
    "nu": [0, 0, 0], "alpha": [0], "sample_mean": [3, 3.1, 2.9]
  }],
  "provenance": {"cohort": "c.pulse"}
}`

// The fixture is a real SEM fit file whose model rebuilds, not a
// stand-in.
func TestSEMFitFileFixture_Decodes(t *testing.T) {
	t.Parallel()

	f, err := model.DecodeSEMFile("fixture", []byte(semFitFileFixture))
	if err != nil {
		t.Fatalf("fixture is not a valid SEM fit file: %v", err)
	}
	if err := vectors.CheckSEMRebuild(f.SEMFit("brand")); err != nil {
		t.Fatalf("fixture's model does not rebuild: %v", err)
	}
}

// The fixture is a real population file, not a stand-in.
func TestPopulationFileFixture_Decodes(t *testing.T) {
	t.Parallel()

	if _, err := model.DecodePopulationFile("fixture", []byte(populationFileFixture)); err != nil {
		t.Fatalf("fixture is not a valid population file: %v", err)
	}
}

// The fixture is a real model file, not a stand-in.
func TestModelFileFixture_Decodes(t *testing.T) {
	t.Parallel()

	if _, err := model.DecodeFile("fixture", []byte(modelFileFixture)); err != nil {
		t.Fatalf("fixture is not a valid model file: %v", err)
	}
}

func TestLoadLabelTables_SkipsPulseSidecars(t *testing.T) {
	t.Parallel()

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
		{
			name:     "saved model file",
			filename: "mmm" + model.FileSuffix,
			body:     modelFileFixture,
		},
		{
			name:     "saved population file",
			filename: "brand" + model.PopulationSuffix,
			body:     populationFileFixture,
		},
		{
			name:     "saved SEM fit file",
			filename: "brand" + model.SEMSuffix,
			body:     semFitFileFixture,
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
	t.Parallel()

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
	t.Parallel()

	cases := []struct {
		name string
		want bool
	}{
		{name: "cohort.pulse.spss.json", want: true},
		{name: "cohort.pulse.meta.json", want: true},
		{name: "cohort.pulse.indexes.json", want: true},
		{name: "mmm.pulse-model.json", want: true},
		{name: "models/a.b.pulse-model.json", want: true},
		{name: "pulse-model.json", want: false},
		{name: "mmm.pulse-model.json.123.tmp", want: false},
		{name: "brand.pulse-population.json", want: true},
		{name: "pops/a.b.pulse-population.json", want: true},
		{name: "pulse-population.json", want: false},
		{name: "brand.pulse-population.json.42.tmp", want: false},
		{name: "brand.pulse-sem.json", want: true},
		{name: "fits/a.b.pulse-sem.json", want: true},
		{name: "pulse-sem.json", want: false},
		{name: "brand.pulse-sem.json.7.tmp", want: false},
		{name: "a.b.c.pulse.spss.json", want: true},
		{name: "regions.json", want: false},
		{name: "spss.json", want: false},
		{name: "meta.json", want: false},
		{name: "indexes.json", want: false},
		{name: "country.json", want: false},
		{name: "cohort.pulse.9f3a.idx", want: false},
	}
	for _, tc := range cases {
		if got := isPulseSidecarName(tc.name); got != tc.want {
			t.Errorf("isPulseSidecarName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
