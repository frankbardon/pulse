package spsstest

// Tests for the record type 2 missing-value slot.
//
// The slot is what makes the three numeric missing shapes reachable from
// a fixture at all. Before it existed the only missing-value record this
// package could emit was 7/22, which covers exactly one of them — long
// STRING missing values — so the reader's handling of a discrete list, a
// range and a range-plus-discrete could only be asserted against
// hand-built structs, which prove that a function does the right thing
// with a shape and never that the shape survives a parse.

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// missingBytes locates a variable's missing-value slots in a built file.
//
// It walks the record type 2 stream rather than seeking to a hardcoded
// offset, because a fixture's variable records vary in length: a variable
// label is length-prefixed and 4-byte aligned, and missing slots follow
// it. A hardcoded offset would silently start reading the wrong bytes the
// first time a test added a label.
func missingBytes(t *testing.T, raw []byte, want string) (code int32, slots [][]byte) {
	t.Helper()
	bo := binary.ByteOrder(binary.LittleEndian)
	off := HeaderSize
	i32 := func() int32 {
		v := int32(bo.Uint32(raw[off : off+4]))
		off += 4
		return v
	}
	for {
		if off+4 > len(raw) {
			t.Fatalf("ran off the end of the dictionary looking for variable %q", want)
		}
		if tag := i32(); tag != 2 {
			t.Fatalf("record tag %d where a record type 2 was expected; variable %q not found", tag, want)
		}
		_ = i32() // type
		hasLabel := i32()
		nMissing := i32()
		_, _ = i32(), i32() // print, write
		name := strings.TrimRight(string(raw[off:off+shortNameLenTest]), " ")
		off += shortNameLenTest
		if hasLabel == 1 {
			n := int(i32())
			off += roundUpTest(n, 4)
		}
		n := int(nMissing)
		if n < 0 {
			n = -n
		}
		var got [][]byte
		for j := 0; j < n; j++ {
			got = append(got, raw[off:off+ElementSize])
			off += ElementSize
		}
		if name == want {
			return nMissing, got
		}
	}
}

const shortNameLenTest = 8

func roundUpTest(n, to int) int { return ((n + to - 1) / to) * to }

func f64bytes(v float64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
	return b
}

// TestMissingValues_AllThreeShapesOnTheWire checks the n_missing_values
// field and the slots it governs, for each shape the format defines.
//
// The sign of the count field is the ONLY thing that says whether the
// leading two slots are a range, which is why the code is derived from
// the struct rather than declared by the caller: a spec that could state
// a count disagreeing with its slots would be a fixture generator that
// emits files no reader should trust.
func TestMissingValues_AllThreeShapesOnTheWire(t *testing.T) {
	spec := Spec{
		Vars: []Var{
			{
				Name: "ONE", Print: Format{Type: FormatF, Width: 8},
				Missing: &MissingValues{Discrete: []Value{Num(99)}},
			},
			{
				Name: "THREE", Label: "Has a label too",
				Print:   Format{Type: FormatF, Width: 8},
				Missing: &MissingValues{Discrete: []Value{Num(97), Num(98), Num(99)}},
			},
			{
				Name: "RANGE", Print: Format{Type: FormatF, Width: 8},
				Missing: &MissingValues{Range: &MissingRange{Low: 900, High: 999}},
			},
			{
				Name: "BOTH", Print: Format{Type: FormatF, Width: 8},
				Missing: &MissingValues{
					Range: &MissingRange{Low: 90, High: 95}, Discrete: []Value{Num(-1)},
				},
			},
			{Name: "NONE", Print: Format{Type: FormatF, Width: 8}},
			{
				Name: "TEXT", Width: 4,
				Missing: &MissingValues{Discrete: []Value{Text("REF"), Text("DK")}},
			},
		},
		Cases: [][]Value{{Num(1), Num(2), Num(3), Num(4), Num(5), Text("AB")}},
	}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cases := []struct {
		name  string
		code  int32
		slots [][]byte
	}{
		{"ONE", 1, [][]byte{f64bytes(99)}},
		{"THREE", 3, [][]byte{f64bytes(97), f64bytes(98), f64bytes(99)}},
		// A range's two bounds come FIRST and the count goes negative.
		{"RANGE", -2, [][]byte{f64bytes(900), f64bytes(999)}},
		{"BOTH", -3, [][]byte{f64bytes(90), f64bytes(95), f64bytes(-1)}},
		{"NONE", 0, nil},
		// A string missing value occupies the same eight bytes,
		// space-padded, whatever the variable's declared width.
		{"TEXT", 2, [][]byte{[]byte("REF     "), []byte("DK      ")}},
	}
	for _, c := range cases {
		code, slots := missingBytes(t, raw, c.name)
		if code != c.code {
			t.Errorf("%s n_missing_values = %d, want %d", c.name, code, c.code)
		}
		if len(slots) != len(c.slots) {
			t.Errorf("%s has %d slot(s), want %d", c.name, len(slots), len(c.slots))
			continue
		}
		for i := range slots {
			if !bytes.Equal(slots[i], c.slots[i]) {
				t.Errorf("%s slot %d = %v, want %v", c.name, i, slots[i], c.slots[i])
			}
		}
	}
}

// TestMissingValues_Deterministic keeps the package's central promise:
// the same spec always yields byte-identical output, missing values
// included.
func TestMissingValues_Deterministic(t *testing.T) {
	spec := Spec{
		Vars: []Var{{
			Name: "Q", Print: Format{Type: FormatF, Width: 8},
			Missing: &MissingValues{
				Range: &MissingRange{Low: 1, High: 2}, Discrete: []Value{Num(9)},
			},
		}},
		Cases: [][]Value{{Num(5)}},
	}
	first, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, err := Build(spec)
		if err != nil {
			t.Fatalf("Build %d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("build %d differs from the first; emission is not deterministic", i)
		}
	}
}

// TestMissingValues_AbsentSlotIsByteIdentical guards the additive claim.
// A spec that declares no missing values must build exactly the bytes it
// built before the slot existed — the reference fixture's pinned hash is
// the other half of this, and this is the direct statement.
func TestMissingValues_AbsentSlotIsByteIdentical(t *testing.T) {
	withNil, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	spec := ReferenceSpec()
	spec.Vars[0].Missing = nil
	explicit, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !bytes.Equal(withNil, explicit) {
		t.Error("a nil Missing changed the output; the slot must be purely additive")
	}
}

// TestMissingValues_Refusals sweeps every specification the emitter will
// not write.
//
// Nothing here is coerced into legality. A fixture that quietly differs
// from what its author declared is worse than no fixture, because it is
// the ground truth a reader is checked against.
func TestMissingValues_Refusals(t *testing.T) {
	cases := []struct {
		name string
		v    Var
		want string
	}{
		{
			"empty specification",
			Var{Name: "Q", Missing: &MissingValues{}},
			"neither a range nor a discrete value",
		},
		{
			"four discrete values",
			Var{Name: "Q", Missing: &MissingValues{Discrete: []Value{Num(1), Num(2), Num(3), Num(4)}}},
			"over the 3 a record type 2 can carry",
		},
		{
			"range plus two discrete values",
			Var{Name: "Q", Missing: &MissingValues{
				Range: &MissingRange{Low: 1, High: 2}, Discrete: []Value{Num(8), Num(9)},
			}},
			"carries exactly one",
		},
		{
			"inverted range",
			Var{Name: "Q", Missing: &MissingValues{Range: &MissingRange{Low: 9, High: 1}}},
			"is above high",
		},
		{
			"NaN bound",
			Var{Name: "Q", Missing: &MissingValues{Range: &MissingRange{Low: math.NaN(), High: 1}}},
			"range bound is NaN",
		},
		{
			"range on a string",
			Var{Name: "Q", Width: 4, Missing: &MissingValues{Range: &MissingRange{Low: 1, High: 2}}},
			"no range form for strings",
		},
		{
			"sysmis as a user-missing code",
			Var{Name: "Q", Missing: &MissingValues{Discrete: []Value{SysMis()}}},
			"slot Discrete[0] is SysMis()",
		},
		{
			"text on a numeric",
			Var{Name: "Q", Missing: &MissingValues{Discrete: []Value{Text("REF")}}},
			"given for numeric variable",
		},
		{
			"number on a string",
			Var{Name: "Q", Width: 4, Missing: &MissingValues{Discrete: []Value{Num(9)}}},
			"given for string variable",
		},
		{
			"value over the declared width",
			Var{Name: "Q", Width: 2, Missing: &MissingValues{Discrete: []Value{Text("REF")}}},
			"over the declared width",
		},
		{
			"value over the eight-byte slot",
			Var{Name: "Q", Width: 20, Missing: &MissingValues{Discrete: []Value{Text("REFUSED-LONG")}}},
			"a record type 2 missing-value slot holds",
		},
		{
			"very long string",
			Var{Name: "Q", Width: 600, Missing: &MissingValues{Discrete: []Value{Text("REF")}}},
			"use Spec.LongStringMissingValues",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := Spec{Vars: []Var{c.v}}
			if c.v.IsString() {
				spec.Cases = [][]Value{{Text("")}}
			} else {
				spec.Cases = [][]Value{{Num(0)}}
			}
			_, err := Build(spec)
			if err == nil {
				t.Fatalf("Build succeeded; want a refusal mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestMissingValues_WideStringIsEmitted is the deliberate exception to
// the refusals above. A record type 2 missing value on a string wider
// than eight bytes is malformed by the letter of the format, but real
// writers emit it — SPSS compares only a long string's first eight bytes
// — and a reader has to resolve it against the 7/22 record that means
// the same thing. A generator that refused the pair could not produce
// the file the resolution exists for.
func TestMissingValues_WideStringIsEmitted(t *testing.T) {
	spec := Spec{
		Vars: []Var{{
			Name: "NOTE", Width: 20,
			Missing: &MissingValues{Discrete: []Value{Text("OLD")}},
		}},
		Cases: [][]Value{{Text("hello")}},
		LongStringMissingValues: []LongStringMissingValues{{
			Var: "NOTE", Values: []string{"NEW"},
		}},
	}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	code, slots := missingBytes(t, raw, "NOTE")
	if code != 1 || len(slots) != 1 || !bytes.Equal(slots[0], []byte("OLD     ")) {
		t.Fatalf("record type 2 missing = %d %q, want one slot holding \"OLD\" space-padded", code, slots)
	}
}

// TestMissingValues_Transcoded checks a string missing value goes through
// the wire charset like the datum it is compared against. A missing value
// left in UTF-8 inside a windows-1252 file would never match a datum of
// the same variable.
func TestMissingValues_Transcoded(t *testing.T) {
	spec := Spec{
		CharacterEncoding: "windows-1252",
		Vars: []Var{{
			Name: "CODE", Width: 4,
			Missing: &MissingValues{Discrete: []Value{Text("nül")}},
		}},
		Cases: [][]Value{{Text("nül")}},
	}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, slots := missingBytes(t, raw, "CODE")
	if len(slots) != 1 {
		t.Fatalf("got %d slot(s), want 1", len(slots))
	}
	// "nül" in windows-1252 is n, 0xFC, l — three bytes, then padding.
	want := []byte{'n', 0xFC, 'l', ' ', ' ', ' ', ' ', ' '}
	if !bytes.Equal(slots[0], want) {
		t.Errorf("slot = % X, want % X; the value was not encoded into the declared charset", slots[0], want)
	}
	// And the caller's own spec was not mutated by the transcode.
	if got := spec.Vars[0].Missing.Discrete[0].str; got != "nül" {
		t.Errorf("the caller's spec now holds %q; transcoding must copy, never mutate", got)
	}
}

// TestMissingValues_CodeAndSlots covers the derived count field directly,
// including the zero-value nil receiver every non-missing variable uses.
func TestMissingValues_CodeAndSlots(t *testing.T) {
	cases := []struct {
		name  string
		m     *MissingValues
		code  int32
		slots int
	}{
		{"nil", nil, 0, 0},
		{"one discrete", &MissingValues{Discrete: []Value{Num(1)}}, 1, 1},
		{"three discrete", &MissingValues{Discrete: []Value{Num(1), Num(2), Num(3)}}, 3, 3},
		{"range", &MissingValues{Range: &MissingRange{}}, -2, 2},
		{"range plus one", &MissingValues{Range: &MissingRange{}, Discrete: []Value{Num(9)}}, -3, 3},
	}
	for _, c := range cases {
		if got := c.m.code(); got != c.code {
			t.Errorf("%s: code = %d, want %d", c.name, got, c.code)
		}
		if got := c.m.slots(); got != c.slots {
			t.Errorf("%s: slots = %d, want %d", c.name, got, c.slots)
		}
	}
}
