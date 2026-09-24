package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func runConvertLeaf(t *testing.T, out *bytes.Buffer, args ...string) error {
	t.Helper()
	root := ConvertCommand()
	if out != nil {
		root.Writer = out
	}
	return root.Run(context.Background(), append([]string{"convert"}, args...))
}

// TestConvertCLI_ErrorPathAfterRowsReleasesWithoutEmitting is the guard on
// the RESOURCE half of the io leaves' error return.
//
// The leaves deliberately skip writer.Close() when the job errors: every
// adapter emits its file exactly once inside Close, so skipping it writes
// nothing — the right outcome — while closing would put a zero-row target
// next to a hard error. That data decision stands. What was missing was
// releasing a writer that holds resources the GC cannot reclaim: io/excel
// drives an excelize StreamWriter whose buffer spills to an os.CreateTemp
// file past 16 MiB, and only excelize.File.Close removes those. The error
// return therefore runs pio.DiscardWriter — release, never emit.
//
// The case is reached here through convert's categorical-overflow refusal,
// which fires INSIDE the row loop: header written, rows written, workbook
// live. That is what makes the assertion bite — a Close on this path has a
// workbook to serialise and would leave out.xlsx behind.
func TestConvertCLI_ErrorPathAfterRowsReleasesWithoutEmitting(t *testing.T) {
	dir := t.TempDir()

	// 300 distinct categories against a categorical_u8 (256 max): the
	// dictionary fills mid-pass, well after WriteHeader and 256 rows.
	var b strings.Builder
	b.WriteString("answer\n")
	for i := range 300 {
		b.WriteString("v" + strconv.Itoa(i) + "\n")
	}
	csvPath := filepath.Join(dir, "wide.csv")
	if err := os.WriteFile(csvPath, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`[{"name":"answer","type":"categorical_u8"}]`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	out := filepath.Join(dir, "out.xlsx")

	var buf bytes.Buffer
	err := runConvertLeaf(t, &buf, "--schema", schemaPath, csvPath, out)
	if err == nil {
		t.Fatalf("convert returned nil over a categorical overflow; stdout = %q", buf.String())
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("out.xlsx was written on the error path; the release must never emit")
	}
}
