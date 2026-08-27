package spsstest

import (
	"bytes"
	"strings"
	"testing"
)

// longSpec is the working fixture for these tests: one numeric key and one
// 600-byte very long string, which is three physical variables.
func longSpec() Spec {
	return Spec{
		Vars: []Var{
			{Name: "ID", Print: Format{Type: FormatF, Width: 8}},
			{Name: "COMMENT", Width: 600, LongName: "Comments", Label: "Free text"},
		},
		Cases: [][]Value{
			{Num(1), Text(strings.Repeat("abcdefghij", 60))},
			{Num(2), Text("short")},
		},
	}
}

// TestVeryLongString_SegmentMath pins the segmentation arithmetic against the
// specification, independently of any file.
//
// The width-256 row is the one that decides the whole scheme. Its two
// segments DECLARE 255 and 4 bytes, which sum to 259 — three more than the
// variable's own declared width. Only a rule that reads 252 content bytes out
// of the first segment can reproduce a 256-byte value, which is why the
// divisor is 252 and not the 255 the segment declares.
func TestVeryLongString_SegmentMath(t *testing.T) {
	cases := []struct {
		width    int
		count    int
		widths   []int
		contents []int
	}{
		{255, 1, []int{255}, []int{255}},
		{256, 2, []int{255, 4}, []int{252, 4}},
		{504, 2, []int{255, 252}, []int{252, 252}},
		{505, 3, []int{255, 255, 1}, []int{252, 252, 1}},
		{600, 3, []int{255, 255, 96}, []int{252, 252, 96}},
		{32767, 131, nil, nil},
	}
	for _, tc := range cases {
		if got := VeryLongStringSegmentCount(tc.width); got != tc.count {
			t.Errorf("width %d: segment count = %d, want %d", tc.width, got, tc.count)
		}
		sum := 0
		for i := 0; i < VeryLongStringSegmentCount(tc.width); i++ {
			w := VeryLongStringSegmentWidthAt(tc.width, i)
			c := VeryLongStringSegmentContentAt(tc.width, i)
			if tc.widths != nil {
				if w != tc.widths[i] {
					t.Errorf("width %d segment %d: declared %d, want %d", tc.width, i, w, tc.widths[i])
				}
				if c != tc.contents[i] {
					t.Errorf("width %d segment %d: content %d, want %d", tc.width, i, c, tc.contents[i])
				}
			}
			if w < 1 || w > MaxStringWidth {
				t.Errorf("width %d segment %d: declared width %d is not expressible in a record type 2 type field", tc.width, i, w)
			}
			sum += c
		}
		// The invariant the whole scheme rests on: the content bytes of
		// every segment add up to exactly the logical width, with nothing
		// lost and nothing invented.
		if sum != tc.width {
			t.Errorf("width %d: segment contents sum to %d", tc.width, sum)
		}
	}
}

// TestVeryLongString_SegmentNames pins the generated short names. Segment 0
// keeps the variable's own name because every cross-referencing record — 7/14
// itself, 7/13, 7/11 — addresses the variable by it.
func TestVeryLongString_SegmentNames(t *testing.T) {
	cases := []struct {
		base string
		i    int
		want string
	}{
		{"COMMENT", 0, "COMMENT"},
		{"COMMENT", 1, "COMMENT0"},
		{"COMMENT", 2, "COMMENT1"},
		{"LONGNAME", 1, "LONGNAM0"},
		{"LONGNAME", 11, "LONGNA10"},
	}
	for _, tc := range cases {
		if got := VeryLongStringSegmentName(tc.base, tc.i); got != tc.want {
			t.Errorf("VeryLongStringSegmentName(%q, %d) = %q, want %q", tc.base, tc.i, got, tc.want)
		}
		if got := VeryLongStringSegmentName(tc.base, tc.i); len(got) > MaxShortNameLen {
			t.Errorf("VeryLongStringSegmentName(%q, %d) = %q, over the %d-byte name field", tc.base, tc.i, got, MaxShortNameLen)
		}
	}
}

// TestVeryLongString_Expands checks that the emitted file carries the
// PHYSICAL variables the format needs, not the one logical variable the Spec
// declares — and that the caller never had to say so.
func TestVeryLongString_Expands(t *testing.T) {
	b, err := Build(longSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, name := range []string{"COMMENT ", "COMMENT0", "COMMENT1"} {
		if !bytes.Contains(b, []byte(name)) {
			t.Errorf("the file carries no record type 2 named %q; the very long string was not expanded", name)
		}
	}

	// Record 7/14 states NAME=WIDTH for the LOGICAL width, terminated by a
	// NUL and a tab.
	want := []byte("COMMENT=600\x00\t")
	if !bytes.Contains(b, want) {
		t.Fatalf("the file carries no record 7/14 payload %q", want)
	}

	// The head segment keeps the variable label and the long name; the
	// trailing segments are undecorated implementation detail.
	if got := bytes.Count(b, []byte("Free text")); got != 1 {
		t.Errorf("the variable label appears %d time(s); only the head segment carries it", got)
	}
	if got := bytes.Count(b, []byte("COMMENT=Comments")); got != 1 {
		t.Errorf("the record 7/13 long name mapping appears %d time(s), want 1", got)
	}
}

// TestVeryLongString_Deterministic keeps the byte-determinism promise across
// the new expansion path.
func TestVeryLongString_Deterministic(t *testing.T) {
	spec := longSpec()
	spec.LongStringValueLabels = []LongStringValueLabels{{
		Var: "Comments", Labels: []LongStringValueLabel{{Value: "short", Label: "Terse"}},
	}}
	spec.LongStringMissingValues = []LongStringMissingValues{{Var: "Comments", Values: []string{"REFUSED"}}}

	first, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, err := Build(spec)
		if err != nil {
			t.Fatalf("Build #%d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("Build is not deterministic at attempt %d", i)
		}
	}
}

// TestVeryLongString_SplitsData checks the datum split: the value is padded
// to its full declared width and then cut at fixed offsets, so a segment
// boundary lands in the same place whatever the value's own length.
func TestVeryLongString_SplitsData(t *testing.T) {
	got := splitVeryLongString("AB", 600)
	if len(got) != 3 {
		t.Fatalf("got %d segments, want 3", len(got))
	}
	if got[0] != "AB"+strings.Repeat(" ", 250) {
		t.Errorf("segment 0 = %q...", got[0][:8])
	}
	for i, want := range []int{252, 252, 96} {
		if len(got[i]) != want {
			t.Errorf("segment %d is %d bytes, want %d", i, len(got[i]), want)
		}
	}
	if strings.Join(got, "") != "AB"+strings.Repeat(" ", 598) {
		t.Error("the joined segments are not the padded value")
	}
}

// TestLongStringRecords_PayloadShape walks the records 7/21 and 7/22 payloads
// against the specification, byte by byte.
func TestLongStringRecords_PayloadShape(t *testing.T) {
	spec := Spec{
		Vars: []Var{{Name: "NOTE", Width: 12, LongName: "Note"}},
		Cases: [][]Value{
			{Text("hello")},
		},
		LongStringValueLabels: []LongStringValueLabels{{
			Var: "Note", Labels: []LongStringValueLabel{{Value: "hello", Label: "Greeting"}},
		}},
		LongStringMissingValues: []LongStringMissingValues{{Var: "Note", Values: []string{"REFUSED"}}},
	}
	b, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 7/21: name_len, name, var_width, n_labels, then (value_len, value,
	// label_len, label). The value is padded out to var_width, which is what
	// makes value_len and var_width the same number.
	want21 := concat(
		i32(recTypeExtension), i32(SubtypeLongStringValueLabels), i32(1), i32(4+4+4+4+4+12+4+8),
		i32(4), []byte("Note"),
		i32(12), i32(1),
		i32(12), []byte("hello       "),
		i32(8), []byte("Greeting"),
	)
	if !bytes.Contains(b, want21) {
		t.Errorf("record 7/21 not found; want % X", want21)
	}

	// 7/22: name_len, name, a ONE-byte count, then (value_len, value) with
	// value_len fixed at 8 whatever the variable's width.
	want22 := concat(
		i32(recTypeExtension), i32(SubtypeLongStringMissing), i32(1), i32(4+4+1+4+8),
		i32(4), []byte("Note"),
		[]byte{1},
		i32(8), []byte("REFUSED "),
	)
	if !bytes.Contains(b, want22) {
		t.Errorf("record 7/22 not found; want % X", want22)
	}
}

// TestLongStringRecords_Rejects covers the spec faults the emitter refuses
// rather than writing a file no reader could make sense of.
func TestLongStringRecords_Rejects(t *testing.T) {
	long := func(mut func(*Spec)) Spec {
		s := longSpec()
		mut(&s)
		return s
	}
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{
			"very long string wider than SPSS allows",
			long(func(s *Spec) { s.Vars[1].Width = MaxVeryLongStringWidth + 1 }),
			"widest string variable SPSS supports",
		},
		{
			"very long string with a caller-supplied format",
			long(func(s *Spec) { s.Vars[1].Print = Format{Type: FormatA, Width: 255} }),
			"derived per physical segment",
		},
		{
			"numeric datum in a very long string",
			long(func(s *Spec) { s.Cases[0][1] = Num(1) }),
			"given for a very long string variable",
		},
		{
			"datum over the logical width",
			long(func(s *Spec) { s.Cases[0][1] = Text(strings.Repeat("x", 601)) }),
			"over the declared width 600",
		},
		{
			"7/21 naming no variable",
			long(func(s *Spec) {
				s.LongStringValueLabels = []LongStringValueLabels{{Var: "NOPE", Labels: []LongStringValueLabel{{Value: "a", Label: "b"}}}}
			}),
			"neither the short name nor the long name",
		},
		{
			"7/21 on a numeric variable",
			long(func(s *Spec) {
				s.LongStringValueLabels = []LongStringValueLabels{{Var: "ID", Labels: []LongStringValueLabel{{Value: "a", Label: "b"}}}}
			}),
			"a numeric variable",
		},
		{
			"7/21 on a short string",
			Spec{
				Vars:                  []Var{{Name: "S", Width: 4}},
				Cases:                 [][]Value{{Text("ab")}},
				LongStringValueLabels: []LongStringValueLabels{{Var: "S", Labels: []LongStringValueLabel{{Value: "ab", Label: "x"}}}},
			},
			"carries its value labels in records 3/4",
		},
		{
			"7/21 with no labels",
			long(func(s *Spec) {
				s.LongStringValueLabels = []LongStringValueLabels{{Var: "Comments"}}
			}),
			"declares no labels",
		},
		{
			"7/21 value over the declared width",
			Spec{
				Vars:                  []Var{{Name: "S", Width: 10}},
				Cases:                 [][]Value{{Text("ab")}},
				LongStringValueLabels: []LongStringValueLabels{{Var: "S", Labels: []LongStringValueLabel{{Value: strings.Repeat("x", 11), Label: "y"}}}},
			},
			"over the variable's declared width",
		},
		{
			"7/22 with too many values",
			long(func(s *Spec) {
				s.LongStringMissingValues = []LongStringMissingValues{{Var: "Comments", Values: []string{"a", "b", "c", "d"}}}
			}),
			"record 7/22 allows 1 to 3",
		},
		{
			"7/22 with no values",
			long(func(s *Spec) {
				s.LongStringMissingValues = []LongStringMissingValues{{Var: "Comments"}}
			}),
			"record 7/22 allows 1 to 3",
		},
		{
			"7/22 value over eight bytes",
			long(func(s *Spec) {
				s.LongStringMissingValues = []LongStringMissingValues{{Var: "Comments", Values: []string{"NINEBYTES"}}}
			}),
			"fixes the slot at 8",
		},
		{
			"7/22 on a short string",
			Spec{
				Vars:                    []Var{{Name: "S", Width: 8}},
				Cases:                   [][]Value{{Text("ab")}},
				LongStringMissingValues: []LongStringMissingValues{{Var: "S", Values: []string{"x"}}},
			},
			"carries its missing values in its record type 2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Build(tc.spec)
			if err == nil {
				t.Fatalf("Build succeeded (%d bytes); want an error containing %q", len(b), tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build error = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestVeryLongString_TranscodesBeforeSplitting proves the ordering that makes
// a byte-width segmentation correct: the spec is transcoded into its wire
// charset FIRST, and only then cut. Cutting a Go-source string and then
// changing its byte length would put the boundary in the wrong place.
func TestVeryLongString_TranscodesBeforeSplitting(t *testing.T) {
	// "é" is one byte in windows-1252 and two in UTF-8. 251 ASCII bytes plus
	// one é lands the é ON the 252-byte boundary in windows-1252 and
	// straddling it in UTF-8.
	value := strings.Repeat("x", 251) + "é" + strings.Repeat("y", 20)
	for _, charset := range []string{"UTF-8", "windows-1252"} {
		spec := Spec{
			Vars:              []Var{{Name: "V", Width: 600, LongName: "Wide"}},
			CharacterEncoding: charset,
			Cases:             [][]Value{{Text(value)}},
		}
		if _, err := Build(spec); err != nil {
			t.Fatalf("%s: Build: %v", charset, err)
		}
	}
}
