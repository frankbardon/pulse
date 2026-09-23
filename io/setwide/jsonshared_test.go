package setwide

import (
	"context"
	"testing"

	pfs "github.com/frankbardon/pulse/fs"
	pio "github.com/frankbardon/pulse/io"
	pjsonshared "github.com/frankbardon/pulse/io/jsonshared"
)

// TestJSONShared_WideSetRoundTrip: the coercion pair the two JSON
// adapters share, at 206 members.
//
// jsonshared is not a Reader/Writer, so it has no export to run — and
// that is exactly why it gets its own test rather than being counted as
// covered by ndjson and jsonarray. It is the single point where a JSON
// array becomes a Pulse set cell (ValueToString) and where a Pulse set
// cell becomes a JSON value (CoerceValue). Both adapters inherit
// whatever it decides, including a mistake, and a mistake here would
// look like an adapter bug in two places at once.
//
// The states it has to keep apart are the awkward ones: a JSON `null`
// and a JSON `[]` are different answers that both have "no elements",
// and the string form of the second cannot be the empty string, because
// the import path reads "" as a null before any dictionary is consulted.
func TestJSONShared_WideSetRoundTrip(t *testing.T) {
	// The delimiters must agree, or a cell this package joins is a cell
	// io/import.go cannot split.
	if pjsonshared.SetArrayDelimiter != pio.DefaultSetDelimiter {
		t.Fatalf("jsonshared joins set elements with %q but io splits on %q",
			pjsonshared.SetArrayDelimiter, pio.DefaultSetDelimiter)
	}

	// Inbound: a JSON array of 206-dictionary member labels.
	elems := make([]any, 0, len(selectedBits))
	for _, b := range selectedBits {
		elems = append(elems, memberName(b))
	}
	selection := pjsonshared.ValueToString(elems)
	if selection != selectionCell() {
		t.Errorf("jsonshared: a %d-element array became %q, want %q",
			len(elems), selection, selectionCell())
	}
	empty := pjsonshared.ValueToString([]any{})
	if empty != pio.EmptySetCell {
		t.Errorf("jsonshared: an empty JSON array became %q, want %q (the empty-selection marker)",
			empty, pio.EmptySetCell)
	}
	null := pjsonshared.ValueToString(nil)
	if null != "" {
		t.Errorf("jsonshared: a JSON null became %q, want the empty string", null)
	}
	if empty == null {
		t.Errorf("jsonshared: `[]` and `null` both became %q — ticking nothing is not skipping the question", empty)
	}

	// And those three cells, imported under the wide schema, are the
	// three states. This is the claim that matters: the coercion's output
	// is not merely different per state, it is the RIGHT text for each.
	fs := pfs.NewMemMap().Fs()
	rows := [][]string{{"1", selection}, {"2", empty}, {"3", null}}
	job := pio.NewImportJob(&mockReader{columns: []string{"id", "sel"}, rows: rows}, "js.pulse")
	job.FS = fs
	job.Schema = wideSchema(t)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("jsonshared: importing the coerced cells: %v", err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("jsonshared: imported %d row(s), want %d (errors %v)",
			rep.RowsImported, fixtureRecords, rep.RowErrors)
	}
	states := readStates(t, fs, "js.pulse", "sel")
	if len(states) != fixtureRecords {
		t.Fatalf("jsonshared: the cohort holds %d record(s), want %d", len(states), fixtureRecords)
	}
	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("jsonshared: the selection stored as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("jsonshared: bit %d is clear, want it set", b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("jsonshared: bit %d is set, want it clear", b)
		}
	}
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("jsonshared: the empty selection stored as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("jsonshared: the null stored as %s, want null", states[2])
	}

	// Outbound: the same three cells on their way into a JSON document.
	// A set cell must stay a STRING — coerced to a number or a null it
	// would come back as a different state, or not at all.
	if got := pjsonshared.CoerceValue(selection); got != any(selection) {
		t.Errorf("jsonshared: the selection cell coerced to %#v, want the string %q", got, selection)
	}
	if got := pjsonshared.CoerceValue(pio.EmptySetCell); got == nil {
		t.Errorf("jsonshared: the empty-selection cell coerced to a JSON null, which is the missing answer")
	}
	if got := pjsonshared.CoerceValue(""); got != nil {
		t.Errorf("jsonshared: the null cell coerced to %#v, want a JSON null", got)
	}
}
