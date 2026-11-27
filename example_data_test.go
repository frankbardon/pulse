package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// guideExamples are the Analysis Guide's worked examples
// (compare_groups, change_over_time): both must run on the written
// directory with nothing else on disk.
var guideExamples = []string{"t_test_two_sample", "paired_t_before_after"}

// writeExampleData writes the fixtures to /data on a fresh in-memory
// filesystem and returns the filesystem and the report.
func writeExampleData(t *testing.T) (afero.Fs, *pulse.ExampleDataReport) {
	t.Helper()
	mem := fs.NewMemMap().Fs()
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := p.WriteExampleData(context.Background(), "/data", pulse.ExampleDataOptions{})
	if err != nil {
		t.Fatalf("WriteExampleData: %v", err)
	}
	return mem, rep
}

// TestWriteExampleData_WritesEveryFixtureCohort: one cohort per embedded
// fixture plus the shard archive and the synthetic twin, each a
// readable cohort with records, plus the index, its manifest and the
// twin's profile; no step warns on the shipped fixtures.
func TestWriteExampleData_WritesEveryFixtureCohort(t *testing.T) {
	t.Parallel()
	mem, rep := writeExampleData(t)
	want := []string{}
	for _, s := range examples.FixtureCohorts() {
		want = append(want, s+".pulse")
	}
	want = append(want, "customers_twin.pulse", "transactions_sharded.pulse")
	slices.Sort(want)
	var got []string
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Cohorts {
		got = append(got, c.File)
		if c.Records == 0 || c.Path != filepath.Join("/data", c.File) {
			t.Errorf("%s: records %d path %q", c.File, c.Records, c.Path)
		}
		n, err := p.CountRecords(context.Background(), c.Path)
		if err != nil || n != uint64(c.Records) {
			t.Errorf("%s: CountRecords = %d, %v; report says %d", c.File, n, err, c.Records)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cohorts written:\n got %v\nwant %v", got, want)
	}
	if !slices.Contains(rep.Files, "/data/customers.profile.json") || len(rep.Files) < 3 {
		t.Errorf("files = %v; want the index, its manifest and the profile", rep.Files)
	}
	for _, f := range rep.Files {
		if _, err := mem.Stat(f); err != nil {
			t.Errorf("reported file %s: %v", f, err)
		}
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("warnings on the shipped fixtures: %v", rep.Warnings)
	}
	// Nothing staged lands beside the cohorts.
	entries, _ := afero.ReadDir(mem, "/data")
	for _, e := range entries {
		name := e.Name()
		if !slices.Contains(want, name) && !slices.Contains(rep.Files, filepath.Join("/data", name)) {
			t.Errorf("unreported file in the directory: %s", name)
		}
	}
}

// TestWriteExampleData_GuideExamplesRun runs the guide's worked
// examples, as served, on an instance rooted at the written directory.
func TestWriteExampleData_GuideExamplesRun(t *testing.T) {
	t.Parallel()
	mem, _ := writeExampleData(t)
	// Rooted at the directory, as PULSE_DATA_DIR roots `pulse mcp`
	// (Options.DataDir is ignored beside Options.FS).
	p, err := pulse.New(pulse.Options{FS: afero.NewBasePathFs(mem, "/data")})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range guideExamples {
		ex, ok := p.ExampleGet(name)
		if !ok {
			t.Fatalf("example %s not served", name)
		}
		var req pulse.Request
		if err := json.Unmarshal(ex.Body, &req); err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(context.Background(), &req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(resp.Tests) == 0 || resp.Tests[0].Error != nil {
			t.Fatalf("%s: tests = %+v", name, resp.Tests)
		}
	}
}

// TestWriteExampleData_RefusesExistingUnlessOverwrite: a second write
// into the same directory is refused before anything is touched, naming
// the files found; Overwrite replaces them.
func TestWriteExampleData_RefusesExistingUnlessOverwrite(t *testing.T) {
	t.Parallel()
	mem := fs.NewMemMap().Fs()
	p, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(mem, "/data/experiment.pulse", []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = p.WriteExampleData(context.Background(), "/data", pulse.ExampleDataOptions{})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "target_exists" {
		t.Fatalf("err = %v; want SERVICE_VALIDATION target_exists", err)
	}
	if paths, _ := ce.Details["paths"].([]string); !slices.Equal(paths, []string{"/data/experiment.pulse"}) {
		t.Errorf("details.paths = %v", ce.Details["paths"])
	}
	if b, _ := afero.ReadFile(mem, "/data/experiment.pulse"); string(b) != "keep" {
		t.Error("refused call changed the existing file")
	}
	if entries, _ := afero.ReadDir(mem, "/data"); len(entries) != 1 {
		t.Errorf("refused call wrote %d entries", len(entries)-1)
	}
	rep, err := p.WriteExampleData(context.Background(), "/data", pulse.ExampleDataOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if b, _ := afero.ReadFile(mem, "/data/experiment.pulse"); string(b) == "keep" || len(rep.Cohorts) == 0 {
		t.Error("overwrite left the old file")
	}
	if _, err := p.WriteExampleData(context.Background(), "/data", pulse.ExampleDataOptions{Overwrite: true}); err != nil {
		t.Fatalf("second overwrite (archive and twin present): %v", err)
	}
}
