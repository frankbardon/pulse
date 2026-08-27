package spsstest

import (
	"bytes"
	"strings"
	"testing"
)

// TestCharset_CodecResolution pins which CharacterEncoding values a fixture
// may declare. An unrecognised one is an error rather than an ignored field:
// a Spec that declared windows-1252 and got a UTF-8 fixture would look like
// a charset test and be testing nothing.
func TestCharset_CodecResolution(t *testing.T) {
	cases := []struct {
		in      string
		ascii   bool
		encodes bool // a transformation is applied
		wantErr string
	}{
		{in: "", ascii: true},
		{in: "UTF-8"},
		{in: "utf8"},
		{in: "US-ASCII", ascii: true},
		{in: "ascii", ascii: true},
		{in: "windows-1252", encodes: true},
		{in: "cp1252", encodes: true},
		{in: "1252", encodes: true},
		{in: "ISO-8859-1", encodes: true},
		{in: "latin1", encodes: true},
		{in: "café", wantErr: "a charset name that needs a charset to read is a contradiction"},
		{in: "Klingon-1", wantErr: "not a charset this fixture generator can encode into"},
	}

	for _, tc := range cases {
		name := tc.in
		if name == "" {
			name = "(undeclared)"
		}
		t.Run(name, func(t *testing.T) {
			c, err := specWireCodec(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("specWireCodec(%q) succeeded; want an error mentioning %q", tc.in, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("specWireCodec(%q): %v", tc.in, err)
			}
			if c.ascii != tc.ascii {
				t.Errorf("ascii = %v, want %v", c.ascii, tc.ascii)
			}
			if (c.enc != nil) != tc.encodes {
				t.Errorf("enc != nil is %v, want %v", c.enc != nil, tc.encodes)
			}
		})
	}
}

// TestCharset_FixtureIsWrittenInTheDeclaredCharset is the generator's half
// of the round trip: the bytes on the wire must be the codepage's, not Go's.
func TestCharset_FixtureIsWrittenInTheDeclaredCharset(t *testing.T) {
	spec := Spec{
		CharacterEncoding: "windows-1252",
		FileLabel:         "Enquête",
		Vars: []Var{
			{Name: "SEX", Label: "Genre du répondant", Print: Format{Type: FormatF, Width: 1}},
			{Name: "CITY", Width: 8},
		},
		ValueLabels: []ValueLabelSet{{
			Vars:   []string{"SEX"},
			Labels: []ValueLabel{{Value: Num(1), Label: "Männlich"}},
		}},
		Documents: []string{"Collecté"},
		Cases:     [][]Value{{Num(1), Text("Zürich")}},
	}

	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Every non-ASCII string must be present in its windows-1252 form and
	// absent in its UTF-8 form. Checking only the first would pass
	// against a generator that emitted both.
	for _, tc := range []struct {
		what  string
		utf8  string
		w1252 []byte
	}{
		{"the file label", "Enquête", []byte{'E', 'n', 'q', 'u', 0xEA, 't', 'e'}},
		{"the variable label", "répondant", []byte{'r', 0xE9, 'p', 'o', 'n', 'd', 'a', 'n', 't'}},
		{"the value label", "Männlich", []byte{'M', 0xE4, 'n', 'n', 'l', 'i', 'c', 'h'}},
		{"the document line", "Collecté", []byte{'C', 'o', 'l', 'l', 'e', 'c', 't', 0xE9}},
		{"the string datum", "Zürich", []byte{'Z', 0xFC, 'r', 'i', 'c', 'h'}},
	} {
		if !bytes.Contains(raw, tc.w1252) {
			t.Errorf("%s is not on the wire in its windows-1252 form", tc.what)
		}
		if bytes.Contains(raw, []byte(tc.utf8)) {
			t.Errorf("%s is on the wire in its UTF-8 form; the spec was not transcoded", tc.what)
		}
	}

	// And the declaration itself is present, unencoded — a charset name
	// that needed a charset to read would be a contradiction.
	if !bytes.Contains(raw, []byte("windows-1252")) {
		t.Error("the record 7/20 charset name is missing from the fixture")
	}
}

// TestCharset_UndeclaredStillRefusesNonASCII guards the rule this story
// relaxed rather than removed: without a record 7/20 a high byte on the wire
// means nothing in particular, so a fixture may not carry one.
func TestCharset_UndeclaredStillRefusesNonASCII(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
	}{
		{"a variable label", Spec{Vars: []Var{{Name: "A", Label: "café"}}}},
		{"a long name", Spec{Vars: []Var{{Name: "A", LongName: "café"}}}},
		{"a file label", Spec{Vars: []Var{{Name: "A"}}, FileLabel: "café"}},
		{"a document line", Spec{Vars: []Var{{Name: "A"}}, Documents: []string{"café"}}},
		{
			"a value label",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{
				Vars: []string{"A"}, Labels: []ValueLabel{{Value: Num(1), Label: "café"}},
			}}},
		},
		{
			"a string datum",
			Spec{Vars: []Var{{Name: "A", Width: 8}}, Cases: [][]Value{{Text("café")}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.spec)
			if err == nil {
				t.Fatal("Build succeeded; a non-ASCII value with no declared encoding must be refused")
			}
			if !strings.Contains(err.Error(), "printable 7-bit ASCII") {
				t.Errorf("error = %q, want it to name the ASCII rule", err)
			}
			if !strings.Contains(err.Error(), "CharacterEncoding") {
				t.Errorf("error = %q, want it to point at the fix", err)
			}
		})
	}
}

// TestCharset_DeclaredRefusesUnrepresentableText is the other half of the
// same rule. Declaring windows-1252 does not license arbitrary Unicode: a
// character the charset has no byte for cannot be written, and substituting
// one would produce a fixture that does not say what its author wrote.
func TestCharset_DeclaredRefusesUnrepresentableText(t *testing.T) {
	spec := Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []Var{{Name: "A", Label: "日本語"}},
	}
	_, err := Build(spec)
	if err == nil {
		t.Fatal("Build succeeded; windows-1252 cannot represent Japanese")
	}
	if !strings.Contains(err.Error(), "not representable in the declared encoding") {
		t.Errorf("error = %q, want it to say the text is unrepresentable", err)
	}
}

// TestCharset_WidthIsCheckedOnWireBytes is the second hard rule as it
// applies to the generator: an SPSS width is a BYTE count, so the width
// check has to run on the encoded form.
func TestCharset_WidthIsCheckedOnWireBytes(t *testing.T) {
	// "ééèè" is 4 bytes in windows-1252 and 8 in UTF-8.
	fits := Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []Var{{Name: "A", Width: 4}},
		Cases:             [][]Value{{Text("ééèè")}},
	}
	if _, err := Build(fits); err != nil {
		t.Errorf("Build: %v; four windows-1252 bytes fit a width-4 variable", err)
	}

	// The same text under a UTF-8 declaration does not fit.
	overflows := fits
	overflows.CharacterEncoding = "UTF-8"
	_, err := Build(overflows)
	if err == nil {
		t.Fatal("Build succeeded; eight UTF-8 bytes do not fit a width-4 variable")
	}
	if !strings.Contains(err.Error(), "over the declared width") {
		t.Errorf("error = %q, want the width complaint", err)
	}
}

// TestCharset_ControlBytesStillRefused: relaxing the ASCII rule must not
// have relaxed the rule that keeps a control byte out of a fixed-width field
// or a delimiter-separated payload, which is wrong in every charset.
func TestCharset_ControlBytesStillRefused(t *testing.T) {
	spec := Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []Var{{Name: "A", Label: "a\x01b"}},
	}
	_, err := Build(spec)
	if err == nil {
		t.Fatal("Build succeeded; a C0 control byte is not printable in any charset")
	}
	if !strings.Contains(err.Error(), "non-printable") {
		t.Errorf("error = %q, want the printability complaint", err)
	}
}

// TestCharset_ASCIISpecIsByteIdentical is the compatibility guarantee: a
// spec whose text is entirely 7-bit produces the same bytes whether it
// declares nothing, UTF-8 or a codepage — apart from the record 7/20 the
// declaration itself adds. Nothing about the transcode may perturb the
// fixtures every other test in the tree is pinned against.
func TestCharset_ASCIISpecIsByteIdentical(t *testing.T) {
	base, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, name := range []string{"UTF-8", "US-ASCII", "windows-1252", "ISO-8859-1"} {
		t.Run(name, func(t *testing.T) {
			spec := ReferenceSpec()
			spec.CharacterEncoding = name
			got, err := Build(spec)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			// The ONLY permitted difference is the record 7/20 the
			// declaration itself adds, so cutting that record out
			// must reproduce the undeclared fixture byte for byte.
			rec := record720(name)
			at := bytes.Index(got, rec)
			if at < 0 {
				t.Fatalf("the fixture carries no record 7/20 for %q", name)
			}
			without := append(append([]byte(nil), got[:at]...), got[at+len(rec):]...)
			if !bytes.Equal(base, without) {
				t.Error("declaring an ASCII-superset charset changed the bytes of an all-ASCII fixture beyond adding record 7/20")
			}
		})
	}
}

// TestCharset_SpecIsNotMutated: transcodeSpec returns a new Spec, so a
// caller that builds the same spec twice — or builds one and then inspects
// it — sees the text it wrote and not the wire bytes.
func TestCharset_SpecIsNotMutated(t *testing.T) {
	spec := Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []Var{{Name: "A", Width: 8, Label: "café"}},
		Documents:         []string{"café"},
		ValueLabels: []ValueLabelSet{{
			Vars: []string{"A"}, Labels: []ValueLabel{{Value: Text("é"), Label: "café"}},
		}},
		Cases: [][]Value{{Text("café")}},
	}
	if _, err := Build(spec); err != nil {
		t.Fatalf("Build: %v", err)
	}

	if spec.Vars[0].Label != "café" {
		t.Errorf("Vars[0].Label = %q; Build mutated the caller's spec", spec.Vars[0].Label)
	}
	if spec.Documents[0] != "café" {
		t.Errorf("Documents[0] = %q; Build mutated the caller's spec", spec.Documents[0])
	}
	if spec.ValueLabels[0].Labels[0].Label != "café" {
		t.Errorf("value label = %q; Build mutated the caller's spec", spec.ValueLabels[0].Labels[0].Label)
	}
	if spec.Cases[0][0].str != "café" {
		t.Errorf("Cases[0][0] = %q; Build mutated the caller's spec", spec.Cases[0][0].str)
	}

	// And building twice is deterministic, which it would not be if the
	// first build had left wire bytes behind for the second to re-encode.
	a, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two builds of the same spec differ")
	}
}

func TestCharset_IsWirePrintable(t *testing.T) {
	cases := map[string]bool{
		"plain":      true,
		"":           true,
		"caf\xe9":    true,  // a codepage byte, already validated at transcode
		"a\x01b":     false, // a C0 control byte is wrong in every charset
		"a\x7fb":     false, // DEL
		"tab\there":  false,
		"line\nhere": false,
	}
	for in, want := range cases {
		if got := isWirePrintable(in); got != want {
			t.Errorf("isWirePrintable(%q) = %v, want %v", in, got, want)
		}
	}
}

// record720 renders the record 7/20 bytes a charset name produces: the
// record type, the subtype, an element size of 1, the payload length and the
// name itself.
func record720(name string) []byte {
	var b bytes.Buffer
	for _, v := range []int32{7, 20, 1, int32(len(name))} {
		b.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
	}
	b.WriteString(name)
	return b.Bytes()
}
