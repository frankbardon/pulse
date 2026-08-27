package spss

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// longStringSpec is a two-variable fixture: a numeric key and one very long
// string of the given logical width, carrying the given values.
func longStringSpec(width int, values ...string) spsstest.Spec {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "COMMENT", Width: width, LongName: "Comments", Label: "Free text"},
		},
		CharacterEncoding: "UTF-8",
	}
	for i, v := range values {
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i + 1)), spsstest.Text(v),
		})
	}
	return spec
}

func buildFixture(t *testing.T, spec spsstest.Spec) []byte {
	t.Helper()
	b, err := spsstest.Build(spec)
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	return b
}

// readFixture reads a fixture end to end and returns its header, rows and
// warnings. It is readAll plus the header and the diagnostics, which is what
// every test in this file needs together.
func readFixture(t *testing.T, b []byte, opts ...Option) ([]string, [][]string, []*errors.CodedError) {
	t.Helper()
	r := NewReaderFromBytes(b, opts...)
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	return header, readAll(t, r), r.Warnings()
}

func hasCode(warnings []*errors.CodedError, code errors.Code) bool {
	for _, w := range warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func warningText(warnings []*errors.CodedError) string {
	var b strings.Builder
	for _, w := range warnings {
		b.WriteString(string(w.Code))
		b.WriteString(": ")
		b.WriteString(w.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// The segmentation arithmetic
// ---------------------------------------------------------------------------

// TestVLS_SegmentMath pins the two-layer arithmetic in one place.
//
// The width-256 row is what decides the scheme. Its segments DECLARE 255 and
// 4 bytes, summing to 259 — three MORE than the variable's own declared
// width. Only a 252-byte content stride reproduces a 256-byte value, which is
// why the divisor is 252 and never the 255 a segment declares.
func TestVLS_SegmentMath(t *testing.T) {
	cases := []struct {
		width    int
		count    int
		widths   []int
		contents []int
		elements int
	}{
		{255, 1, []int{255}, []int{255}, 32},
		{256, 2, []int{255, 4}, []int{252, 4}, 33},
		{504, 2, []int{255, 252}, []int{252, 252}, 64},
		{505, 3, []int{255, 255, 1}, []int{252, 252, 1}, 65},
		{600, 3, []int{255, 255, 96}, []int{252, 252, 96}, 76},
		{32767, 131, nil, nil, 0},
	}
	for _, tc := range cases {
		name := strconv.Itoa(tc.width)
		t.Run(name, func(t *testing.T) {
			if got := vlsSegmentCount(tc.width); got != tc.count {
				t.Fatalf("vlsSegmentCount(%d) = %d, want %d", tc.width, got, tc.count)
			}
			sum, elems := 0, 0
			for i := 0; i < tc.count; i++ {
				w := vlsSegmentWidth(tc.width, i)
				c := vlsSegmentContent(tc.width, i)
				if tc.widths != nil && w != tc.widths[i] {
					t.Errorf("segment %d declared width = %d, want %d", i, w, tc.widths[i])
				}
				if tc.contents != nil && c != tc.contents[i] {
					t.Errorf("segment %d content = %d, want %d", i, c, tc.contents[i])
				}
				if w < 1 || w > maxSegmentWidth {
					t.Errorf("segment %d declared width %d cannot ride a record type 2 type field", i, w)
				}
				sum += c
				elems += roundUp(w, elementSize) / elementSize
			}
			if sum != tc.width {
				t.Errorf("segment contents sum to %d, want the logical width %d", sum, tc.width)
			}
			if tc.elements != 0 && elems != tc.elements {
				t.Errorf("the segments occupy %d element(s), want %d", elems, tc.elements)
			}
		})
	}
}

// TestVLS_SegmentWidthTolerance documents which declared widths a fold
// accepts. A non-final segment may declare anything in 252..255 — every one
// of those rounds up to the same 256-byte element span and carries the same
// 252 content bytes — while the last segment is exact, because it is the only
// one that says where the value ends.
func TestVLS_SegmentWidthTolerance(t *testing.T) {
	for _, declared := range []int{251, 252, 253, 255, 256} {
		want := declared >= 252 && declared <= 255
		if got := vlsSegmentWidthOK(600, 0, declared); got != want {
			t.Errorf("vlsSegmentWidthOK(600, 0, %d) = %v, want %v", declared, got, want)
		}
	}
	for _, declared := range []int{95, 96, 97} {
		want := declared == 96
		if got := vlsSegmentWidthOK(600, 2, declared); got != want {
			t.Errorf("vlsSegmentWidthOK(600, 2, %d) = %v, want %v", declared, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Round trip
// ---------------------------------------------------------------------------

// TestVLS_RoundTrip is the story's headline criterion: a long-string fixture
// reads back with the value intact and no segment boundaries visible.
func TestVLS_RoundTrip(t *testing.T) {
	values := []string{
		strings.Repeat("abcdefghij", 60), // exactly the full 600 bytes
		"short",                          // far under one segment
		strings.Repeat("x", 252),         // exactly one segment's content
		strings.Repeat("y", 253),         // one byte into segment two
		strings.Repeat("z", 251) + "  " + strings.Repeat("w", 100), // spaces ON the boundary
		"", // all padding
	}
	header, rows, warnings := readFixture(t, buildFixture(t, longStringSpec(600, values...)))

	if want := []string{"ID", "Comments"}; len(header) != 2 || header[0] != want[0] || header[1] != want[1] {
		t.Fatalf("ReadHeader = %v, want %v; the physical segments must not surface as columns", header, want)
	}
	if len(rows) != len(values) {
		t.Fatalf("read %d row(s), want %d", len(rows), len(values))
	}
	for i, want := range values {
		// Trailing spaces are padding on the wire and are trimmed on read,
		// which is the same rule every other string variable follows.
		want = strings.TrimRight(want, " ")
		if rows[i][1] != want {
			t.Errorf("row %d: got %d byte(s) %q, want %d byte(s) %q",
				i, len(rows[i][1]), truncate(rows[i][1]), len(want), truncate(want))
		}
	}
	if hasCode(warnings, errors.PULSE_SPSS_EXTENSION_UNKNOWN) {
		t.Errorf("record 7/14 still warns as an unrecognised subtype:\n%s", warningText(warnings))
	}
}

// TestVLS_RoundTripWidths sweeps the widths where the segmentation changes
// shape: the first width that needs two segments, the last that needs two,
// the first that needs three, and the widest string SPSS supports.
func TestVLS_RoundTripWidths(t *testing.T) {
	for _, width := range []int{256, 300, 504, 505, 756, 1000, 32767} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			// A value whose every byte is distinguishable by position, so a
			// shifted or duplicated segment cannot pass.
			var b strings.Builder
			for i := 0; i < width; i++ {
				b.WriteByte(byte('!' + i%90))
			}
			value := b.String()

			_, rows, _ := readFixture(t, buildFixture(t, longStringSpec(width, value)))
			got := rows[0][1]
			want := strings.TrimRight(value, " ")
			if got != want {
				at := -1
				for i := 0; i < len(got) && i < len(want); i++ {
					if got[i] != want[i] {
						at = i
						break
					}
				}
				t.Fatalf("width %d: got %d byte(s), want %d; first difference at byte %d", width, len(got), len(want), at)
			}
		})
	}
}

// TestVLS_MultibyteStraddlesSegmentBoundary is the fidelity case the whole
// reassemble-then-decode ordering exists for.
//
// The value places a two-byte UTF-8 character so its bytes fall at logical
// offsets 251 and 252 — one on each side of the 252-byte segment boundary. A
// reader that decoded each segment separately would see a truncated sequence
// at the end of segment one and a stray continuation byte at the start of
// segment two, and would either fail or substitute U+FFFD. Joining raw bytes
// first makes the character whole again.
func TestVLS_MultibyteStraddlesSegmentBoundary(t *testing.T) {
	cases := []struct {
		name  string
		lead  int
		runes string
	}{
		{"two-byte character split 1/1", 251, "é"},
		{"three-byte character split 1/2", 251, "€"},
		{"three-byte character split 2/1", 250, "€"},
		{"four-byte character split 3/1", 249, "😀"},
		{"four-byte character split 1/3", 251, "😀"},
		{"character exactly after the boundary", 252, "é"},
		{"character exactly before the boundary", 250, "é"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := strings.Repeat("x", tc.lead) + tc.runes + strings.Repeat("y", 40)
			if len(value) <= segmentContentWidth {
				t.Fatalf("fixture does not reach the boundary: %d bytes", len(value))
			}
			_, rows, _ := readFixture(t, buildFixture(t, longStringSpec(600, value)))
			got := rows[0][1]
			if got != value {
				t.Fatalf("got %d byte(s) %q, want %d byte(s) %q", len(got), truncate(got), len(value), truncate(value))
			}
			if !utf8.ValidString(got) {
				t.Fatal("the reassembled value is not valid UTF-8; a segment boundary cut a character")
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Fatal("the reassembled value carries U+FFFD; a decode substituted rather than joined")
			}
		})
	}
}

// TestVLS_MultibyteAcrossCodepages runs the same straddle through a
// single-byte codepage, where the boundary can never split a character, and
// through UTF-8, where it can. Both must read back verbatim.
func TestVLS_MultibyteAcrossCodepages(t *testing.T) {
	value := strings.Repeat("x", 251) + "üé" + strings.Repeat("y", 40)
	for _, charset := range []string{"UTF-8", "windows-1252"} {
		t.Run(charset, func(t *testing.T) {
			spec := longStringSpec(600, value)
			spec.CharacterEncoding = charset
			_, rows, _ := readFixture(t, buildFixture(t, spec))
			if rows[0][1] != value {
				t.Fatalf("%s: got %q, want %q", charset, truncate(rows[0][1]), truncate(value))
			}
		})
	}
}

// TestVLS_Compression proves the fold is orthogonal to the data-section
// encoding: the same logical content produces the same cohort whether the
// case bytes arrived uncompressed, bytecode-compressed or through ZSAV.
func TestVLS_Compression(t *testing.T) {
	value := strings.Repeat("abcdefghij", 40) + "é" + strings.Repeat("k", 100)
	var want [][]string
	for _, c := range []spsstest.Compression{
		spsstest.CompressionNone, spsstest.CompressionBytecode, spsstest.CompressionZSAV,
	} {
		t.Run(c.String(), func(t *testing.T) {
			spec := longStringSpec(600, value, "short")
			spec.Compression = c
			_, rows, _ := readFixture(t, buildFixture(t, spec))
			if want == nil {
				want = rows
				if rows[0][1] != value {
					t.Fatalf("got %q, want %q", truncate(rows[0][1]), truncate(value))
				}
				return
			}
			if len(rows) != len(want) {
				t.Fatalf("read %d row(s), want %d", len(rows), len(want))
			}
			for i := range rows {
				for j := range rows[i] {
					if rows[i][j] != want[i][j] {
						t.Fatalf("row %d column %d differs from the uncompressed read", i, j)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Retention for the write path
// ---------------------------------------------------------------------------

// TestVLS_LayoutRetained is the "retained for re-segmentation on write"
// criterion. E5-S4 reads exactly these fields.
func TestVLS_LayoutRetained(t *testing.T) {
	b := buildFixture(t, longStringSpec(600, "hello"))
	r := NewReaderFromBytes(b)
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}
	if len(m.cols) != 2 {
		t.Fatalf("got %d column(s), want 2", len(m.cols))
	}
	col := m.cols[1]

	// The retained width is the LOGICAL total, not the 255 a segment
	// declares. An export re-pads to this.
	if col.declaredWidth != 600 {
		t.Errorf("declaredWidth = %d, want the logical total 600", col.declaredWidth)
	}
	if col.vls == nil {
		t.Fatal("the very-long-string layout was discarded after reassembly")
	}
	if col.vls.width != 600 {
		t.Errorf("vls.width = %d, want 600", col.vls.width)
	}
	want := []vlsSegment{
		{name: "COMMENT", width: 255, content: 252, elements: 32},
		{name: "COMMENT0", width: 255, content: 252, elements: 32},
		{name: "COMMENT1", width: 96, content: 96, elements: 12},
	}
	if len(col.vls.segments) != len(want) {
		t.Fatalf("retained %d segment(s), want %d", len(col.vls.segments), len(want))
	}
	for i, w := range want {
		if col.vls.segments[i] != w {
			t.Errorf("segment %d = %+v, want %+v", i, col.vls.segments[i], w)
		}
	}
	if got := col.vls.elements(); got != 76 {
		t.Errorf("vls.elements() = %d, want 76", got)
	}

	// The record 7/14 declaration itself survives the fold.
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	if len(d.veryLongStrings) != 1 || d.veryLongStrings[0].name != "COMMENT" || d.veryLongStrings[0].width != 600 {
		t.Errorf("veryLongStrings = %+v, want one COMMENT=600 declaration", d.veryLongStrings)
	}
}

// TestVLS_SchemaIsOneField checks the folded variable reaches the .pulse
// schema as one field with one dictionary, not as N segment fields.
func TestVLS_SchemaIsOneField(t *testing.T) {
	b := buildFixture(t, longStringSpec(600, strings.Repeat("q", 400), "short"))
	r := NewReaderFromBytes(b)
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	if len(schema.Fields) != 2 {
		names := make([]string, len(schema.Fields))
		for i, f := range schema.Fields {
			names[i] = f.Name
		}
		t.Fatalf("schema has %d field(s) %v, want 2", len(schema.Fields), names)
	}
	f := schema.Fields[1]
	if f.Name != "Comments" {
		t.Errorf("field name = %q, want %q", f.Name, "Comments")
	}
	if f.Type != encoding.FieldTypeCategoricalU8 {
		t.Errorf("field type = %s, want %s", f.Type, encoding.FieldTypeCategoricalU8)
	}
	if f.Description != "Free text" {
		t.Errorf("description = %q, want the head segment's variable label", f.Description)
	}
	if f.Dictionary == nil || f.Dictionary.Count() != 2 {
		t.Fatalf("dictionary = %v, want the two distinct values", f.Dictionary)
	}
	if got := f.Dictionary.Resolve(0); got != strings.Repeat("q", 400) {
		t.Errorf("dictionary entry 0 is %d byte(s), want the full 400-byte value", len(got))
	}
}

// TestVLS_CardinalityWarningFires confirms E2-S6's schema-bloat signal still
// reaches a very long string. A near-unique 600-byte free-text field is the
// exact case the warning exists for, and it would be easy for the fold to
// bypass the mapping's stats pass without anyone noticing.
func TestVLS_CardinalityWarningFires(t *testing.T) {
	values := make([]string, 120)
	for i := range values {
		values[i] = "response " + strconv.Itoa(i) + " " + strings.Repeat("z", 300)
	}
	_, _, warnings := readFixture(t, buildFixture(t, longStringSpec(600, values...)))
	if !hasCode(warnings, errors.PULSE_SPSS_CARDINALITY_HIGH) {
		t.Fatalf("no PULSE_SPSS_CARDINALITY_HIGH for 120 distinct 600-byte values:\n%s", warningText(warnings))
	}
}

// ---------------------------------------------------------------------------
// Record 7/21 — long string value labels
// ---------------------------------------------------------------------------

// TestLongStringValueLabels_Bound is the 7/21 acceptance criterion: the
// labels parse and bind to the right variable, in the right order, whether
// the entry names the variable by its long name or its short one.
func TestLongStringValueLabels_Bound(t *testing.T) {
	for _, ref := range []string{"Comments", "COMMENT", "comments"} {
		t.Run(ref, func(t *testing.T) {
			spec := longStringSpec(600, strings.Repeat("a", 300), "declined")
			spec.LongStringValueLabels = []spsstest.LongStringValueLabels{{
				Var: ref,
				Labels: []spsstest.LongStringValueLabel{
					{Value: "declined", Label: "Declined to answer"},
					{Value: "unused", Label: "A label no case carries"},
				},
			}}
			b := buildFixture(t, spec)
			r := NewReaderFromBytes(b)
			m, err := r.loadMapping()
			if err != nil {
				t.Fatalf("loadMapping: %v", err)
			}
			col := m.cols[1]

			// Declared labels come first, in record order, because entry
			// order IS the on-wire encoding — exactly as records 3/4 behave.
			want := []struct {
				value    string
				label    string
				labelled bool
				observed bool
			}{
				{"declined", "Declined to answer", true, true},
				{"unused", "A label no case carries", true, false},
				{strings.Repeat("a", 300), "", false, true},
			}
			if len(col.categories) != len(want) {
				t.Fatalf("got %d categor(ies), want %d", len(col.categories), len(want))
			}
			for i, w := range want {
				got := col.categories[i]
				if got.value != w.value || got.label != w.label ||
					got.labelled != w.labelled || got.observed != w.observed {
					t.Errorf("category %d = {value:%q label:%q labelled:%v observed:%v}, want {%q %q %v %v}",
						i, truncate(got.value), got.label, got.labelled, got.observed,
						truncate(w.value), w.label, w.labelled, w.observed)
				}
				if got.id != uint32(i) {
					t.Errorf("category %d has id %d", i, got.id)
				}
			}
			if !hasCode(r.Warnings(), errors.PULSE_SPSS_EXTENSION_UNKNOWN) {
				return
			}
			t.Errorf("record 7/21 still warns as an unrecognised subtype:\n%s", warningText(r.Warnings()))
		})
	}
}

// TestLongStringValueLabels_LabelsAreCharsetDecoded checks the 7/21 label
// text goes through the same charset pass every other label does, and that
// the VALUE does not — a value is a datum and is trimmed on raw bytes first,
// so that it can still compare equal to the cell that carries it.
func TestLongStringValueLabels_LabelsAreCharsetDecoded(t *testing.T) {
	spec := longStringSpec(600, "café")
	spec.CharacterEncoding = "windows-1252"
	spec.LongStringValueLabels = []spsstest.LongStringValueLabels{{
		Var:    "Comments",
		Labels: []spsstest.LongStringValueLabel{{Value: "café", Label: "Küche"}},
	}}
	b := buildFixture(t, spec)
	r := NewReaderFromBytes(b)
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}
	col := m.cols[1]
	if len(col.categories) != 1 {
		t.Fatalf("got %d categor(ies), want 1; the declared label and the datum did not resolve to one entry", len(col.categories))
	}
	if col.categories[0].value != "café" {
		t.Errorf("value = %q, want %q", col.categories[0].value, "café")
	}
	if col.categories[0].label != "Küche" {
		t.Errorf("label = %q, want %q", col.categories[0].label, "Küche")
	}
}

// TestLongStringValueLabels_ShortStringUnaffected checks a plain long string
// — over eight bytes but under 256, so no record 7/14 at all — also picks up
// its 7/21 labels. Record 7/21 is not a very-long-string feature; it applies
// to every string wider than the eight-byte value slot.
func TestLongStringValueLabels_ShortStringUnaffected(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "NOTE", Width: 20, LongName: "Note"}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("hello there")},
		},
		LongStringValueLabels: []spsstest.LongStringValueLabels{{
			Var:    "Note",
			Labels: []spsstest.LongStringValueLabel{{Value: "hello there", Label: "Greeting"}},
		}},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}
	if len(m.cols[0].categories) != 1 || m.cols[0].categories[0].label != "Greeting" {
		t.Fatalf("categories = %+v, want one entry labelled Greeting", m.cols[0].categories)
	}
}

// ---------------------------------------------------------------------------
// Record 7/22 — long string missing values
// ---------------------------------------------------------------------------

// TestLongStringMissingValues_Parsed is the 7/22 acceptance criterion.
func TestLongStringMissingValues_Parsed(t *testing.T) {
	spec := longStringSpec(600, "REFUSED", "DK")
	spec.LongStringMissingValues = []spsstest.LongStringMissingValues{{
		Var: "Comments", Values: []string{"REFUSED", "DK", "NA"},
	}}
	r := NewReaderFromBytes(buildFixture(t, spec))
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	v := d.vars[1]
	if v.missing.count() != 3 {
		t.Fatalf("missing spec holds %d slot(s), want 3", v.missing.count())
	}
	if v.missing.isRange() {
		t.Error("a string missing spec cannot be a range")
	}
	want := []string{"REFUSED", "DK", "NA"}
	for i, w := range want {
		if v.missing.text[i] != w {
			t.Errorf("missing value %d = %q, want %q", i, v.missing.text[i], w)
		}
		// The raw slot is the full eight bytes, space-padded, because that
		// is what the format writes and what a datum comparison needs.
		if got := string(v.missing.raw[i][:]); got != w+strings.Repeat(" ", 8-len(w)) {
			t.Errorf("missing slot %d = %q, want %q space-padded to eight bytes", i, got, w)
		}
	}
	if hasCode(r.Warnings(), errors.PULSE_SPSS_EXTENSION_UNKNOWN) {
		t.Errorf("record 7/22 still warns as an unrecognised subtype:\n%s", warningText(r.Warnings()))
	}
}

// TestLongStringMissingValues_CharsetDecoded checks a 7/22 value goes through
// the dictionary-wide charset pass, like every other missing-value slot.
func TestLongStringMissingValues_CharsetDecoded(t *testing.T) {
	spec := longStringSpec(600, "x")
	spec.CharacterEncoding = "windows-1252"
	spec.LongStringMissingValues = []spsstest.LongStringMissingValues{{
		Var: "Comments", Values: []string{"nüll"},
	}}
	r := NewReaderFromBytes(buildFixture(t, spec))
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	if got := d.vars[1].missing.text[0]; got != "nüll" {
		t.Errorf("missing value = %q, want %q", got, "nüll")
	}
}

// TestLongStringMissingValues_RecordTypeTwoConflict covers a file carrying
// BOTH a record type 2 missing spec and a record 7/22 one for the same
// variable. Real files do: a record type 2 slot is eight bytes and SPSS
// compares only a long string's first eight, so a writer can state the
// pair. 7/22 wins — it is the record that can express the whole value —
// and the collision is surfaced rather than resolved silently.
//
// E3-S4 could only assert this against a hand-built dictionary, because
// internal/spsstest had no record type 2 missing-value slot at the time.
// It has one now, so the claim is made against real bytes: a hand-built
// dictionary can only prove that bindLongStringMissingValues does the
// right thing with a shape, never that the shape survives a parse.
func TestLongStringMissingValues_RecordTypeTwoConflict(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "NOTE", Width: 20,
			Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Text("OLD")}},
		}},
		Cases: [][]spsstest.Value{{spsstest.Text("hello")}},
		LongStringMissingValues: []spsstest.LongStringMissingValues{{
			Var: "NOTE", Values: []string{"NEW"},
		}},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	if got := d.vars[0].missing.text; len(got) != 1 || got[0] != "NEW" {
		t.Fatalf("missing spec = %v, want the record 7/22 value to win", got)
	}
	if !hasCode(d.warnings, errors.PULSE_SPSS_EXTENSION_INVALID) {
		t.Fatalf("the collision was resolved silently:\n%s", warningText(d.warnings))
	}
}

// TestRecordTypeTwoMissingValues_ShortStringParsed is the plain,
// well-formed half of the pair: a string narrow enough for its missing
// values to ride its own record type 2, with no 7/22 anywhere.
func TestRecordTypeTwoMissingValues_ShortStringParsed(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{
			Name: "CODE", Width: 4,
			Missing: &spsstest.MissingValues{
				Discrete: []spsstest.Value{spsstest.Text("REF"), spsstest.Text("DK")},
			},
		}},
		Cases: [][]spsstest.Value{{spsstest.Text("AB")}, {spsstest.Text("REF")}},
	}
	r := NewReaderFromBytes(buildFixture(t, spec))
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	v := d.vars[0]
	if v.missing.code != 2 || v.missing.isRange() {
		t.Fatalf("missing code = %d (range %v), want 2 discrete values", v.missing.code, v.missing.isRange())
	}
	if !equalStrings(v.missing.text, []string{"REF", "DK"}) {
		t.Errorf("missing values = %q, want [REF DK]", v.missing.text)
	}
	// The slot is the full eight bytes regardless of the declared width.
	if got := string(v.missing.raw[0][:]); got != "REF     " {
		t.Errorf("missing slot 0 = %q, want the value space-padded to eight bytes", got)
	}
}

// ---------------------------------------------------------------------------
// Malformed records
// ---------------------------------------------------------------------------

// TestVLS_MalformedRecords sweeps the record 7/14 payloads a fold refuses.
//
// Every one is a WARNING that leaves the physical variables in place. That is
// the whole reason a fold failure is not fatal: the record only says how to
// JOIN columns that are already there, so declining to join loses no bytes.
func TestVLS_MalformedRecords(t *testing.T) {
	// A fixture with the physical shape of a very long string but no record
	// 7/14 of its own, so a hand-written one can be injected.
	segmented := func(payload string) []byte {
		spec := spsstest.Spec{
			Vars: []spsstest.Var{
				{Name: "COMMENT", Width: 255},
				{Name: "COMMENT0", Width: 255},
				{Name: "COMMENT1", Width: 96},
			},
			Cases: [][]spsstest.Value{{
				spsstest.Text(strings.Repeat("a", 252)),
				spsstest.Text(strings.Repeat("b", 252)),
				spsstest.Text(strings.Repeat("c", 96)),
			}},
			RawExtensions: []spsstest.RawExtension{
				{Subtype: 14, Size: 1, Payload: []byte(payload)},
			},
		}
		return buildFixture(t, spec)
	}

	cases := []struct {
		name string
		// payload is the hand-written record 7/14.
		payload string
		// want is a phrase the diagnostic must carry.
		want string
		// columns is how many columns the file reads as afterwards. It is 3
		// — the unfolded segments — for every entry that folds nothing, and
		// 1 for the one case where a good entry folds before a bad one is
		// reached.
		columns int
	}{
		{"no equals sign", "COMMENT600\x00\t", "carries no '='", 3},
		{"non-numeric width", "COMMENT=wide\x00\t", "does not state a decimal byte width", 3},
		{"width inside the short-string range", "COMMENT=200\x00\t", "exists only for strings wider than 255", 3},
		{"width past the SPSS ceiling", "COMMENT=40000\x00\t", "past the 32767-byte ceiling", 3},
		{"unknown variable", "NOSUCH=600\x00\t", "no record type 2 in this dictionary declares", 3},
		{"width needing more variables than follow", "COMMENT=1000\x00\t", "only 3 variable(s) follow", 3},
		{"segment widths disagree with the declared total", "COMMENT=601\x00\t", "do not match the declared width", 3},
		{"variable claimed twice", "COMMENT=600\x00\tCOMMENT=600\x00\t", "already part of another very long string", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header, rows, warnings := readFixture(t, segmented(tc.payload))
			if !hasCode(warnings, errors.PULSE_SPSS_VERY_LONG_STRING_INVALID) {
				t.Fatalf("no PULSE_SPSS_VERY_LONG_STRING_INVALID:\n%s", warningText(warnings))
			}
			if !strings.Contains(warningText(warnings), tc.want) {
				t.Errorf("warning does not explain the fault (%q):\n%s", tc.want, warningText(warnings))
			}
			if len(header) != tc.columns {
				t.Fatalf("header = %v, want %d column(s)", header, tc.columns)
			}
			// The bytes are all still there either way — a refused fold
			// surfaces the segments under the names the dictionary literally
			// declares, and loses nothing.
			if len(rows) != 1 || !strings.HasPrefix(rows[0][0], strings.Repeat("a", 252)) {
				t.Errorf("the segment data did not survive the refused fold")
			}
		})
	}
}

// TestVLS_MalformedRecords_PartialFoldStillApplies checks one bad entry does
// not take a good one down with it: a record 7/14 naming two variables, one
// resolvable and one not, folds the one it can.
func TestVLS_MalformedRecords_PartialFoldStillApplies(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "A", Width: 255},
			{Name: "A0", Width: 45},
		},
		Cases: [][]spsstest.Value{{
			spsstest.Text(strings.Repeat("a", 252)), spsstest.Text(strings.Repeat("b", 45)),
		}},
		RawExtensions: []spsstest.RawExtension{
			{Subtype: 14, Size: 1, Payload: []byte("NOSUCH=600\x00\tA=297\x00\t")},
		},
	}
	header, rows, warnings := readFixture(t, buildFixture(t, spec))
	if !hasCode(warnings, errors.PULSE_SPSS_VERY_LONG_STRING_INVALID) {
		t.Fatalf("the unresolvable entry did not warn:\n%s", warningText(warnings))
	}
	if len(header) != 1 || header[0] != "A" {
		t.Fatalf("header = %v, want the one folded variable", header)
	}
	if want := strings.Repeat("a", 252) + strings.Repeat("b", 45); rows[0][0] != want {
		t.Errorf("got %d byte(s), want the %d-byte reassembled value", len(rows[0][0]), len(want))
	}
}

// TestLongStringRecords_TruncationSweep cuts every record 7/21 and 7/22
// payload at every byte and requires a coded diagnostic and no panic.
func TestLongStringRecords_TruncationSweep(t *testing.T) {
	// The payloads are built here rather than sliced out of a fixture so the
	// sweep covers the shape, not one file's offsets.
	label := concatBytes(
		i32le(4), []byte("NOTE"), i32le(20), i32le(1),
		i32le(20), []byte("hello there         "), i32le(8), []byte("Greeting"),
	)
	missing := concatBytes(
		i32le(4), []byte("NOTE"), []byte{2},
		i32le(8), []byte("REFUSED "), i32le(8), []byte("DK      "),
	)
	for _, tc := range []struct {
		name    string
		subtype int32
		payload []byte
	}{
		{"7/21", 21, label},
		{"7/22", 22, missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for cut := 0; cut < len(tc.payload); cut++ {
				spec := spsstest.Spec{
					Vars:  []spsstest.Var{{Name: "NOTE", Width: 20}},
					Cases: [][]spsstest.Value{{spsstest.Text("hello there")}},
					RawExtensions: []spsstest.RawExtension{
						{Subtype: tc.subtype, Size: 1, Payload: tc.payload[:cut]},
					},
				}
				b, err := spsstest.Build(spec)
				if err != nil {
					t.Fatalf("cut %d: Build: %v", cut, err)
				}
				// The contract is that a truncated payload never stops the
				// parse: the record framing was sound, so the walk stayed
				// aligned and the file still reads.
				r := NewReaderFromBytes(b)
				if _, err := r.ReadHeader(); err != nil {
					t.Fatalf("cut %d: ReadHeader: %v", cut, err)
				}
				if err := r.ReadRows(context.Background(), func([]string) error { return nil }); err != nil {
					t.Fatalf("cut %d: ReadRows: %v", cut, err)
				}
				for _, w := range r.Warnings() {
					if w.Code == "" || w.Message == "" {
						t.Fatalf("cut %d: a warning carries no code or message", cut)
					}
				}
			}
		})
	}
}

// TestLongStringRecords_BindingFaults covers the 7/21 and 7/22 entries that
// parse but cannot be bound.
func TestLongStringRecords_BindingFaults(t *testing.T) {
	cases := []struct {
		name    string
		subtype int32
		payload []byte
		want    string
	}{
		{
			"7/21 naming an absent variable",
			21,
			concatBytes(i32le(6), []byte("NOSUCH"), i32le(20), i32le(1),
				i32le(1), []byte("x"), i32le(1), []byte("y")),
			"which this dictionary does not contain",
		},
		{
			"7/21 naming a numeric variable",
			21,
			concatBytes(i32le(2), []byte("ID"), i32le(20), i32le(1),
				i32le(1), []byte("x"), i32le(1), []byte("y")),
			"which is a numeric variable",
		},
		{
			"7/21 stating the wrong width",
			21,
			concatBytes(i32le(4), []byte("NOTE"), i32le(99), i32le(1),
				i32le(1), []byte("x"), i32le(1), []byte("y")),
			"states width 99",
		},
		{
			"7/21 with a negative label count",
			21,
			concatBytes(i32le(4), []byte("NOTE"), i32le(20), i32le(-1)),
			"a count cannot be negative",
		},
		{
			"7/22 naming an absent variable",
			22,
			concatBytes(i32le(6), []byte("NOSUCH"), []byte{1}, i32le(8), []byte("REFUSED ")),
			"which this dictionary does not contain",
		},
		{
			"7/22 naming a numeric variable",
			22,
			concatBytes(i32le(2), []byte("ID"), []byte{1}, i32le(8), []byte("REFUSED ")),
			"which is a numeric variable",
		},
		{
			"7/22 with an out-of-range count",
			22,
			concatBytes(i32le(4), []byte("NOTE"), []byte{9}, i32le(8), []byte("REFUSED ")),
			"the format allows 1 to 3",
		},
		{
			"7/22 with an off-length value slot",
			22,
			concatBytes(i32le(4), []byte("NOTE"), []byte{1}, i32le(3), []byte("REF")),
			"the format fixes the slot at 8",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := spsstest.Spec{
				Vars: []spsstest.Var{
					{Name: "ID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
					{Name: "NOTE", Width: 20},
				},
				Cases: [][]spsstest.Value{{spsstest.Num(1), spsstest.Text("hello")}},
				RawExtensions: []spsstest.RawExtension{
					{Subtype: tc.subtype, Size: 1, Payload: tc.payload},
				},
			}
			_, _, warnings := readFixture(t, buildFixture(t, spec))
			if !hasCode(warnings, errors.PULSE_SPSS_EXTENSION_INVALID) {
				t.Fatalf("no PULSE_SPSS_EXTENSION_INVALID:\n%s", warningText(warnings))
			}
			if !strings.Contains(warningText(warnings), tc.want) {
				t.Errorf("warning does not explain the fault (%q):\n%s", tc.want, warningText(warnings))
			}
		})
	}
}

// TestVLS_SubtypesNoLongerUnknown is the direct handoff from E2-S3: subtypes
// 14, 21 and 22 warned as unrecognised, and must not any more.
//
// "Clean" here means no FAULT is reported. The fixture deliberately
// declares a record 7/22 missing value on a string variable, which E4-S3
// flags in the column's own dictionary and summarises once with the
// informational PULSE_SPSS_CATEGORICAL_USER_MISSING — that diagnostic
// says nothing is wrong, it says which entry to leave out of a base, so
// it is expected here rather than excluded.
func TestVLS_SubtypesNoLongerUnknown(t *testing.T) {
	spec := longStringSpec(600, "hello", "declined")
	spec.LongStringValueLabels = []spsstest.LongStringValueLabels{{
		Var: "Comments", Labels: []spsstest.LongStringValueLabel{{Value: "declined", Label: "Declined"}},
	}}
	spec.LongStringMissingValues = []spsstest.LongStringMissingValues{{
		Var: "Comments", Values: []string{"declined"},
	}}
	_, _, warnings := readFixture(t, buildFixture(t, spec))
	for _, w := range warnings {
		if w.Code == errors.PULSE_SPSS_EXTENSION_UNKNOWN {
			t.Errorf("still unrecognised: %s", w.Message)
		}
	}
	var faults []*errors.CodedError
	for _, w := range warnings {
		if w.Code != errors.PULSE_SPSS_CATEGORICAL_USER_MISSING {
			faults = append(faults, w)
		}
	}
	if len(faults) != 0 {
		t.Errorf("a clean very-long-string file should read without fault warnings:\n%s", warningText(faults))
	}
	if !hasCode(warnings, errors.PULSE_SPSS_CATEGORICAL_USER_MISSING) {
		t.Errorf("the record 7/22 missing value was not flagged on the categorical column:\n%s", warningText(warnings))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func truncate(s string) string {
	if len(s) <= 48 {
		return s
	}
	return s[:24] + "…(" + strconv.Itoa(len(s)) + " bytes)…" + s[len(s)-16:]
}

func concatBytes(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
