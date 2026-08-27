package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// withTempDataDir gives each test a hermetic PULSE_DATA_DIR rooted at
// a t.TempDir(). The pulse.New constructed in the CLI commands reads
// the env var; restoring it after the test is t.Setenv's job.
func withTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Setenv("PULSE_IMPORTS_DIR", "imports")
	t.Setenv("PULSE_IMPORT_TTL", "7d")
	return dir
}

func writeCSVFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// runImportCLI drives a fresh ImportCommand with the supplied args.
// stdout is redirected to a discard writer so the test doesn't pollute
// `go test -v` output; the test asserts side effects on disk rather
// than captured stdout to stay robust against cli/v3 Writer wiring
// changes.
func runImportCLI(t *testing.T, args ...string) error {
	t.Helper()
	root := ImportCommand()
	root.Writer = io.Discard
	return root.Run(context.Background(), append([]string{"import"}, args...))
}

func TestImportAutoCLI_CSVImport(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse")); err != nil {
		t.Errorf("imports/data.pulse missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse.meta.json")); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}
}

func TestImportAutoCLI_PulsePassthrough(t *testing.T) {
	dir := withTempDataDir(t)
	if err := os.WriteFile(filepath.Join(dir, "curated.pulse"), []byte("PULSE\x00\x00\x00\x01"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := runImportCLI(t, "auto", "curated.pulse"); err != nil {
		t.Fatalf("run: %v", err)
	}
	// No sidecar should exist for passthrough.
	if _, err := os.Stat(filepath.Join(dir, "imports", "curated.pulse.meta.json")); !os.IsNotExist(err) {
		t.Errorf("sidecar created for passthrough; err=%v", err)
	}
}

func TestImportListAndDropCLI(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv"); err != nil {
		t.Fatalf("auto: %v", err)
	}

	// List should not error and should not change disk state.
	if err := runImportCLI(t, "list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse")); err != nil {
		t.Errorf("list removed file: %v", err)
	}

	if err := runImportCLI(t, "drop", "data"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "data.pulse")); !os.IsNotExist(err) {
		t.Errorf("managed file present after drop: err=%v", err)
	}
}

func TestImportAutoCLI_OverwriteRequired(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv"); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second call without --overwrite must fail.
	if err := runImportCLI(t, "auto", "data.csv"); err == nil {
		t.Errorf("collision second call accepted; expected error")
	}
	if err := runImportCLI(t, "auto", "--overwrite", "data.csv"); err != nil {
		t.Errorf("overwrite call failed: %v", err)
	}
}

// writeUndeclaredCharsetSav writes a `.sav` that declares NO character
// encoding — no record 7/20 name, no record 7/3 code — whose single text
// datum carries the windows-1252 byte for "ü". The reader's default is
// strict UTF-8 (deliberately, so a pre-Unicode file fails loudly rather than
// importing mojibake), so the byte is undecodable and the file has no
// further evidence to offer.
func writeUndeclaredCharsetSav(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := spsstest.Build(spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "CITY", Width: 6}},
		Cases: [][]spsstest.Value{{spsstest.Text("Zurich")}},
	})
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	at := bytes.Index(raw, []byte("Zurich"))
	if at < 0 {
		t.Fatal("the fixture does not hold the datum")
	}
	raw[at+1] = 0xFC
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestImportAutoCLI_CharsetRescuesUndeclaredSav is the E6-S3 acceptance
// criterion driven through the real command tree. Before the flag existed
// this file could not be imported into the managed pool at all: the override
// lived on `pulse import spss` and on spss.WithCharset, and `import auto`
// reached neither.
func TestImportAutoCLI_CharsetRescuesUndeclaredSav(t *testing.T) {
	dir := withTempDataDir(t)
	writeUndeclaredCharsetSav(t, dir, "legacy.sav")

	// Without the flag the import must fail, and fail with the code that
	// names the problem — a bare error would also pass against a reader
	// that had started substituting U+FFFD.
	err := runImportCLI(t, "auto", "legacy.sav")
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("error = %v, want %s", err, perrors.PULSE_SPSS_CHARSET_INVALID)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "legacy.pulse")); !os.IsNotExist(err) {
		t.Errorf("a cohort was written despite the failure; err=%v", err)
	}

	// With it, the same file imports.
	if err := runImportCLI(t, "auto", "legacy.sav", "--charset", "windows-1252"); err != nil {
		t.Fatalf("run with --charset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports", "legacy.pulse")); err != nil {
		t.Errorf("imports/legacy.pulse missing: %v", err)
	}
}

// TestImportAutoCLI_CharsetInertForCSV. The flag rides the shared
// format.ReaderOptions struct exactly as --sheet does, so a format with no
// opinion about codepages must ignore it rather than reject it — and produce
// the same cohort bytes it would have produced without it.
func TestImportAutoCLI_CharsetInertForCSV(t *testing.T) {
	dir := withTempDataDir(t)
	_ = writeCSVFile(t, dir, "data.csv")

	if err := runImportCLI(t, "auto", "data.csv", "--handle", "plain"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := runImportCLI(t, "auto", "data.csv", "--handle", "withcharset",
		"--charset", "windows-1252"); err != nil {
		t.Fatalf("run with --charset: %v — the flag must be inert for CSV, not rejected", err)
	}
	a, err := os.ReadFile(filepath.Join(dir, "imports", "plain.pulse"))
	if err != nil {
		t.Fatalf("read plain.pulse: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "imports", "withcharset.pulse"))
	if err != nil {
		t.Fatalf("read withcharset.pulse: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("the CSV cohort differs with --charset set; the flag is not inert for non-SPSS formats")
	}
}
