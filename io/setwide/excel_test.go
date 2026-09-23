package setwide

import (
	"context"
	"strings"
	"testing"

	pfs "github.com/frankbardon/pulse/fs"
	pio "github.com/frankbardon/pulse/io"
	pexcel "github.com/frankbardon/pulse/io/excel"
)

// TestExcel_WideSetRoundTrip: cohort -> .xlsx -> cohort, at 206 members.
//
// Excel is flat text like CSV, but it is the one flat target whose cells
// are addressed rather than positional: an empty cell is not written at
// all, and a row's used range stops at its last non-empty cell. The null
// record is deliberately the LAST row and the set column deliberately
// the LAST column, so a reader that lets a trailing empty cell shorten
// its row loses the null state here rather than somewhere quieter.
//
// The width claim is the same as everywhere else and it is checked the
// same way, because "Excel carries a label list, so it carries a wide
// set" is precisely the kind of inheritance-by-resemblance this package
// exists to stop taking on trust.
func TestExcel_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "excel", fs, src, pexcel.NewWriter(fs, "out.xlsx"))
	cells := cellsFrom(t, "excel", pexcel.NewReader(fs, "out.xlsx"))

	if cells[0] != selectionCell() {
		t.Errorf("excel: the selection exported as %q, want %q", cells[0], selectionCell())
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("excel: the empty selection exported as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("excel: the null exported as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("excel: the empty selection and the null both exported as %q — they are different answers", cells[1])
	}

	states := reimportThrough(t, "excel", fs, pexcel.NewReader(fs, "out.xlsx"), "rt-excel.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("excel: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("excel: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("excel: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("excel: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("excel: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-excel.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("excel: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if n := len(f.Dictionary.Values()); n != members {
		t.Errorf("excel: the round-tripped dictionary holds %d label(s), want %d", n, members)
	}
}

// TestExcel_EverySelectedMemberSurvives is the width probe the story
// singles Excel out for: a record selecting ALL 206 members.
//
// A set cell is the only Pulse cell whose length grows with the
// dictionary, and Excel's is the only target with a hard per-cell
// character limit. 206 labels is about a kilobyte — far inside the
// limit — but "far inside" is a measurement nobody had made, and the
// failure it would have hidden is a truncated label list that still
// imports cleanly, one member short.
func TestExcel_EverySelectedMemberSurvives(t *testing.T) {
	all := make([]string, 0, members)
	for i := 0; i < members; i++ {
		all = append(all, memberName(i))
	}
	full := strings.Join(all, pio.DefaultSetDelimiter)

	fs := pfs.NewMemMap().Fs()
	job := pio.NewImportJob(&mockReader{columns: []string{"id", "sel"}, rows: [][]string{{"1", full}}}, "full.pulse")
	job.FS = fs
	job.Schema = wideSchema(t)
	if rep, err := job.Run(context.Background()); err != nil {
		t.Fatalf("excel: building the all-members cohort: %v", err)
	} else if rep.RowsImported != 1 {
		t.Fatalf("excel: the all-members cohort imported %d row(s), want 1 (errors %v)",
			rep.RowsImported, rep.RowErrors)
	}

	w := pexcel.NewWriter(fs, "full.xlsx")
	xjob := pio.NewExportJob("full.pulse", w)
	xjob.FS = fs
	rep, err := xjob.Run(context.Background())
	if err != nil {
		t.Fatalf("excel: exporting the all-members cohort: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("excel: writer Close: %v", err)
	}
	if rep.RowsExported != 1 {
		t.Fatalf("excel: exported %d row(s), want 1 (errors %v)", rep.RowsExported, rep.RowErrors)
	}

	rd := pexcel.NewReader(fs, "full.xlsx")
	if _, err := rd.ReadHeader(); err != nil {
		t.Fatalf("excel: ReadHeader: %v", err)
	}
	var cell string
	var seen int
	if err := rd.ReadRows(context.Background(), func(row []string) error {
		seen++
		cell = row[1]
		return nil
	}); err != nil {
		t.Fatalf("excel: ReadRows: %v", err)
	}
	if err := rd.Close(); err != nil {
		t.Fatalf("excel: reader Close: %v", err)
	}
	if seen != 1 {
		t.Fatalf("excel: read back %d row(s), want 1", seen)
	}
	if cell != full {
		t.Fatalf("excel: the 206-member selection came back as %d character(s), want %d — the list was truncated",
			len(cell), len(full))
	}

	job2 := pio.NewImportJob(pexcel.NewReader(fs, "full.xlsx"), "rt-full.pulse")
	job2.FS = fs
	job2.Schema = wideSchema(t)
	if _, err := job2.Run(context.Background()); err != nil {
		t.Fatalf("excel: re-importing the all-members cohort: %v", err)
	}
	states := readStates(t, fs, "rt-full.pulse", "sel")
	if len(states) != 1 {
		t.Fatalf("excel: the round-tripped cohort holds %d record(s), want 1", len(states))
	}
	if states[0].Null || states[0].Mask.PopCount() != members {
		t.Fatalf("excel: the all-members selection came back with %d member(s), want %d (%s)",
			states[0].Mask.PopCount(), members, states[0])
	}
	for i := 0; i < members; i++ {
		if !states[0].Mask.Has(i) {
			t.Fatalf("excel: bit %d is clear after the round trip of an all-members selection", i)
		}
	}
}
