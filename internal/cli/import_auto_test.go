package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
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

// writeJoinCSVFile writes a synthetic denormalized orders⋈customers CSV:
// each customer's two f64 coordinates repeat on every one of its orders.
func writeJoinCSVFile(t *testing.T, dir, name string, rows, customers int) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("order_id,cust_id,cust_lat,cust_lon,amount\n")
	for i := 0; i < rows; i++ {
		c := i % customers
		fmt.Fprintf(&b, "%d,%d,%.6f,%.6f,%.2f\n", 1000+i, c+1, 10.123457+float64(c)*1.5, -70.654321-float64(c)*0.75, float64(i%37)+0.25)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestImportAutoCLI_GroupFlag: --group reaches the managed cohort (0x02),
// in the per-format leaves' own KEY:MEMBER syntax.
func TestImportAutoCLI_GroupFlag(t *testing.T) {
	dir := withTempDataDir(t)
	writeJoinCSVFile(t, dir, "orders.csv", 200, 10)
	if err := runImportCLI(t, "auto", "--group", "cust_id:cust_lat,cust_lon", "orders.csv"); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "imports", "orders.pulse"))
	if err != nil {
		t.Fatalf("read cohort: %v", err)
	}
	if b[8] != 0x02 {
		t.Errorf("version byte = 0x%02x, want 0x02 with --group", b[8])
	}
}

// TestImportAutoCLI_GroupWarningInEnvelope: a gate finding lands in the
// --json envelope's warnings array as a coded entry.
func TestImportAutoCLI_GroupWarningInEnvelope(t *testing.T) {
	dir := withTempDataDir(t)
	writeJoinCSVFile(t, dir, "orders.csv", 20, 12)
	var out bytes.Buffer
	root := ImportCommand()
	root.Writer = &out
	if err := root.Run(context.Background(), []string{"import", "auto", "--json", "--group", "cust_id:cust_lat,cust_lon", "orders.csv"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	var env struct {
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
		Data struct {
			Groups []map[string]any `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	if len(env.Warnings) != 1 || env.Warnings[0].Code != string(perrors.PULSE_DEDUP_LOW_RATIO) || env.Warnings[0].Details == nil {
		t.Errorf("warnings = %+v, want one coded PULSE_DEDUP_LOW_RATIO", env.Warnings)
	}
	if len(env.Data.Groups) != 1 {
		t.Errorf("data.groups = %v, want one group report", env.Data.Groups)
	}
}

// TestImportAutoCLI_BadGroupFlagIsCoded: a malformed --group fails with
// the declaration code and imports nothing.
func TestImportAutoCLI_BadGroupFlagIsCoded(t *testing.T) {
	dir := withTempDataDir(t)
	writeJoinCSVFile(t, dir, "orders.csv", 200, 10)
	err := runImportCLI(t, "auto", "--group", "a:b:c", "orders.csv")
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_GROUP_DECLARATION_INVALID {
		t.Fatalf("err = %v, want PULSE_GROUP_DECLARATION_INVALID", err)
	}
	// The parser's own refusal, naming the flag value — not a later
	// encoder complaint about an empty group the bad value decayed into.
	if ce.Details["declaration"] != "a:b:c" {
		t.Errorf("details = %v, want the parser's declaration echo", ce.Details)
	}
	if _, serr := os.Stat(filepath.Join(dir, "imports", "orders.pulse")); !os.IsNotExist(serr) {
		t.Errorf("a malformed --group still imported")
	}
}
