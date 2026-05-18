package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
)

// runCohortCLI drives a fresh CohortCommand with the supplied args.
// Stdout is redirected to discard so tests assert side effects on disk
// rather than captured stdout — mirrors runImportCLI's pattern.
func runCohortCLI(t *testing.T, args ...string) error {
	t.Helper()
	root := CohortCommand()
	root.Writer = io.Discard
	return root.Run(context.Background(), append([]string{"cohort"}, args...))
}

// seedCohortViaImport rounds-trips a CSV through `pulse import auto` so
// the test cohort lives at PULSE_DATA_DIR/imports/<base>.pulse with the
// schema the importer chooses for the supplied data.
func seedCohortViaImport(t *testing.T, dir, csvName, csv string) string {
	t.Helper()
	csvPath := filepath.Join(dir, csvName)
	if err := os.WriteFile(csvPath, []byte(csv), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := runImportCLI(t, "auto", csvName); err != nil {
		t.Fatalf("import auto: %v", err)
	}
	base := csvName[:len(csvName)-len(".csv")]
	return filepath.Join(dir, "imports", base+".pulse")
}

func writeInclude(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write include: %v", err)
	}
	return p
}

// inspectRecordCount opens the cohort at the absolute path through a
// dedicated pulse.Pulse instance (rooted at the file's parent dir) and
// returns Cohort.RecordCount. We don't reuse descriptor.InspectFromBytes
// because the single-file inspect path doesn't populate RecordCount —
// the value is derived from total bytes / record size, which Cohort
// already computes.
func inspectRecordCount(t *testing.T, path string) int64 {
	t.Helper()
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	cohort, err := p.Open(context.Background(), base)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	n, err := cohort.RecordCount()
	if err != nil {
		t.Fatalf("RecordCount %s: %v", path, err)
	}
	return n
}

func TestCohortFilterCLI_IncludeOnly_Integer(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n101,10\n102,20\n103,30\n104,40\n105,50\n")
	inc := writeInclude(t, dir, "ids.txt", "102\n104\n")
	dst := filepath.Join(dir, "filtered.pulse")

	if err := runCohortCLI(t,
		"filter",
		"-i", src,
		"-o", dst,
		"--include-from", inc,
		"--include-field", "id",
	); err != nil {
		t.Fatalf("cohort filter: %v", err)
	}
	if n := inspectRecordCount(t, dst); n != 2 {
		t.Errorf("filtered row count = %d, want 2", n)
	}
}

func TestCohortFilterCLI_IncludePlusFilter_AND(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n101,10\n102,20\n103,30\n104,40\n105,50\n")
	inc := writeInclude(t, dir, "ids.txt", "102\n104\n")
	dst := filepath.Join(dir, "both.pulse")

	if err := runCohortCLI(t,
		"filter",
		"-i", src,
		"-o", dst,
		"--include-from", inc,
		"--include-field", "id",
		"--filter", "score > 25.0",
	); err != nil {
		t.Fatalf("cohort filter (combined): %v", err)
	}
	if n := inspectRecordCount(t, dst); n != 1 {
		t.Errorf("combined row count = %d, want 1 (id 104)", n)
	}
}

func TestCohortFilterCLI_FilterOnly_RegressionPath(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n101,10\n102,20\n103,30\n104,40\n105,50\n")
	dst := filepath.Join(dir, "filter_only.pulse")

	if err := runCohortCLI(t,
		"filter",
		"-i", src,
		"-o", dst,
		"--filter", "score > 25.0",
	); err != nil {
		t.Fatalf("cohort filter (filter-only): %v", err)
	}
	if n := inspectRecordCount(t, dst); n != 3 {
		t.Errorf("filter-only row count = %d, want 3", n)
	}
}

func TestCohortFilterCLI_RequiresAtLeastOnePredicate(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10\n2,20\n")
	dst := filepath.Join(dir, "nope.pulse")

	err := runCohortCLI(t, "filter", "-i", src, "-o", dst)
	if err == nil {
		t.Fatal("expected error when neither --filter nor --include-from supplied")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("dst written despite validation failure: %v", statErr)
	}
}

func TestCohortFilterCLI_IncludeFromWithoutField(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10\n2,20\n")
	inc := writeInclude(t, dir, "ids.txt", "1\n")
	dst := filepath.Join(dir, "nope.pulse")

	err := runCohortCLI(t, "filter",
		"-i", src,
		"-o", dst,
		"--include-from", inc,
	)
	if err == nil {
		t.Fatal("expected error when --include-from set without --include-field")
	}
}

func TestCohortFilterCLI_IncludeFieldWithoutFrom(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10\n2,20\n")
	dst := filepath.Join(dir, "nope.pulse")

	err := runCohortCLI(t, "filter",
		"-i", src,
		"-o", dst,
		"--include-field", "id",
	)
	if err == nil {
		t.Fatal("expected error when --include-field set without --include-from")
	}
}

func TestCohortFilterCLI_MissingIncludeFile(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10\n2,20\n")
	dst := filepath.Join(dir, "nope.pulse")

	err := runCohortCLI(t, "filter",
		"-i", src,
		"-o", dst,
		"--include-from", filepath.Join(dir, "does-not-exist.txt"),
		"--include-field", "id",
	)
	if err == nil {
		t.Fatal("expected error when --include-from path does not exist")
	}
}

func TestCohortFilterCLI_UnknownIncludeField(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10\n2,20\n")
	inc := writeInclude(t, dir, "ids.txt", "1\n")
	dst := filepath.Join(dir, "nope.pulse")

	err := runCohortCLI(t, "filter",
		"-i", src,
		"-o", dst,
		"--include-from", inc,
		"--include-field", "no_such_field",
	)
	if err == nil {
		t.Fatal("expected error when --include-field is not in cohort schema")
	}
}

func TestCohortFilterCLI_FloatFieldRejected(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10.5\n2,20.5\n3,30.5\n")
	inc := writeInclude(t, dir, "scores.txt", "10.5\n")
	dst := filepath.Join(dir, "nope.pulse")

	err := runCohortCLI(t, "filter",
		"-i", src,
		"-o", dst,
		"--include-from", inc,
		"--include-field", "score",
	)
	if err == nil {
		t.Fatal("expected error when --include-field is a float column")
	}
}

func TestCohortFilterCLI_EmptyIncludeFileWritesZeroRows(t *testing.T) {
	dir := withTempDataDir(t)
	src := seedCohortViaImport(t, dir, "ids.csv",
		"id,score\n1,10\n2,20\n3,30\n")
	inc := writeInclude(t, dir, "empty.txt", "")
	dst := filepath.Join(dir, "empty_out.pulse")

	if err := runCohortCLI(t, "filter",
		"-i", src,
		"-o", dst,
		"--include-from", inc,
		"--include-field", "id",
	); err != nil {
		t.Fatalf("cohort filter empty include: %v", err)
	}
	if n := inspectRecordCount(t, dst); n != 0 {
		t.Errorf("empty-include row count = %d, want 0", n)
	}
}
