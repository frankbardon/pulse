package setwide

import (
	"encoding/json"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pjsonarray "github.com/frankbardon/pulse/io/jsonarray"
	"github.com/spf13/afero"
)

// TestJSONArray_WideSetRoundTrip: cohort -> .json -> cohort, at 206
// members.
//
// jsonarray buffers the whole document and emits one array of objects,
// where ndjson streams one object per line. The set cell's spelling is
// shared, the surrounding document is not, so the null state is asserted
// against the PARSED document here: a JSON null and the string "" are
// two different values that a `%v` formatter renders identically, and
// only a decode can tell them apart.
func TestJSONArray_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "jsonarray", fs, src, pjsonarray.NewWriter(fs, "out.json"))

	raw, err := afero.ReadFile(fs, "out.json")
	if err != nil {
		t.Fatalf("jsonarray: reading the emitted document: %v", err)
	}
	var doc []map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("jsonarray: the emitted document does not parse: %v", err)
	}
	if len(doc) != fixtureRecords {
		t.Fatalf("jsonarray: the document holds %d object(s), want %d", len(doc), fixtureRecords)
	}
	if got, ok := doc[0]["sel"].(string); !ok || got != selectionCell() {
		t.Errorf("jsonarray: the selection is %#v, want the string %q", doc[0]["sel"], selectionCell())
	}
	if doc[2]["sel"] != nil {
		t.Errorf("jsonarray: the null is %#v, want a JSON null", doc[2]["sel"])
	}
	if doc[1]["sel"] == nil {
		t.Errorf("jsonarray: the empty selection was emitted as a JSON null, which is the missing answer")
	}

	cells := cellsFrom(t, "jsonarray", pjsonarray.NewReader(fs, "out.json"))

	if cells[0] != selectionCell() {
		t.Errorf("jsonarray: the selection read back as %q, want %q", cells[0], selectionCell())
	}
	if cells[1] != pio.EmptySetCell {
		t.Errorf("jsonarray: the empty selection read back as %q, want %q", cells[1], pio.EmptySetCell)
	}
	if cells[2] != "" {
		t.Errorf("jsonarray: the null read back as %q, want the empty string", cells[2])
	}
	if cells[1] == cells[2] {
		t.Errorf("jsonarray: the empty selection and the null both read back as %q", cells[1])
	}

	states := reimportThrough(t, "jsonarray", fs, pjsonarray.NewReader(fs, "out.json"), "rt-jsonarray.pulse")

	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("jsonarray: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("jsonarray: bit %d is clear after the round trip, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("jsonarray: bit %d is set after the round trip, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("jsonarray: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("jsonarray: the null came back as %s, want null", states[2])
	}

	f := wideField(t, fs, "rt-jsonarray.pulse", "sel")
	if f.Type != wideType {
		t.Errorf("jsonarray: the round-tripped column is %s, want %s", f.Type, wideType)
	}
	if got := len(f.Dictionary.Values()); got != members {
		t.Errorf("jsonarray: the round-tripped dictionary holds %d label(s), want %d", got, members)
	}
}
