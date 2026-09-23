package setwide

import (
	"bytes"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/spf13/afero"
)

// TestArrow_WideSetRoundTrip: cohort -> Arrow IPC -> cohort, at 206
// members.
//
// Arrow is one of the two adapters that was CAUGHT: it declared the
// column LIST<UTF8> and then handed the delimiter-joined token string to
// AppendValueFromString, which parses JSON. Every row failed, and the
// export returned a clean report carrying RowsExported 0 — at every
// rung, including the four that predate wide sets. So this test looks at
// the native buffers as well as the round trip: a list column's validity
// bit and its element count are where the three states actually live,
// and a text-level assertion cannot see either.
func TestArrow_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "arrow", fs, src, parrow.NewWriter(fs, "out.arrow"))

	// The native view.
	raw, err := afero.ReadFile(fs, "out.arrow")
	if err != nil {
		t.Fatalf("arrow: reading the emitted file: %v", err)
	}
	fr, err := ipc.NewFileReader(bytes.NewReader(raw), ipc.WithAllocator(memory.NewGoAllocator()))
	if err != nil {
		t.Fatalf("arrow: opening the emitted file: %v", err)
	}
	idx := fr.Schema().FieldIndices("sel")
	if len(idx) != 1 {
		t.Fatalf("arrow: the emitted schema has %d sel column(s), want 1", len(idx))
	}
	sel := fr.Schema().Field(idx[0])
	if sel.Type.ID() != arrow.LIST {
		t.Fatalf("arrow: sel is %s, want a list column", sel.Type)
	}
	rec, err := fr.RecordBatchAt(0)
	if err != nil {
		t.Fatalf("arrow: reading the first record batch: %v", err)
	}
	if got := int(rec.NumRows()); got != fixtureRecords {
		t.Fatalf("arrow: the batch holds %d row(s), want %d", got, fixtureRecords)
	}
	list, ok := rec.Column(idx[0]).(*array.List)
	if !ok {
		t.Fatalf("arrow: sel materialized as %T, want *array.List", rec.Column(idx[0]))
	}
	vals, ok := list.ListValues().(*array.String)
	if !ok {
		t.Fatalf("arrow: sel's elements are %T, want *array.String", list.ListValues())
	}
	if list.IsNull(0) {
		t.Errorf("arrow: the selection is natively null")
	}
	got := make(map[string]bool)
	for i := list.Offsets()[0]; i < list.Offsets()[1]; i++ {
		got[vals.Value(int(i))] = true
	}
	if len(got) != len(selectedBits) {
		t.Errorf("arrow: the selection list holds %d element(s), want %d", len(got), len(selectedBits))
	}
	for _, b := range selectedBits {
		if !got[memberName(b)] {
			t.Errorf("arrow: the selection list omits %q (bit %d) — a low-word-only mask stops before it",
				memberName(b), b)
		}
	}
	for _, b := range clearBits {
		if got[memberName(b)] {
			t.Errorf("arrow: the selection list names %q (bit %d), which was never selected", memberName(b), b)
		}
	}
	if list.IsNull(1) {
		t.Errorf("arrow: the empty selection is natively null — a present answer became a missing one")
	}
	if n := list.Offsets()[2] - list.Offsets()[1]; n != 0 {
		t.Errorf("arrow: the empty selection holds %d element(s), want 0", n)
	}
	if !list.IsNull(2) {
		t.Errorf("arrow: the null is not natively null")
	}

	cells := cellsFrom(t, "arrow", parrow.NewReader(fs, "out.arrow"))

	if cells[0] != selectionCell() {
		t.Errorf("arrow: the selection read back as %q, want %q", cells[0], selectionCell())
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("arrow: the empty selection read back as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("arrow: the null read back as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("arrow: the empty selection and the null both read back as %q", cells[1])
	}

	states := reimportThrough(t, "arrow", fs, parrow.NewReader(fs, "out.arrow"), "rt-arrow.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("arrow: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("arrow: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("arrow: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("arrow: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("arrow: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-arrow.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("arrow: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if n := len(f.Dictionary.Values()); n != members {
		t.Errorf("arrow: the round-tripped dictionary holds %d label(s), want %d", n, members)
	}
}
