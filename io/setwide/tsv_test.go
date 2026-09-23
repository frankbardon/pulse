package setwide

import (
	"strings"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	ptsv "github.com/frankbardon/pulse/io/tsv"
)

// TestTSV_WideSetRoundTrip: cohort -> .tsv -> cohort, at 206 members.
//
// TSV shares CSV's shape and not its escaping, which is the reason it
// gets its own test rather than a shared one: a set cell is the only
// cell in Pulse whose text routinely contains a delimiter character, and
// a tab-separated writer that mishandled the pipe-joined form would lose
// members from the middle of the list while the first and last survived.
func TestTSV_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "tsv", fs, src, ptsv.NewWriter(fs, "out.tsv"))
	cells := cellsFrom(t, "tsv", ptsv.NewReader(fs, "out.tsv"))

	if cells[0] != selectionCell() {
		t.Errorf("tsv: the selection exported as %q, want %q", cells[0], selectionCell())
	}
	if strings.Contains(cells[0], "\t") {
		t.Errorf("tsv: the set cell %q carries a tab, which the column separator cannot survive", cells[0])
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("tsv: the empty selection exported as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("tsv: the null exported as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("tsv: the empty selection and the null both exported as %q — they are different answers", cells[1])
	}

	states := reimportThrough(t, "tsv", fs, ptsv.NewReader(fs, "out.tsv"), "rt-tsv.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("tsv: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("tsv: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("tsv: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("tsv: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("tsv: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-tsv.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("tsv: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if got := len(f.Dictionary.Values()); got != members {
		t.Errorf("tsv: the round-tripped dictionary holds %d label(s), want %d", got, members)
	}
}
