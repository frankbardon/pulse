package setwide

import (
	"bytes"
	"context"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	pio "github.com/frankbardon/pulse/io"
	pparquet "github.com/frankbardon/pulse/io/parquet"
	"github.com/spf13/afero"
)

// TestParquet_WideSetRoundTrip: cohort -> .parquet -> cohort, at 206
// members.
//
// Parquet is the second adapter that was caught, with the identical
// hole: appendCell had no IsSet arm, so the delimiter-joined token
// string reached AppendValueFromString on a LIST<UTF8> builder, every
// row failed to parse as JSON, and the export reported success having
// written zero rows — at every rung, narrow ones included. The set arm
// landed in E3-S6; this test is the standing guard that it cannot be
// removed silently.
//
// Like the Arrow test it reads the emitted file natively, because the
// zero-rows failure produced a structurally valid Parquet file: a
// text-level round trip over an empty table has nothing to disagree
// with. The row count is therefore asserted twice, once at the export
// report and once at the materialized table.
func TestParquet_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "parquet", fs, src, pparquet.NewWriter(fs, "out.parquet"))

	// The native view.
	raw, err := afero.ReadFile(fs, "out.parquet")
	if err != nil {
		t.Fatalf("parquet: reading the emitted file: %v", err)
	}
	pf, err := file.NewParquetReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parquet: opening the emitted file: %v", err)
	}
	ar, err := pqarrow.NewFileReader(pf, pqarrow.ArrowReadProperties{}, memory.NewGoAllocator())
	if err != nil {
		t.Fatalf("parquet: creating the arrow reader: %v", err)
	}
	tbl, err := ar.ReadTable(context.Background())
	if err != nil {
		t.Fatalf("parquet: reading the table: %v", err)
	}
	if got := int(tbl.NumRows()); got != fixtureRecords {
		t.Fatalf("parquet: the emitted table holds %d row(s), want %d — a set export that writes zero rows is the failure this guards",
			got, fixtureRecords)
	}
	idx := tbl.Schema().FieldIndices("sel")
	if len(idx) != 1 {
		t.Fatalf("parquet: the emitted schema has %d sel column(s), want 1", len(idx))
	}
	if id := tbl.Schema().Field(idx[0]).Type.ID(); id != arrow.LIST {
		t.Fatalf("parquet: sel is %s, want a list column", tbl.Schema().Field(idx[0]).Type)
	}
	// The column may arrive as several chunks (one per row group), so the
	// rows are flattened before they are indexed — the state of row 2 is
	// not the state of chunk 2.
	type nativeRow struct {
		null  bool
		elems []string
	}
	var rows []nativeRow
	for c, chunk := range tbl.Column(idx[0]).Data().Chunks() {
		list, ok := chunk.(*array.List)
		if !ok {
			t.Fatalf("parquet: sel chunk %d materialized as %T, want *array.List", c, chunk)
		}
		vals, ok := list.ListValues().(*array.String)
		if !ok {
			t.Fatalf("parquet: sel's elements are %T, want *array.String", list.ListValues())
		}
		offs := list.Offsets()
		for i := 0; i < list.Len(); i++ {
			r := nativeRow{null: list.IsNull(i)}
			for j := offs[i]; j < offs[i+1]; j++ {
				r.elems = append(r.elems, vals.Value(int(j)))
			}
			rows = append(rows, r)
		}
	}
	if len(rows) != fixtureRecords {
		t.Fatalf("parquet: sel holds %d row(s) across its chunks, want %d", len(rows), fixtureRecords)
	}

	if rows[0].null {
		t.Errorf("parquet: the selection is natively null")
	}
	got := make(map[string]bool, len(rows[0].elems))
	for _, e := range rows[0].elems {
		got[e] = true
	}
	if len(got) != len(selectedBits) {
		t.Errorf("parquet: the selection list holds %d element(s), want %d", len(got), len(selectedBits))
	}
	for _, b := range selectedBits {
		if !got[memberName(b)] {
			t.Errorf("parquet: the selection list omits %q (bit %d) — a low-word-only mask stops before it",
				memberName(b), b)
		}
	}
	for _, b := range clearBits {
		if got[memberName(b)] {
			t.Errorf("parquet: the selection list names %q (bit %d), which was never selected", memberName(b), b)
		}
	}
	if rows[1].null {
		t.Errorf("parquet: the empty selection is natively null — a present answer became a missing one")
	}
	if n := len(rows[1].elems); n != 0 {
		t.Errorf("parquet: the empty selection holds %d element(s), want 0", n)
	}
	if !rows[2].null {
		t.Errorf("parquet: the null is not natively null")
	}

	cells := cellsFrom(t, "parquet", pparquet.NewReader(fs, "out.parquet"))

	if cells[0] != selectionCell() {
		t.Errorf("parquet: the selection read back as %q, want %q", cells[0], selectionCell())
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("parquet: the empty selection read back as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("parquet: the null read back as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("parquet: the empty selection and the null both read back as %q", cells[1])
	}

	states := reimportThrough(t, "parquet", fs, pparquet.NewReader(fs, "out.parquet"), "rt-parquet.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("parquet: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("parquet: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("parquet: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("parquet: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("parquet: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-parquet.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("parquet: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if n := len(f.Dictionary.Values()); n != members {
		t.Errorf("parquet: the round-tripped dictionary holds %d label(s), want %d", n, members)
	}
}

// TestParquet_NarrowSetRoundTripsToo is the narrow half of this story's
// added scope. The zero-rows failure was NOT width-specific — it hit
// set_u8 exactly as hard as set_u256 — so verifying the fix only at the
// wide rung would leave the case that has been broken longest unproven.
func TestParquet_NarrowSetRoundTripsToo(t *testing.T) {
	fs, src, want := narrowSetCohort(t)

	exportThrough(t, "parquet/narrow", fs, src, pparquet.NewWriter(fs, "narrow.parquet"))
	cells := cellsFrom(t, "parquet/narrow", pparquet.NewReader(fs, "narrow.parquet"))

	if cells[0] != narrowSelectionCell {
		t.Errorf("parquet/narrow: the selection exported as %q, want %q", cells[0], narrowSelectionCell)
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("parquet/narrow: the empty selection exported as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("parquet/narrow: the null exported as %q, want the empty string", cells[2])
	}

	states := reimportNarrow(t, "parquet/narrow", fs, pparquet.NewReader(fs, "narrow.parquet"), "rt-narrow.pulse")
	if states[0].Null || !states[0].Mask.Equal(want) {
		t.Errorf("parquet/narrow: the selection came back as %s, want %s", states[0], bitsOf(want))
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("parquet/narrow: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("parquet/narrow: the null came back as %s, want null", states[2])
	}
}
