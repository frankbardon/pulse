package setwide

import (
	"context"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	pspss "github.com/frankbardon/pulse/io/spss"
)

// TestSPSS_WideSetRoundTrip: cohort -> .sav -> cohort, at 206 members.
//
// `.sav` is the one target with no set cell at all. A multiple-response
// set is not a column there: it is N numeric dichotomy variables plus a
// record 7/7 definition naming them, so the export EXPANDS the 206-bit
// mask into 206 variables and the import folds them back. Nothing about
// that path shares code with the delimiter-joined token string the other
// eight adapters carry, which is why it cannot inherit their result —
// and why it is the one adapter where a wide set was genuinely, not
// hypothetically, refused at 64 until this effort.
//
// The re-import runs with NO explicit schema, unlike the other eight.
// That is deliberate: the `.sav` reader is schema-aware and DERIVES the
// set column, its rung and its dictionary from the emitted variables, so
// letting it infer is what proves the width survived the file rather
// than having been supplied by the test.
func TestSPSS_WideSetRoundTrip(t *testing.T) {
	fs, src := sourceCohort(t)

	exportThrough(t, "spss", fs, src, pspss.NewWriter(fs, "out.sav", pspss.WriterOptions{}))

	job := pio.NewImportJob(pspss.NewReader(fs, "out.sav"), "rt-spss.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("spss: re-import: %v", err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("spss: re-imported %d row(s), want %d (errors %v)",
			rep.RowsImported, fixtureRecords, rep.RowErrors)
	}

	// The derived column, its rung and its dictionary — all three read
	// off the emitted file rather than declared by the test.
	f := wideField(t, fs, "rt-spss.pulse", "sel")
	if f.Type != wideType {
		t.Fatalf("spss: the derived column is %s, want %s — 206 constituents do not fit a narrower rung",
			f.Type, wideType)
	}
	labels := f.Dictionary.Values()
	if len(labels) != members {
		t.Fatalf("spss: the derived dictionary holds %d label(s), want %d", len(labels), members)
	}
	for i, got := range labels {
		if got != memberName(i) {
			t.Fatalf("spss: dictionary entry %d is %q, want %q — the entry order IS the bit order",
				i, got, memberName(i))
		}
	}

	states := readStates(t, fs, "rt-spss.pulse", "sel")
	if len(states) != fixtureRecords {
		t.Fatalf("spss: the round-tripped cohort holds %d record(s), want %d", len(states), fixtureRecords)
	}
	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Errorf("spss: the selection came back as %s, want the bits %v", states[0], selectedBits)
	}
	for _, b := range selectedBits {
		if !states[0].Mask.Has(b) {
			t.Errorf("spss: bit %d is clear after the round trip, want it set — a mask read through a uint64 stops at 63",
				b)
		}
	}
	for _, b := range clearBits {
		if states[0].Mask.Has(b) {
			t.Errorf("spss: bit %d is set after the round trip, want it clear", b)
		}
	}

	// The two states a dichotomy expansion most easily fuses. Every
	// member of a null set goes out system-missing and every member of an
	// empty selection goes out 0; collapse that distinction and a
	// respondent who ticked nothing becomes one who was never asked.
	if states[1].Null || !states[1].Mask.IsEmpty() {
		t.Errorf("spss: the empty selection came back as %s, want empty-mask", states[1])
	}
	if !states[2].Null {
		t.Errorf("spss: the null came back as %s, want null", states[2])
	}
}
