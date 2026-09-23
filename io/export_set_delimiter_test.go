package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// TestExport_SetDelimiterIsCanonicalNotPerColumn is the VERIFICATION of a
// reported defect that turned out to be the contract.
//
// The report: a set column imported with a non-default delimiter is
// exported joined with DefaultSetDelimiter, so a re-import driven by
// ImportJob.SetDelimiters mis-splits the cell.
//
// The behaviour is real and this test pins it — but it is correct, and
// "export with the column's own delimiter" is not a fix available to take:
//
//  1. There is nowhere to read the delimiter FROM. ImportJob.SetDelimiters
//     is an inference-steering knob for the SOURCE text; the `.pulse`
//     format persists no delimiter (grep encoding/ — there is none), and
//     ExportJob has no such slot. The cohort holds a dictionary and a
//     bitmask, not a spelling.
//  2. EmptySetCell IS DefaultSetDelimiter. A bare "|" is how every adapter
//     spells the present-but-empty selection. Join a selection with ";"
//     while the empty marker stays "|" and the marker stops being a
//     delimiter — it re-imports as a one-token selection of a member
//     literally named "|", collapsing the tri-state this PR's
//     io/settristate and io/setwide suites exist to protect.
//  3. The readers agree on "|" unconditionally: io/arrow splits on
//     pio.DefaultSetDelimiter, io/jsonshared pins SetArrayDelimiter, and
//     io/spss records setElementDelimiter as "not a choice".
//
// So "|" is the canonical EXTERNAL form at every rung and every format,
// and the re-import that mis-splits is one told the wrong thing about a
// Pulse-produced file. Inference on such a file picks "|" and round-trips.
func TestExport_SetDelimiterIsCanonicalNotPerColumn(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Source cells use ";" — deliberately NOT the default.
	rows := [][]string{
		{"1", "red;blue"},
		{"2", EmptySetCell}, // present, nothing selected
		{"3", ""},           // null
	}
	source := newMockReader([]string{"id", "colours"}, rows)

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "colours", Type: encoding.FieldTypeSetU8, CsvColumnIdx: 1,
			Nullable: true, Dictionary: encoding.NewDictionary()},
	}}

	ij := NewImportJob(source, "sets.pulse")
	ij.FS = fs
	ij.Schema = schema
	ij.SetDelimiters = map[string]string{"colours": ";"}
	if _, err := ij.Run(context.Background()); err != nil {
		t.Fatalf("import: %v", err)
	}

	target := &collectWriter{}
	ej := NewExportJob("sets.pulse", target)
	ej.FS = fs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatalf("export: %v", err)
	}

	if len(target.rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(target.rows))
	}
	want := []string{"red|blue", EmptySetCell, ""}
	for i, w := range want {
		if got := exportedCellText(target.rows[i][1]); got != w {
			t.Errorf("row %d colours = %q, want %q", i, got, w)
		}
	}

	// The tri-state must stay three distinct spellings. If a future
	// change joins with the source delimiter, the selection becomes
	// "red;blue" and this still holds — but the assertions above fail
	// first, which is the point: the canonical form is the contract.
	if want[0] == want[1] || want[1] == want[2] || want[0] == want[2] {
		t.Fatal("tri-state spellings collided")
	}
}
