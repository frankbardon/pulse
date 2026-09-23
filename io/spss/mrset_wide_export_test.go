package spss

// The EXPORT half of the 206-constituent battery.
//
// E3-S3 landed the import: a 206-option "select all that apply" set now
// derives one set_u256 column instead of refusing at 64. It never
// exercised the way back out, and the way back out had a hole of exactly
// the kind this effort exists to close.
//
// [CaseValue.Mask] was a `uint64`. Three things followed from that and
// only one of them was loud:
//
//  1. readCase reached a set_u256 column through encoding.ReadFieldValue,
//     which REFUSES the wide rungs rather than truncating — so exporting
//     a 206-option cohort failed outright with ENCODING_TYPE_MISMATCH.
//     Loud, and total: the whole file, not the column.
//  2. The set-member encoder tested `v.Mask & (1 << col.SetBit)` and
//     refused any bit at or above 64 per CASE, which is a refusal a
//     `pulse export predict` could not see.
//  3. Nothing bounded a member's bit against its own rung at PLAN time,
//     so a cohort declaring more dictionary entries than its set type
//     addresses got a clean predict and a mid-write failure.
//
// The tests below are the claim that the export is now as wide as the
// import, at both ends of the ladder. The width-blind claims (the derived
// column is DROPPED by kind, the constituents are rebuilt by name) are
// each asserted at two widths, because a claim that something does not
// depend on width is worth nothing asserted at one width.

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// The metadata sidecar
// ---------------------------------------------------------------------------

// TestSidecar_WideSetRecordsTheFullTriple is the story's first criterion:
// the sidecar records the code <-> label <-> dictionary-ID triple for all
// 206 constituents, with the dictionary IDs matching the BIT POSITIONS in
// the cohort.
//
// The sidecar is the only home for that mapping — there is no second copy
// to reconcile against — so the assertion is made against the cohort's own
// dictionary rather than against the order the fixture declared. Sources[i]
// must be dictionary entry i, which is bit i, for every i.
func TestSidecar_WideSetRecordsTheFullTriple(t *testing.T) {
	const members = 206

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "source.sav", build(t, wideSelectionSpec(members)), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	importSav(t, fs, "source.sav", "first.pulse")
	doc := readSidecar(t, fs, "first.pulse")
	schema := cohortSchema(t, fs, "first.pulse")

	field := schema.Field("wide")
	if field == nil || field.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("wide field = %+v, want a set_u256 column", field)
	}
	entries := field.Dictionary.Values()
	if len(entries) != members {
		t.Fatalf("the cohort dictionary has %d entries, want %d", len(entries), members)
	}

	// The derived-column registry entry, and the bit-order claim.
	var md *Derived
	for i := range doc.Payload.Derived {
		if doc.Payload.Derived[i].Kind == DerivedKindMultipleDichotomy {
			if md != nil {
				t.Fatalf("two multiple_dichotomy entries; want exactly one")
			}
			md = &doc.Payload.Derived[i]
		}
	}
	if md == nil {
		t.Fatalf("the sidecar records no multiple_dichotomy derived column: %+v", doc.Payload.Derived)
	}
	if md.Name != "wide" || md.SetName != "$wide" {
		t.Errorf("derived entry = {name %q, set %q}, want {\"wide\", \"$wide\"}", md.Name, md.SetName)
	}
	if len(md.Sources) != members {
		t.Fatalf("the registry names %d constituent(s), want %d — a wide set must not be recorded at a narrow width",
			len(md.Sources), members)
	}
	for i := range entries {
		if md.Sources[i] != entries[i] {
			t.Fatalf("sources[%d] = %q but dictionary entry %d (bit %d) is %q; the triple's dictionary ID must be the bit position",
				i, md.Sources[i], i, i, entries[i])
		}
		if want := wideMemberName(i); entries[i] != want {
			t.Fatalf("dictionary entry %d = %q, want %q", i, entries[i], want)
		}
	}
	if !md.Complete() {
		t.Errorf("the registry entry is not self-sufficient, so an export cannot fold it back: %+v", md)
	}

	// The set definition itself — the counted value and the label live
	// here, not on the registry entry, and the export re-emits record 7/7
	// from it.
	var set *MRSet
	for i := range doc.Payload.MultipleResponseSets {
		if doc.Payload.MultipleResponseSets[i].Name == "$wide" {
			set = &doc.Payload.MultipleResponseSets[i]
		}
	}
	if set == nil {
		t.Fatalf("the sidecar records no $wide multiple-response set")
	}
	if len(set.Variables) != members {
		t.Errorf("the set names %d variable(s), want %d", len(set.Variables), members)
	}
	if len(set.Fields) != len(set.Variables) {
		t.Errorf("fields/variables are a parallel array: %d vs %d", len(set.Fields), len(set.Variables))
	}
	for i := range set.Fields {
		if set.Fields[i] != entries[i] {
			t.Errorf("fields[%d] = %q, want the bit-%d constituent %q", i, set.Fields[i], i, entries[i])
		}
	}
	if set.Kind != MRSetKindDichotomy {
		t.Errorf("kind = %q, want %q", set.Kind, MRSetKindDichotomy)
	}
}

// ---------------------------------------------------------------------------
// The cohort path: drop the derived column, rebuild the constituents
// ---------------------------------------------------------------------------

// TestRoundTrip_WideSetSurvivesTheSPSSCycle is the headline: import,
// export, re-import a 206-option battery and the same respondent selects
// the same labels.
//
// The masks are read out of the `.pulse` BYTES on both sides rather than
// compared through a rendered cell, for the reason E3-S3 gave: a mask
// assembled through a uint64 renders identically and drops every bit past
// 63, and only the cohort can see that.
func TestRoundTrip_WideSetSurvivesTheSPSSCycle(t *testing.T) {
	const members = 206

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "source.sav", build(t, wideSelectionSpec(members)), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	importSav(t, fs, "source.sav", "first.pulse")
	out := exportSav(t, fs, "first.pulse", "out.sav", WriterOptions{})
	importSav(t, fs, "out.sav", "second.pulse")

	// The derived column is DROPPED, and every constituent is rebuilt
	// under its own name in declaration order.
	emitted := savVariableNames(t, out)
	if containsFold(emitted, "wide") {
		t.Errorf("the emitted file declares %q, which is a column this reader synthesised", "wide")
	}
	if len(emitted) != members {
		t.Fatalf("the emitted file declares %d variable(s), want %d — one per constituent and nothing else",
			len(emitted), members)
	}
	for i := 0; i < members; i++ {
		if emitted[i] != wideMemberName(i) {
			t.Fatalf("variable %d = %q, want %q; the constituents must be rebuilt in order",
				i, emitted[i], wideMemberName(i))
		}
	}

	// The re-imported cohort derives the same column at the same width,
	// over the same dictionary.
	second := cohortSchema(t, fs, "second.pulse")
	f2 := second.Field("wide")
	if f2 == nil || f2.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("the re-imported wide field = %+v, want a set_u256 column", f2)
	}
	first := cohortSchema(t, fs, "first.pulse")
	if !equalStrings(first.Field("wide").Dictionary.Values(), f2.Dictionary.Values()) {
		t.Fatalf("the dictionary did not survive the cycle")
	}

	// And the values. Same respondent, same labels.
	before, beforeNull := readCohortSetColumn(t, fs, "first.pulse", "wide")
	after, afterNull := readCohortSetColumn(t, fs, "second.pulse", "wide")
	if len(before) != len(after) {
		t.Fatalf("the cycle produced %d record(s), want %d", len(after), len(before))
	}
	for i := range before {
		if beforeNull[i] != afterNull[i] {
			t.Errorf("row %d null = %v, want %v", i, afterNull[i], beforeNull[i])
			continue
		}
		if beforeNull[i] {
			continue
		}
		if !before[i].Equal(after[i]) {
			t.Errorf("row %d selected %v, want %v", i, setBits(after[i]), setBits(before[i]))
		}
	}

	// Named so a failure says which respondent, not just which row.
	if got := setBits(after[0]); len(got) != 3 || got[0] != 3 || got[1] != 70 || got[2] != 200 {
		t.Errorf("the headline respondent selected %v, want [3 70 200]", got)
	}
	if after[2].PopCount() != 0 || afterNull[2] {
		t.Errorf("row 2 = %v (null %v), want an EMPTY mask — answered and ticked nothing",
			setBits(after[2]), afterNull[2])
	}
}

// TestRoundTrip_DerivedSetDropIsWidthBlind is the other half of the added
// scope: the drop is name- and kind-driven, so it SHOULD be width-blind,
// and a width-blind claim asserted at one width is not asserted at all.
//
// So the same cycle runs at the narrowest rung and the widest, and the
// expectations are read off pio.SetTypeFor rather than written down.
func TestRoundTrip_DerivedSetDropIsWidthBlind(t *testing.T) {
	for _, members := range []int{3, 206} {
		t.Run(itoa(members), func(t *testing.T) {
			want, derives := pio.SetTypeFor(members)
			if !derives {
				t.Fatalf("%d constituents do not derive a column; the case is not the one being tested", members)
			}

			fs := afero.NewMemMapFs()
			if err := afero.WriteFile(fs, "source.sav", build(t, wideSelectionSpec(members)), 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			importSav(t, fs, "source.sav", "first.pulse")
			if got := cohortSchema(t, fs, "first.pulse").Field("wide"); got == nil || got.Type != want {
				t.Fatalf("wide field = %+v, want %s", got, want)
			}

			out := exportSav(t, fs, "first.pulse", "out.sav", WriterOptions{})
			if names := savVariableNames(t, out); containsFold(names, "wide") {
				t.Errorf("the emitted file declares the derived column %q at width %s", "wide", want)
			} else if len(names) != members {
				t.Errorf("the emitted file declares %d variable(s), want %d", len(names), members)
			}

			importSav(t, fs, "out.sav", "second.pulse")
			before, beforeNull := readCohortSetColumn(t, fs, "first.pulse", "wide")
			after, afterNull := readCohortSetColumn(t, fs, "second.pulse", "wide")
			if len(before) != len(after) {
				t.Fatalf("the cycle produced %d record(s), want %d", len(after), len(before))
			}
			for i := range before {
				if beforeNull[i] != afterNull[i] || (!beforeNull[i] && !before[i].Equal(after[i])) {
					t.Errorf("row %d = %v (null %v), want %v (null %v)",
						i, setBits(after[i]), afterNull[i], setBits(before[i]), beforeNull[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The synthesised path: a set column with no sidecar
// ---------------------------------------------------------------------------

// TestWriter_SynthesisedWideSetExpandsToConstituents is the criterion
// stated against pio.CohortWriter directly: a wide set goes out as its
// constituent dichotomy variables, preserving names and order.
//
// The cohort here was never SPSS-derived, so there is no sidecar and no
// derived registry — synthesiseSet is what has to expand the column, and
// its member list is the set's own dictionary. A mask read through a
// uint64 would emit 206 variables and report 0 for every one of them at
// or above bit 64, which is a perfectly well-formed `.sav` that is wrong.
func TestWriter_SynthesisedWideSetExpandsToConstituents(t *testing.T) {
	const members = 206
	selections := [][]int{
		{3, 70, 200},
		{0, 63, 64, 127, 128, 191, 192, 205},
		{},
	}

	fs, path := wideSetCohort(t, encoding.FieldTypeSetU256, members, selections)
	out := exportSav(t, fs, path, "out.sav", WriterOptions{})

	names := savVariableNames(t, out)
	if len(names) != members {
		t.Fatalf("the emitted file declares %d variable(s), want one per dictionary entry (%d)",
			len(names), members)
	}
	for i := 0; i < members; i++ {
		if names[i] != wideMemberName(i) {
			t.Fatalf("variable %d = %q, want %q — the member order is the dictionary's bit order",
				i, names[i], wideMemberName(i))
		}
	}

	for bit := 0; bit < members; bit++ {
		col := savColumn(t, out, wideMemberName(bit))
		if len(col) != len(selections) {
			t.Fatalf("%q has %d case(s), want %d", wideMemberName(bit), len(col), len(selections))
		}
		for row, sel := range selections {
			want := 0.0
			for _, b := range sel {
				if b == bit {
					want = float64(synthSetCountedValue)
				}
			}
			if col[row] != want {
				t.Fatalf("row %d, bit %d (%q) = %v, want %v — a mask read through a uint64 drops every bit at or above 64",
					row, bit, wideMemberName(bit), col[row], want)
			}
		}
	}

	// And back: re-importing the emitted file reproduces the selections.
	importSav(t, fs, "out.sav", "back.pulse")
	back, backNull := readCohortSetColumn(t, fs, "back.pulse", "wide")
	if len(back) != len(selections) {
		t.Fatalf("the re-imported cohort has %d record(s), want %d", len(back), len(selections))
	}
	for row, sel := range selections {
		var want encoding.SetMask
		for _, b := range sel {
			want = want.WithBit(b)
		}
		if backNull[row] || !back[row].Equal(want) {
			t.Errorf("row %d came back as %v (null %v), want %v",
				row, setBits(back[row]), backNull[row], sel)
		}
	}
}

// TestWriter_SynthesisedNarrowSetIsUnchanged is the width-blind control
// for the test above: the narrow rung must expand exactly as it did
// before, so a fix that promoted every set to the wide read path would
// still have to answer for set_u8.
func TestWriter_SynthesisedNarrowSetIsUnchanged(t *testing.T) {
	selections := [][]int{{0, 2}, {}, {1}}
	fs, path := wideSetCohort(t, encoding.FieldTypeSetU8, 3, selections)
	out := exportSav(t, fs, path, "out.sav", WriterOptions{})

	if names := savVariableNames(t, out); len(names) != 3 {
		t.Fatalf("the emitted file declares %d variable(s), want 3", len(names))
	}
	for bit := 0; bit < 3; bit++ {
		col := savColumn(t, out, wideMemberName(bit))
		for row, sel := range selections {
			want := 0.0
			for _, b := range sel {
				if b == bit {
					want = float64(synthSetCountedValue)
				}
			}
			if col[row] != want {
				t.Errorf("row %d, bit %d = %v, want %v", row, bit, col[row], want)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Target-aware export predict
// ---------------------------------------------------------------------------

// TestExportPredict_WideSetIsAcceptedByTheSPSSTarget is the predict
// criterion at the positive end: the `.sav` target CAN represent a wide
// set, so predict must not refuse it. A false refusal is worse than
// silence — see .claude/reference/byte-layout.md (Export predict is
// target-aware).
func TestExportPredict_WideSetIsAcceptedByTheSPSSTarget(t *testing.T) {
	fs, path := wideSetCohort(t, encoding.FieldTypeSetU256, 206, [][]int{{3, 70, 200}})

	job := pio.NewExportJob(path, NewWriter(fs, "out.sav", WriterOptions{}))
	job.FS = fs
	rep, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("predicting a wide-set export: %v", err)
	}
	if f := rep.Schema.Field("wide"); f == nil || f.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("the predicted schema's wide field = %+v, want set_u256", f)
	}
	// The cohort was never SPSS-derived, so the ABSENT sidecar warning is
	// the one legitimate diagnostic. Anything else is predict refusing a
	// column the export would have written.
	for _, w := range rep.TargetWarnings {
		if w.Code != perr.PULSE_SPSS_SIDECAR_ABSENT {
			t.Errorf("predict raised %s for a wide set the export accepts: %s", w.Code, w.Message)
		}
	}
}

// TestExportPredict_SetBitBeyondItsRungIsRefusedAtPlanTime is the other
// end: a cohort whose set column declares more dictionary entries than
// its own rung addresses has no honest `.sav` form, and the refusal has
// to be reachable from schema facts alone.
//
// Before the fix the bound lived on the per-CASE path, so predict
// answered "fine" and the export failed on record 1 — the export's own
// check and its prediction disagreeing, which is exactly what
// io.CohortValidator exists to prevent.
func TestExportPredict_SetBitBeyondItsRungIsRefusedAtPlanTime(t *testing.T) {
	// A set_u8 addresses bits 0..7. Ten dictionary entries mean members
	// standing for bits 8 and 9, which no set_u8 mask has.
	fs, path := wideSetCohort(t, encoding.FieldTypeSetU8, 10, [][]int{{0}})

	job := pio.NewExportJob(path, NewWriter(fs, "out.sav", WriterOptions{}))
	job.FS = fs
	if _, err := job.Predict(context.Background()); err == nil {
		t.Fatalf("predict accepted a set_u8 column with 10 dictionary entries; the export cannot write it")
	} else if ce := codedErr(t, err); ce.Code != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_EXPORT_UNSUPPORTED)
	}

	// The export itself refuses on the same terms, which is the property
	// that makes the prediction worth anything.
	w := NewWriter(fs, "out2.sav", WriterOptions{})
	run := pio.NewExportJob(path, w)
	run.FS = fs
	if _, err := run.Run(context.Background()); err == nil {
		t.Fatalf("the export wrote a file predict refused")
	}
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// maskOf builds a [encoding.SetMask] from the bits it names. It exists so
// a Case literal reads as the selection it stands for rather than as a
// binary constant — `maskOf(0, 1)` and `0b11` are the same mask, but only
// one of them still means something at bit 200.
func maskOf(bits ...int) encoding.SetMask {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

// wideSetCohort writes a `.pulse` cohort carrying exactly one set column
// of the given rung, with n dictionary entries named as the mrset
// fixtures name theirs, and one record per selection list.
//
// No metadata sidecar is written: this is the synthesised path, where the
// export has nothing but the schema to go on.
func wideSetCohort(t *testing.T, ft encoding.FieldType, n int, selections [][]int) (afero.Fs, string) {
	t.Helper()
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, wideMemberName(i))
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "wide", Type: ft, Dictionary: dictOf(t, names...)},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, sel := range selections {
		var m encoding.SetMask
		for _, b := range sel {
			m = m.WithBit(b)
		}
		if ft.IsWideSet() {
			if err := encoding.WriteSetMask(&buf, ft, m); err != nil {
				t.Fatalf("WriteSetMask: %v", err)
			}
			continue
		}
		low, ok := m.Uint64()
		if !ok {
			t.Fatalf("selection %v does not fit a narrow rung", sel)
		}
		if err := encoding.WriteFieldValue(&buf, ft, low); err != nil {
			t.Fatalf("WriteFieldValue: %v", err)
		}
	}

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "wide.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatalf("writing the cohort: %v", err)
	}
	return fs, "wide.pulse"
}
