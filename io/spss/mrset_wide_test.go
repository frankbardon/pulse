package spss

// The business case for the wide set rungs, asserted end to end.
//
// A real survey cohort carries a 206-option "select all that apply"
// battery. Before the wide rungs it did not derive a set column at all:
// the importer refused anything over 64 constituents and said, in the
// warning text, that there was no wider set type to widen to. There now
// is, and these tests are the claim that the ceiling moved rather than
// that the refusal was merely softened.
//
// Two failure shapes are specifically hunted here, because both are
// SILENT:
//
//  1. A mask assembled through a uint64 would drop every bit at or above
//     64 — the cohort imports, the column is there, and a 206-option
//     battery reports that nobody ever picked anything past option 64.
//     So the selections asserted straddle 63/64, 127/128, 191/192 and sit
//     at 200, which is the top word of a [4]uint64.
//  2. A second width ladder in this package would fall behind the one in
//     io/infer.go without anything failing. So the width the importer
//     picks is asserted against pio.SetTypeFor rather than against a
//     table written out again here.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// One ladder
// ---------------------------------------------------------------------------

// TestMRSet_WidthLadderIsTheSharedOne pins the derived column's width at
// every rung boundary the inference ladder is pinned at
// (TestSetWidth_LadderBoundaries), including the moved ceiling.
//
// The expectation is READ OFF pio.SetTypeFor rather than written down
// again, which is the whole point: a table restated here could agree with
// the ladder on the day it was written and silently disagree afterwards,
// which is exactly the defect this story fixes.
func TestMRSet_WidthLadderIsTheSharedOne(t *testing.T) {
	for _, members := range []int{64, 65, 128, 129, 256, 257} {
		t.Run(itoa(members), func(t *testing.T) {
			want, derives := pio.SetTypeFor(members)

			r := NewReaderFromBytes(buildFixture(t, wideMDSpec(members)))
			header, _ := readHeaderAndRows(t, r)
			schema, err := r.PulseSchema()
			if err != nil {
				t.Fatalf("PulseSchema: %v", err)
			}

			// Constituents are present at every size, above the ceiling
			// included — that is what makes a refusal cost ergonomics and
			// never data.
			for i := 0; i < members; i++ {
				if !containsString(header, wideMemberName(i)) {
					t.Fatalf("constituent %q missing from the header", wideMemberName(i))
				}
			}

			f := schema.Field("wide")
			if !derives {
				if f != nil {
					t.Fatalf("a %d-constituent set derived a %s column; the widest set type holds %d",
						members, f.Type, pio.MaxSetElements())
				}
				return
			}
			if f == nil {
				t.Fatalf("no derived column for a %d-constituent set; pio.SetTypeFor says %s fits",
					members, want)
			}
			if f.Type != want {
				t.Fatalf("width for %d constituents = %s, want %s — this package must not carry a ladder of its own",
					members, f.Type, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The 206-option battery
// ---------------------------------------------------------------------------

// TestMRSet_TwoHundredSixConstituentsDeriveOneWideColumn is the story's
// headline criterion: the cohort that could not import gets exactly one
// set_u256 column with one dictionary entry per constituent, in
// DECLARATION order, beside all 206 constituents.
func TestMRSet_TwoHundredSixConstituentsDeriveOneWideColumn(t *testing.T) {
	const members = 206

	r := NewReaderFromBytes(buildFixture(t, wideSelectionSpec(members)))
	header, _ := readHeaderAndRows(t, r)
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}

	// Exactly ONE derived set column, not one per word and not one per
	// 64-constituent slice.
	sets := 0
	for _, f := range schema.Fields {
		if f.Type.IsSet() {
			sets++
		}
	}
	if sets != 1 {
		t.Fatalf("derived %d set columns, want exactly 1", sets)
	}

	f := schema.Field("wide")
	if f == nil {
		t.Fatalf("no derived column; header = %q", header)
	}
	if f.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("wide type = %s, want set_u256 for %d constituents", f.Type, members)
	}
	if f.Dictionary == nil {
		t.Fatalf("wide has no inline dictionary")
	}

	// One bit per constituent, in declaration order: entry i is the i'th
	// member variable the set definition named.
	values := f.Dictionary.Values()
	if len(values) != members {
		t.Fatalf("dictionary has %d entries, want %d", len(values), members)
	}
	for i, v := range values {
		if want := wideMemberName(i); v != want {
			t.Fatalf("dictionary entry %d = %q, want %q — bit i must be the i'th declared constituent", i, v, want)
		}
	}

	// Additive: every constituent is still its own column.
	for i := 0; i < members; i++ {
		if schema.Field(wideMemberName(i)) == nil {
			t.Fatalf("constituent %q is missing from the schema", wideMemberName(i))
		}
	}
}

// TestMRSet_WideSelectionRoundTripsThroughTheCohort is the acceptance
// criterion stated as a round trip: a respondent who ticked constituents
// #3, #70 and #200 reads back as exactly those three labels, off the
// wire, through the shared pio import job.
//
// Reading the mask back out of the `.pulse` bytes is deliberate. A test
// that stopped at the reader's rendered cell text would pass against a
// mask assembled into a uint64, because the cell is built from the
// constituents and never from the mask — the truncation happens later,
// where only the cohort can see it.
func TestMRSet_WideSelectionRoundTripsThroughTheCohort(t *testing.T) {
	const members = 206
	spec := wideSelectionSpec(members)

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "wide.sav", buildFixture(t, spec), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	job := pio.NewImportJob(NewReader(fs, "wide.sav"), "wide.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	if len(rep.RowErrors) != 0 {
		t.Fatalf("%d row errors; first = %v", len(rep.RowErrors), rep.RowErrors[0].Err)
	}
	if rep.RowsImported != len(spec.Cases) {
		t.Fatalf("RowsImported = %d, want %d", rep.RowsImported, len(spec.Cases))
	}

	field := rep.Schema.Field("wide")
	if field == nil || field.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("wide field = %+v, want a set_u256 column", field)
	}

	masks, nulls := readCohortSetColumn(t, fs, "wide.pulse", "wide")
	if len(masks) != len(spec.Cases) {
		t.Fatalf("decoded %d records, want %d", len(masks), len(spec.Cases))
	}

	for _, tc := range []struct {
		row  int
		bits []int
		null bool
		why  string
	}{
		{row: 0, bits: []int{3, 70, 200},
			why: "the headline selection: one bit in each of three different words"},
		{row: 1, bits: []int{0, 63, 64, 127, 128, 191, 192, 205},
			why: "every word boundary of a [4]uint64, from both sides"},
		{row: 2, bits: nil,
			why: "answered the battery and ticked nothing — an EMPTY mask, which is not null"},
		{row: 3, bits: nil, null: true,
			why: "every constituent missing — nothing is known, so the row is null"},
	} {
		if nulls[tc.row] != tc.null {
			t.Errorf("row %d null = %v, want %v (%s)", tc.row, nulls[tc.row], tc.null, tc.why)
			continue
		}
		if tc.null {
			continue
		}
		var want encoding.SetMask
		for _, b := range tc.bits {
			want = want.WithBit(b)
		}
		if !masks[tc.row].Equal(want) {
			t.Errorf("row %d mask bits = %v, want %v (%s)",
				tc.row, setBits(masks[tc.row]), tc.bits, tc.why)
			continue
		}
		// And the labels, which is what a caller actually reads.
		got := masks[tc.row].Labels(field.Dictionary)
		wantLabels := make([]string, 0, len(tc.bits))
		for _, b := range tc.bits {
			wantLabels = append(wantLabels, wideMemberName(b))
		}
		if !equalStrings(got, wantLabels) {
			t.Errorf("row %d labels = %q, want %q (%s)", tc.row, got, wantLabels, tc.why)
		}
	}
}

// TestMRSet_NarrowSetsAreStillNarrow is the other half of the widening:
// a battery that fit before must cost exactly what it cost before.
//
// Widening the ceiling by promoting every derived set to the top rung
// would pass every test above and quietly cost 31 bytes a record on every
// narrow battery in every survey cohort — the kind of regression a
// correctness suite does not notice. So the rung's ON-WIRE width is
// asserted, not just its name.
func TestMRSet_NarrowSetsAreStillNarrow(t *testing.T) {
	for _, tc := range []struct {
		members int
		want    encoding.FieldType
		bytes   int
	}{
		{3, encoding.FieldTypeSetU8, 1},
		{33, encoding.FieldTypeSetU64, 8},
		{64, encoding.FieldTypeSetU64, 8},
	} {
		t.Run(itoa(tc.members), func(t *testing.T) {
			fs := afero.NewMemMapFs()
			if err := afero.WriteFile(fs, "n.sav", buildFixture(t, wideMDSpec(tc.members)), 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			job := pio.NewImportJob(NewReader(fs, "n.sav"), "n.pulse")
			job.FS = fs
			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("importing: %v", err)
			}
			f := rep.Schema.Field("wide")
			if f == nil {
				t.Fatalf("no derived column for %d constituents", tc.members)
			}
			if f.Type != tc.want {
				t.Fatalf("type = %s, want %s", f.Type, tc.want)
			}
			if got := f.Type.ByteSize(); got != tc.bytes {
				t.Fatalf("%s costs %d bytes a record, want %d", f.Type, got, tc.bytes)
			}
			// Only the first constituent holds the counted value.
			masks, _ := readCohortSetColumn(t, fs, "n.pulse", "wide")
			if len(masks) != 1 || !masks[0].Equal(encoding.SetMask{}.WithBit(0)) {
				t.Fatalf("mask bits = %v, want exactly bit 0", setBits(masks[0]))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The ceiling moved; the failure shape did not
// ---------------------------------------------------------------------------

// TestMRSet_AboveTheCeilingStillRefuses asserts the refusal survived the
// widening: 257 constituents still derive nothing, still under
// PULSE_SPSS_MR_SET_NOT_DERIVED, still as a WARNING with every
// constituent imported — and the message now names 256 and set_u256
// rather than 64 and set_u64.
func TestMRSet_AboveTheCeilingStillRefuses(t *testing.T) {
	over := pio.MaxSetElements() + 1
	r := NewReaderFromBytes(buildFixture(t, wideMDSpec(over)))
	header, rows := readHeaderAndRows(t, r)

	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	if f := schema.Field("wide"); f != nil {
		t.Fatalf("a %d-constituent set derived a %s column", over, f.Type)
	}
	for i := 0; i < over; i++ {
		if schema.Field(wideMemberName(i)) == nil {
			t.Fatalf("constituent %q missing — a refusal must never cost data", wideMemberName(i))
		}
	}
	if len(rows) != 1 || len(rows[0]) != len(header) {
		t.Fatalf("the import did not produce a readable row: %d rows", len(rows))
	}

	// The warning still names the set, and now names the moved ceiling.
	assertMRSetWarning(t, r, "$wide", "more than the "+itoa(pio.MaxSetElements()))
	ce := findWarning(t, r, perr.PULSE_SPSS_MR_SET_NOT_DERIVED)
	if !strings.Contains(ce.Message, pio.WidestSetType().String()) {
		t.Errorf("warning = %q, want it to name the widest set type %s",
			ce.Message, pio.WidestSetType())
	}
	if strings.Contains(ce.Message, "set_u64") {
		t.Errorf("warning = %q, still names set_u64 as the ceiling", ce.Message)
	}
}

// TestMRSet_NoStaleWiderTypeClaim is the story's regression gate. The
// refusal message used to assert, in prose, that no wider set type
// existed. Two of them now do, and a sentence that is false is worse
// than one that is missing: it tells a reader hitting a 206-option
// battery that the limit they hit is the format's, and there is nothing
// to be done.
//
// The needle is assembled rather than written, so this file does not
// itself trip the gate.
func TestMRSet_NoStaleWiderTypeClaim(t *testing.T) {
	needle := "there is no wider set type" + " to widen to"

	root := repoRoot(t)
	skip := map[string]bool{
		".git": true, ".planning": true, "bin": true, "tmp": true, "book": true,
	}
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".md", ".json":
		default:
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), needle) {
			rel, _ := filepath.Rel(root, path)
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(hits) != 0 {
		t.Errorf("%q still appears in %v — two wider set types exist, so the claim is false", needle, hits)
	}
}

// repoRoot locates the module root from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("%s is not the module root: %v", dir, err)
	}
	return dir
}

// ---------------------------------------------------------------------------
// Fixtures and helpers
// ---------------------------------------------------------------------------

// wideSelectionSpec builds an n-constituent dichotomy battery over four
// cases chosen to exercise the three row states and every word boundary
// of a 256-bit mask.
//
//	case 0: constituents #3, #70 and #200 ticked
//	case 1: #0, #63, #64, #127, #128, #191, #192 and #205 ticked
//	case 2: answered, ticked nothing        -> empty mask
//	case 3: every constituent system-missing -> null
func wideSelectionSpec(n int) spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	spec := spsstest.Spec{}
	members := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := wideMemberName(i)
		spec.Vars = append(spec.Vars, spsstest.Var{Name: name, Print: num})
		members = append(members, name)
	}

	pick := func(bits ...int) []spsstest.Value {
		on := make(map[int]bool, len(bits))
		for _, b := range bits {
			on[b] = true
		}
		row := make([]spsstest.Value, 0, n)
		for i := 0; i < n; i++ {
			if on[i] {
				row = append(row, spsstest.Num(1))
			} else {
				row = append(row, spsstest.Num(0))
			}
		}
		return row
	}
	allMissing := make([]spsstest.Value, 0, n)
	for i := 0; i < n; i++ {
		allMissing = append(allMissing, spsstest.SysMis())
	}

	spec.Cases = [][]spsstest.Value{
		pick(3, 70, 200),
		pick(0, 63, 64, 127, 128, 191, 192, 205),
		pick(),
		allMissing,
	}
	spec.MultipleResponseSets = []spsstest.MRSet{{
		Name: "$wide", Kind: spsstest.MRDichotomy, CountedValue: "1",
		Vars: members, Subtype: spsstest.SubtypeMRSets,
	}}
	return spec
}

// readCohortSetColumn walks a written `.pulse` cohort and returns the
// named set column's mask per record, plus whether the record's null
// bitmap marks it null.
//
// It reads the BYTES rather than going back through a decoder, because
// the claim under test is about what was written.
func readCohortSetColumn(t *testing.T, fs afero.Fs, path, field string) ([]encoding.SetMask, []bool) {
	t.Helper()
	blob, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := strings.NewReader(string(blob))
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	payload := blob[len(blob)-r.Len():]

	off, width, index := 0, 0, -1
	for i, f := range schema.Fields {
		if f.Name == field {
			if f.Type.IsBitPacked() {
				t.Fatalf("field %q is bit-packed, not a set", field)
			}
			width, index = f.Type.ByteSize(), i
			break
		}
		if f.Type.IsBitPacked() {
			off++
			continue
		}
		off += f.Type.ByteSize()
	}
	if width == 0 {
		t.Fatalf("field %q not found or zero width", field)
	}

	stride := schema.RecordByteSize()
	bitmapAt := stride - schema.BitmapByteSize()
	ft := schema.Field(field).Type

	var masks []encoding.SetMask
	var nulls []bool
	for i := 0; i+stride <= len(payload); i += stride {
		rec := payload[i : i+stride]
		masks = append(masks, decodeSetPayload(t, ft, rec[off:off+width]))
		nulls = append(nulls, encoding.BitmapIsNull(rec[bitmapAt:], index))
	}
	return masks, nulls
}

// decodeSetPayload turns a set field's payload bytes into a mask, by the
// rung's own rule.
//
// The two rungs are read DIFFERENTLY on purpose, and the difference is
// the contract: the wide API refuses set_u8..set_u64 outright rather than
// zero-extending them, so a narrow rung is still a little-endian uint64
// and nothing about this story changed that.
func decodeSetPayload(t *testing.T, ft encoding.FieldType, b []byte) encoding.SetMask {
	t.Helper()
	if ft.IsWideSet() {
		m, err := encoding.SetMaskFromBytes(ft, b)
		if err != nil {
			t.Fatalf("SetMaskFromBytes(%s): %v", ft, err)
		}
		return m
	}
	var low uint64
	for i := len(b) - 1; i >= 0; i-- {
		low = low<<8 | uint64(b[i])
	}
	return encoding.SetMaskFromUint64(low)
}

// setBits lists a mask's set bits, for a readable failure message.
func setBits(m encoding.SetMask) []int {
	var out []int
	for b := range m.Bits() {
		out = append(out, b)
	}
	return out
}
