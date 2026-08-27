package spsstest

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// TestExtensionFixture_ByteLayout walks the record type 6 and record type 7
// bytes of [ExtensionReferenceSpec] against the specification, the same way
// TestReferenceFixture_HandVerified walks the dictionary spine.
//
// The expected bytes here are written out from the format description, not
// read back off the emitter, which is the only way this test can catch the
// emitter being wrong.
func TestExtensionFixture_ByteLayout(t *testing.T) {
	got, err := Build(ExtensionReferenceSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The extension fixture is the reference fixture with records inserted
	// between the value labels and the terminator, so everything before the
	// first document record must be byte-identical.
	base, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build(ReferenceSpec): %v", err)
	}
	prefix := indexOf(t, base, i32(recTypeTerminator))
	if !bytes.Equal(got[:prefix], base[:prefix]) {
		t.Fatal("the dictionary spine differs from the reference fixture; the extension records were not inserted, they replaced something")
	}

	rest := got[prefix:]

	t.Run("record type 6 documents", func(t *testing.T) {
		want := concat(
			i32(recTypeDocument), i32(2),
			padded("Collected 2024-01-01.", DocumentLineLen),
			padded("Second line.", DocumentLineLen),
		)
		if !bytes.HasPrefix(rest, want) {
			t.Fatalf("documents record = % X..., want % X...", head(rest, 32), head(want, 32))
		}
		rest = rest[len(want):]
	})

	t.Run("extension records in ascending subtype order", func(t *testing.T) {
		mi := DefaultMachineIntegerInfo()
		mf := DefaultMachineFloatInfo()

		want := []struct {
			name    string
			subtype int32
			size    int32
			payload []byte
		}{
			{
				name: "7/3 machine integer info", subtype: 3, size: 4,
				payload: i32(mi.VersionMajor, mi.VersionMinor, mi.VersionRevision,
					mi.MachineCode, mi.FloatingPointRep, mi.CompressionCode,
					mi.Endianness, mi.CharacterCode),
			},
			{
				name: "7/4 machine float info", subtype: 4, size: 8,
				payload: concat(f64(mf.SysMis), f64(mf.Highest), f64(mf.Lowest)),
			},
			{
				name: "7/5 variable sets", subtype: 5, size: 1,
				payload: []byte("demographics= ID SEX\n"),
			},
			{
				name: "7/7 multiple-response sets", subtype: 7, size: 1,
				// "$media" '=' 'D' counted("1") ' ' counted("Media used")
				// then the member short names, then '\n'.
				payload: []byte("$media=D1 1 10 Media used ID SEX\n" +
					"$brands=C 6 Brands ID SEX\n"),
			},
			{
				name: "7/11 display parameters", subtype: 11, size: 4,
				payload: i32(
					int32(MeasureScale), 10, int32(AlignRight),
					int32(MeasureNominal), 4, int32(AlignLeft),
					int32(MeasureNominal), 12, int32(AlignLeft)),
			},
			{
				name: "7/13 long variable names", subtype: 13, size: 1,
				payload: []byte("ID=RespondentId\tNAME=FullName"),
			},
			{
				name: "7/16 64-bit case count", subtype: 16, size: 8,
				payload: concat(i64(1), i64(2)),
			},
			{
				name: "7/17 data file attributes", subtype: 17, size: 1,
				payload: []byte("$@Role('0'\n)\n"),
			},
			{
				name: "7/18 variable attributes", subtype: 18, size: 1,
				payload: []byte("ID:$@Role('0'\n)\n"),
			},
			{
				name: "7/19 extended multiple-response sets", subtype: 19, size: 1,
				// 'E' ' ' "11" ' ' counted("1") ' ' counted("Extended")
				payload: []byte("$ext=E 11 1 1 8 Extended SEX\n"),
			},
			{
				name: "7/20 character encoding", subtype: 20, size: 1,
				payload: []byte("UTF-8"),
			},
			{
				name: "raw unknown subtype", subtype: 4242, size: 1,
				payload: []byte("nobody knows what this is"),
			},
		}

		for _, w := range want {
			t.Run(w.name, func(t *testing.T) {
				header := i32(recTypeExtension, w.subtype, w.size,
					int32(len(w.payload))/w.size)
				full := append(header, w.payload...)
				if !bytes.HasPrefix(rest, full) {
					t.Fatalf("record = % X...\nwant     % X...\npayload as text: %q\nwant as text:    %q",
						head(rest, 48), head(full, 48), string(head(rest[16:], len(w.payload))), string(w.payload))
				}
				rest = rest[len(full):]
			})
		}
	})

	t.Run("the terminator follows", func(t *testing.T) {
		if !bytes.HasPrefix(rest, concat(i32(recTypeTerminator), i32(0))) {
			t.Fatalf("after the extension records: % X..., want the record type 999 terminator", head(rest, 16))
		}
	})
}

// TestExtensionFixture_Deterministic asserts the property the whole package
// rests on still holds with extension records in play. Their emission order
// is fixed rather than caller-controlled precisely so it can.
func TestExtensionFixture_Deterministic(t *testing.T) {
	first, err := Build(ExtensionReferenceSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := Build(ExtensionReferenceSpec())
		if err != nil {
			t.Fatalf("Build (iteration %d): %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("iteration %d differs from the first build; extension emission is not deterministic", i)
		}
	}
}

// TestExtensions_AbsentByDefault is the regression guard for the promise the
// reader depends on: a spec that asks for no extension records emits none,
// and the pre-extension fixtures are unchanged.
//
// TestReferenceFixture_Pinned pins the reference bytes by hash; this asserts
// the reason that hash did not move.
func TestExtensions_AbsentByDefault(t *testing.T) {
	got, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, rt := range []int32{recTypeDocument, recTypeExtension} {
		if bytes.Contains(got, i32(rt, 0)) && rt == recTypeDocument {
			t.Errorf("the reference fixture contains a record type %d", rt)
		}
	}
	if n := countExtensionRecords(t, got); n != 0 {
		t.Errorf("the reference fixture emits %d extension record(s), want 0; a reader that requires one is reading something the format does not promise", n)
	}
}

// TestExtensions_TwoFieldDisplayParams covers the older record 7/11 shape.
func TestExtensions_TwoFieldDisplayParams(t *testing.T) {
	spec := ExtensionReferenceSpec()
	spec.OmitDisplayWidth = true
	got, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := concat(
		i32(recTypeExtension, SubtypeDisplayParams, 4, 6),
		i32(int32(MeasureScale), int32(AlignRight),
			int32(MeasureNominal), int32(AlignLeft),
			int32(MeasureNominal), int32(AlignLeft)),
	)
	if !bytes.Contains(got, want) {
		t.Errorf("the two-field 7/11 record is not present as % X", want)
	}
}

// TestExtensions_DisplayWidthDefaultsToFormatWidth checks the one derived
// value in the 7/11 payload.
func TestExtensions_DisplayWidthDefaultsToFormatWidth(t *testing.T) {
	spec := Spec{
		DisplayParams: true,
		Vars: []Var{
			{Name: "A", Print: Format{Type: FormatF, Width: 7}, Measure: MeasureScale},
		},
	}
	got, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := concat(
		i32(recTypeExtension, SubtypeDisplayParams, 4, 3),
		i32(int32(MeasureScale), 7, int32(AlignLeft)),
	)
	if !bytes.Contains(got, want) {
		t.Errorf("a zero DisplayWidth did not fall back to the print format width 7")
	}
}

// TestExtensions_MRSetGrammar pins the exact text each set flavour renders to.
// The grammar is the load-bearing part: a reader built against a wrong
// rendering here would be wrong against every real file.
func TestExtensions_MRSetGrammar(t *testing.T) {
	cases := []struct {
		name string
		set  MRSet
		want string
	}{
		{
			name: "dichotomy",
			set:  MRSet{Name: "$s", Kind: MRDichotomy, Label: "Lbl", CountedValue: "1", Vars: []string{"A", "B"}},
			want: "$s=D1 1 3 Lbl A B\n",
		},
		{
			name: "dichotomy with an empty label",
			set:  MRSet{Name: "$s", Kind: MRDichotomy, CountedValue: "1", Vars: []string{"A"}},
			want: "$s=D1 1 0  A\n",
		},
		{
			name: "dichotomy with a multi-byte counted value",
			set:  MRSet{Name: "$s", Kind: MRDichotomy, Label: "L", CountedValue: "YES", Vars: []string{"A"}},
			want: "$s=D3 YES 1 L A\n",
		},
		{
			name: "category",
			set:  MRSet{Name: "$c", Kind: MRCategory, Label: "Lbl", Vars: []string{"A", "B"}},
			want: "$c=C 3 Lbl A B\n",
		},
		{
			name: "extended with label source 1",
			set:  MRSet{Name: "$e", Kind: MRDichotomy, Extended: true, Label: "L", CountedValue: "1", Vars: []string{"A"}, Subtype: SubtypeMRSetsExtended},
			want: "$e=E 1 1 1 1 L A\n",
		},
		{
			name: "extended with label source 11",
			set:  MRSet{Name: "$e", Kind: MRDichotomy, Extended: true, LabelFromVarLabel: true, Label: "L", CountedValue: "1", Vars: []string{"A"}, Subtype: SubtypeMRSetsExtended},
			want: "$e=E 11 1 1 1 L A\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subtype := tc.set.Subtype
			if subtype == 0 {
				subtype = SubtypeMRSets
			}
			spec := Spec{
				Vars:                 []Var{{Name: "A"}, {Name: "B"}},
				MultipleResponseSets: []MRSet{tc.set},
			}
			got, err := Build(spec)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			want := append(
				i32(recTypeExtension, subtype, 1, int32(len(tc.want))),
				[]byte(tc.want)...)
			if !bytes.Contains(got, want) {
				t.Errorf("the record does not carry %q", tc.want)
			}
		})
	}
}

// TestExtensions_SetsShareOneRecordPerSubtype checks the grouping rule: sets
// with the same subtype ride one record, in slice order.
func TestExtensions_SetsShareOneRecordPerSubtype(t *testing.T) {
	spec := Spec{
		Vars: []Var{{Name: "A"}},
		MultipleResponseSets: []MRSet{
			{Name: "$one", Kind: MRDichotomy, Label: "1", CountedValue: "1", Vars: []string{"A"}, Subtype: SubtypeMRSets},
			{Name: "$two", Kind: MRCategory, Label: "2", Vars: []string{"A"}, Subtype: SubtypeMRSetsExtended},
			{Name: "$three", Kind: MRCategory, Label: "3", Vars: []string{"A"}, Subtype: SubtypeMRSets},
		},
	}
	got, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if n := countExtensionRecords(t, got); n != 2 {
		t.Errorf("emitted %d extension record(s), want 2 (one per distinct subtype)", n)
	}
	want := "$one=D1 1 1 1 A\n$three=C 1 3 A\n"
	if !bytes.Contains(got, []byte(want)) {
		t.Errorf("the 7/7 record does not carry %q in slice order", want)
	}
}

// TestExtensions_Rejects covers the validation that keeps a fixture honest.
// The emitter refuses rather than coerces, because a fixture that quietly
// differs from what its author declared is how a reader bug becomes
// invisible.
func TestExtensions_Rejects(t *testing.T) {
	one := func(vs ...Var) []Var {
		if len(vs) == 0 {
			return []Var{{Name: "A"}}
		}
		return vs
	}

	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{
			"a document line over 80 bytes",
			Spec{Vars: one(), Documents: []string{strings.Repeat("x", 81)}},
			"over the 80-byte document line width",
		},
		{
			"a non-ASCII document line",
			Spec{Vars: one(), Documents: []string{"café"}},
			"printable 7-bit ASCII",
		},
		{
			"a long name over 64 bytes",
			Spec{Vars: one(Var{Name: "A", LongName: strings.Repeat("x", 65)})},
			"over the 64-byte SPSS limit",
		},
		{
			"a long name containing the payload's own delimiter",
			Spec{Vars: one(Var{Name: "A", LongName: "a=b"})},
			"delimiters and there is no escape",
		},
		{
			// A tab is caught by the printable-ASCII rule before the
			// delimiter rule reaches it. Either refusal is correct; what
			// matters is that it never reaches the payload, where it would
			// silently split one pair into two.
			"a long name containing a tab",
			Spec{Vars: one(Var{Name: "A", LongName: "a\tb"})},
			"printable 7-bit ASCII",
		},
		{
			"an out-of-range measure",
			Spec{Vars: one(Var{Name: "A", Measure: Measure(9)}), DisplayParams: true},
			"outside the 0..3",
		},
		{
			"an out-of-range alignment",
			Spec{Vars: one(Var{Name: "A", Align: Alignment(9)}), DisplayParams: true},
			"outside the 0..2",
		},
		{
			"a negative 64-bit case count",
			Spec{Vars: one(), CaseCount64: int64Ptr(-1)},
			"cannot be negative",
		},
		{
			"an MR set name without a leading $",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "s", Kind: MRCategory, Vars: []string{"A"}}}},
			"does not begin with '$'",
		},
		{
			"an MR set with no Kind",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Vars: []string{"A"}}}},
			"has no Kind",
		},
		{
			"a dichotomy with no counted value",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRDichotomy, Vars: []string{"A"}}}},
			"nothing says which value counts as selected",
		},
		{
			"a category set carrying a counted value",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRCategory, CountedValue: "1", Vars: []string{"A"}}}},
			"only a dichotomy has one",
		},
		{
			"a category set marked extended",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRCategory, Extended: true, Vars: []string{"A"}}}},
			"the 'E' form is a dichotomy form",
		},
		{
			"a label source without the extended form",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRDichotomy, CountedValue: "1", LabelFromVarLabel: true, Vars: []string{"A"}}}},
			"only expressible in the 'E' form",
		},
		{
			"an MR set naming an undeclared variable",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRCategory, Vars: []string{"NOPE"}}}},
			"names no declared variable",
		},
		{
			"an MR set naming no variables",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRCategory}}},
			"names no variables",
		},
		{
			"an MR set on a subtype that carries none",
			Spec{Vars: one(), MultipleResponseSets: []MRSet{{Name: "$s", Kind: MRCategory, Vars: []string{"A"}, Subtype: 13}}},
			"rides record 7/5, 7/7 or 7/19",
		},
		{
			"a variable set whose name begins with $",
			Spec{Vars: one(), VariableSets: []VariableSet{{Name: "$s", Vars: []string{"A"}}}},
			"indistinguishable from one",
		},
		{
			"a variable set naming an undeclared variable",
			Spec{Vars: one(), VariableSets: []VariableSet{{Name: "s", Vars: []string{"NOPE"}}}},
			"names no declared variable",
		},
		{
			"a raw extension whose payload does not divide by its element size",
			Spec{Vars: one(), RawExtensions: []RawExtension{{Subtype: 99, Size: 4, Payload: make([]byte, 6)}}},
			"not a multiple of its 4-byte element size",
		},
		{
			"a raw extension with a negative element size",
			Spec{Vars: one(), RawExtensions: []RawExtension{{Subtype: 99, Size: -1}}},
			"cannot be negative",
		},
		{
			"a non-ASCII charset name",
			Spec{Vars: one(), CharacterEncoding: "café"},
			"a charset name that needs a charset to read is a contradiction",
		},
		{
			"attribute text with a control byte",
			Spec{Vars: one(), FileAttributes: "a\x01b"},
			"printable 7-bit ASCII, tab or newline",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.spec)
			if err == nil {
				t.Fatalf("Build succeeded; want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestExtensions_AttributeNewlinesAreLegal is the flip side of the control
// byte rejection: the attribute records are line-structured, so a newline is
// content, not a fault.
func TestExtensions_AttributeNewlinesAreLegal(t *testing.T) {
	spec := Spec{Vars: []Var{{Name: "A"}}, FileAttributes: "k('v1'\n'v2'\n)\n"}
	got, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !bytes.Contains(got, []byte("k('v1'\n'v2'\n)\n")) {
		t.Error("the attribute text was not emitted verbatim")
	}
}

// TestExtensions_Stringers covers the two new enums' String methods,
// including their out-of-range arms.
func TestExtensions_Stringers(t *testing.T) {
	checks := []struct{ got, want string }{
		{MeasureUnset.String(), "unset"},
		{MeasureNominal.String(), "nominal"},
		{MeasureOrdinal.String(), "ordinal"},
		{MeasureScale.String(), "scale"},
		{Measure(9).String(), "Measure(?)"},
		{AlignLeft.String(), "left"},
		{AlignRight.String(), "right"},
		{AlignCenter.String(), "center"},
		{Alignment(9).String(), "Alignment(?)"},
		{MRDichotomy.String(), "multiple dichotomy"},
		{MRCategory.String(), "multiple category"},
		{MRSetKind(9).String(), "MRSetKind(?)"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("String() = %q, want %q", c.got, c.want)
		}
	}
}

// TestExtensions_DefaultsAreConforming checks the two default payload helpers
// against the values the format defines.
func TestExtensions_DefaultsAreConforming(t *testing.T) {
	mi := DefaultMachineIntegerInfo()
	if mi.FloatingPointRep != 1 {
		t.Errorf("FloatingPointRep = %d, want 1 (IEEE 754)", mi.FloatingPointRep)
	}
	if mi.Endianness != 2 {
		t.Errorf("Endianness = %d, want 2 (little-endian), which is the only order this package emits", mi.Endianness)
	}

	mf := DefaultMachineFloatInfo()
	if mf.SysMis != SysMisDouble {
		t.Errorf("SysMis = %v, want the same -DBL_MAX the data section writes (%v)", mf.SysMis, SysMisDouble)
	}
	if !(mf.SysMis < mf.Lowest && mf.Lowest < mf.Highest) {
		t.Errorf("the default triple {%v %v %v} is not the ordered sysmis < lowest < highest the format defines", mf.SysMis, mf.Lowest, mf.Highest)
	}
	if mf.Highest != math.MaxFloat64 {
		t.Errorf("Highest = %v, want +DBL_MAX", mf.Highest)
	}
}

// --- helpers ---------------------------------------------------------------

func int64Ptr(v int64) *int64 { return &v }

func i32(vs ...int32) []byte {
	out := make([]byte, 0, 4*len(vs))
	for _, v := range vs {
		out = binary.LittleEndian.AppendUint32(out, uint32(v))
	}
	return out
}

func i64(vs ...int64) []byte {
	out := make([]byte, 0, 8*len(vs))
	for _, v := range vs {
		out = binary.LittleEndian.AppendUint64(out, uint64(v))
	}
	return out
}

func f64(v float64) []byte {
	return binary.LittleEndian.AppendUint64(make([]byte, 0, 8), math.Float64bits(v))
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func padded(s string, n int) []byte {
	b := bytes.Repeat([]byte{' '}, n)
	copy(b, s)
	return b
}

func head(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}

func indexOf(t *testing.T, b, needle []byte) int {
	t.Helper()
	i := bytes.Index(b, needle)
	if i < 0 {
		t.Fatalf("% X not found", needle)
	}
	return i
}

// countExtensionRecords walks the dictionary counting record type 7s. The
// walk is independent of the emitter so it cannot inherit an emitter bug.
func countExtensionRecords(t *testing.T, b []byte) int {
	t.Helper()
	off := HeaderSize
	n := 0
	for off+8 <= len(b) {
		rt := int32(binary.LittleEndian.Uint32(b[off:]))
		switch rt {
		case recTypeVariable:
			hasLabel := binary.LittleEndian.Uint32(b[off+8:])
			off += 32
			if hasLabel == 1 {
				l := int(binary.LittleEndian.Uint32(b[off:]))
				off += 4 + roundUp(l, 4)
			}
		case recTypeValueLabel:
			count := int(binary.LittleEndian.Uint32(b[off+4:]))
			off += 8
			for i := 0; i < count; i++ {
				off += ElementSize
				off += roundUp(int(b[off])+1, ElementSize)
			}
		case recTypeLabelVars:
			off += 8 + int(binary.LittleEndian.Uint32(b[off+4:]))*4
		case recTypeDocument:
			off += 8 + int(binary.LittleEndian.Uint32(b[off+4:]))*DocumentLineLen
		case recTypeExtension:
			n++
			size := int(binary.LittleEndian.Uint32(b[off+8:]))
			count := int(binary.LittleEndian.Uint32(b[off+12:]))
			off += 16 + size*count
		case recTypeTerminator:
			return n
		default:
			t.Fatalf("unexpected record type %d at offset %d", rt, off)
		}
	}
	t.Fatal("no record type 999 terminator")
	return 0
}
