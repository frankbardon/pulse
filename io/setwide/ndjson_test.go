package setwide

import (
	"strings"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pndjson "github.com/frankbardon/pulse/io/ndjson"
	"github.com/spf13/afero"
)

// TestNDJSON_WideSetRoundTrip: cohort -> .ndjson -> cohort, at 206
// members.
//
// NDJSON is the one adapter whose Reader derives its column names from a
// DATA record — the first object in the file — so the record that
// carries the header is the record most easily lost. It was, until
// E3-S8: ReadHeader consumed the first object and ReadRows resumed after
// it, so a 60-line file imported 59 and io/settristate had to prepend a
// filler record to keep its three states addressable.
//
// The fixture here carries NO filler. Its first record is the wide
// selection, which is also the record ReadHeader reads the column names
// from, so the count assertion in cellsFrom and the first-cell assertion
// below are together the regression test for that fix: if the header
// record is dropped again, the selection is what goes missing.
func TestNDJSON_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "ndjson", fs, src, pndjson.NewWriter(fs, "out.ndjson"))

	// The document itself, before any Reader interprets it: JSON has a
	// native null and NDJSON uses it, so the two non-selection states
	// are distinguishable on the wire and not only after a decode.
	raw, err := afero.ReadFile(fs, "out.ndjson")
	if err != nil {
		t.Fatalf("ndjson: reading the emitted document: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != fixtureRecords {
		t.Fatalf("ndjson: the document has %d line(s), want %d", len(lines), fixtureRecords)
	}
	for _, b := range selectedBits {
		if !strings.Contains(lines[0], memberName(b)) {
			t.Errorf("ndjson: the first object does not name member %q (bit %d): %s",
				memberName(b), b, lines[0])
		}
	}
	if !strings.Contains(lines[2], `"sel":null`) {
		t.Errorf("ndjson: the null record is %s, want a JSON null for sel", lines[2])
	}
	if strings.Contains(lines[1], `"sel":null`) {
		t.Errorf("ndjson: the empty selection was emitted as a JSON null: %s", lines[1])
	}

	cells := cellsFrom(t, "ndjson", pndjson.NewReader(fs, "out.ndjson"))

	if cells[0] != selectionCell() {
		t.Errorf("ndjson: the selection read back as %q, want %q — the header record must survive",
			cells[0], selectionCell())
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("ndjson: the empty selection read back as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("ndjson: the null read back as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("ndjson: the empty selection and the null both read back as %q", cells[1])
	}

	states := reimportThrough(t, "ndjson", fs, pndjson.NewReader(fs, "out.ndjson"), "rt-ndjson.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("ndjson: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("ndjson: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("ndjson: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("ndjson: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("ndjson: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-ndjson.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("ndjson: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if got := len(f.Dictionary.Values()); got != members {
		t.Errorf("ndjson: the round-tripped dictionary holds %d label(s), want %d", got, members)
	}
}
