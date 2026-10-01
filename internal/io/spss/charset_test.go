package spss

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	"golang.org/x/text/encoding/charmap"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// latin1Spec is the reference non-UTF-8 fixture: a windows-1252 file whose
// every text-bearing slot carries a character that is NOT ASCII and whose
// windows-1252 byte differs from its UTF-8 bytes.
//
// It is deliberately not a single accented word in one label. Charset bugs
// are per-call-site — a decode wired into the variable labels but not the
// value labels reads perfectly until someone looks at a dictionary — so the
// fixture puts non-ASCII text in the file label, a variable label, a long
// variable name, a document line, a value label, a value-label KEY and a
// string datum at once.
func latin1Spec() spsstest.Spec {
	return spsstest.Spec{
		CharacterEncoding: "windows-1252",
		FileLabel:         "Enquête 2024",
		Vars: []spsstest.Var{
			{Name: "ID", LongName: "Identität", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "SEX", Label: "Genre du répondant", Print: spsstest.Format{Type: spsstest.FormatF, Width: 1}},
			{Name: "CITY", Width: 12},
			{Name: "GRADE", Width: 3},
		},
		ValueLabels: []spsstest.ValueLabelSet{
			{
				Vars: []string{"SEX"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(1), Label: "Männlich"},
					{Value: spsstest.Num(2), Label: "Weiblich"},
				},
			},
			{
				Vars: []string{"GRADE"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Text("é"), Label: "Élevé"},
					{Value: spsstest.Text("ø"), Label: "Bas"},
				},
			},
		},
		Documents: []string{"Collecté à Genève."},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(1), spsstest.Text("Zürich"), spsstest.Text("é")},
			{spsstest.Num(2), spsstest.Num(2), spsstest.Text("Genève"), spsstest.Text("ø")},
		},
	}
}

// assertNoReplacementChar is the assertion the whole story turns on. A
// decoder that substitutes rather than failing produces text that looks fine
// to len() and to a diff of column counts; the only way to see it is to look
// for the replacement character itself.
func assertNoReplacementChar(t *testing.T, what, s string) {
	t.Helper()
	if strings.ContainsRune(s, utf8.RuneError) {
		t.Errorf("%s = %q contains U+FFFD; an undecodable byte must be a coded error, never a substitution", what, s)
	}
	if !utf8.ValidString(s) {
		t.Errorf("%s = %q is not valid UTF-8", what, s)
	}
}

// codedErr extracts the CodedError from an error, failing when there is none.
func codedErr(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	ce, ok := err.(*perr.CodedError)
	if !ok {
		t.Fatalf("error %v is %T, not a *errors.CodedError", err, err)
	}
	return ce
}

// ---------------------------------------------------------------------------
// Name resolution
// ---------------------------------------------------------------------------

// TestCharset_LookupSpellings pins the lookup policy: an explicit table of
// the spellings a real `.sav` carries, then the IANA registry, and NEVER a
// near-miss. "1250" must not reach windows-1252 no matter how the name is
// punctuated.
func TestCharset_LookupSpellings(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" ⇒ must not resolve
	}{
		{"UTF-8", "UTF-8"},
		{"utf8", "UTF-8"},
		{"UTF8", "UTF-8"},
		{"65001", "UTF-8"},
		{"windows-1252", "windows-1252"},
		{"WINDOWS-1252", "windows-1252"},
		{"Windows_1252", "windows-1252"},
		{"cp1252", "windows-1252"},
		{"CP-1252", "windows-1252"},
		{"1252", "windows-1252"},
		{"IBM-1252", "windows-1252"},
		{"1250", "windows-1250"},
		{"ISO-8859-1", "ISO-8859-1"},
		{"iso_8859-1", "ISO-8859-1"},
		{"latin1", "ISO-8859-1"},
		{"ISO-8859-15", "ISO-8859-15"},
		{"US-ASCII", "US-ASCII"},
		{"ascii", "US-ASCII"},
		{"Shift_JIS", "Shift_JIS"},
		{"cp932", "Shift_JIS"},
		{"KOI8-R", "KOI8-R"},
		{"macintosh", "macintosh"},
		// Registered names not in the explicit table still resolve,
		// through the IANA fallback.
		{"ISO-8859-8-I", "ISO_8859-8-I"},
		// Neither table knows these, and neither may guess.
		{"", ""},
		{"definitely-not-a-charset", ""},
		{"windows-9999", ""},
		{"EBCDIC", ""},
		// UTF-16 is IANA-registered and implemented, and is refused
		// anyway: it does not encode ASCII as itself, so it cannot
		// carry the .sav format's own padding and delimiters.
		{"UTF-16", ""},
		{"UTF-16LE", ""},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			dec, err := lookupCharset(tc.in)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("lookupCharset(%q) resolved to %q; it must not resolve", tc.in, dec.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("lookupCharset(%q): %v", tc.in, err)
			}
			if dec.name != tc.want {
				t.Errorf("lookupCharset(%q).name = %q, want %q", tc.in, dec.name, tc.want)
			}
		})
	}
}

// TestCharset_IANAUnsupportedIsNotUTF8 covers the specific trap the lookup
// was written around: ianaindex returns (nil, nil) for a name it recognises
// but has no decoder for, and a caller treating that nil as "no
// transformation needed" would read the file as UTF-8.
func TestCharset_IANAUnsupportedIsNotUTF8(t *testing.T) {
	// IBM037 is EBCDIC: IANA-registered, and x/text ships no codec.
	for _, name := range []string{"IBM037", "IBM273", "IBM500"} {
		if dec, err := lookupCharset(name); err == nil {
			t.Errorf("lookupCharset(%q) resolved to %q; an unimplemented registered charset must be refused, not silently read as UTF-8", name, dec.name)
		}
	}
}

// TestCharset_ForCode pins the record 7/3 numeric mapping.
func TestCharset_ForCode(t *testing.T) {
	cases := []struct {
		code int32
		name string
		ok   bool
	}{
		{0, "", false}, // the field left unset is not a code
		{1, "EBCDIC", true},
		{2, "US-ASCII", true},
		{3, "US-ASCII", true},
		{4, "DEC-Kanji", true},
		{1252, "1252", true},
		{65001, "65001", true},
	}
	for _, tc := range cases {
		got, ok := charsetForCode(tc.code)
		if got != tc.name || ok != tc.ok {
			t.Errorf("charsetForCode(%d) = (%q, %v), want (%q, %v)", tc.code, got, ok, tc.name, tc.ok)
		}
	}
}

// ---------------------------------------------------------------------------
// The round trip
// ---------------------------------------------------------------------------

// TestCharset_Windows1252RoundTrip is the story's headline acceptance: a
// non-UTF-8 fixture whose labels and values come back as the text that was
// written, not as mojibake.
func TestCharset_Windows1252RoundTrip(t *testing.T) {
	raw := build(t, latin1Spec())

	// The fixture really is windows-1252 on the wire, not UTF-8 — a test
	// that skipped this could pass against a generator that quietly
	// ignored CharacterEncoding.
	if bytes.Contains(raw, []byte("Zürich")) {
		t.Fatal("the fixture holds the UTF-8 bytes of \"Zürich\"; it was not encoded into windows-1252")
	}
	if !bytes.Contains(raw, []byte{'Z', 0xFC, 'r', 'i', 'c', 'h'}) {
		t.Fatal("the fixture does not hold the windows-1252 bytes of \"Zürich\"")
	}

	d := mustParse(t, raw)

	if got := d.header.fileLabel; got != "Enquête 2024" {
		t.Errorf("file label = %q, want %q", got, "Enquête 2024")
	}
	if got := d.vars[0].fieldName(); got != "Identität" {
		t.Errorf("long name = %q, want %q", got, "Identität")
	}
	if got := d.vars[1].label; got != "Genre du répondant" {
		t.Errorf("variable label = %q, want %q", got, "Genre du répondant")
	}
	if got := strings.TrimRight(d.documents[0], " "); got != "Collecté à Genève." {
		t.Errorf("document line = %q, want %q", got, "Collecté à Genève.")
	}
	if got := d.valueLabels[0].labels[0].label; got != "Männlich" {
		t.Errorf("value label = %q, want %q", got, "Männlich")
	}

	for _, s := range []string{d.header.fileLabel, d.vars[0].fieldName(), d.vars[1].label,
		d.documents[0], d.valueLabels[0].labels[0].label} {
		assertNoReplacementChar(t, "dictionary text", s)
	}

	r := NewReaderFromBytes(raw)
	rows := readAll(t, r)
	assertRows(t, rows, [][]string{
		{"1", "1", "Zürich", "é"},
		{"2", "2", "Genève", "ø"},
	})
	for _, row := range rows {
		for _, cell := range row {
			assertNoReplacementChar(t, "data cell", cell)
		}
	}

	// The value-label KEY is a datum, decoded on the data path rather
	// than the metadata one, so it gets its own assertion: the schema
	// dictionary for GRADE must hold the decoded source values.
	schema := mustSchema(t, r)
	grade := fieldOf(t, schema, "GRADE")
	if grade.Dictionary == nil {
		t.Fatal("GRADE has no dictionary")
	}
	values := grade.Dictionary.Values()
	want := []string{"é", "ø"}
	if len(values) != len(want) {
		t.Fatalf("GRADE dictionary = %q, want %q", values, want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Errorf("GRADE dictionary[%d] = %q, want %q", i, values[i], want[i])
		}
		assertNoReplacementChar(t, "dictionary value", values[i])
	}
}

// TestCharset_UTF8FixtureUnchanged is the control: the same shape of file
// declaring UTF-8 reads identically, so the windows-1252 result above is the
// decoder working and not a coincidence of the fixture.
func TestCharset_UTF8FixtureUnchanged(t *testing.T) {
	spec := latin1Spec()
	spec.CharacterEncoding = "UTF-8"
	r := NewReaderFromBytes(build(t, spec))
	assertRows(t, readAll(t, r), [][]string{
		{"1", "1", "Zürich", "é"},
		{"2", "2", "Genève", "ø"},
	})
}

// TestCharset_ASCIIFixtureIsByteIdentical guards the compatibility promise:
// a file whose text is entirely 7-bit reads exactly as it did before any of
// this existed, whatever it declares — every supported charset is an ASCII
// superset, so the decode is a no-op on such a file.
func TestCharset_ASCIIFixtureIsByteIdentical(t *testing.T) {
	base := readAll(t, NewReaderFromBytes(build(t, spsstest.ReferenceSpec())))

	for _, name := range []string{"", "UTF-8", "US-ASCII", "windows-1252", "ISO-8859-1", "cp850"} {
		t.Run("declaring "+name, func(t *testing.T) {
			spec := spsstest.ReferenceSpec()
			spec.CharacterEncoding = name
			assertRows(t, readAll(t, NewReaderFromBytes(build(t, spec))), base)
		})
	}
}

// ---------------------------------------------------------------------------
// Rule 1: never substitute silently
// ---------------------------------------------------------------------------

// TestCharset_UndecodableDatumIsAnError puts a byte that windows-1252 leaves
// undefined into a string datum. x/text would decode it to U+FFFD and report
// no error at all; this reader must refuse.
func TestCharset_UndecodableDatumIsAnError(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "CITY", Width: 6}},
		Cases:             [][]spsstest.Value{{spsstest.Text("Zürich")}},
	}
	raw := build(t, spec)

	// 0x81 is one of the five positions windows-1252 leaves undefined.
	at := bytes.Index(raw, []byte{'Z', 0xFC, 'r', 'i', 'c', 'h'})
	if at < 0 {
		t.Fatal("the fixture does not hold the expected windows-1252 datum")
	}
	raw[at+1] = 0x81

	r := NewReaderFromBytes(raw)
	err := r.ReadRows(context.Background(), func([]string) error { return nil })
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_INVALID)
	}
	if !strings.Contains(ce.Message, "CITY") {
		t.Errorf("message %q does not name the variable", ce.Message)
	}
	if !strings.Contains(ce.Message, "windows-1252") {
		t.Errorf("message %q does not name the charset", ce.Message)
	}
	if !strings.Contains(ce.Message, "\\x81") {
		t.Errorf("message %q does not show the offending bytes", ce.Message)
	}
	if got := ce.Details[perr.DetailSPSSVariable]; got != "CITY" {
		t.Errorf("Details[variable] = %v, want CITY", got)
	}
	if got := ce.Details[perr.DetailSPSSCharset]; got != "windows-1252" {
		t.Errorf("Details[charset] = %v, want windows-1252", got)
	}
}

// TestCharset_UndecodableLabelIsAnError is the same rule on the metadata
// side, where the fault stops the dictionary parse rather than the row pass.
func TestCharset_UndecodableLabelIsAnError(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "SEX", Label: "Genre"}},
		Cases:             [][]spsstest.Value{{spsstest.Num(1)}},
	}
	raw := build(t, spec)
	at := bytes.Index(raw, []byte("Genre"))
	if at < 0 {
		t.Fatal("the fixture does not hold the variable label")
	}
	raw[at+1] = 0x90 // undefined in windows-1252

	_, err := parseDictionary(raw)
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_INVALID)
	}
	if !strings.Contains(ce.Message, "variable label") {
		t.Errorf("message %q does not say what failed to decode", ce.Message)
	}
}

// TestCharset_InvalidUTF8IsAnError covers the arm that cannot use the
// U+FFFD-in-output test, because U+FFFD is a legal UTF-8 character: a file
// declaring UTF-8 and holding a broken sequence.
func TestCharset_InvalidUTF8IsAnError(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "UTF-8",
		Vars:              []spsstest.Var{{Name: "CITY", Width: 7}},
		Cases:             [][]spsstest.Value{{spsstest.Text("Zürich")}},
	}
	raw := build(t, spec)
	at := bytes.Index(raw, []byte("Zürich"))
	if at < 0 {
		t.Fatal("the fixture does not hold the UTF-8 datum")
	}
	raw[at+1] = 0xC3 // a lone lead byte followed by 'r'
	raw[at+2] = 'r'

	r := NewReaderFromBytes(raw)
	err := r.ReadRows(context.Background(), func([]string) error { return nil })
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_INVALID)
	}
}

// TestCharset_UndeclaredHighByteIsAnError pins the default. A file that
// declares nothing is read as strict UTF-8, so a pre-Unicode file with no
// record 7/20 fails loudly rather than importing mojibake — the alternative,
// assuming windows-1252 because it almost never fails, is the silent
// degradation this reader exists to prevent.
func TestCharset_UndeclaredHighByteIsAnError(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "CITY", Width: 6}},
		Cases: [][]spsstest.Value{{spsstest.Text("Zurich")}},
	}
	raw := build(t, spec)
	at := bytes.Index(raw, []byte("Zurich"))
	if at < 0 {
		t.Fatal("the fixture does not hold the datum")
	}
	raw[at+1] = 0xFC // the windows-1252 'ü', in a file declaring nothing

	r := NewReaderFromBytes(raw)
	err := r.ReadRows(context.Background(), func([]string) error { return nil })
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_INVALID)
	}

	// ...and WithCharset is the documented way out of it.
	r2 := NewReaderFromBytes(raw, WithCharset("windows-1252"))
	assertRows(t, readAll(t, r2), [][]string{{"Zürich"}})
}

// ---------------------------------------------------------------------------
// Rule: an unknown charset is a coded error, not a fallback
// ---------------------------------------------------------------------------

func TestCharset_UnsupportedNameIsCodedError(t *testing.T) {
	cases := []struct {
		name    string
		declare string
	}{
		{"an unregistered name", "Klingon-1"},
		{"a registered but unimplemented charset", "IBM037"},
		{"a charset that is not an ASCII superset", "UTF-16"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The name rides a raw record 7/20 rather than
			// Spec.CharacterEncoding: the fixture generator
			// refuses to declare a charset it cannot encode
			// into, which is correct of it and exactly why a
			// hand-built record is what tests the reader here.
			spec := spsstest.ReferenceSpec()
			spec.RawExtensions = []spsstest.RawExtension{
				{Subtype: 20, Size: 1, Payload: []byte(tc.declare)},
			}
			_, err := parseDictionary(build(t, spec))
			ce := codedErr(t, err)
			if ce.Code != perr.PULSE_SPSS_CHARSET_UNSUPPORTED {
				t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_UNSUPPORTED)
			}
			if !strings.Contains(ce.Message, tc.declare) {
				t.Errorf("message %q does not name the charset", ce.Message)
			}
			if got := ce.Details[perr.DetailSPSSCharset]; got != tc.declare {
				t.Errorf("Details[charset] = %v, want %q", got, tc.declare)
			}
		})
	}
}

// TestCharset_UnsupportedCodeIsCodedError is the same refusal reached
// through record 7/3 rather than 7/20.
func TestCharset_UnsupportedCodeIsCodedError(t *testing.T) {
	mi := spsstest.DefaultMachineIntegerInfo()
	mi.CharacterCode = 1 // EBCDIC
	spec := spsstest.ReferenceSpec()
	spec.MachineIntegerInfo = &mi

	_, err := parseDictionary(build(t, spec))
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_CHARSET_UNSUPPORTED {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_UNSUPPORTED)
	}
	if !strings.Contains(ce.Message, "EBCDIC") {
		t.Errorf("message %q does not name the charset", ce.Message)
	}
}

func TestCharset_UnsupportedOverrideIsCodedError(t *testing.T) {
	r := NewReaderFromBytes(build(t, spsstest.ReferenceSpec()), WithCharset("Klingon-1"))
	_, err := r.PulseSchema()
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_CHARSET_UNSUPPORTED {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_UNSUPPORTED)
	}
	if !strings.Contains(ce.Message, "override") {
		t.Errorf("message %q does not say the override is what failed", ce.Message)
	}
}

// ---------------------------------------------------------------------------
// The 7/3 ↔ 7/20 cross-check
// ---------------------------------------------------------------------------

func TestCharset_CrossCheck(t *testing.T) {
	cases := []struct {
		name    string
		code    int32
		declare string
		want    string // the charset that must win
		warn    bool
	}{
		{
			// The overwhelmingly common real case: the legacy
			// numeric field left at its ASCII default while 7/20
			// names the real charset. Warning here would fire on
			// most files and mean nothing.
			name: "ASCII code against a named charset", code: 2,
			declare: "windows-1252", want: "windows-1252", warn: false,
		},
		{
			name: "8-bit ASCII code against a named charset", code: 3,
			declare: "ISO-8859-1", want: "ISO-8859-1", warn: false,
		},
		{
			name: "the two agree", code: 1252,
			declare: "windows-1252", want: "windows-1252", warn: false,
		},
		{
			name: "the two agree through different spellings", code: 65001,
			declare: "utf8", want: "UTF-8", warn: false,
		},
		{
			name: "they disagree", code: 1252,
			declare: "ISO-8859-1", want: "ISO-8859-1", warn: true,
		},
		{
			// A code with no charset behind it is evidence of
			// nothing, so it is not a disagreement.
			name: "an unresolvable code", code: 4,
			declare: "windows-1252", want: "windows-1252", warn: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mi := spsstest.DefaultMachineIntegerInfo()
			mi.CharacterCode = tc.code
			spec := spsstest.ReferenceSpec()
			spec.MachineIntegerInfo = &mi
			spec.CharacterEncoding = tc.declare

			d := mustParse(t, build(t, spec))
			if d.charset.name != tc.want {
				t.Errorf("charset = %q, want %q (the 7/20 name must win)", d.charset.name, tc.want)
			}
			var warns int
			for _, w := range d.warnings {
				if w.Code == perr.PULSE_SPSS_CHARSET_MISMATCH {
					warns++
					if !strings.Contains(w.Message, tc.declare) {
						t.Errorf("warning %q does not name the 7/20 declaration", w.Message)
					}
				}
			}
			if tc.warn && warns != 1 {
				t.Errorf("got %d mismatch warning(s), want exactly 1", warns)
			}
			if !tc.warn && warns != 0 {
				t.Errorf("got %d mismatch warning(s), want none", warns)
			}
		})
	}
}

// TestCharset_CodeOnlyNoName is the 7/3-only path proper, built by stripping
// the 7/20 name out of a windows-1252 fixture's bytes.
func TestCharset_CodeOnlyNoName(t *testing.T) {
	mi := spsstest.DefaultMachineIntegerInfo()
	mi.CharacterCode = 1252
	spec := latin1Spec()
	spec.MachineIntegerInfo = &mi
	spec.CharacterEncoding = "windows-1252"
	raw := build(t, spec)

	// Blank the 7/20 subtype tag so the record parses as an unknown
	// subtype and contributes no name. The dictionary then has a code
	// and no name, which is the shape under test.
	stripped := stripExtensionSubtype(t, raw, 20)
	d := mustParse(t, stripped)
	if d.charsetName != "" {
		t.Fatalf("the 7/20 record survived: charsetName = %q", d.charsetName)
	}
	if d.charset.name != "windows-1252" {
		t.Errorf("charset = %q, want windows-1252 resolved from record 7/3", d.charset.name)
	}
	if got := d.vars[1].label; got != "Genre du répondant" {
		t.Errorf("variable label = %q, want it decoded from the 7/3 codepage", got)
	}
}

// stripExtensionSubtype rewrites an extension record's subtype tag to an
// unrecognised value, so the reader keeps the bytes and drops the meaning.
func stripExtensionSubtype(t *testing.T, raw []byte, subtype int32) []byte {
	t.Helper()
	out := append([]byte(nil), raw...)
	// A record type 7 is: int32 7, int32 subtype, int32 size, int32 count.
	want := []byte{7, 0, 0, 0, byte(subtype), 0, 0, 0}
	at := bytes.Index(out, want)
	if at < 0 {
		t.Fatalf("no record 7/%d found in the fixture", subtype)
	}
	out[at+4] = 0xFE // an unassigned subtype
	out[at+5] = 0x0F
	return out
}

// ---------------------------------------------------------------------------
// Retention for the write path
// ---------------------------------------------------------------------------

// TestCharset_DeclarationRetained is E5-S4's precondition: the file's own
// spelling survives the read, so an export can re-encode into the charset
// the source declared rather than into this package's canonical name for it.
func TestCharset_DeclarationRetained(t *testing.T) {
	mi := spsstest.DefaultMachineIntegerInfo()
	mi.CharacterCode = 1252
	spec := latin1Spec()
	spec.CharacterEncoding = "cp1252" // a non-canonical spelling, on purpose
	spec.MachineIntegerInfo = &mi

	r := NewReaderFromBytes(build(t, spec))
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}

	if got := m.charset.declaredName; got != "cp1252" {
		t.Errorf("declaredName = %q, want the file's own spelling %q", got, "cp1252")
	}
	if got := m.charset.declaredCode; got != 1252 {
		t.Errorf("declaredCode = %d, want 1252", got)
	}
	if got := m.charset.name; got != "windows-1252" {
		t.Errorf("name = %q, want the canonical %q", got, "windows-1252")
	}
	if m.charset.overridden {
		t.Error("overridden is set on a file that was read with its own declaration")
	}
	if !m.charset.declared() {
		t.Error("declared() is false on a file carrying both a 7/20 and a 7/3")
	}
	if m.charset.dec == nil {
		t.Error("dec is nil after a successful resolution")
	}
}

// TestCharset_OverrideRetainsTheDeclaration is the same guarantee under
// WithCharset: an override changes DECODING only. An export must still
// re-encode into what the source file said, not into what a reader was told
// to read with — otherwise every override would silently rewrite the
// dictionary of the file it round-trips.
func TestCharset_OverrideRetainsTheDeclaration(t *testing.T) {
	spec := latin1Spec()
	spec.CharacterEncoding = "windows-1252"

	r := NewReaderFromBytes(build(t, spec), WithCharset("ISO-8859-1"))
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}
	if got := m.charset.declaredName; got != "windows-1252" {
		t.Errorf("declaredName = %q, want the file's %q", got, "windows-1252")
	}
	if got := m.charset.name; got != "ISO-8859-1" {
		t.Errorf("name = %q, want the override %q", got, "ISO-8859-1")
	}
	if !m.charset.overridden {
		t.Error("overridden is false after WithCharset")
	}
}

// TestCharset_UndeclaredIsVisible: a file that said nothing must be
// distinguishable from one that said UTF-8, because the write path may not
// invent a record 7/20 the source did not carry.
func TestCharset_UndeclaredIsVisible(t *testing.T) {
	d := mustParse(t, build(t, spsstest.ReferenceSpec()))
	if d.charset.declared() {
		t.Error("declared() is true on a file carrying neither a 7/20 nor a 7/3")
	}
	if d.charset.name != defaultCharsetName {
		t.Errorf("charset = %q, want the default %q", d.charset.name, defaultCharsetName)
	}
}

// ---------------------------------------------------------------------------
// Rule 2: widths are byte counts, not rune counts
// ---------------------------------------------------------------------------

// TestCharset_DeclaredWidthStaysAByteCount is the retention half of the
// second hard rule. "Zürich" is 6 bytes in windows-1252 and 7 in UTF-8, so
// after decoding neither len() nor the rune count of the decoded value
// reproduces the width the source declared. The declared byte width must
// therefore be carried alongside, which is what E5-S4 re-pads against.
func TestCharset_DeclaredWidthStaysAByteCount(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "CITY", Width: 6}},
		Cases:             [][]spsstest.Value{{spsstest.Text("Zürich")}},
	}
	r := NewReaderFromBytes(build(t, spec))
	m, err := r.loadMapping()
	if err != nil {
		t.Fatalf("loadMapping: %v", err)
	}

	if got := m.cols[0].declaredWidth; got != 6 {
		t.Errorf("declaredWidth = %d, want the source's 6 BYTES", got)
	}
	rows := readAll(t, r)
	decoded := rows[0][0]
	if decoded != "Zürich" {
		t.Fatalf("value = %q, want %q", decoded, "Zürich")
	}
	if len(decoded) != 7 {
		t.Fatalf("the decoded value is %d bytes; the test's premise is that it is not 6", len(decoded))
	}
	if utf8.RuneCountInString(decoded) != 6 {
		t.Fatalf("the decoded value is %d runes", utf8.RuneCountInString(decoded))
	}
	// The rune count coinciding with the byte width here is an accident
	// of Latin-1: a two-byte windows-1252 sequence does not exist, but a
	// Shift_JIS one does, so nothing may key off it.
}

// TestCharset_WidthIsMeasuredOnTheWire proves the trim-then-decode ordering:
// a value that exactly fills its declared BYTE width in the source charset
// is accepted and read whole, even though its UTF-8 form overflows it.
func TestCharset_WidthIsMeasuredOnTheWire(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "S", Width: 4}},
		Cases:             [][]spsstest.Value{{spsstest.Text("ééèè")}},
	}
	r := NewReaderFromBytes(build(t, spec))
	rows := readAll(t, r)
	if got := rows[0][0]; got != "ééèè" {
		t.Errorf("value = %q, want %q", got, "ééèè")
	}
}

// ---------------------------------------------------------------------------
// The decoder itself
// ---------------------------------------------------------------------------

func TestCharsetDecoder_Decode(t *testing.T) {
	w1252, err := lookupCharset("windows-1252")
	if err != nil {
		t.Fatalf("lookupCharset: %v", err)
	}
	utf8dec, err := lookupCharset("UTF-8")
	if err != nil {
		t.Fatalf("lookupCharset: %v", err)
	}
	asciidec, err := lookupCharset("US-ASCII")
	if err != nil {
		t.Fatalf("lookupCharset: %v", err)
	}
	sjis, err := lookupCharset("Shift_JIS")
	if err != nil {
		t.Fatalf("lookupCharset: %v", err)
	}

	cases := []struct {
		name string
		dec  *charsetDecoder
		in   []byte
		want string
		at   int
	}{
		{"empty", w1252, nil, "", -1},
		{"pure ASCII", w1252, []byte("plain"), "plain", -1},
		{"a high byte", w1252, []byte{'Z', 0xFC, 'r'}, "Zür", -1},
		{"the euro sign at 0x80", w1252, []byte{0x80}, "€", -1},
		{"an undefined byte", w1252, []byte{'a', 0x81, 'b'}, "", 1},
		{"another undefined byte", w1252, []byte{0x8D}, "", 0},

		{"UTF-8 passthrough", utf8dec, []byte("Zürich"), "Zürich", -1},
		{"a real U+FFFD is data in UTF-8", utf8dec, []byte("�"), "�", -1},
		{"a lone lead byte", utf8dec, []byte{'a', 0xC3, 'b'}, "", 1},
		{"a stray continuation byte", utf8dec, []byte{0x80}, "", 0},

		{"ASCII accepts 7-bit", asciidec, []byte("plain"), "plain", -1},
		{"ASCII refuses a high byte", asciidec, []byte{'a', 0xFC}, "", 1},

		{"Shift_JIS multi-byte", sjis, []byte{0x93, 0xFA, 0x96, 0x7B}, "日本", -1},
		{"Shift_JIS ASCII", sjis, []byte("ok"), "ok", -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, at := tc.dec.decode(tc.in)
			if at != tc.at {
				t.Fatalf("decode(%v) offset = %d, want %d (result %q)", tc.in, at, tc.at, got)
			}
			if got != tc.want {
				t.Errorf("decode(%v) = %q, want %q", tc.in, got, tc.want)
			}
			if at < 0 {
				assertNoReplacementChar(t, "decoded", strings.ReplaceAll(got, "�", ""))
			}
		})
	}
}

// TestCharsetDecoder_NoSubstitutionAnywhere sweeps every byte value through
// every single-byte charset in the table and asserts the two-way rule: a
// byte the charset defines decodes to something that is not U+FFFD, and a
// byte it does not define is reported rather than substituted.
//
// This is the systematic form of rule 1. A per-charset spot check would miss
// exactly the codepage whose undefined positions the table got wrong.
func TestCharsetDecoder_NoSubstitutionAnywhere(t *testing.T) {
	for i := range charsetTable {
		e := &charsetTable[i]
		cm, ok := e.enc.(*charmap.Charmap)
		if !ok {
			continue
		}
		t.Run(e.name, func(t *testing.T) {
			dec, err := lookupCharset(e.name)
			if err != nil {
				t.Fatalf("lookupCharset(%q): %v", e.name, err)
			}
			for b := 0; b < 256; b++ {
				got, at := dec.decode([]byte{byte(b)})
				defined := cm.DecodeByte(byte(b)) != utf8.RuneError
				switch {
				case defined && at >= 0:
					t.Errorf("byte 0x%02X is defined in %s but was rejected", b, e.name)
				case !defined && at < 0:
					t.Errorf("byte 0x%02X is undefined in %s but decoded to %q instead of being reported", b, e.name, got)
				case at < 0 && strings.ContainsRune(got, utf8.RuneError):
					t.Errorf("byte 0x%02X in %s decoded to U+FFFD without an error", b, e.name)
				}
			}
		})
	}
}

// TestCharset_TableEntriesAreASCIISupersets guards the invariant every fast
// path in this file relies on: a 7-bit datum needs no decoding whatever the
// declared charset is.
func TestCharset_TableEntriesAreASCIISupersets(t *testing.T) {
	for i := range charsetTable {
		e := &charsetTable[i]
		if e.enc == nil {
			continue
		}
		if !isASCIISuperset(e.enc) {
			t.Errorf("%s is in charsetTable but does not encode ASCII as itself", e.name)
		}
	}
}

func TestCharset_NormaliseName(t *testing.T) {
	cases := map[string]string{
		"windows-1252": "WINDOWS1252",
		"Windows_1252": "WINDOWS1252",
		"WINDOWS 1252": "WINDOWS1252",
		"cp1252":       "CP1252",
		"UTF-8":        "UTF8",
		"":             "",
		"...":          "",
	}
	for in, want := range cases {
		if got := normaliseCharsetName(in); got != want {
			t.Errorf("normaliseCharsetName(%q) = %q, want %q", in, got, want)
		}
	}
}
