package spss

import (
	"encoding/binary"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// dictOf builds an encoding dictionary from its entries in ID order.
func dictOf(t *testing.T, entries ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, e := range entries {
		if _, err := d.Add(e); err != nil {
			t.Fatalf("adding %q: %v", e, err)
		}
	}
	return d
}

// synthesise emits a dictionary from a schema with no sidecar at all.
func synthesise(t *testing.T, fields ...encoding.Field) *DictionaryPlan {
	t.Helper()
	return emit(t, DictionaryRequest{
		Schema:      &encoding.Schema{Fields: fields},
		Cases:       0,
		Compression: compressionNone,
	})
}

// ---------------------------------------------------------------------------
// Type mapping
// ---------------------------------------------------------------------------

// TestSynthesise_TypeMapping is the whole `.pulse` -> `.sav` table in one
// place. SPSS has exactly two storage types, so every row lands on a numeric
// or a string and the format code is what carries the rest of the meaning.
func TestSynthesise_TypeMapping(t *testing.T) {
	for _, tc := range []struct {
		name      string
		field     encoding.Field
		wantWidth int // 0 = numeric
		wantPrint Format
		wantMeas  measureLevel
		wantEnc   ValueEncoding
	}{
		{"packed_bool", encoding.Field{Name: "OPTED", Type: encoding.FieldTypePackedBool},
			0, Format{Code: fmtF, Width: 1}, measureNominal, EncodeNumeric},
		{"u4", encoding.Field{Name: "GRADE", Type: encoding.FieldTypeU4},
			0, Format{Code: fmtF, Width: 2}, measureScale, EncodeNumeric},
		{"u8", encoding.Field{Name: "AGE", Type: encoding.FieldTypeU8},
			0, Format{Code: fmtF, Width: 3}, measureScale, EncodeNumeric},
		{"u16", encoding.Field{Name: "COUNT", Type: encoding.FieldTypeU16},
			0, Format{Code: fmtF, Width: 5}, measureScale, EncodeNumeric},
		{"u32", encoding.Field{Name: "ID", Type: encoding.FieldTypeU32},
			0, Format{Code: fmtF, Width: 10}, measureScale, EncodeNumeric},
		{"u64", encoding.Field{Name: "BIG", Type: encoding.FieldTypeU64},
			0, Format{Code: fmtF, Width: 20}, measureScale, EncodeNumeric},
		{"f32", encoding.Field{Name: "RATE", Type: encoding.FieldTypeF32},
			0, Format{Code: fmtF, Width: 8, Decimals: 2}, measureScale, EncodeNumeric},
		{"f64", encoding.Field{Name: "INCOME", Type: encoding.FieldTypeF64},
			0, Format{Code: fmtF, Width: 8, Decimals: 2}, measureScale, EncodeNumeric},
		{"decimal128 keeps its scale", encoding.Field{Name: "AMOUNT", Type: encoding.FieldTypeDecimal128, Precision: 12, Scale: 4},
			0, Format{Code: fmtF, Width: 14, Decimals: 4}, measureScale, EncodeNumeric},
		{"date", encoding.Field{Name: "JOINED", Type: encoding.FieldTypeDate},
			0, Format{Code: fmtDATE, Width: 11}, measureScale, EncodeDateDays},
		{"datetime", encoding.Field{Name: "SEEN", Type: encoding.FieldTypeDateTime},
			0, Format{Code: fmtDATETIME, Width: 20}, measureScale, EncodeDateTimeSeconds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := synthesise(t, tc.field)
			col := planColumn(t, plan, tc.field.Name)
			if col.Width != tc.wantWidth {
				t.Errorf("width = %d, want %d", col.Width, tc.wantWidth)
			}
			if col.PrintFormat != tc.wantPrint {
				t.Errorf("print format = %+v, want %+v", col.PrintFormat, tc.wantPrint)
			}
			if col.WriteFormat != tc.wantPrint {
				t.Errorf("write format = %+v, want it to match print %+v", col.WriteFormat, tc.wantPrint)
			}
			if col.Encoding != tc.wantEnc {
				t.Errorf("encoding = %v, want %v", col.Encoding, tc.wantEnc)
			}

			d := reparse(t, plan)
			for _, w := range d.warnings {
				t.Errorf("re-parsing warned: %v", w)
			}
			v := readVar(t, d, tc.field.Name)
			if v.print != (format{code: tc.wantPrint.Code, width: tc.wantPrint.Width, decimals: tc.wantPrint.Decimals}) {
				t.Errorf("re-parsed print format = %+v, want %+v", v.print, tc.wantPrint)
			}
			if v.display.measure != tc.wantMeas {
				t.Errorf("measure level = %v, want %v", v.display.measure, tc.wantMeas)
			}
		})
	}
}

// TestSynthesise_TemporalFormatsSurviveTheRoundTrip closes the loop on the
// two temporal types: an emitted DATE must come back as a `date` and an
// emitted DATETIME as a `datetime`, or the type has been quietly widened.
func TestSynthesise_TemporalFormatsSurviveTheRoundTrip(t *testing.T) {
	plan := synthesise(t,
		encoding.Field{Name: "JOINED", Type: encoding.FieldTypeDate},
		encoding.Field{Name: "SEEN", Type: encoding.FieldTypeDateTime},
	)
	d := reparse(t, plan)
	for _, tc := range []struct {
		name string
		want columnKind
	}{
		{"JOINED", kindDate},
		{"SEEN", kindDateTime},
	} {
		if got := classify(readVar(t, d, tc.name), false); got != tc.want {
			t.Errorf("%s re-imports as %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// The fidelity rule: no invented codes
// ---------------------------------------------------------------------------

// TestSynthesise_CategoricalBecomesAStringNeverInventedCodes is the
// synthesis path's central decision.
//
// A cohort with no sidecar holds dictionary TEXT and no SPSS codes. Emitting
// the text as a string variable states exactly what is known. Emitting a
// numeric variable with codes taken from the dictionary POSITIONS would look
// far more like an SPSS file and would be a fabrication: SPSS syntax
// addresses values, so `IF region EQ 1` would then mean whatever happened to
// sort first at import time.
func TestSynthesise_CategoricalBecomesAStringNeverInventedCodes(t *testing.T) {
	plan := synthesise(t, encoding.Field{
		Name: "REGION", Type: encoding.FieldTypeCategoricalU16,
		Dictionary: dictOf(t, "north", "south", "east-midlands"),
	})
	col := planColumn(t, plan, "REGION")

	if col.Encoding != EncodeText {
		t.Fatalf("encoding = %v, want %v — a categorical with no recorded codes is a string", col.Encoding, EncodeText)
	}
	if col.Width != len("east-midlands") {
		t.Errorf("declared width = %d, want %d (the widest entry, in BYTES)", col.Width, len("east-midlands"))
	}
	want := []string{"north", "south", "east-midlands"}
	if len(col.Categories) != len(want) {
		t.Fatalf("plan carries %d categor(ies), want %d", len(col.Categories), len(want))
	}
	for id, w := range want {
		got := col.Categories[id]
		if got.Text != w {
			t.Errorf("dictionary ID %d carries %q, want %q", id, got.Text, w)
		}
		if got.Known {
			t.Errorf("dictionary ID %d is marked Known; nothing recorded an SPSS code for it, and the plan must say so", id)
		}
		if got.Code != 0 {
			t.Errorf("dictionary ID %d carries the numeric code %v; a synthesised categorical has no code at all", id, got.Code)
		}
	}

	d := reparse(t, plan)
	v := readVar(t, d, "REGION")
	if !v.isString() {
		t.Error("the emitted variable is numeric; a positional code reached the wire")
	}
	if len(d.valueLabels) != 0 {
		t.Errorf("%d value-label set(s) were emitted; a synthesised dictionary declares no labels", len(d.valueLabels))
	}
}

// TestSynthesise_StringFormatWidthIsNotTheNumericCeiling is a regression
// guard on a real bug this test caught.
//
// SPSS caps an F format at 40 characters, and applying that ceiling to an A
// format silently declares A40 over a 100-byte variable. Nothing rejects
// such a file — the DATA is still 100 bytes wide, because the record type 2
// `type` field is what declares the width — but every reader that renders
// from the print format shows a truncated value, which is precisely the
// quiet degradation the fidelity mandate rules out. The ceilings are
// separate because the format types are.
func TestSynthesise_StringFormatWidthIsNotTheNumericCeiling(t *testing.T) {
	plan := synthesise(t, encoding.Field{
		Name: "NOTE", Type: encoding.FieldTypeCategoricalU8,
		Dictionary: dictOf(t, strings.Repeat("n", 100)),
	})
	col := planColumn(t, plan, "NOTE")
	if col.PrintFormat != (Format{Code: fmtA, Width: 100}) {
		t.Errorf("print format = %+v, want A100 — an A format's width IS the declared byte width", col.PrintFormat)
	}
	if got := readVar(t, reparse(t, plan), "NOTE"); got.print.width != 100 || got.width != 100 {
		t.Errorf("re-parsed as print width %d over a %d-byte variable, want both 100", got.print.width, got.width)
	}
}

// TestSynthesise_WideDictionaryEntriesSegment checks that a categorical
// whose entries pass 255 bytes is laid out as a very long string rather than
// truncated. Truncating a value is the one outcome the fidelity mandate
// rules out entirely.
func TestSynthesise_WideDictionaryEntriesSegment(t *testing.T) {
	long := strings.Repeat("x", 600)
	plan := synthesise(t, encoding.Field{
		Name: "NOTES", Type: encoding.FieldTypeCategoricalU8,
		Dictionary: dictOf(t, "short", long),
	})
	col := planColumn(t, plan, "NOTES")
	if col.Width != 600 {
		t.Fatalf("declared width = %d, want the full 600", col.Width)
	}
	if len(col.Segments) != 3 {
		t.Fatalf("planned %d physical segment(s), want 3 (252 + 252 + 96)", len(col.Segments))
	}
	total := 0
	names := map[string]bool{}
	for i, s := range col.Segments {
		total += s.Content
		if names[s.Name] {
			t.Errorf("segment %d re-uses the short name %q; trailing segments occupy the same 8-byte name space as every other variable", i, s.Name)
		}
		names[s.Name] = true
		if len(s.Name) > shortNameLen {
			t.Errorf("segment %d minted the %d-byte name %q", i, len(s.Name), s.Name)
		}
	}
	if total != 600 {
		t.Errorf("the segments carry %d content byte(s) between them, want 600", total)
	}

	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}
	if v := readVar(t, d, "NOTES"); v.vls == nil || v.width != 600 {
		t.Errorf("the re-parsed variable is width %d with vls %v, want a folded 600-byte very long string", v.width, v.vls != nil)
	}

	// Each trailing segment declares its OWN A format, derived from its
	// own declared width — A255 for a full segment and A<remainder> for
	// the last. That is what SPSS writes, and it is what the independent
	// fixture generator derives from the same specification.
	wantSegmentFormats := []int{255, 255, 96}
	for i, want := range wantSegmentFormats {
		if got := col.Segments[i].Width; got != want {
			t.Errorf("segment %d declares width %d, want %d", i, got, want)
		}
	}
	for i, seg := range physicalRecords(t, plan.Bytes) {
		if i >= len(wantSegmentFormats) {
			break
		}
		want := format{code: fmtA, width: wantSegmentFormats[i]}
		if seg != want {
			t.Errorf("physical segment %d writes the print format %+v, want %+v", i, seg, want)
		}
	}
}

// physicalRecords decodes the print format of every non-continuation record
// type 2 in an emitted dictionary, in file order.
func physicalRecords(t *testing.T, b []byte) []format {
	t.Helper()
	var out []format
	off := headerSize
	for off+32 <= len(b) {
		rt := int32(binary.LittleEndian.Uint32(b[off:]))
		if rt != recTypeVariable {
			break
		}
		typeCode := int32(binary.LittleEndian.Uint32(b[off+4:]))
		hasLabel := int32(binary.LittleEndian.Uint32(b[off+8:]))
		nMissing := int32(binary.LittleEndian.Uint32(b[off+12:]))
		if typeCode != typeStringContinuation {
			out = append(out, unpackFormat(int32(binary.LittleEndian.Uint32(b[off+16:]))))
		}
		at := off + 32
		if hasLabel == 1 {
			n := int(binary.LittleEndian.Uint32(b[at:]))
			at += 4 + roundUp(n, 4)
		}
		if nMissing < 0 {
			nMissing = -nMissing
		}
		off = at + int(nMissing)*elementSize
	}
	return out
}

// ---------------------------------------------------------------------------
// Sets
// ---------------------------------------------------------------------------

// TestSynthesise_SetExpandsToDichotomyMembers checks the one place
// synthesis expands rather than narrows.
//
// SPSS has no set type but it has the shape a set came from: N indicator
// variables plus a multiple-dichotomy definition naming them. The member's
// name is its dictionary entry, because the import derives a set's dictionary
// from its members' names — so entry order is bit order is member order and
// the mask round-trips.
func TestSynthesise_SetExpandsToDichotomyMembers(t *testing.T) {
	plan := synthesise(t, encoding.Field{
		Name: "media", Type: encoding.FieldTypeSetU8,
		Dictionary: dictOf(t, "tv", "radio", "web"), Description: "Media consumed",
	})

	if len(plan.Columns) != 3 {
		t.Fatalf("emitted %d variable(s), want one per set element", len(plan.Columns))
	}
	for bit, name := range []string{"tv", "radio", "web"} {
		col := planColumn(t, plan, name)
		if col.Encoding != EncodeSetMember {
			t.Errorf("%s encodes as %v, want %v", name, col.Encoding, EncodeSetMember)
		}
		if col.SetBit != bit {
			t.Errorf("%s stands for bit %d, want %d — entry order IS bit order", name, col.SetBit, bit)
		}
		if col.CountedValue != synthSetCountedValue {
			t.Errorf("%s counted value = %v, want %d", name, col.CountedValue, synthSetCountedValue)
		}
		if col.Field != 0 {
			t.Errorf("%s binds cohort field %d, want 0 — every member reads the one set column", name, col.Field)
		}
	}

	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}
	if len(d.mrSets) != 1 {
		t.Fatalf("re-parsed %d response set(s), want 1", len(d.mrSets))
	}
	set, ok := d.mrSets[0].(*mrDichotomySet)
	if !ok {
		t.Fatalf("the set came back as %T, want a dichotomy", d.mrSets[0])
	}
	if set.name != "$media" {
		t.Errorf("set name = %q, want %q — SPSS requires the leading '$'", set.name, "$media")
	}
	if set.label != "Media consumed" {
		t.Errorf("set label = %q, want the cohort field's description", set.label)
	}
	if set.countedValue != strconv.Itoa(synthSetCountedValue) {
		t.Errorf("counted value = %q, want %q", set.countedValue, strconv.Itoa(synthSetCountedValue))
	}
	if got := strings.Join(set.vars, ","); got != "TV,RADIO,WEB" {
		t.Errorf("members = %q, want the three minted short names in bit order", got)
	}
}

// TestSynthesise_SetWithNoDictionaryIsRefused: a set column with an empty
// dictionary has no member for a response set to name, and there is no
// honest thing to emit.
func TestSynthesise_SetWithNoDictionaryIsRefused(t *testing.T) {
	_, err := BuildDictionary(DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "media", Type: encoding.FieldTypeSetU8, Dictionary: encoding.NewDictionary()},
		}},
		Cases: 0,
	})
	if got := codeOf(t, err); got != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want PULSE_SPSS_EXPORT_UNSUPPORTED", got)
	}
}

// ---------------------------------------------------------------------------
// Names
// ---------------------------------------------------------------------------

// TestSynthesise_NamesAreMintedAndTheRealNameSurvives checks the short-name
// minting rules and, more importantly, that the real Pulse name always comes
// back.
//
// The 8-byte record type 2 name is upper-cased by construction, so WITHOUT a
// record 7/13 entry a field called `age` would return as `AGE`. A round trip
// that changes a field's name has broken every request referencing it, so a
// long name is emitted whenever the two differ at all — case included.
func TestSynthesise_NamesAreMintedAndTheRealNameSurvives(t *testing.T) {
	fields := []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeU8},
		{Name: "household.income", Type: encoding.FieldTypeF64},
		{Name: "total_2024", Type: encoding.FieldTypeF64},
		{Name: "ALLCAPS", Type: encoding.FieldTypeU8},
		{Name: "respondent_number_one", Type: encoding.FieldTypeU32},
		{Name: "respondent_number_two", Type: encoding.FieldTypeU32},
	}
	plan := synthesise(t, fields...)

	shorts := map[string]bool{}
	for _, c := range plan.Columns {
		if len(c.ShortName) > shortNameLen {
			t.Errorf("%q minted the %d-byte short name %q, over the 8-byte record type 2 field",
				c.Name, len(c.ShortName), c.ShortName)
		}
		if c.ShortName == "" {
			t.Errorf("%q minted an empty short name", c.Name)
		}
		if first := c.ShortName[0]; !(first >= 'A' && first <= 'Z') && first != '@' && first != '#' && first != '$' {
			t.Errorf("%q minted the short name %q, which does not open with a letter", c.Name, c.ShortName)
		}
		if shorts[c.ShortName] {
			t.Errorf("the short name %q was minted twice; the two variables would collide", c.ShortName)
		}
		shorts[c.ShortName] = true
	}

	// The two long names sharing their first eight bytes must still get
	// distinct short names.
	if a, b := planColumn(t, plan, "respondent_number_one"), planColumn(t, plan, "respondent_number_two"); a.ShortName == b.ShortName {
		t.Errorf("both respondent_number_* columns minted %q", a.ShortName)
	}

	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}
	if len(d.vars) != len(fields) {
		t.Fatalf("re-parsed %d variable(s), want %d", len(d.vars), len(fields))
	}
	for i, f := range fields {
		if got := d.vars[i].fieldName(); got != f.Name {
			t.Errorf("field %q came back as %q; a round trip must not rename a column", f.Name, got)
		}
	}
	// ALLCAPS is already a legal short name, so it needs no 7/13 entry —
	// the record carries only what actually differs.
	if got := readVar(t, d, "ALLCAPS").longName; got != "" {
		t.Errorf("ALLCAPS declared the long name %q; short and long agree, so the entry is noise", got)
	}
	if got := readVar(t, d, "age").longName; got != "age" {
		t.Errorf("age declared the long name %q, want %q — without it the field returns upper-cased", got, "age")
	}
}

// TestSynthesise_UnrepresentableNamesAreRefused covers the names a `.sav`
// cannot carry, which since E5-S5 is a name-validation refusal rather than
// the generic "cannot be expressed" code. dict_names_test.go owns the rule
// itself; this checks that the synthesis front-end is behind it.
func TestSynthesise_UnrepresentableNamesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
	}{
		{"an '=' is the record 7/13 delimiter", "gross=net"},
		{"a tab is the record 7/13 separator", "gross\tnet"},
		{"a space splits a record 7/7 member list", "household income"},
		{"a leading digit cannot open an SPSS name", "2024_total"},
		{"past the 64-byte SPSS name ceiling", strings.Repeat("a", 65)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildDictionary(DictionaryRequest{
				Schema: &encoding.Schema{Fields: []encoding.Field{{Name: tc.field, Type: encoding.FieldTypeF64}}},
				Cases:  0,
			})
			if got := codeOf(t, err); got != perr.PULSE_SPSS_NAME_INVALID {
				t.Errorf("code = %s, want PULSE_SPSS_NAME_INVALID", got)
			}
		})
	}
}

// TestSynthesise_CollidingFinalNamesAreRefused covers the collision this
// file can CREATE: a set member named for its dictionary entry landing on a
// name some other column already has.
//
// Two variables answering to one name is not survivable — record 7/13 drops
// the second silently and the file then holds a column no name reaches — so
// it is a refusal rather than a rename. A silent rename would be the quiet
// kind of wrong the whole effort exists to avoid.
func TestSynthesise_CollidingFinalNamesAreRefused(t *testing.T) {
	_, err := BuildDictionary(DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "tv", Type: encoding.FieldTypeU8},
			{Name: "media", Type: encoding.FieldTypeSetU8, Dictionary: dictOf(t, "tv", "radio")},
		}},
		Cases: 0,
	})
	if got := codeOf(t, err); got != perr.PULSE_SPSS_NAME_COLLISION {
		t.Errorf("code = %s, want PULSE_SPSS_NAME_COLLISION", got)
	}
}

// ---------------------------------------------------------------------------
// The whole synthesised file
// ---------------------------------------------------------------------------

// TestSynthesise_MixedCohortOpens is the acceptance criterion in one shot: a
// cohort that was never SPSS-derived produces a dictionary that parses.
func TestSynthesise_MixedCohortOpens(t *testing.T) {
	plan := synthesise(t,
		encoding.Field{Name: "respondent_id", Type: encoding.FieldTypeU32, Description: "Respondent identifier"},
		encoding.Field{Name: "age", Type: encoding.FieldTypeU8, Nullable: true},
		encoding.Field{Name: "income", Type: encoding.FieldTypeF64, Description: "Household income"},
		encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dictOf(t, "north", "south")},
		encoding.Field{Name: "signed_up", Type: encoding.FieldTypeDate},
		encoding.Field{Name: "last_seen", Type: encoding.FieldTypeDateTime},
		encoding.Field{Name: "media", Type: encoding.FieldTypeSetU8, Dictionary: dictOf(t, "tv", "web")},
	)
	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}
	if len(d.vars) != 8 {
		t.Fatalf("re-parsed %d variable(s), want 8 (six plain columns plus two set members)", len(d.vars))
	}
	if d.elementCount != plan.ElementCount {
		t.Errorf("re-parsed element count %d, but the plan promised the data encoder %d", d.elementCount, plan.ElementCount)
	}
	if len(plan.UnboundFields) != 0 {
		t.Errorf("synthesis left cohort fields unbound: %v; every field must reach the file", plan.UnboundFields)
	}
	if !d.hasDisplayParams {
		t.Error("no record 7/11 was emitted; every synthesised variable states a measure level")
	}
}

// TestSynthesise_ElementIndicesCountElementsNotVariables pins the indexing
// rule every index-bearing field in the format depends on. A wide string
// occupies several elements, so the variable after it does not have its
// ordinal position as its index.
func TestSynthesise_ElementIndicesCountElementsNotVariables(t *testing.T) {
	plan := synthesise(t,
		encoding.Field{Name: "ID", Type: encoding.FieldTypeU32},
		encoding.Field{Name: "NOTE", Type: encoding.FieldTypeCategoricalU8,
			Dictionary: dictOf(t, strings.Repeat("n", 20))},
		encoding.Field{Name: "AGE", Type: encoding.FieldTypeU8},
	)
	want := map[string]int32{"ID": 1, "NOTE": 2, "AGE": 5}
	for name, idx := range want {
		if got := planColumn(t, plan, name).Index; got != idx {
			t.Errorf("%s is at element index %d, want %d", name, got, idx)
		}
	}
	if plan.ElementCount != 5 {
		t.Errorf("nominal_case_size = %d, want 5 (1 + ceil(20/8) + 1)", plan.ElementCount)
	}
}
