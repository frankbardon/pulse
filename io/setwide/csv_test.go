package setwide

import (
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pcsv "github.com/frankbardon/pulse/io/csv"
)

// TestCSV_WideSetRoundTrip: cohort -> .csv -> cohort, at 206 members.
//
// CSV has no null channel and no list channel, so all three states ride
// the text convention alone: labels joined by the delimiter, the bare
// delimiter for an empty selection, the empty string for a null. The
// wide part it carries for free — a label list does not know how many
// bits its members stand for — and "for free" is a claim, so it is
// asserted rather than assumed.
func TestCSV_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "csv", fs, src, pcsv.NewWriter(fs, "out.csv"))
	cells := cellsFrom(t, "csv", pcsv.NewReader(fs, "out.csv"))

	if cells[0] != selectionCell() {
		t.Errorf("csv: the selection exported as %q, want %q", cells[0], selectionCell())
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("csv: the empty selection exported as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("csv: the null exported as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("csv: the empty selection and the null both exported as %q — they are different answers", cells[1])
	}

	states := reimportThrough(t, "csv", fs, pcsv.NewReader(fs, "out.csv"), "rt-csv.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("csv: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("csv: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("csv: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("csv: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("csv: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-csv.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("csv: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if got := len(f.Dictionary.Values()); got != members {
		t.Errorf("csv: the round-tripped dictionary holds %d label(s), want %d", got, members)
	}
}
