package spss

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustEncoder builds a write-side encoder or fails.
func mustEncoder(t *testing.T, name string) *charsetEncoder {
	t.Helper()
	e, err := newCharsetEncoder(name)
	if err != nil {
		t.Fatalf("newCharsetEncoder(%q): %v", name, err)
	}
	return e
}

// emitFails builds a dictionary expecting a specific coded refusal.
func emitFails(t *testing.T, req DictionaryRequest, want perr.Code) *perr.CodedError {
	t.Helper()
	plan, err := BuildDictionary(req)
	if err == nil {
		t.Fatalf("BuildDictionary succeeded; it must refuse with %s rather than degrade (%d column(s) emitted)",
			want, len(plan.Columns))
	}
	ce := codedErr(t, err)
	if ce.Code != want {
		t.Fatalf("code = %s, want %s (%v)", ce.Code, want, err)
	}
	return ce
}

// dictField is a categorical cohort field carrying the given values, which
// is how a synthesised export reaches a STRING variable.
func dictField(t *testing.T, name string, values ...string) encoding.Field {
	t.Helper()
	return encoding.Field{
		Name:       name,
		Type:       encoding.FieldTypeCategoricalU8,
		Dictionary: dictOf(t, values...),
	}
}

// synthIn emits a dictionary from a schema alone, in a named charset.
func synthIn(t *testing.T, charset string, fields ...encoding.Field) *DictionaryPlan {
	t.Helper()
	return emit(t, DictionaryRequest{
		Schema:      &encoding.Schema{Fields: fields},
		Cases:       0,
		Compression: compressionNone,
		Options:     WriterOptions{Charset: charset},
	})
}

// ---------------------------------------------------------------------------
// Rule 1: never substitute
// ---------------------------------------------------------------------------

// TestCharsetEncoder_NoSubstitutionAnywhere is the encode-direction mirror of
// E3-S3's TestCharsetDecoder_NoSubstitutionAnywhere, and it is the gate the
// first hard rule stands on.
//
// E3-S3 measured that x/text DECODERS hand back U+FFFD with a nil error. The
// encoders were assumed to do the mirror-image thing and are checked here
// rather than trusted — and the measured answer is the opposite: they refuse.
// Trusting that would still be wrong, because it is a fact about a
// dependency, so this sweeps for the substitution bytes themselves.
//
// Two properties, over every supported charset:
//
//   - every rune the charset defines encodes to bytes that decode BACK to it;
//   - no rune outside the repertoire produces bytes at all — in particular
//     never '?' (0x3F, the conventional substitute) and never 0x1A
//     (encoding.ASCIISub, which charmap.EncodeRune returns alongside ok=false
//     and which a caller who skipped the check would write).
func TestCharsetEncoder_NoSubstitutionAnywhere(t *testing.T) {
	// Runes chosen to be outside at least one supported repertoire each:
	// Latin Extended-A, Greek, Cyrillic, Han, an emoji, and U+FFFD itself,
	// which is the replacement character a decoder would have produced.
	probes := []rune{'Ā', 'Ω', 'Ж', '日', '😀', utf8.RuneError, '€', 'ñ'}

	for i := range charsetTable {
		e := &charsetTable[i]
		t.Run(e.name, func(t *testing.T) {
			enc := mustEncoder(t, e.name)

			// Every byte the charset defines, through its own rune and
			// back. This is the 256-byte sweep E3-S3 ran on the decode
			// side, run in reverse.
			if enc.dec.byteText != nil {
				for b := 0; b < 256; b++ {
					text := enc.dec.byteText[b]
					if text == "" {
						continue
					}
					out, at := enc.encode(text)
					if at >= 0 {
						t.Errorf("byte 0x%02x decodes to %q but that text will not encode back (offset %d)", b, text, at)
						continue
					}
					back, bad := enc.dec.decode(out)
					if bad >= 0 || back != text {
						t.Errorf("byte 0x%02x: %q encoded to % x which decodes to %q, not back to itself", b, text, out, back)
					}
				}
			}

			for _, r := range probes {
				out, at := enc.encode(string(r))
				if at < 0 {
					// Encodable. It must be faithful, which encode
					// already guarantees, and it must not be a
					// substitution byte standing in for the rune.
					if len(out) == 1 && (out[0] == '?' || out[0] == 0x1A) && r != '?' && r != 0x1A {
						t.Errorf("U+%04X encoded to the substitution byte 0x%02x; a substituted character is indistinguishable from data", r, out[0])
					}
					back, bad := enc.dec.decode(out)
					if bad >= 0 || back != string(r) {
						t.Errorf("U+%04X encoded to % x which decodes to %q", r, out, back)
					}
					continue
				}
				// Refused. It must have produced NOTHING — a partial
				// encode reaching the wire is the same fault as a
				// substitution.
				if out != nil {
					t.Errorf("U+%04X was refused at offset %d but still produced % x", r, at, out)
				}
			}
		})
	}
}

// TestCharsetEncoder_ReportsTheOffendingRune pins the diagnostic contract:
// the offset is a BYTE offset into the UTF-8 input, so a caller can slice
// straight to the character at fault.
func TestCharsetEncoder_ReportsTheOffendingRune(t *testing.T) {
	enc := mustEncoder(t, "windows-1252")
	// "Zürich" is 7 UTF-8 bytes; the Greek capital omega is unrepresentable
	// in windows-1252 and starts at byte 7.
	const in = "ZürichΩ"
	out, at := enc.encode(in)
	if at != 7 {
		t.Fatalf("offset = %d, want 7 (%q)", at, in)
	}
	if out != nil {
		t.Errorf("bytes = % x, want none", out)
	}
	if r, _ := utf8.DecodeRuneInString(in[at:]); r != 'Ω' {
		t.Errorf("the offset points at %q, not the offending rune", r)
	}
}

// TestCharsetEncoder_RefusesAnEncodableButUnfaithfulRune covers the case the
// per-rune repertoire check alone misses: a rune the encoding accepts and
// then hands back as a DIFFERENT character.
//
// GB18030 is the only charset in the table that does it — its Private Use
// Area mapping is asymmetric, so U+E000 encodes to four bytes that decode to
// U+F014. That is a quiet change of a value rather than a loud refusal, and
// it is refused for exactly the reason a substitution is.
func TestCharsetEncoder_RefusesAnEncodableButUnfaithfulRune(t *testing.T) {
	enc := mustEncoder(t, "GB18030")

	raw, err := enc.enc.NewEncoder().Bytes([]byte(string(rune(0xE000))))
	if err != nil {
		t.Skip("GB18030 no longer encodes U+E000; the premise of this test has changed")
	}
	if back, _ := enc.dec.decode(raw); back == string(rune(0xE000)) {
		t.Skip("GB18030 now round-trips U+E000; the premise of this test has changed")
	}

	if out, at := enc.encode(string(rune(0xE000))); at < 0 {
		t.Errorf("U+E000 encoded to % x; it decodes back to a different character, which is a silent change of the value", out)
	}
	// A neighbouring character that DOES round-trip is untouched by the check.
	if _, at := enc.encode("日"); at >= 0 {
		t.Errorf("a faithfully round-tripping character was refused at offset %d", at)
	}
}

// TestCharsetEncoder_ASCIIIsIdentity licenses the 7-bit fast path. Every
// charset the format can carry encodes ASCII as itself — the 0x20 padding
// and the '=' / tab / newline delimiters of records 7/5, 7/13 and 7/14 all
// depend on it.
func TestCharsetEncoder_ASCIIIsIdentity(t *testing.T) {
	for i := range charsetTable {
		e := &charsetTable[i]
		t.Run(e.name, func(t *testing.T) {
			enc := mustEncoder(t, e.name)
			for r := rune(0); r < utf8.RuneSelf; r++ {
				out, at := enc.encode(string(r))
				if at >= 0 || len(out) != 1 || out[0] != byte(r) {
					t.Fatalf("0x%02x encoded to % x (offset %d), want itself", r, out, at)
				}
			}
		})
	}
}

// TestCharsetEncoder_USASCIIRefusesHighText: US-ASCII is a real restriction
// and not a synonym for UTF-8 on the way out either.
func TestCharsetEncoder_USASCIIRefusesHighText(t *testing.T) {
	enc := mustEncoder(t, "US-ASCII")
	if _, at := enc.encode("Zürich"); at != 1 {
		t.Errorf("offset = %d, want 1; US-ASCII cannot carry U+00FC", at)
	}
	if _, at := enc.encode("Zurich"); at >= 0 {
		t.Errorf("plain ASCII was refused at offset %d", at)
	}
}

// TestCharsetTable_CodeRoundTrips pins the two directions of the record
// 7/3 <-> 7/20 mapping against each other. charsetCodeFor is what fills in
// the emitted 7/3, and a code that did not resolve back to the charset it
// came from would make the file contradict itself.
func TestCharsetTable_CodeRoundTrips(t *testing.T) {
	for i := range charsetTable {
		e := &charsetTable[i]
		if e.code == 0 {
			t.Errorf("%s carries no record 7/3 character code", e.name)
			continue
		}
		if got := charsetCodeFor(e.name); got != e.code {
			t.Errorf("charsetCodeFor(%q) = %d, want %d", e.name, got, e.code)
		}
		named, ok := charsetForCode(e.code)
		if !ok {
			t.Errorf("charsetForCode(%d) resolved nothing, but %s declares it", e.code, e.name)
			continue
		}
		back, found := charsetIndex[normaliseCharsetName(named)]
		if !found || back.name != e.name {
			t.Errorf("code %d resolves to %q, which is not %s", e.code, named, e.name)
		}
	}
}

// ---------------------------------------------------------------------------
// Resolution: which charset the emitted file is written in
// ---------------------------------------------------------------------------

// TestResolveWriteCharset_Precedence pins the four-way decision, including
// the two spellings that must survive it.
func TestResolveWriteCharset_Precedence(t *testing.T) {
	side := func(c Charset) *SidecarResolution {
		return &SidecarResolution{Document: &Document{Payload: Payload{Charset: c}}}
	}
	for _, tc := range []struct {
		name         string
		opt          string
		sidecar      *SidecarResolution
		wantName     string
		wantDeclared string
		wantCode     int32
	}{
		{
			name: "no source at all writes the default",
			// A synth cohort, a CSV import, a processing run: no SPSS
			// provenance, so UTF-8 and a UTF-8 declaration.
			wantName: "UTF-8", wantDeclared: "UTF-8", wantCode: 65001,
		},
		{
			name:    "the source's 7/20 name wins, in the source's own spelling",
			sidecar: side(Charset{DeclaredName: "cp1252", DeclaredCode: 1252, Declared: true, ResolvedName: "windows-1252"}),
			// "cp1252", not "windows-1252": record 7/20 is a quotation.
			wantName: "windows-1252", wantDeclared: "cp1252", wantCode: 1252,
		},
		{
			name: "a stale 7/3 code rides along with the name that won",
			// The source stated code 3 ("8-bit ASCII") and named
			// windows-1252. Re-emitting both reproduces the file the source
			// was, disagreement included; inventing 1252 would be tidying
			// up a statement the source made.
			sidecar:  side(Charset{DeclaredName: "windows-1252", DeclaredCode: 3, Declared: true}),
			wantName: "windows-1252", wantDeclared: "windows-1252", wantCode: 3,
		},
		{
			name:    "a code with no name resolves to the canonical spelling",
			sidecar: side(Charset{DeclaredCode: 1251, Declared: true, ResolvedName: "windows-1251"}),
			// Nothing was quoted, so nothing is quoted back.
			wantName: "windows-1251", wantDeclared: "windows-1251", wantCode: 1251,
		},
		{
			name:    "the caller's option outranks the source",
			opt:     "UTF-8",
			sidecar: side(Charset{DeclaredName: "cp1252", DeclaredCode: 1252, Declared: true}),
			// The escape hatch for a cohort edited since import.
			wantName: "UTF-8", wantDeclared: "UTF-8", wantCode: 65001,
		},
		{
			name:     "the caller's own spelling is quoted too",
			opt:      "latin1",
			wantName: "ISO-8859-1", wantDeclared: "latin1", wantCode: 28591,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := resolveWriteCharset(DictionaryRequest{
				Sidecar: tc.sidecar,
				Options: WriterOptions{Charset: tc.opt},
			})
			if err != nil {
				t.Fatalf("resolveWriteCharset: %v", err)
			}
			if enc.name != tc.wantName {
				t.Errorf("charset = %q, want %q", enc.name, tc.wantName)
			}
			if enc.declaredName != tc.wantDeclared {
				t.Errorf("record 7/20 = %q, want %q", enc.declaredName, tc.wantDeclared)
			}
			if enc.declaredCode != tc.wantCode {
				t.Errorf("record 7/3 code = %d, want %d", enc.declaredCode, tc.wantCode)
			}
		})
	}
}

// TestResolveWriteCharset_RefusesAnUnwritableCharset: falling back to UTF-8
// while declaring something else is the corruption this whole story exists
// to stop, so an unusable name is a hard refusal.
func TestResolveWriteCharset_RefusesAnUnwritableCharset(t *testing.T) {
	for _, name := range []string{"EBCDIC", "UTF-16", "not-a-charset"} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveWriteCharset(DictionaryRequest{Options: WriterOptions{Charset: name}})
			if err == nil {
				t.Fatal("resolveWriteCharset accepted a charset the writer cannot express the format in")
			}
			if ce := codedErr(t, err); ce.Code != perr.PULSE_SPSS_CHARSET_UNSUPPORTED {
				t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_UNSUPPORTED)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The declaration and the bytes
// ---------------------------------------------------------------------------

// TestBuildDictionary_ReEncodesIntoTheSourceCharset is the story's headline:
// a file whose source declared windows-1252 goes back out in windows-1252,
// declaring windows-1252 — not UTF-8 bytes under a windows-1252 header,
// which would corrupt every non-ASCII label.
func TestBuildDictionary_ReEncodesIntoTheSourceCharset(t *testing.T) {
	schema, res := exportFixture(t, latin1Spec())
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 0, Compression: compressionNone})

	// The dictionary bytes must hold the codepage form of the text, and
	// must NOT hold its UTF-8 form. Both halves matter: finding the
	// windows-1252 byte proves the encode ran, and not finding the UTF-8
	// sequence proves nothing was written twice or left behind.
	for _, tc := range []struct {
		what   string
		w1252  []byte
		asUTF8 string
	}{
		{"the file label", []byte("Enqu\xeate 2024"), "Enquête 2024"},
		{"a variable label", []byte("Genre du r\xe9pondant"), "Genre du répondant"},
		{"a value label", []byte("M\xe4nnlich"), "Männlich"},
		{"a long variable name", []byte("Identit\xe4t"), "Identität"},
		{"a document line", []byte("Collect\xe9 \xe0 Gen\xe8ve."), "Collecté à Genève."},
	} {
		if !bytes.Contains(plan.Bytes, tc.w1252) {
			t.Errorf("%s is not on the wire as windows-1252 (% x)", tc.what, tc.w1252)
		}
		if bytes.Contains(plan.Bytes, []byte(tc.asUTF8)) {
			t.Errorf("%s is on the wire as UTF-8 (%q) in a file declaring windows-1252", tc.what, tc.asUTF8)
		}
	}

	d := reparse(t, plan)
	if d.charsetName != "windows-1252" {
		t.Errorf("record 7/20 declares %q, want %q", d.charsetName, "windows-1252")
	}
	if !d.machineInteger.present || d.machineInteger.characterCode != 1252 {
		t.Errorf("record 7/3 character code = %d, want 1252", d.machineInteger.characterCode)
	}
	for _, w := range d.warnings {
		t.Errorf("re-parsing the emitted file warned: %v", w)
	}
}

// TestBuildDictionary_DataValuesGoOutInTheFileCharset covers the other half:
// a string DATUM is codepage bytes too, not just the dictionary's metadata.
func TestBuildDictionary_DataValuesGoOutInTheFileCharset(t *testing.T) {
	fs, cohort, _ := importFixture(t, latin1Spec())
	sav := exportCohort(t, fs, cohort, WriterOptions{Uncompressed: true})

	if !bytes.Contains(sav, []byte("Z\xfcrich")) {
		t.Error("the data section does not carry \"Zürich\" as windows-1252")
	}
	if bytes.Contains(sav, []byte("Zürich")) {
		t.Error("the data section carries \"Zürich\" as UTF-8 in a file declaring windows-1252")
	}
}

// ---------------------------------------------------------------------------
// The acceptance criterion: a non-UTF-8 fixture survives import then export
// ---------------------------------------------------------------------------

// TestExport_NonUTF8FixtureRoundTripsByteComparably runs the reference
// windows-1252 fixture all the way round — build, import, export — and
// compares the emitted labels and values BYTE for byte against the source's.
//
// It is a byte comparison and not a text one on purpose. Reading both files
// back with our own reader and comparing the decoded strings would pass even
// if the writer emitted UTF-8 under a UTF-8 declaration, because our reader
// would decode that correctly too. What has to be true is stronger: the same
// bytes, in the same charset, in the same places.
func TestExport_NonUTF8FixtureRoundTripsByteComparably(t *testing.T) {
	spec := latin1Spec()
	source := build(t, spec)

	fs, cohort, _ := importFixture(t, spec)
	out := exportCohort(t, fs, cohort, WriterOptions{Uncompressed: true})

	// Every text-bearing slot of the fixture, in its wire form. The fixture
	// puts non-ASCII into all of them precisely because charset bugs are
	// per-call-site.
	for _, tc := range []struct {
		what string
		wire []byte
	}{
		{"the file label", []byte("Enqu\xeate 2024")},
		{"a variable label", []byte("Genre du r\xe9pondant")},
		{"a long variable name", []byte("Identit\xe4t")},
		{"a value label", []byte("M\xe4nnlich")},
		{"a second value label", []byte("Weiblich")},
		{"a long-string value label", []byte("\xc9lev\xe9")},
		{"a document line", []byte("Collect\xe9 \xe0 Gen\xe8ve.")},
		{"a string datum", []byte("Z\xfcrich")},
		{"a second string datum", []byte("Gen\xe8ve")},
	} {
		if !bytes.Contains(source, tc.wire) {
			t.Fatalf("%s is not in the SOURCE fixture as % x; the test's premise is wrong", tc.what, tc.wire)
		}
		if !bytes.Contains(out, tc.wire) {
			t.Errorf("%s did not survive the round trip byte-comparably (% x)", tc.what, tc.wire)
		}
	}

	// And the whole thing still reads back as the same text, which is the
	// semantic half of the same claim.
	head, rows := savRows(t, out)
	wantHead := []string{"Identität", "SEX", "CITY", "GRADE"}
	if len(head) != len(wantHead) {
		t.Fatalf("header = %v, want %v", head, wantHead)
	}
	for i := range wantHead {
		if head[i] != wantHead[i] {
			t.Errorf("header[%d] = %q, want %q", i, head[i], wantHead[i])
		}
	}
	if len(rows) != 2 || rows[0][2] != "Zürich" || rows[1][2] != "Genève" {
		t.Errorf("rows = %v, want the two cities back", rows)
	}
}

// TestExport_NonUTF8FixtureReimportsIdentically closes the loop the other
// way: the emitted file, put back through the import path, produces the same
// cohort bytes the source did. Nothing was lost and nothing was invented.
func TestExport_NonUTF8FixtureReimportsIdentically(t *testing.T) {
	spec := latin1Spec()
	fs, cohort, _ := importFixture(t, spec)
	first, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading the imported cohort: %v", err)
	}

	out := exportCohort(t, fs, cohort, WriterOptions{})
	if second := reimport(t, out); !bytes.Equal(first, second) {
		t.Errorf("re-importing the emitted file gives a different cohort: %d vs %d bytes", len(first), len(second))
	}
}

// ---------------------------------------------------------------------------
// Rule 2: widths are byte counts, not rune counts
// ---------------------------------------------------------------------------

// TestBuildDictionary_RecomputesWidthFromEncodedBytes is the second hard rule
// as the writer sees it. "Zürich" is 7 bytes as UTF-8 and 6 as windows-1252,
// so a width taken before the encode is the wrong number in one of the two
// files — and a width taken from the RUNE count is the wrong number in both.
func TestBuildDictionary_RecomputesWidthFromEncodedBytes(t *testing.T) {
	for _, tc := range []struct {
		charset string
		want    int
	}{
		{"UTF-8", 7},
		{"windows-1252", 6},
		{"ISO-8859-1", 6},
	} {
		t.Run(tc.charset, func(t *testing.T) {
			plan := synthIn(t, tc.charset, dictField(t, "city", "Zürich"))
			col := planColumn(t, plan, "city")
			if col.Width != tc.want {
				t.Errorf("declared width = %d, want %d BYTES in %s", col.Width, tc.want, tc.charset)
			}
			if n := utf8.RuneCountInString("Zürich"); col.Width == n && tc.want != n {
				t.Error("the width is the rune count; SPSS widths are byte counts")
			}
			// The A format's width IS the physical variable's byte width,
			// so it moves with it or the file contradicts itself.
			if int(col.PrintFormat.Width) != tc.want {
				t.Errorf("print format width = %d, want %d", col.PrintFormat.Width, tc.want)
			}
			d := reparse(t, plan)
			if got := readVar(t, d, "city").width; got != tc.want {
				t.Errorf("the emitted record type 2 declares %d, want %d", got, tc.want)
			}
		})
	}
}

// TestBuildDictionary_WidensARecordedWidthRatherThanTruncating: a recorded
// A6 windows-1252 variable written out as UTF-8 needs seven bytes for the
// same value. Widening is the only answer that neither truncates nor fails —
// SPSS pads to the declared width and the read path trims the padding, so
// the extra byte is invisible on the way home.
func TestBuildDictionary_WidensARecordedWidthRatherThanTruncating(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "CITY", Width: 6}},
		Cases:             [][]spsstest.Value{{spsstest.Text("Zürich")}},
	}
	fs, cohort, _ := importFixture(t, spec)

	// Same charset: the recorded width is already true of the encoded
	// bytes, so it is left exactly as the source declared it.
	same := exportCohort(t, fs, cohort, WriterOptions{Uncompressed: true})
	if got := reparse(t, &DictionaryPlan{Bytes: same}); readVar(t, got, "CITY").width != 6 {
		t.Errorf("width = %d, want the source's own 6", readVar(t, got, "CITY").width)
	}

	// UTF-8: seven bytes are needed and seven are declared.
	wide := exportCohort(t, fs, cohort, WriterOptions{Uncompressed: true, Charset: "UTF-8"})
	d := reparse(t, &DictionaryPlan{Bytes: wide})
	if got := readVar(t, d, "CITY").width; got != 7 {
		t.Fatalf("width = %d, want 7; a 6-byte declaration would truncate the value", got)
	}
	_, rows := savRows(t, wide)
	if len(rows) != 1 || rows[0][0] != "Zürich" {
		t.Errorf("rows = %v, want the whole value back", rows)
	}
}

// TestBuildDictionary_KeepsAWiderRecordedWidth: recomputation widens and
// never narrows. The source said A20 and A20 is what the round trip owes
// back, even though every value in it fits in six bytes.
func TestBuildDictionary_KeepsAWiderRecordedWidth(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "CITY", Width: 20}},
		Cases:             [][]spsstest.Value{{spsstest.Text("Zürich")}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 0, Compression: compressionNone})
	if got := planColumn(t, plan, "CITY").Width; got != 20 {
		t.Errorf("width = %d, want the source's declared 20", got)
	}
}

// TestBuildDictionary_RefusesUnencodableText is the first hard rule at the
// dictionary boundary: a character the target charset has no form for stops
// the export naming the variable and the value, rather than reaching the
// wire as a replacement character.
//
// The cause is ordinary — a cohort edited since it was imported holds UTF-8
// text a legacy codepage cannot express — which is why the error has to name
// enough for a caller to act on it.
func TestBuildDictionary_RefusesUnencodableText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		field   encoding.Field
		wantVar string
		wantVal string
	}{
		{
			name:    "a value",
			field:   dictField(t, "city", "Zürich", "Москва"),
			wantVar: "city", wantVal: "Москва",
		},
		{
			name: "a variable label",
			field: encoding.Field{
				Name: "score", Type: encoding.FieldTypeU8,
				Description: "Σ of the parts",
			},
			wantVar: "score", wantVal: "Σ of the parts",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ce := emitFails(t, DictionaryRequest{
				Schema:      &encoding.Schema{Fields: []encoding.Field{tc.field}},
				Cases:       0,
				Compression: compressionNone,
				Options:     WriterOptions{Charset: "windows-1252"},
			}, perr.PULSE_SPSS_CHARSET_UNENCODABLE)

			if got, _ := ce.Details[perr.DetailSPSSVariable].(string); got != tc.wantVar {
				t.Errorf("details variable = %q, want %q", got, tc.wantVar)
			}
			if got, _ := ce.Details[perr.DetailSPSSValue].(string); got != tc.wantVal {
				t.Errorf("details value = %q, want %q", got, tc.wantVal)
			}
			if got, _ := ce.Details[perr.DetailSPSSCharset].(string); got != "windows-1252" {
				t.Errorf("details charset = %q, want %q", got, "windows-1252")
			}
			// The message has to carry the character itself: "some rune
			// somewhere is wrong" is not actionable on a wide cohort.
			if !strings.Contains(ce.Message, "U+") {
				t.Errorf("message does not name the offending code point: %s", ce.Message)
			}
			if strings.Contains(ce.Message, "�") {
				t.Errorf("message carries a replacement character: %s", ce.Message)
			}
		})
	}
}

// TestBuildDictionary_RefusesWidthOverflow covers every field whose width the
// FORMAT fixes, where widening is not available. Each one is a refusal and
// never a truncation.
func TestBuildDictionary_RefusesWidthOverflow(t *testing.T) {
	// A string variable past the 32767-byte ceiling SPSS puts on one. This
	// is the case dict_synth.go's cannotExpress used to hold under
	// PULSE_SPSS_EXPORT_UNSUPPORTED; it is a width overflow and now says so.
	t.Run("a string past the very-long-string ceiling", func(t *testing.T) {
		ce := emitFails(t, DictionaryRequest{
			Schema: &encoding.Schema{Fields: []encoding.Field{
				dictField(t, "notes", strings.Repeat("x", maxVeryLongStringWidth+1)),
			}},
			Cases: 0, Compression: compressionNone,
		}, perr.PULSE_SPSS_WIDTH_OVERFLOW)
		if got, _ := ce.Details[perr.DetailSPSSDeclaredWidth].(int); got != maxVeryLongStringWidth {
			t.Errorf("details declared_width = %v, want %d", ce.Details[perr.DetailSPSSDeclaredWidth], maxVeryLongStringWidth)
		}
		if got, _ := ce.Details[perr.DetailSPSSWidth].(int); got != maxVeryLongStringWidth+1 {
			t.Errorf("details width = %v, want %d", ce.Details[perr.DetailSPSSWidth], maxVeryLongStringWidth+1)
		}
	})

	// A value label past the 255 bytes a record type 3's ONE-BYTE length
	// field can count. Writing it would wrap the count and desynchronise
	// every record after it.
	t.Run("a value label past its one-byte length field", func(t *testing.T) {
		// The euro sign is ONE byte in windows-1252 (0x80) and THREE in
		// UTF-8, so a label that fits the count comfortably in the source
		// charset overflows it when the file is written as UTF-8. That
		// tripling is the whole reason the ceiling has to be rechecked
		// after the encode rather than before it.
		spec := spsstest.Spec{
			CharacterEncoding: "windows-1252",
			Vars:              []spsstest.Var{{Name: "Q1", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}}},
			ValueLabels: []spsstest.ValueLabelSet{{
				Vars:   []string{"Q1"},
				Labels: []spsstest.ValueLabel{{Value: spsstest.Num(1), Label: strings.Repeat("€", 100)}},
			}},
			Cases: [][]spsstest.Value{{spsstest.Num(1)}},
		}
		schema, res := exportFixture(t, spec)
		// 100 windows-1252 bytes: it fits, and re-emitting it is fine.
		emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 1, Compression: compressionNone})
		// 300 UTF-8 bytes: it does not, and the one-byte count would wrap.
		ce := emitFails(t, DictionaryRequest{
			Schema: schema, Sidecar: res, Cases: 1, Compression: compressionNone,
			Options: WriterOptions{Charset: "UTF-8"},
		}, perr.PULSE_SPSS_WIDTH_OVERFLOW)
		if got, _ := ce.Details[perr.DetailSPSSDeclaredWidth].(int); got != maxValueLabelLen {
			t.Errorf("details declared_width = %v, want %d", ce.Details[perr.DetailSPSSDeclaredWidth], maxValueLabelLen)
		}
	})

	// The 64-byte header file label and the 80-byte record type 6 line.
	for _, tc := range []struct {
		name  string
		spec  func(*spsstest.Spec)
		field string
	}{
		{
			name:  "the header file label",
			spec:  func(s *spsstest.Spec) { s.FileLabel = strings.Repeat("é", 40) },
			field: "file label",
		},
		{
			name:  "a record type 6 document line",
			spec:  func(s *spsstest.Spec) { s.Documents = []string{strings.Repeat("é", 50)} },
			field: "document line",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := spsstest.Spec{
				CharacterEncoding: "windows-1252",
				Vars:              []spsstest.Var{{Name: "Q1", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}}},
				Cases:             [][]spsstest.Value{{spsstest.Num(1)}},
			}
			tc.spec(&spec)
			schema, res := exportFixture(t, spec)
			emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 1, Compression: compressionNone})
			ce := emitFails(t, DictionaryRequest{
				Schema: schema, Sidecar: res, Cases: 1, Compression: compressionNone,
				Options: WriterOptions{Charset: "UTF-8"},
			}, perr.PULSE_SPSS_WIDTH_OVERFLOW)
			if !strings.Contains(ce.Message, tc.field) {
				t.Errorf("message does not name the %s: %s", tc.field, ce.Message)
			}
		})
	}
}

// TestTranscode_RefusesAShortNameThatOutgrewItsField covers the one
// fixed-width field no public entry point can currently overflow, so the
// guard is exercised at the pass instead of through BuildDictionary.
//
// Both front-ends hand this pass an ASCII short name — synthesis folds every
// non-ASCII byte to '_' in the name minter, and a source's own short name is
// ASCII by the format's own naming rules — so the encode is an identity and
// the width cannot move. The check stays because "it cannot happen today" is
// not a reason to let dictEncoder.ascii cut a name silently if it ever does.
func TestTranscode_RefusesAShortNameThatOutgrewItsField(t *testing.T) {
	f := &outFile{
		charset: mustEncoder(t, "UTF-8"),
		vars: []*outVar{{
			// Eight characters, sixteen UTF-8 bytes.
			name: "ÉÉÉÉÉÉÉÉ", shortName: "ÉÉÉÉÉÉÉÉ",
			segments: []SegmentPlan{{Name: "ÉÉÉÉÉÉÉÉ", Elements: 1}},
		}},
	}
	err := applyCharsetWrite(f)
	if err == nil {
		t.Fatal("applyCharsetWrite accepted a short name that does not fit its eight-byte field")
	}
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_WIDTH_OVERFLOW {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_WIDTH_OVERFLOW)
	}
	// The diagnostic names the variable as UTF-8, not as the wire bytes the
	// pass has just produced.
	if got, _ := ce.Details[perr.DetailSPSSVariable].(string); got != "ÉÉÉÉÉÉÉÉ" {
		t.Errorf("details variable = %q, want the UTF-8 name", got)
	}
}

// ---------------------------------------------------------------------------
// Re-segmentation: encode, then measure, then slice
// ---------------------------------------------------------------------------

// TestBuildDictionary_ResegmentsOnEncodedBytes is the write-side mirror of
// E3-S4's straddle fixture, and it is the ordering crux of this story.
//
// A very long string is sliced across physical variables on a fixed 252-byte
// stride, so a multi-byte character CAN land across a segment boundary. The
// reader joins the pieces and then decodes once (dataPlan.stringBytes), so
// the writer has to encode once and then slice — segmenting the UTF-8 form
// and encoding each piece would put a partial character on the wire.
//
// The value is built so that byte 252 falls INSIDE a character in every
// charset under test, which is what makes the test capable of failing.
func TestBuildDictionary_ResegmentsOnEncodedBytes(t *testing.T) {
	// One ASCII byte then 130 three-byte characters: character 83 spans
	// bytes 250..253, so the 252-byte segment boundary is inside it.
	straddle := "a" + strings.Repeat("日", 130)

	for _, charset := range []string{"UTF-8", "Shift_JIS", "EUC-JP"} {
		t.Run(charset, func(t *testing.T) {
			enc := mustEncoder(t, charset)
			wire, at := enc.encode(straddle)
			if at >= 0 {
				t.Fatalf("the fixture is not encodable in %s (offset %d)", charset, at)
			}
			if len(wire) <= segmentContentWidth {
				t.Fatalf("the encoded value is %d bytes; the test needs more than %d to segment at all",
					len(wire), segmentContentWidth)
			}
			// Byte 252 must be inside a character, or the test proves
			// nothing about straddling.
			if back, bad := enc.dec.decode(wire[:segmentContentWidth]); bad < 0 {
				t.Fatalf("the first %d bytes decode cleanly to %q in %s; the boundary does not straddle a character",
					segmentContentWidth, back, charset)
			}

			plan := synthIn(t, charset, dictField(t, "notes", straddle))
			col := planColumn(t, plan, "notes")

			if col.Width != len(wire) {
				t.Errorf("declared width = %d, want the encoded %d bytes", col.Width, len(wire))
			}
			if want := vlsSegmentCount(len(wire)); len(col.Segments) != want {
				t.Fatalf("segments = %d, want %d for %d encoded bytes", len(col.Segments), want, len(wire))
			}
			content := 0
			for _, sg := range col.Segments {
				content += sg.Content
			}
			if content != len(wire) {
				t.Errorf("the segments carry %d byte(s), want the encoded %d", content, len(wire))
			}

			// The record 7/14 declaration must state the ENCODED logical
			// width, because that is what the reader reassembles against.
			d := reparse(t, plan)
			var declared int
			for _, vls := range d.veryLongStrings {
				declared = vls.width
			}
			if declared != len(wire) {
				t.Errorf("record 7/14 declares %d, want the encoded %d", declared, len(wire))
			}

			// And the whole thing comes back, which is the only claim that
			// really matters: a character split across the boundary and
			// encoded per piece would not.
			sav := encodeCases(t, plan, &encoding.Schema{Fields: []encoding.Field{dictField(t, "notes", straddle)}},
				Case{{Num: 0}})
			_, rows := savRows(t, sav)
			if len(rows) != 1 || rows[0][0] != straddle {
				got := ""
				if len(rows) == 1 {
					got = rows[0][0]
				}
				t.Errorf("the value did not survive segmentation: %d rune(s) back, want %d",
					utf8.RuneCountInString(got), utf8.RuneCountInString(straddle))
			}
		})
	}
}

// TestBuildDictionary_ReproducesTheSourceSegmentation: when nothing about the
// width moved, the source's own physical layout — segment names included —
// is what goes back out. Re-deriving a layout that happens to differ would
// still read back correctly and would still not be the file the source was.
func TestBuildDictionary_ReproducesTheSourceSegmentation(t *testing.T) {
	spec := spsstest.Spec{
		CharacterEncoding: "windows-1252",
		Vars:              []spsstest.Var{{Name: "NOTES", Width: 400}},
		Cases:             [][]spsstest.Value{{spsstest.Text("Genève " + strings.Repeat("x", 380))}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 1, Compression: compressionNone})
	col := planColumn(t, plan, "NOTES")

	if col.Width != 400 {
		t.Errorf("width = %d, want the source's 400", col.Width)
	}
	if len(col.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(col.Segments))
	}
	want := res.Document.Payload.Variables[0].VeryLongString
	if want == nil || len(want.Segments) != 2 {
		t.Fatal("the sidecar did not record a two-segment layout; the test's premise is wrong")
	}
	for i, sg := range col.Segments {
		if sg.Name != want.Segments[i].Name || sg.Width != want.Segments[i].Width {
			t.Errorf("segment %d = %q/%d, want the source's %q/%d",
				i, sg.Name, sg.Width, want.Segments[i].Name, want.Segments[i].Width)
		}
	}
}

// TestResegment_MintsOnlyTheSegmentsTheSourceDidNotHave: a widened very long
// string keeps the source's names for the segments the source had, and the
// minted ones cannot land on a name already in use.
func TestResegment_MintsOnlyTheSegmentsTheSourceDidNotHave(t *testing.T) {
	f := &outFile{vars: []*outVar{
		{name: "OTHER", shortName: "NOTES2", segments: []SegmentPlan{{Name: "NOTES2", Width: 0}}},
		{
			name: "NOTES", shortName: "NOTES", width: 300,
			segments: []SegmentPlan{
				{Name: "NOTES", Width: maxSegmentWidth, Content: segmentContentWidth, Elements: 32},
				{Name: "SRCTAIL", Width: 48, Content: 48, Elements: 6},
			},
		},
	}}
	m := seedMinter(f)
	v := f.vars[1]
	v.width = 600
	resegment(m, v)

	if len(v.segments) != 3 {
		t.Fatalf("segments = %d, want 3 for a 600-byte value", len(v.segments))
	}
	if v.segments[0].Name != "NOTES" || v.segments[1].Name != "SRCTAIL" {
		t.Errorf("segments 0 and 1 are %q and %q; the source's own names must be kept",
			v.segments[0].Name, v.segments[1].Name)
	}
	if v.segments[2].Name == "NOTES2" {
		t.Error("the minted segment collided with another variable's short name")
	}
	if v.segments[2].Name == "" {
		t.Error("the minted segment has no name")
	}
}

// ---------------------------------------------------------------------------
// The verbatim extension payloads
// ---------------------------------------------------------------------------

// TestBuildDictionary_VerbatimExtensionsPassThrough is the routed E3-S3
// question, answered: records 7/10, 7/17 and 7/18 are captured as raw bytes
// and never decoded, so they are re-emitted VERBATIM. Transcoding them would
// require decoding them first, which the reader deliberately does not do.
func TestBuildDictionary_VerbatimExtensionsPassThrough(t *testing.T) {
	spec := richSpec() // ASCII attribute records, a cp1252 declaration
	schema, res := exportFixture(t, spec)

	// Same charset as the source: verbatim is exactly right, and silent.
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})
	for _, rt := range []*RawText{res.Document.Payload.FileAttributes, res.Document.Payload.VariableAttributes} {
		if rt == nil {
			t.Fatal("the fixture recorded no attribute record; the test's premise is wrong")
		}
		if !bytes.Contains(plan.Bytes, rt.Raw) {
			t.Errorf("the record 7/%d payload was not re-emitted verbatim", rt.Subtype)
		}
	}
	for _, w := range plan.Warnings {
		if w.Code == perr.PULSE_SPSS_CHARSET_MISMATCH {
			t.Errorf("verbatim passthrough into the source's own charset warned: %v", w)
		}
	}

}

// TestVerbatimExtensions_WarnWhenTheTargetCharsetMoves: verbatim bytes in a
// file that declares a different charset is the one case where passthrough is
// wrong. They are still emitted — the reader never decoded them, so they are
// the only record of what the source said and dropping them would lose more
// than mislabelling them does — and the disagreement is a warning.
//
// It is built from the emission model directly because the fixture generator
// will not write a non-ASCII 7/17 or 7/18 payload (extension text must be
// printable 7-bit ASCII there), which is precisely the state the routed
// E3-S3 note recorded: the generator's attribute text is ASCII-only and
// nothing decodes it.
func TestVerbatimExtensions_WarnWhenTheTargetCharsetMoves(t *testing.T) {
	build := func(source, target string) []*perr.CodedError {
		t.Helper()
		f := &outFile{
			sourceCharset: source,
			charset:       mustEncoder(t, target),
			fileAttrs:     &RawText{Subtype: extFileAttributes, Raw: []byte("$@Ville('Gen\xe8ve')\n")},
			varAttrs:      &RawText{Subtype: extVarAttributes, Raw: []byte("SEX:$@Origine('cle')\n")},
		}
		if err := applyCharsetWrite(f); err != nil {
			t.Fatalf("applyCharsetWrite: %v", err)
		}
		var out []*perr.CodedError
		for _, w := range f.warnings {
			if w.Code == perr.PULSE_SPSS_CHARSET_MISMATCH {
				out = append(out, w)
			}
		}
		// Whatever else happens, the bytes are untouched.
		if !bytes.Contains(f.fileAttrs.Raw, []byte("Gen\xe8ve")) {
			t.Error("the verbatim payload was rewritten; it is the only record of what the source said")
		}
		return out
	}

	if got := build("windows-1252", "windows-1252"); len(got) != 0 {
		t.Errorf("%d warning(s) for verbatim bytes in the source's own charset, want 0", len(got))
	}
	// Only the 7/17 payload carries a high byte; the ASCII 7/18 one is
	// correct under every supported charset and must stay silent.
	got := build("windows-1252", "UTF-8")
	if len(got) != 1 {
		t.Fatalf("%d warning(s), want 1 — the non-ASCII payload only", len(got))
	}
	if st, _ := got[0].Details[perr.DetailSPSSSubtype].(int32); st != extFileAttributes {
		t.Errorf("the warning names subtype %v, want %d", got[0].Details[perr.DetailSPSSSubtype], extFileAttributes)
	}
}

// TestBuildDictionary_ASCIIVerbatimExtensionsAreSilent: a pure-ASCII payload
// is correct under every supported charset, so re-emitting one verbatim into
// a differently-declared file is not a disagreement and must not warn. This
// is the state of the fixture generator's own 7/17 and 7/18 records.
func TestBuildDictionary_ASCIIVerbatimExtensionsAreSilent(t *testing.T) {
	spec := richSpec() // its attribute records are ASCII, its charset cp1252
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{
		Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone,
		Options: WriterOptions{Charset: "UTF-8"},
	})
	for _, w := range plan.Warnings {
		if w.Code == perr.PULSE_SPSS_CHARSET_MISMATCH {
			t.Errorf("an ASCII verbatim payload warned about a charset disagreement it does not have: %v", w)
		}
	}
}

// ---------------------------------------------------------------------------
// The synthesised path
// ---------------------------------------------------------------------------

// TestSynthesise_DefaultsToUTF8: a cohort with no SPSS provenance has no
// source charset to be faithful to, so it gets UTF-8 and says so.
func TestSynthesise_DefaultsToUTF8(t *testing.T) {
	plan := synthesise(t, dictField(t, "city", "Zürich"))
	d := reparse(t, plan)
	if d.charsetName != "UTF-8" {
		t.Errorf("record 7/20 declares %q, want %q", d.charsetName, "UTF-8")
	}
	if !d.machineInteger.present || d.machineInteger.characterCode != 65001 {
		t.Errorf("record 7/3 character code = %d, want 65001", d.machineInteger.characterCode)
	}
	if got := planColumn(t, plan, "city").Width; got != len("Zürich") {
		t.Errorf("width = %d, want %d UTF-8 bytes", got, len("Zürich"))
	}
}

// TestSynthesise_RoundTripsInAnyCharset walks the whole synthesised path in
// several charsets at once: emit, write cases, read back, compare the text.
func TestSynthesise_RoundTripsInAnyCharset(t *testing.T) {
	for _, tc := range []struct {
		charset string
		values  []string
	}{
		{"UTF-8", []string{"Zürich", "Genève", "Ω"}},
		{"windows-1252", []string{"Zürich", "Genève", "Écolé"}},
		{"windows-1251", []string{"Москва", "Санкт"}},
		{"ISO-8859-7", []string{"Αθήνα", "Ελλάδα"}},
		{"Shift_JIS", []string{"東京", "日本語"}},
		{"EUC-KR", []string{"서울", "한국어"}},
		{"Big5", []string{"台北", "中文"}},
	} {
		t.Run(tc.charset, func(t *testing.T) {
			schema := &encoding.Schema{Fields: []encoding.Field{dictField(t, "place", tc.values...)}}
			plan := synthIn(t, tc.charset, schema.Fields...)

			cases := make([]Case, 0, len(tc.values))
			for i := range tc.values {
				cases = append(cases, Case{{Num: float64(i)}})
			}
			sav := encodeCases(t, plan, schema, cases...)

			d := reparse(t, &DictionaryPlan{Bytes: sav})
			if d.charsetName != tc.charset {
				t.Errorf("record 7/20 declares %q, want %q", d.charsetName, tc.charset)
			}
			_, rows := savRows(t, sav)
			if len(rows) != len(tc.values) {
				t.Fatalf("%d row(s) back, want %d", len(rows), len(tc.values))
			}
			for i, want := range tc.values {
				if rows[i][0] != want {
					t.Errorf("row %d = %q, want %q", i, rows[i][0], want)
				}
			}
		})
	}
}

// TestSynthesise_MintedShortNamesStayASCII guards a quiet interaction: the
// name minter folds every non-ASCII byte to '_', so a synthesised short name
// is 7-bit and its width cannot move under any charset. If that ever stops
// being true, the eight-byte refusal above becomes reachable from a plain
// synthesised export rather than only from a hand-set charset.
func TestSynthesise_MintedShortNamesStayASCII(t *testing.T) {
	// The name is legal SPSS (E5-S5 refuses a space), and every byte past
	// the first is still non-ASCII, which is what the minter has to fold.
	plan := synthIn(t, "windows-1252", dictField(t, "Zürich_Größe", "a"))
	col := plan.Columns[0]
	if i := firstNonASCII(col.ShortName); i >= 0 {
		t.Errorf("short name %q carries a non-ASCII byte at %d", col.ShortName, i)
	}
	if len(col.ShortName) > shortNameLen {
		t.Errorf("short name %q is %d bytes", col.ShortName, len(col.ShortName))
	}
	// The real name survives on record 7/13, which has no such limit.
	if col.Name != "Zürich_Größe" {
		t.Errorf("final name = %q, want the cohort's own", col.Name)
	}
}

// ---------------------------------------------------------------------------
// Encoded values reach the data encoder
// ---------------------------------------------------------------------------

// TestColumnPlan_CarriesEncodedValues pins the seam: the data encoder writes
// CategoryCode.Encoded and never Text, so the encoding is done once, at plan
// time, where a refusal costs nothing.
func TestColumnPlan_CarriesEncodedValues(t *testing.T) {
	plan := synthIn(t, "windows-1252", dictField(t, "city", "Zürich", "Genève"))
	col := planColumn(t, plan, "city")
	for _, want := range []struct {
		text string
		wire []byte
	}{
		{"Zürich", []byte("Z\xfcrich")},
		{"Genève", []byte("Gen\xe8ve")},
	} {
		found := false
		for _, c := range col.Categories {
			if c.Text != want.text {
				continue
			}
			found = true
			if !bytes.Equal(c.Encoded, want.wire) {
				t.Errorf("%q encoded to % x, want % x", want.text, c.Encoded, want.wire)
			}
		}
		if !found {
			t.Errorf("the plan records no category for %q", want.text)
		}
	}
}

// TestDataEncoder_PadsToTheEncodedWidth: a value shorter than its variable is
// space-padded out to the DECLARED byte width, and the padding is measured on
// the encoded bytes like everything else.
func TestDataEncoder_PadsToTheEncodedWidth(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{dictField(t, "city", "Zürich", "Bern")}}
	plan := emit(t, DictionaryRequest{
		Schema: schema, Cases: 2, Compression: compressionNone,
		Options: WriterOptions{Charset: "windows-1252", Uncompressed: true},
	})
	col := planColumn(t, plan, "city")
	if col.Width != 6 {
		t.Fatalf("width = %d, want 6 windows-1252 bytes", col.Width)
	}
	sav := encodeCases(t, plan, schema, Case{{Num: 0}}, Case{{Num: 1}})
	flat := flatCases(t, sav)
	if !bytes.Contains(flat, []byte("Z\xfcrich  ")) {
		t.Errorf("the 6-byte value is not padded out to its 8-byte element: % x", flat)
	}
	if !bytes.Contains(flat, []byte("Bern    ")) {
		t.Errorf("the 4-byte value is not space-padded: % x", flat)
	}
	_, rows := savRows(t, sav)
	if len(rows) != 2 || rows[0][0] != "Zürich" || rows[1][0] != "Bern" {
		t.Errorf("rows = %v, want the padding trimmed back off", rows)
	}
}

// TestBuildDictionary_ValueLabelKeysAreEncodedToo: a value-label KEY is a
// datum, so it goes on the wire in the file's charset like any other. A key
// left as UTF-8 stops matching the data it names, silently.
func TestBuildDictionary_ValueLabelKeysAreEncodedToo(t *testing.T) {
	schema, res := exportFixture(t, latin1Spec())
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 2, Compression: compressionNone})

	// GRADE is A3 with keys "é" and "ø", which ride a record type 3's
	// eight-byte value slot space-padded.
	if !bytes.Contains(plan.Bytes, append([]byte("\xe9"), bytes.Repeat([]byte{' '}, 7)...)) {
		t.Error("the value-label key \"é\" is not on the wire as the windows-1252 byte 0xe9")
	}

	// And it still resolves: the label comes back attached to the datum.
	fs, cohort, _ := importFixture(t, latin1Spec())
	sav := exportCohort(t, fs, cohort, WriterOptions{})
	d := reparse(t, &DictionaryPlan{Bytes: sav})
	var labels []string
	for _, set := range d.valueLabels {
		for _, l := range set.labels {
			labels = append(labels, l.label)
		}
	}
	if !contains(labels, "Élevé") {
		t.Errorf("value labels = %v, want the long-string label back", labels)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestBuildDictionary_WidthDetailsAreNumbers keeps the coded-error details
// machine-readable: a caller sizing a fix needs the two widths as numbers,
// not as prose it has to parse back out of the message.
func TestBuildDictionary_WidthDetailsAreNumbers(t *testing.T) {
	ce := emitFails(t, DictionaryRequest{
		Schema: &encoding.Schema{Fields: []encoding.Field{
			dictField(t, "notes", strings.Repeat("x", maxVeryLongStringWidth+9)),
		}},
		Cases: 0, Compression: compressionNone,
	}, perr.PULSE_SPSS_WIDTH_OVERFLOW)

	need, ok := ce.Details[perr.DetailSPSSWidth].(int)
	if !ok {
		t.Fatalf("details width = %T, want an int", ce.Details[perr.DetailSPSSWidth])
	}
	have, ok := ce.Details[perr.DetailSPSSDeclaredWidth].(int)
	if !ok {
		t.Fatalf("details declared_width = %T, want an int", ce.Details[perr.DetailSPSSDeclaredWidth])
	}
	if need <= have {
		t.Errorf("width %d is not past declared_width %d; the pair does not describe an overflow", need, have)
	}
	if !strings.Contains(ce.Message, strconv.Itoa(need)) {
		t.Errorf("the message does not carry the required width: %s", ce.Message)
	}
}
