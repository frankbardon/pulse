package spss

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// build renders a spsstest spec, failing the test if the generator rejects it.
// Every fixture in this file comes from internal/spsstest, whose reference
// output was verified byte-by-byte against the PSPP specification and
// cross-checked against R's foreign::read.spss. Where a test needs a construct
// the generator deliberately does not emit — missing-value specs, document
// records, extension records — it splices those bytes into a generated file
// rather than hand-rolling a whole second emitter, so the surrounding bytes
// stay ground truth.
func build(t *testing.T, spec spsstest.Spec) []byte {
	t.Helper()
	b, err := spsstest.Build(spec)
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	return b
}

func mustParse(t *testing.T, b []byte) *dictionary {
	t.Helper()
	d, err := parseDictionary(b)
	if err != nil {
		t.Fatalf("parseDictionary: %v", err)
	}
	return d
}

// TestParseDictionary_ReferenceFixture asserts every field the parser recovers
// from the one fixture whose bytes a human checked against the spec. The
// expected values are read off the offset walkthrough on
// spsstest.TestReferenceFixture_HandVerified, not off this parser.
func TestParseDictionary_ReferenceFixture(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())
	d := mustParse(t, raw)

	t.Run("header", func(t *testing.T) {
		h := d.header
		checks := []struct {
			field string
			got   any
			want  any
		}{
			{"magic", h.magic, "$FL2"},
			{"productName", h.productName, spsstest.DefaultProductName},
			{"layoutCode", h.layoutCode, int32(2)},
			{"nominalCaseSize", h.nominalCaseSize, int32(4)},
			{"compression", h.compression, compressionNone},
			{"weightIndex", h.weightIndex, int32(0)},
			{"caseCount", h.caseCount, int32(2)},
			{"bias", h.bias, 100.0},
			{"creationDate", h.creationDate, spsstest.DefaultCreationDate},
			{"creationTime", h.creationTime, spsstest.DefaultCreationTime},
			{"fileLabel", h.fileLabel, ""},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("header.%s = %v, want %v", c.field, c.got, c.want)
			}
		}
		if d.byteOrder != binary.ByteOrder(binary.LittleEndian) {
			t.Errorf("byteOrder = %v, want little-endian", d.byteOrder)
		}
	})

	t.Run("variables", func(t *testing.T) {
		if len(d.vars) != 3 {
			t.Fatalf("len(vars) = %d, want 3 (continuation records must not appear as variables)", len(d.vars))
		}
		want := []variable{
			{
				name: "ID", index: 1, typeCode: 0, width: 0, segments: 1,
				print: format{code: 5, width: 8}, write: format{code: 5, width: 8},
				hasLabel: false, label: "", offset: 0x00B0,
			},
			{
				name: "SEX", index: 2, typeCode: 0, width: 0, segments: 1,
				print: format{code: 5, width: 1}, write: format{code: 5, width: 1},
				hasLabel: true, label: "Sex", offset: 0x00D0,
			},
			{
				name: "NAME", index: 3, typeCode: 10, width: 10, segments: 2,
				print: format{code: 1, width: 10}, write: format{code: 1, width: 10},
				hasLabel: false, label: "", offset: 0x00F8,
			},
		}
		for i, w := range want {
			g := d.vars[i]
			if g.name != w.name || g.index != w.index || g.typeCode != w.typeCode ||
				g.width != w.width || g.segments != w.segments || g.print != w.print ||
				g.write != w.write || g.hasLabel != w.hasLabel || g.label != w.label ||
				g.offset != w.offset {
				t.Errorf("vars[%d] =\n %+v\nwant\n %+v", i, g, w)
			}
			if n := g.missing.count(); n != 0 {
				t.Errorf("vars[%d].missing.count() = %d, want 0", i, n)
			}
			if g.missing.code != 0 {
				t.Errorf("vars[%d].missing.code = %d, want 0", i, g.missing.code)
			}
		}
		// NAME is width 10 => 2 elements, so the case stride is 1+1+2.
		if d.elementCount != 4 {
			t.Errorf("elementCount = %d, want 4", d.elementCount)
		}
		if d.elementCount != d.header.nominalCaseSize {
			t.Errorf("elementCount %d disagrees with nominal_case_size %d", d.elementCount, d.header.nominalCaseSize)
		}
	})

	t.Run("value labels", func(t *testing.T) {
		if len(d.valueLabels) != 1 {
			t.Fatalf("len(valueLabels) = %d, want 1", len(d.valueLabels))
		}
		set := d.valueLabels[0]
		if set.offset != 0x0138 {
			t.Errorf("set.offset = 0x%04X, want 0x0138", set.offset)
		}
		if set.varsOffset != 0x0160 {
			t.Errorf("set.varsOffset = 0x%04X, want 0x0160", set.varsOffset)
		}
		if set.width != 0 {
			t.Errorf("set.width = %d, want 0 (SEX is numeric)", set.width)
		}
		if len(set.varIndices) != 1 || set.varIndices[0] != 2 {
			t.Errorf("set.varIndices = %v, want [2] (SEX is the second ELEMENT)", set.varIndices)
		}
		wantPairs := []struct {
			value float64
			label string
		}{{1, "Male"}, {2, "Female"}}
		if len(set.labels) != len(wantPairs) {
			t.Fatalf("len(set.labels) = %d, want %d", len(set.labels), len(wantPairs))
		}
		for i, w := range wantPairs {
			if got := set.labels[i].numeric(d.byteOrder); got != w.value {
				t.Errorf("labels[%d].numeric() = %v, want %v", i, got, w.value)
			}
			if got := set.labels[i].label; got != w.label {
				t.Errorf("labels[%d].label = %q, want %q", i, got, w.label)
			}
		}
	})

	t.Run("terminator and data offset", func(t *testing.T) {
		// The walkthrough puts the terminator at 0x016C and the data section
		// at 0x0174.
		if d.dataOffset != 0x0174 {
			t.Errorf("dataOffset = 0x%04X, want 0x0174", d.dataOffset)
		}
		if n := len(raw) - d.dataOffset; n != 64 {
			t.Errorf("%d bytes follow the dictionary, want 64 (2 cases x 4 elements x 8)", n)
		}
	})

	t.Run("sysmis defaults without a record 7/4", func(t *testing.T) {
		// The fixture emits no extension records at all, which is legal.
		if bits := math.Float64bits(d.sysmis); bits != 0xFFEFFFFFFFFFFFFF {
			t.Errorf("sysmis bits = 0x%016X, want 0xFFEFFFFFFFFFFFFF (-DBL_MAX)", bits)
		}
		if d.sysmis != spsstest.SysMisDouble {
			t.Errorf("sysmis = %v, want the sentinel the generator writes (%v)", d.sysmis, spsstest.SysMisDouble)
		}
	})
}

// TestParseDictionary_Matrix runs the parser over the structural axes the
// reference fixture does not reach.
func TestParseDictionary_Matrix(t *testing.T) {
	cases := []struct {
		name  string
		spec  spsstest.Spec
		check func(t *testing.T, d *dictionary)
	}{
		{
			name: "single numeric variable, dictionary only",
			spec: spsstest.Spec{Vars: []spsstest.Var{{Name: "A"}}},
			check: func(t *testing.T, d *dictionary) {
				if len(d.vars) != 1 || d.vars[0].name != "A" {
					t.Fatalf("vars = %+v", d.vars)
				}
				if d.elementCount != 1 {
					t.Errorf("elementCount = %d, want 1", d.elementCount)
				}
				if d.header.caseCount != 0 {
					t.Errorf("caseCount = %d, want 0", d.header.caseCount)
				}
				if len(d.valueLabels) != 0 {
					t.Errorf("valueLabels = %+v, want none", d.valueLabels)
				}
			},
		},
		{
			name: "unknown case count",
			spec: spsstest.Spec{
				Vars:             []spsstest.Var{{Name: "A"}},
				Cases:            [][]spsstest.Value{{spsstest.Num(1)}},
				UnknownCaseCount: true,
			},
			check: func(t *testing.T, d *dictionary) {
				if d.header.caseCount != -1 {
					t.Errorf("caseCount = %d, want -1", d.header.caseCount)
				}
			},
		},
		{
			name: "widest string spans 32 elements",
			spec: spsstest.Spec{Vars: []spsstest.Var{{Name: "A", Width: 255}}},
			check: func(t *testing.T, d *dictionary) {
				if len(d.vars) != 1 {
					t.Fatalf("len(vars) = %d, want 1", len(d.vars))
				}
				if d.vars[0].segments != 32 {
					t.Errorf("segments = %d, want 32", d.vars[0].segments)
				}
				if d.elementCount != 32 {
					t.Errorf("elementCount = %d, want 32 (31 continuation records)", d.elementCount)
				}
			},
		},
		{
			name: "string of exactly 8 needs no continuation",
			spec: spsstest.Spec{Vars: []spsstest.Var{{Name: "A", Width: 8}, {Name: "B"}}},
			check: func(t *testing.T, d *dictionary) {
				if d.vars[0].segments != 1 {
					t.Errorf("A.segments = %d, want 1", d.vars[0].segments)
				}
				if d.vars[1].index != 2 {
					t.Errorf("B.index = %d, want 2", d.vars[1].index)
				}
			},
		},
		{
			name: "dictionary indices count continuation records",
			spec: spsstest.Spec{
				Vars:      []spsstest.Var{{Name: "WIDE", Width: 20}, {Name: "TARGET"}},
				WeightVar: "TARGET",
				ValueLabels: []spsstest.ValueLabelSet{{
					Vars:   []string{"TARGET"},
					Labels: []spsstest.ValueLabel{{Value: spsstest.Num(7), Label: "Seven"}},
				}},
			},
			check: func(t *testing.T, d *dictionary) {
				// WIDE is width 20 => 3 elements, so TARGET is element 4.
				if d.vars[0].index != 1 || d.vars[0].segments != 3 {
					t.Errorf("WIDE index/segments = %d/%d, want 1/3", d.vars[0].index, d.vars[0].segments)
				}
				if d.vars[1].index != 4 {
					t.Errorf("TARGET.index = %d, want 4 (a variable count would say 2)", d.vars[1].index)
				}
				if d.header.weightIndex != 4 {
					t.Errorf("weightIndex = %d, want 4", d.header.weightIndex)
				}
				if got := d.valueLabels[0].varIndices; len(got) != 1 || got[0] != 4 {
					t.Errorf("varIndices = %v, want [4]", got)
				}
				v, first, ok := d.variableByIndex(4)
				if !ok || !first || v.name != "TARGET" {
					t.Errorf("variableByIndex(4) = %q/%v/%v, want TARGET/true/true", v.name, first, ok)
				}
				if v, first, ok := d.variableByIndex(2); !ok || first || v.name != "WIDE" {
					t.Errorf("variableByIndex(2) = %q/%v/%v, want WIDE/false/true (a continuation element)", v.name, first, ok)
				}
				if _, _, ok := d.variableByIndex(5); ok {
					t.Error("variableByIndex(5) resolved; the dictionary has only 4 elements")
				}
			},
		},
		{
			name: "one label set shared by several variables",
			spec: spsstest.Spec{
				Vars: []spsstest.Var{{Name: "Q1"}, {Name: "Q2"}, {Name: "Q3"}},
				ValueLabels: []spsstest.ValueLabelSet{{
					Vars: []string{"Q1", "Q3"},
					Labels: []spsstest.ValueLabel{
						{Value: spsstest.Num(1), Label: "Yes"},
						{Value: spsstest.Num(2), Label: "No"},
					},
				}},
			},
			check: func(t *testing.T, d *dictionary) {
				if len(d.valueLabels) != 1 {
					t.Fatalf("len(valueLabels) = %d, want 1 — one record 3 serves both variables", len(d.valueLabels))
				}
				got := d.valueLabels[0].varIndices
				if len(got) != 2 || got[0] != 1 || got[1] != 3 {
					t.Errorf("varIndices = %v, want [1 3]", got)
				}
			},
		},
		{
			name: "several label sets and a weight variable",
			spec: spsstest.Spec{
				FileLabel: "matrix probe",
				WeightVar: "WT",
				Vars: []spsstest.Var{
					{Name: "WT", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 4}},
					{Name: "Q1", Label: "Question one"},
					{Name: "Q2", Label: "Question two"},
					{Name: "CODE", Width: 4},
				},
				ValueLabels: []spsstest.ValueLabelSet{
					{Vars: []string{"Q1", "Q2"}, Labels: []spsstest.ValueLabel{
						{Value: spsstest.Num(1), Label: "Yes"},
						{Value: spsstest.Num(0), Label: "No"},
					}},
					{Vars: []string{"CODE"}, Labels: []spsstest.ValueLabel{
						{Value: spsstest.Text("AB"), Label: "Alpha Bravo"},
					}},
				},
				Cases: [][]spsstest.Value{
					{spsstest.Num(1.5), spsstest.Num(1), spsstest.Num(0), spsstest.Text("AB")},
					{spsstest.Num(0.5), spsstest.SysMis(), spsstest.Num(1), spsstest.Text("CD")},
				},
			},
			check: func(t *testing.T, d *dictionary) {
				if d.header.fileLabel != "matrix probe" {
					t.Errorf("fileLabel = %q", d.header.fileLabel)
				}
				if d.header.weightIndex != 1 {
					t.Errorf("weightIndex = %d, want 1", d.header.weightIndex)
				}
				if d.vars[0].print != (format{code: 5, width: 8, decimals: 4}) {
					t.Errorf("WT print = %+v, want F8.4", d.vars[0].print)
				}
				if d.vars[1].label != "Question one" || d.vars[2].label != "Question two" {
					t.Errorf("variable labels = %q / %q", d.vars[1].label, d.vars[2].label)
				}
				if len(d.valueLabels) != 2 {
					t.Fatalf("len(valueLabels) = %d, want 2", len(d.valueLabels))
				}
				if d.valueLabels[0].width != 0 {
					t.Errorf("numeric set width = %d, want 0", d.valueLabels[0].width)
				}
				if d.valueLabels[1].width != 4 {
					t.Errorf("string set width = %d, want 4", d.valueLabels[1].width)
				}
			},
		},
		{
			name: "variable label lengths across the 4-byte padding boundary",
			spec: spsstest.Spec{Vars: []spsstest.Var{
				{Name: "A", Label: "a"},
				{Name: "B", Label: "ab"},
				{Name: "C", Label: "abc"},
				{Name: "D", Label: "abcd"},
				{Name: "E", Label: "abcde"},
				{Name: "F"},
			}},
			check: func(t *testing.T, d *dictionary) {
				want := []string{"a", "ab", "abc", "abcd", "abcde", ""}
				for i, w := range want {
					if d.vars[i].label != w {
						t.Errorf("vars[%d].label = %q, want %q", i, d.vars[i].label, w)
					}
					if hl := d.vars[i].hasLabel; hl != (w != "") {
						t.Errorf("vars[%d].hasLabel = %v, want %v", i, hl, w != "")
					}
				}
				if d.elementCount != 6 {
					t.Errorf("elementCount = %d, want 6 — a mis-padded label would desynchronise the walk", d.elementCount)
				}
			},
		},
		{
			name: "value label lengths across the 8-byte padding boundary",
			spec: spsstest.Spec{
				Vars: []spsstest.Var{{Name: "A"}},
				ValueLabels: []spsstest.ValueLabelSet{{
					Vars: []string{"A"},
					Labels: []spsstest.ValueLabel{
						{Value: spsstest.Num(1), Label: "abcdefg"},  // 1+7 = 8, no pad
						{Value: spsstest.Num(2), Label: "abcdefgh"}, // 1+8 = 9 -> 16
						{Value: spsstest.Num(3), Label: "a"},        // 1+1 = 2 -> 8
					},
				}},
			},
			check: func(t *testing.T, d *dictionary) {
				want := []string{"abcdefg", "abcdefgh", "a"}
				got := d.valueLabels[0].labels
				if len(got) != len(want) {
					t.Fatalf("len(labels) = %d, want %d", len(got), len(want))
				}
				for i, w := range want {
					if got[i].label != w {
						t.Errorf("labels[%d] = %q, want %q", i, got[i].label, w)
					}
					if v := got[i].numeric(d.byteOrder); v != float64(i+1) {
						t.Errorf("labels[%d].numeric() = %v, want %d", i, v, i+1)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, mustParse(t, build(t, tc.spec)))
		})
	}
}

// TestParseDictionary_LabelTextIsVerbatim asserts labels are taken exactly as
// their length field says. A label is user content: the length prefix is
// exact, so trimming it — a trailing space in a variable label, a value label
// deliberately aligned with spaces — would silently rewrite the file's data.
// The alignment padding that follows a label is a separate thing and is
// stepped over, never read.
func TestParseDictionary_LabelTextIsVerbatim(t *testing.T) {
	raw := build(t, spsstest.Spec{
		Vars: []spsstest.Var{{Name: "A", Label: "Trailing space "}, {Name: "B", Label: " Leading"}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"A", "B"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Yes "},
				{Value: spsstest.Num(2), Label: " No"},
				{Value: spsstest.Num(3), Label: "a b"},
			},
		}},
	})
	d := mustParse(t, raw)

	if got := d.vars[0].label; got != "Trailing space " {
		t.Errorf("vars[0].label = %q, want %q", got, "Trailing space ")
	}
	if got := d.vars[1].label; got != " Leading" {
		t.Errorf("vars[1].label = %q, want %q", got, " Leading")
	}
	want := []string{"Yes ", " No", "a b"}
	for i, w := range want {
		if got := d.valueLabels[0].labels[i].label; got != w {
			t.Errorf("labels[%d] = %q, want %q", i, got, w)
		}
	}
	// The walk must still be aligned afterwards.
	if d.dataOffset != len(raw) {
		t.Errorf("dataOffset = %d, want %d", d.dataOffset, len(raw))
	}
}

// TestValueLabel_ShortStringKeyTrimsToDeclaredWidth pins the routed E2-S1
// finding: a short-string value-label key is padded to the FULL eight-byte
// slot, not to the variable's declared width. A width-4 CODE variable
// labelling "AB" stores "AB      " — six trailing spaces, not two. A reader
// that compares the raw slot against a data value trimmed to the declared
// width misses every short-string label.
func TestValueLabel_ShortStringKeyTrimsToDeclaredWidth(t *testing.T) {
	raw := build(t, spsstest.Spec{
		Vars: []spsstest.Var{{Name: "CODE", Width: 4}},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"CODE"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Text("AB"), Label: "Alpha Bravo"},
				{Value: spsstest.Text("CDEF"), Label: "Full width"},
				{Value: spsstest.Text(""), Label: "Blank"},
			},
		}},
	})
	d := mustParse(t, raw)

	set := d.valueLabels[0]
	if set.width != 4 {
		t.Fatalf("set.width = %d, want 4", set.width)
	}

	// The slot really is eight bytes wide on the wire.
	if got := string(set.labels[0].raw[:]); got != "AB      " {
		t.Errorf("raw slot = %q, want %q — the fixture is not exercising the finding", got, "AB      ")
	}

	want := []string{"AB", "CDEF", ""}
	for i, w := range want {
		if got := set.labels[i].text(set.width); got != w {
			t.Errorf("labels[%d].text(%d) = %q, want %q", i, set.width, got, w)
		}
	}

	// The same slot read at the full eight bytes must agree, which is what
	// makes the width trim safe rather than merely convenient.
	for i, w := range want {
		if got := set.labels[i].text(elementSize); got != w {
			t.Errorf("labels[%d].text(8) = %q, want %q", i, got, w)
		}
	}
	// A nonsense width falls back to the full slot rather than panicking.
	if got := set.labels[0].text(0); got != "AB" {
		t.Errorf("labels[0].text(0) = %q, want %q", got, "AB")
	}
	if got := set.labels[0].text(99); got != "AB" {
		t.Errorf("labels[0].text(99) = %q, want %q", got, "AB")
	}
}

// TestParseDictionary_MissingValueSpecs covers the record type 2
// missing-value payload. internal/spsstest deliberately does not emit one, so
// the payload is spliced into a generated file: n_missing_values is set on the
// first variable's record and the eight-byte slots are inserted immediately
// after its fixed part. Every other byte is generator output.
func TestParseDictionary_MissingValueSpecs(t *testing.T) {
	// A single unlabelled numeric variable, so its record type 2 is exactly
	// the 32-byte fixed part at headerSize with nothing following it.
	numericBase := spsstest.Spec{Vars: []spsstest.Var{{Name: "A"}, {Name: "B"}}}
	stringBase := spsstest.Spec{Vars: []spsstest.Var{{Name: "A", Width: 4}, {Name: "B"}}}

	cases := []struct {
		name  string
		base  spsstest.Spec
		code  int32
		slots [][]byte
		check func(t *testing.T, m missingSpec)
	}{
		{
			name: "no missing values",
			base: numericBase, code: 0, slots: nil,
			check: func(t *testing.T, m missingSpec) {
				if m.count() != 0 || m.isRange() || m.discreteCount() != 0 {
					t.Errorf("spec = %+v, want empty", m)
				}
			},
		},
		{
			name: "three discrete numeric codes",
			base: numericBase, code: 3,
			slots: [][]byte{f64le(97), f64le(98), f64le(99)},
			check: func(t *testing.T, m missingSpec) {
				if m.count() != 3 || m.isRange() || m.discreteCount() != 3 {
					t.Fatalf("spec = %+v", m)
				}
				want := []float64{97, 98, 99}
				for i, w := range want {
					if m.numeric[i] != w {
						t.Errorf("numeric[%d] = %v, want %v", i, m.numeric[i], w)
					}
				}
				if len(m.text) != 0 {
					t.Errorf("text = %v, want none on a numeric variable", m.text)
				}
			},
		},
		{
			name: "one discrete numeric code",
			base: numericBase, code: 1, slots: [][]byte{f64le(-1)},
			check: func(t *testing.T, m missingSpec) {
				if m.count() != 1 || m.isRange() || m.numeric[0] != -1 {
					t.Errorf("spec = %+v", m)
				}
			},
		},
		{
			name: "a lo..hi range",
			base: numericBase, code: -2, slots: [][]byte{f64le(90), f64le(99)},
			check: func(t *testing.T, m missingSpec) {
				if !m.isRange() || m.count() != 2 || m.discreteCount() != 0 {
					t.Fatalf("spec = %+v", m)
				}
				if m.numeric[0] != 90 || m.numeric[1] != 99 {
					t.Errorf("range = %v..%v, want 90..99", m.numeric[0], m.numeric[1])
				}
			},
		},
		{
			name: "a range plus one discrete code",
			base: numericBase, code: -3, slots: [][]byte{f64le(90), f64le(98), f64le(0)},
			check: func(t *testing.T, m missingSpec) {
				if !m.isRange() || m.count() != 3 || m.discreteCount() != 1 {
					t.Fatalf("spec = %+v", m)
				}
				if m.numeric[2] != 0 {
					t.Errorf("discrete code = %v, want 0", m.numeric[2])
				}
			},
		},
		{
			name: "string missing values trim to the declared width",
			base: stringBase, code: 2,
			// The slots are padded to the full eight bytes exactly as
			// short-string value-label keys are.
			slots: [][]byte{[]byte("NA      "), []byte("DK      ")},
			check: func(t *testing.T, m missingSpec) {
				if m.count() != 2 || m.isRange() {
					t.Fatalf("spec = %+v", m)
				}
				want := []string{"NA", "DK"}
				for i, w := range want {
					if m.text[i] != w {
						t.Errorf("text[%d] = %q, want %q", i, m.text[i], w)
					}
				}
				if len(m.numeric) != 0 {
					t.Errorf("numeric = %v, want none on a string variable", m.numeric)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := spliceMissingValues(t, build(t, tc.base), headerSize, tc.code, tc.slots)
			d := mustParse(t, raw)
			if len(d.vars) != 2 {
				t.Fatalf("len(vars) = %d, want 2 — the splice desynchronised the walk", len(d.vars))
			}
			if d.vars[1].name != "B" {
				t.Errorf("vars[1].name = %q, want B", d.vars[1].name)
			}
			if got := d.vars[0].missing.code; got != tc.code {
				t.Errorf("missing.code = %d, want %d", got, tc.code)
			}
			tc.check(t, d.vars[0].missing)
			for i, slot := range tc.slots {
				if got := d.vars[0].missing.raw[i][:]; string(got) != string(slot) {
					t.Errorf("missing.raw[%d] = % X, want % X", i, got, slot)
				}
			}
		})
	}
}

// TestParseDictionary_SkipsDocumentAndExtensionRecords proves the two record
// types this story does not interpret are still stepped over exactly, so the
// terminator is found and the data offset stays right.
//
// The second half is the other routed E2-S1 finding: a dictionary with NO
// extension records at all is legal, and nothing here may require one.
func TestParseDictionary_SkipsDocumentAndExtensionRecords(t *testing.T) {
	base := build(t, spsstest.ReferenceSpec())
	baseDict := mustParse(t, base)

	if n := countRecordType(t, base, recTypeExtension); n != 0 {
		t.Fatalf("the reference fixture emits %d extension records; the no-7/* case is not being exercised", n)
	}

	cases := []struct {
		name    string
		inject  []byte
		grownBy int
	}{
		{
			name:    "a document record with two lines",
			inject:  documentRecord(2),
			grownBy: 8 + 2*documentLineLen,
		},
		{
			name:    "a document record with no lines",
			inject:  documentRecord(0),
			grownBy: 8,
		},
		{
			name:    "extension subtype 3 (machine integer info)",
			inject:  extRecordBytes(3, 4, 8),
			grownBy: 16 + 32,
		},
		{
			name:    "extension subtype 4 (machine float info)",
			inject:  extRecordBytes(4, 8, 3),
			grownBy: 16 + 24,
		},
		{
			name:    "extension subtype 13 (long variable names)",
			inject:  extRecordBytes(13, 1, 21),
			grownBy: 16 + 21,
		},
		{
			name:    "an extension record with an empty payload",
			inject:  extRecordBytes(999, 1, 0),
			grownBy: 16,
		},
		{
			name:    "several extension records in a row",
			inject:  concat(extRecordBytes(3, 4, 8), extRecordBytes(4, 8, 3), documentRecord(1)),
			grownBy: (16 + 32) + (16 + 24) + (8 + 80),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Records go in immediately before the terminator, which sits 8
			// bytes before the data section.
			raw := splice(base, baseDict.dataOffset-8, tc.inject)
			d := mustParse(t, raw)

			if d.dataOffset != baseDict.dataOffset+tc.grownBy {
				t.Errorf("dataOffset = %d, want %d — the skip did not consume exactly the injected record(s)",
					d.dataOffset, baseDict.dataOffset+tc.grownBy)
			}
			if len(d.vars) != len(baseDict.vars) {
				t.Errorf("len(vars) = %d, want %d", len(d.vars), len(baseDict.vars))
			}
			if len(d.valueLabels) != len(baseDict.valueLabels) {
				t.Errorf("len(valueLabels) = %d, want %d", len(d.valueLabels), len(baseDict.valueLabels))
			}
			if d.sysmis != defaultSysmis {
				t.Errorf("sysmis = %v, want the spec default %v — subtype 4 must be an override, not a precondition", d.sysmis, defaultSysmis)
			}
		})
	}
}

// TestParseDictionary_BigEndian exercises the layout-code endianness probe.
// internal/spsstest emits little-endian only, so the fixture here is
// hand-assembled: a header, one numeric variable and the terminator, every
// multi-byte field big-endian.
func TestParseDictionary_BigEndian(t *testing.T) {
	b := make([]byte, 0, headerSize+32+8)
	b = append(b, []byte("$FL2")...)
	b = append(b, padASCII("@(#) SPSS DATA FILE big-endian probe", 60)...)
	be := binary.BigEndian
	b = be.AppendUint32(b, 2)                     // layout_code
	b = be.AppendUint32(b, 1)                     // nominal_case_size
	b = be.AppendUint32(b, 0)                     // compression
	b = be.AppendUint32(b, 0)                     // weight_index
	b = be.AppendUint32(b, 5)                     // ncases
	b = be.AppendUint64(b, math.Float64bits(100)) // bias
	b = append(b, []byte("01 Jan 24")...)
	b = append(b, []byte("00:00:00")...)
	b = append(b, padASCII("BE", 64)...)
	b = append(b, 0, 0, 0)

	b = be.AppendUint32(b, uint32(recTypeVariable))
	b = be.AppendUint32(b, 0)          // type: numeric
	b = be.AppendUint32(b, 0)          // has_var_label
	b = be.AppendUint32(b, 0)          // n_missing_values
	b = be.AppendUint32(b, 0x00050802) // print F8.2
	b = be.AppendUint32(b, 0x00050802) // write F8.2
	b = append(b, padASCII("A", shortNameLen)...)

	b = be.AppendUint32(b, uint32(recTypeTerminator))
	b = be.AppendUint32(b, 0)

	d := mustParse(t, b)
	if d.byteOrder != binary.ByteOrder(binary.BigEndian) {
		t.Fatalf("byteOrder = %v, want big-endian", d.byteOrder)
	}
	if d.header.layoutCode != 2 || d.header.caseCount != 5 || d.header.bias != 100 {
		t.Errorf("header = %+v", d.header)
	}
	if d.header.fileLabel != "BE" {
		t.Errorf("fileLabel = %q, want BE", d.header.fileLabel)
	}
	if len(d.vars) != 1 || d.vars[0].name != "A" || d.vars[0].print != (format{code: 5, width: 8, decimals: 2}) {
		t.Errorf("vars = %+v", d.vars)
	}
	if d.dataOffset != len(b) {
		t.Errorf("dataOffset = %d, want %d", d.dataOffset, len(b))
	}
}

// TestParseDictionary_AcceptsZSAVMagic checks the $FL3 tag opens a file. The
// dictionary layout is identical; only the data section differs, and that is
// E2-S4's problem.
func TestParseDictionary_AcceptsZSAVMagic(t *testing.T) {
	raw := build(t, spsstest.Spec{Vars: []spsstest.Var{{Name: "A"}}})
	copy(raw[0:4], magicZSAV)
	d := mustParse(t, raw)
	if d.header.magic != magicZSAV {
		t.Errorf("magic = %q, want %q", d.header.magic, magicZSAV)
	}
}

// TestParseDictionary_Truncated truncates the reference fixture at every
// single byte offset and asserts the exact boundary: every prefix shorter than
// the dictionary is a PULSE_SPSS_DICT_TRUNCATED coded error, and every prefix
// reaching the terminator parses.
//
// This is also the no-panic proof for the truncation axis — a missed bounds
// check anywhere in the walk shows up here as a panic, not as a wrong value.
func TestParseDictionary_Truncated(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())
	full := mustParse(t, raw)

	for n := 0; n <= len(raw); n++ {
		d, err := parseDictionary(raw[:n])
		switch {
		case n < full.dataOffset:
			if err == nil {
				t.Fatalf("truncated to %d bytes parsed successfully; the dictionary needs %d", n, full.dataOffset)
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_DICT_TRUNCATED {
				t.Errorf("truncated to %d bytes: code = %s, want %s (%v)", n, ce.Code, perr.PULSE_SPSS_DICT_TRUNCATED, err)
			}
			assertDetails(t, ce, n)
		default:
			if err != nil {
				t.Fatalf("truncated to %d bytes failed but the dictionary ends at %d: %v", n, full.dataOffset, err)
			}
			if d.dataOffset != full.dataOffset {
				t.Errorf("truncated to %d bytes: dataOffset = %d, want %d", n, d.dataOffset, full.dataOffset)
			}
		}
	}
}

// TestParseDictionary_Malformed is the corruption matrix. Each case mutates
// the reference fixture at one place and asserts a coded error naming the
// record type and the byte offset — never a panic, never a silent
// misinterpretation.
func TestParseDictionary_Malformed(t *testing.T) {
	ref := build(t, spsstest.ReferenceSpec())
	refDict := mustParse(t, ref)
	termOff := refDict.dataOffset - 8

	cases := []struct {
		name       string
		mutate     func(b []byte) []byte
		wantCode   perr.Code
		wantRecord string
		wantMsg    string
	}{
		{
			name:       "bad magic",
			mutate:     func(b []byte) []byte { copy(b[0:4], "$FL9"); return b },
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: recordHeader,
			wantMsg:    "not a .sav system file",
		},
		{
			name:       "empty input",
			mutate:     func(b []byte) []byte { return nil },
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: recordHeader,
			wantMsg:    "file header record is complete",
		},
		{
			name: "layout code identifies neither byte order",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[offLayoutCode:], 7)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: recordHeader,
			wantMsg:    "byte order cannot be determined",
		},
		{
			name: "unknown compression code",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[offCompression:], 9)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: recordHeader,
			wantMsg:    "compression is 9",
		},
		{
			name: "negative weight index",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[offWeightIndex:], negU32(-1))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: recordHeader,
			wantMsg:    "weight_index is -1",
		},
		{
			name: "case count below -1",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[offCaseCount:], negU32(-2))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: recordHeader,
			wantMsg:    "ncases is -2",
		},
		{
			name: "unknown record type",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[headerSize:], 42)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: recordUnknown,
			wantMsg:    "unknown record type 42",
		},
		{
			name: "variable type field out of range",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[headerSize+4:], uint32(300))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "variable type field is 300",
		},
		{
			name: "has_var_label is neither 0 nor 1",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[headerSize+8:], 2)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "has_var_label is 2",
		},
		{
			name: "n_missing_values out of range",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[headerSize+12:], negU32(-1))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "n_missing_values is -1",
		},
		{
			name: "a range missing-value spec on a string variable",
			mutate: func(b []byte) []byte {
				out := build(t, spsstest.Spec{Vars: []spsstest.Var{{Name: "A", Width: 4}}})
				return spliceMissingValues(t, out, headerSize, -2, [][]byte{f64le(1), f64le(2)})
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "no range form for strings",
		},
		{
			name: "a stray string continuation record",
			mutate: func(b []byte) []byte {
				// ID is numeric, so the record after it must not be a
				// continuation.
				binary.LittleEndian.PutUint32(b[headerSize+32+4:], negU32(typeStringContinuation))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "no long string variable is expecting one",
		},
		{
			name: "a variable interrupting a long string's continuations",
			mutate: func(b []byte) []byte {
				// Turn ID into a width-16 string, which then owes one
				// continuation that SEX is not.
				binary.LittleEndian.PutUint32(b[headerSize+4:], 16)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "still owes 1 continuation record",
		},
		{
			name: "the dictionary ends owing a continuation record",
			mutate: func(b []byte) []byte {
				out := build(t, spsstest.Spec{Vars: []spsstest.Var{{Name: "A"}}})
				binary.LittleEndian.PutUint32(out[headerSize+4:], 16)
				return out
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "999",
			wantMsg:    "terminates while a long string variable still owes",
		},
		{
			name: "a record type 4 with no preceding record type 3",
			mutate: func(b []byte) []byte {
				return splice(b, termOff, i32le(int32(recTypeLabelVars), 1, 1))
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "without an immediately preceding record type 3",
		},
		{
			name: "a record type 3 not followed by a record type 4",
			mutate: func(b []byte) []byte {
				// Retag the reference fixture's record type 4 as a document
				// record, so the type 3 before it is left unbound.
				binary.LittleEndian.PutUint32(b[0x0160:], uint32(recTypeDocument))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "3",
			wantMsg:    "followed by record type 6",
		},
		{
			name: "a negative value-label count",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x013C:], negU32(-1))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "3",
			wantMsg:    "value-label count is -1",
		},
		{
			name: "a value-label count larger than the file",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x013C:], 1<<20)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "3",
			wantMsg:    "value-label pair(s) it declares",
		},
		{
			name: "a record type 4 naming no variables",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x0164:], 0)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "names 0 variables",
		},
		{
			name: "a record type 4 variable count larger than the file",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x0164:], 1<<20)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "4",
			wantMsg:    "variable index/indices it declares",
		},
		{
			name: "a record type 4 index past the end of the dictionary",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x0168:], 99)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "the dictionary has only 4 element(s)",
		},
		{
			name: "a record type 4 index of zero",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x0168:], 0)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "indices are 1-based",
		},
		{
			name: "a record type 4 naming a continuation element",
			mutate: func(b []byte) []byte {
				// Element 4 is NAME's continuation.
				binary.LittleEndian.PutUint32(b[0x0168:], 4)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "continuation element",
		},
		{
			name: "a record type 4 mixing variable widths",
			mutate: func(b []byte) []byte {
				out := build(t, spsstest.Spec{
					Vars: []spsstest.Var{{Name: "A"}, {Name: "B"}},
					ValueLabels: []spsstest.ValueLabelSet{{
						Vars:   []string{"A", "B"},
						Labels: []spsstest.ValueLabel{{Value: spsstest.Num(1), Label: "one"}},
					}},
				})
				// Retype B as a 4-byte string, so the shared set now mixes
				// a numeric and a string.
				binary.LittleEndian.PutUint32(out[headerSize+32+4:], 4)
				return out
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "same type and width",
		},
		{
			name: "value labels attached to a long string",
			mutate: func(b []byte) []byte {
				out := build(t, spsstest.Spec{
					Vars: []spsstest.Var{{Name: "A", Width: 8}},
					ValueLabels: []spsstest.ValueLabelSet{{
						Vars:   []string{"A"},
						Labels: []spsstest.ValueLabel{{Value: spsstest.Text("x"), Label: "ex"}},
					}},
				})
				// Widen A to 9 bytes without adding its continuation record:
				// the widening is what the assertion is about, and the
				// continuation-owed check would fire first, so add one.
				binary.LittleEndian.PutUint32(out[headerSize+4:], 9)
				return splice(out, headerSize+32, continuationRecord())
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "4",
			wantMsg:    "record 7/21",
		},
		{
			name: "a negative document line count",
			mutate: func(b []byte) []byte {
				return splice(b, termOff, i32le(int32(recTypeDocument), -1))
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "6",
			wantMsg:    "cannot be negative",
		},
		{
			name: "a document record claiming more lines than the file holds",
			mutate: func(b []byte) []byte {
				return splice(b, termOff, i32le(int32(recTypeDocument), 1<<20))
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "6",
			wantMsg:    "document line(s)",
		},
		{
			name: "a negative extension element size",
			mutate: func(b []byte) []byte {
				return splice(b, termOff, i32le(int32(recTypeExtension), 3, -4, 8))
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "7",
			wantMsg:    "neither can be negative",
		},
		{
			name: "an extension record claiming more payload than the file holds",
			mutate: func(b []byte) []byte {
				return splice(b, termOff, i32le(int32(recTypeExtension), 3, 4, 1<<20))
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "7",
			wantMsg:    "payload bytes of extension subtype 3",
		},
		{
			name: "an extension size x count product that overflows 32 bits",
			mutate: func(b []byte) []byte {
				return splice(b, termOff, i32le(int32(recTypeExtension), 3, 1<<20, 1<<20))
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "7",
			wantMsg:    "payload bytes of extension subtype 3",
		},
		{
			name: "no terminator at all",
			mutate: func(b []byte) []byte {
				// Retag the terminator as a document record with no lines,
				// leaving the walk to run off the end of the dictionary.
				binary.LittleEndian.PutUint32(b[termOff:], uint32(recTypeDocument))
				binary.LittleEndian.PutUint32(b[termOff+4:], 0)
				return b[:termOff+8]
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: recordUnknown,
			wantMsg:    "dictionary has no terminator",
		},
		{
			name:       "a terminator with no filler field",
			mutate:     func(b []byte) []byte { return b[:termOff+4] },
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "999",
			wantMsg:    "record type 999 filler field",
		},
		{
			name: "a variable label longer than the file",
			mutate: func(b []byte) []byte {
				// SEX's label_len sits at 0x00F0.
				binary.LittleEndian.PutUint32(b[0x00F0:], 1<<20)
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_TRUNCATED,
			wantRecord: "2",
			wantMsg:    "variable label it declares",
		},
		{
			name: "a negative variable label length",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[0x00F0:], negU32(-8))
				return b
			},
			wantCode:   perr.PULSE_SPSS_DICT_INVALID,
			wantRecord: "2",
			wantMsg:    "label length is -8",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.mutate(clone(ref))
			d, err := parseDictionary(in)
			if err == nil {
				t.Fatalf("parseDictionary succeeded (%d vars, dataOffset %d); want %s", len(d.vars), d.dataOffset, tc.wantCode)
			}
			ce := codedError(t, err)
			if ce.Code != tc.wantCode {
				t.Errorf("code = %s, want %s (%v)", ce.Code, tc.wantCode, err)
			}
			if !strings.Contains(ce.Message, tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", ce.Message, tc.wantMsg)
			}
			if got := ce.Details[perr.DetailSPSSRecord]; got != tc.wantRecord {
				t.Errorf("Details[%q] = %v, want %q", perr.DetailSPSSRecord, got, tc.wantRecord)
			}
			assertDetails(t, ce, len(in))
		})
	}
}

// TestUnpackFormat covers the 0x00TTWWDD unpacking, mirroring the packing
// table in internal/spsstest.
func TestUnpackFormat(t *testing.T) {
	cases := []struct {
		name string
		raw  int32
		want format
	}{
		{"F8.2", 0x00050802, format{code: 5, width: 8, decimals: 2}},
		{"F8.0", 0x00050800, format{code: 5, width: 8, decimals: 0}},
		{"F1.0", 0x00050100, format{code: 5, width: 1, decimals: 0}},
		{"A10", 0x00010A00, format{code: 1, width: 10, decimals: 0}},
		{"A255", 0x0001FF00, format{code: 1, width: 255, decimals: 0}},
		{"F40.16", 0x00052810, format{code: 5, width: 40, decimals: 16}},
		{"EDATE10", 0x00260A00, format{code: 38, width: 10, decimals: 0}},
		{"SDATE10", 0x00270A00, format{code: 39, width: 10, decimals: 0}},
		{"WKDAY", 0x001A0200, format{code: 26, width: 2, decimals: 0}},
		{"MONTH", 0x001B0300, format{code: 27, width: 3, decimals: 0}},
		{"zero", 0, format{}},
		{"the unused high byte is ignored", highByte(0xFF050802), format{code: 5, width: 8, decimals: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unpackFormat(tc.raw); got != tc.want {
				t.Errorf("unpackFormat(0x%08X) = %+v, want %+v", uint32(tc.raw), got, tc.want)
			}
		})
	}
}

// TestRecordName pins the strings that land in the error details.
func TestRecordName(t *testing.T) {
	cases := map[int32]string{2: "2", 3: "3", 4: "4", 6: "6", 7: "7", 999: "999", 5: recordUnknown, -1: recordUnknown}
	for rt, want := range cases {
		if got := recordName(rt); got != want {
			t.Errorf("recordName(%d) = %q, want %q", rt, got, want)
		}
	}
}

// --- helpers ---------------------------------------------------------------

func clone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// splice inserts ins into b at off.
func splice(b []byte, off int, ins []byte) []byte {
	out := make([]byte, 0, len(b)+len(ins))
	out = append(out, b[:off]...)
	out = append(out, ins...)
	out = append(out, b[off:]...)
	return out
}

// spliceMissingValues rewrites the n_missing_values field of the record type 2
// starting at recOff and inserts the eight-byte slots right after the record's
// 32-byte fixed part. It only handles a record with no variable label, which
// is the only shape the callers use.
func spliceMissingValues(t *testing.T, b []byte, recOff int, code int32, slots [][]byte) []byte {
	t.Helper()
	if rt := int32(binary.LittleEndian.Uint32(b[recOff:])); rt != recTypeVariable {
		t.Fatalf("offset %d holds record type %d, not a variable record", recOff, rt)
	}
	if hl := binary.LittleEndian.Uint32(b[recOff+8:]); hl != 0 {
		t.Fatalf("the record at %d carries a variable label; the splice helper assumes it does not", recOff)
	}
	out := clone(b)
	binary.LittleEndian.PutUint32(out[recOff+12:], uint32(code))
	var payload []byte
	for _, s := range slots {
		if len(s) != elementSize {
			t.Fatalf("missing-value slot is %d bytes, want %d", len(s), elementSize)
		}
		payload = append(payload, s...)
	}
	return splice(out, recOff+32, payload)
}

// documentRecord renders a record type 6 with n blank 80-byte lines.
func documentRecord(n int) []byte {
	out := i32le(int32(recTypeDocument), int32(n))
	return append(out, padASCII("", n*documentLineLen)...)
}

// extRecordBytes renders a record type 7 with a zero-filled payload.
func extRecordBytes(subtype, size, count int32) []byte {
	out := i32le(int32(recTypeExtension), subtype, size, count)
	return append(out, make([]byte, int(size)*int(count))...)
}

// continuationRecord renders a bare record type 2 string continuation.
func continuationRecord() []byte {
	out := i32le(int32(recTypeVariable), typeStringContinuation, 0, 0, 0, 0)
	return append(out, padASCII("", shortNameLen)...)
}

// negU32 renders a negative int32 as the uint32 the wire carries.
func negU32(v int32) uint32 { return uint32(v) }

// highByte renders a raw 32-bit format word whose unused high byte is set.
func highByte(v uint32) int32 { return int32(v) }

func i32le(vs ...int32) []byte {
	out := make([]byte, 0, 4*len(vs))
	for _, v := range vs {
		out = binary.LittleEndian.AppendUint32(out, uint32(v))
	}
	return out
}

func i64le(vs ...int64) []byte {
	out := make([]byte, 0, 8*len(vs))
	for _, v := range vs {
		out = binary.LittleEndian.AppendUint64(out, uint64(v))
	}
	return out
}

func f64le(v float64) []byte {
	return binary.LittleEndian.AppendUint64(make([]byte, 0, 8), math.Float64bits(v))
}

func padASCII(s string, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	copy(b, s)
	return b
}

func codedError(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	ce, ok := err.(*perr.CodedError)
	if !ok {
		t.Fatalf("error is %T, not *errors.CodedError: %v", err, err)
	}
	return ce
}

// assertDetails checks the two details every SPSS parse error must carry.
// size is the length of the input, which the offset can equal (a read that ran
// off the end reports the end) but never exceed.
func assertDetails(t *testing.T, ce *perr.CodedError, size int) {
	t.Helper()
	rec, ok := ce.Details[perr.DetailSPSSRecord].(string)
	if !ok || rec == "" {
		t.Errorf("Details[%q] = %v, want a non-empty record name", perr.DetailSPSSRecord, ce.Details[perr.DetailSPSSRecord])
	}
	off, ok := ce.Details[perr.DetailSPSSOffset].(int)
	if !ok {
		t.Fatalf("Details[%q] = %v (%T), want an int", perr.DetailSPSSOffset, ce.Details[perr.DetailSPSSOffset], ce.Details[perr.DetailSPSSOffset])
	}
	if off < 0 || off > size {
		t.Errorf("Details[%q] = %d, outside the input's 0..%d", perr.DetailSPSSOffset, off, size)
	}
	if !strings.Contains(ce.Message, "byte offset") {
		t.Errorf("message %q does not name a byte offset", ce.Message)
	}
}

// countRecordType walks the dictionary counting records of one type, using an
// independent walk so it cannot inherit a parser bug.
func countRecordType(t *testing.T, b []byte, want int32) int {
	t.Helper()
	d, err := parseDictionary(b)
	if err != nil {
		t.Fatalf("parseDictionary: %v", err)
	}
	n := 0
	off := headerSize
	for off+8 <= d.dataOffset {
		rt := int32(binary.LittleEndian.Uint32(b[off:]))
		if rt == want {
			n++
		}
		switch rt {
		case recTypeVariable:
			hasLabel := binary.LittleEndian.Uint32(b[off+8:])
			nMissing := int32(binary.LittleEndian.Uint32(b[off+12:]))
			off += 32
			if hasLabel == 1 {
				l := int(binary.LittleEndian.Uint32(b[off:]))
				off += 4 + roundUp(l, 4)
			}
			if nMissing < 0 {
				nMissing = -nMissing
			}
			off += int(nMissing) * elementSize
		case recTypeValueLabel:
			count := int(binary.LittleEndian.Uint32(b[off+4:]))
			off += 8
			for i := 0; i < count; i++ {
				off += elementSize
				off += roundUp(int(b[off])+1, elementSize)
			}
		case recTypeLabelVars:
			off += 8 + int(binary.LittleEndian.Uint32(b[off+4:]))*4
		case recTypeDocument:
			off += 8 + int(binary.LittleEndian.Uint32(b[off+4:]))*documentLineLen
		case recTypeExtension:
			size := int(binary.LittleEndian.Uint32(b[off+8:]))
			count := int(binary.LittleEndian.Uint32(b[off+12:]))
			off += 16 + size*count
		case recTypeTerminator:
			return n
		default:
			t.Fatalf("unexpected record type %d at %d", rt, off)
		}
	}
	return n
}
