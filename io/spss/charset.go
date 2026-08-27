package spss

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frankbardon/pulse/errors"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// ---------------------------------------------------------------------------
// What a `.sav` says about its own character set, and what that costs
// ---------------------------------------------------------------------------
//
// A `.sav` file states its character encoding in up to two places, and the
// format's own history is why both exist:
//
//   - Record 7/3 (machine integer info) carries a NUMBER — the legacy
//     `character_code` field. 1 is EBCDIC, 2 is 7-bit ASCII, 3 is 8-bit
//     ASCII, 4 is DEC Kanji, and anything else is a Windows code page
//     identifier (1252, 65001, …).
//   - Record 7/20 carries a NAME, which is the modern statement and the one
//     PSPP and recent SPSS versions fill in.
//
// Neither is mandatory, both are frequently stale, and the number cannot
// express what the name can: `3` ("8-bit ASCII") is what a writer emits for
// ISO-8859-1, for windows-1252 and for UTF-8 alike. That asymmetry decides
// the cross-check below.
//
// # Why decoding happens in a second pass
//
// Record 7/20 appears AFTER every record type 2, 3, 4 and 6 in the file, so
// no single forward walk can decode a variable label at the moment it reads
// it — the charset is not known yet. The dictionary parse therefore keeps
// every string as the raw bytes it found (a Go string is a byte container,
// not a Unicode container, so this costs nothing), and applyCharset makes one
// pass over the finished dictionary once the declaration is in hand. Data
// section cells decode later still, at the point of use, because that is
// where the variable name needed for a good error message is available.
//
// # Why an undecodable byte is an error
//
// golang.org/x/text decoders do NOT report undecodable input. A charmap
// decoder maps an undefined byte to U+FFFD and returns a nil error; the
// multi-byte CJK decoders do the same. Taking that default would turn a
// codepage mismatch into a cohort full of replacement characters that no
// later stage — not the schema mapping, not a filter, not an export — could
// tell from real data. This package opts out explicitly: every decode is
// checked, and an undecodable byte is PULSE_SPSS_CHARSET_INVALID naming the
// variable and the offending value.
//
// # Why the supported set is "ASCII supersets"
//
// SPSS pads a string datum to its declared width with 0x20, pads fixed
// header fields with 0x20, and delimits the record 7/5 and 7/13 payloads
// with '=', '\t', ' ' and '\n'. Every one of those is an ASCII byte carrying
// structural meaning, so a charset that does not encode ASCII as itself
// (EBCDIC, UTF-16) cannot express this file format at all. Rejecting them is
// therefore a statement about the format, not a limitation of this reader —
// and it is what lets the fast paths below treat a 7-bit datum as needing no
// decoding whatever the declared charset is.

// defaultCharsetName is the charset assumed for a file that declares none.
//
// The alternative — guessing windows-1252, which never fails because almost
// every byte is defined in it — is exactly the silent degradation this
// reader exists to prevent. UTF-8 is strictly validated instead, so a
// pure-ASCII file (every fixture, and the overwhelming majority of small
// real files) reads byte-for-byte as before, while an undeclared 8-bit file
// stops with PULSE_SPSS_CHARSET_INVALID naming the override rather than
// importing mojibake.
const defaultCharsetName = "UTF-8"

// charsetInfo is what the file declared about its own character encoding,
// retained verbatim.
//
// It is kept whole rather than reduced to a decoder because the write path
// (E5-S4) has to re-encode INTO the charset the source declared: emitting
// UTF-8 into a file whose header says windows-1252 corrupts every non-ASCII
// label. declaredName is retained separately from name for the same reason —
// a file spelling its charset "cp1252" must get "cp1252" back, not this
// package's canonical spelling of it.
type charsetInfo struct {
	// declaredName is the record 7/20 name exactly as the file spelled
	// it, or "" when the file carries no 7/20.
	declaredName string

	// declaredCode is the record 7/3 character_code, or 0 when the file
	// carries no 7/3 or leaves the field unset.
	declaredCode int32

	// overridden records that a caller's WithCharset replaced whatever
	// the file declared. The declaration is still retained above.
	overridden bool

	// name is the canonical name of the charset actually used.
	name string

	// dec decodes source bytes to UTF-8. Never nil once resolution has
	// succeeded.
	dec *charsetDecoder
}

// declared reports whether the file said anything at all about its charset.
// A file that did not is being read as defaultCharsetName by assumption, and
// the write path must know the difference.
func (c charsetInfo) declared() bool {
	return c.declaredName != "" || c.declaredCode != 0
}

// ---------------------------------------------------------------------------
// The name table
// ---------------------------------------------------------------------------

// charsetEntry is one supported charset: its canonical name, the x/text
// encoding behind it, and the spellings a `.sav` has been seen to use.
type charsetEntry struct {
	name    string
	enc     encoding.Encoding
	aliases []string

	// code is the record 7/3 character_code that names this charset: its
	// Windows code page identifier.
	//
	// It exists for the WRITE side. A file states its encoding in two
	// places and a writer has to fill in both, so the name->code
	// direction is needed as well as the code->name one charsetForCode
	// provides. It is a field rather than a lookup through the aliases
	// (every entry happens to carry its number as one) because a
	// declaration the emitted file makes about itself should not depend
	// on a list whose job is to be tolerant of spellings.
	//
	// charsetCodeRoundTrips pins that charsetForCode(code) resolves back
	// to this same entry, so the two directions cannot drift apart.
	code int32
}

// charsetTable is the explicit, closed list of charset spellings this reader
// recognises directly.
//
// It exists because SPSS charset names are NOT reliably IANA-canonical: real
// files carry "cp1252", "1252", "IBM-1252" and "windows-1252" for the same
// codepage, and only the last is registered. The lookup below therefore
// tries this table first and only then falls through to the IANA registry —
// deliberately in that order. The reverse (registry first) would be no
// looser, but it would make the behaviour of a given spelling depend on a
// table this repository does not own.
//
// A UTF-8 entry carries a nil encoding on purpose: it is decoded by
// validation rather than transformation (see charsetDecoder.decode).
var charsetTable = []charsetEntry{
	{name: "UTF-8", enc: nil, aliases: []string{"utf8", "utf-8", "65001", "cp65001", "unicode"}, code: 65001},
	{name: "US-ASCII", enc: nil, aliases: []string{"ascii", "us-ascii", "ansi_x3.4-1968", "iso646-us", "ibm367", "cp367", "20127", "cp20127"}, code: 20127},

	{name: "windows-1250", enc: charmap.Windows1250, aliases: []string{"cp1250", "1250", "ibm1250", "ms1250"}, code: 1250},
	{name: "windows-1251", enc: charmap.Windows1251, aliases: []string{"cp1251", "1251", "ibm1251", "ms1251"}, code: 1251},
	{name: "windows-1252", enc: charmap.Windows1252, aliases: []string{"cp1252", "1252", "ibm1252", "ms1252", "ansi"}, code: 1252},
	{name: "windows-1253", enc: charmap.Windows1253, aliases: []string{"cp1253", "1253", "ibm1253", "ms1253"}, code: 1253},
	{name: "windows-1254", enc: charmap.Windows1254, aliases: []string{"cp1254", "1254", "ibm1254", "ms1254"}, code: 1254},
	{name: "windows-1255", enc: charmap.Windows1255, aliases: []string{"cp1255", "1255", "ibm1255", "ms1255"}, code: 1255},
	{name: "windows-1256", enc: charmap.Windows1256, aliases: []string{"cp1256", "1256", "ibm1256", "ms1256"}, code: 1256},
	{name: "windows-1257", enc: charmap.Windows1257, aliases: []string{"cp1257", "1257", "ibm1257", "ms1257"}, code: 1257},
	{name: "windows-1258", enc: charmap.Windows1258, aliases: []string{"cp1258", "1258", "ibm1258", "ms1258"}, code: 1258},
	{name: "windows-874", enc: charmap.Windows874, aliases: []string{"cp874", "874", "ibm874", "ms874", "tis-620", "iso-8859-11"}, code: 874},

	{name: "ISO-8859-1", enc: charmap.ISO8859_1, aliases: []string{"iso8859-1", "iso_8859-1", "latin1", "l1", "cp819", "ibm819", "28591"}, code: 28591},
	{name: "ISO-8859-2", enc: charmap.ISO8859_2, aliases: []string{"iso8859-2", "iso_8859-2", "latin2", "l2", "28592"}, code: 28592},
	{name: "ISO-8859-3", enc: charmap.ISO8859_3, aliases: []string{"iso8859-3", "iso_8859-3", "latin3", "l3", "28593"}, code: 28593},
	{name: "ISO-8859-4", enc: charmap.ISO8859_4, aliases: []string{"iso8859-4", "iso_8859-4", "latin4", "l4", "28594"}, code: 28594},
	{name: "ISO-8859-5", enc: charmap.ISO8859_5, aliases: []string{"iso8859-5", "iso_8859-5", "cyrillic", "28595"}, code: 28595},
	{name: "ISO-8859-6", enc: charmap.ISO8859_6, aliases: []string{"iso8859-6", "iso_8859-6", "arabic", "28596"}, code: 28596},
	{name: "ISO-8859-7", enc: charmap.ISO8859_7, aliases: []string{"iso8859-7", "iso_8859-7", "greek", "28597"}, code: 28597},
	{name: "ISO-8859-8", enc: charmap.ISO8859_8, aliases: []string{"iso8859-8", "iso_8859-8", "hebrew", "28598"}, code: 28598},
	{name: "ISO-8859-9", enc: charmap.ISO8859_9, aliases: []string{"iso8859-9", "iso_8859-9", "latin5", "l5", "28599"}, code: 28599},
	{name: "ISO-8859-10", enc: charmap.ISO8859_10, aliases: []string{"iso8859-10", "iso_8859-10", "latin6", "l6", "28600"}, code: 28600},
	{name: "ISO-8859-13", enc: charmap.ISO8859_13, aliases: []string{"iso8859-13", "iso_8859-13", "latin7", "l7", "28603"}, code: 28603},
	{name: "ISO-8859-14", enc: charmap.ISO8859_14, aliases: []string{"iso8859-14", "iso_8859-14", "latin8", "l8", "28604"}, code: 28604},
	{name: "ISO-8859-15", enc: charmap.ISO8859_15, aliases: []string{"iso8859-15", "iso_8859-15", "latin9", "l9", "28605"}, code: 28605},
	{name: "ISO-8859-16", enc: charmap.ISO8859_16, aliases: []string{"iso8859-16", "iso_8859-16", "latin10", "l10", "28606"}, code: 28606},

	{name: "KOI8-R", enc: charmap.KOI8R, aliases: []string{"koi8r", "cp20866", "20866"}, code: 20866},
	{name: "KOI8-U", enc: charmap.KOI8U, aliases: []string{"koi8u", "cp21866", "21866"}, code: 21866},
	{name: "macintosh", enc: charmap.Macintosh, aliases: []string{"mac", "macroman", "x-mac-roman", "cp10000", "10000"}, code: 10000},

	{name: "IBM437", enc: charmap.CodePage437, aliases: []string{"cp437", "437", "oem-us", "pc-8"}, code: 437},
	{name: "IBM850", enc: charmap.CodePage850, aliases: []string{"cp850", "850"}, code: 850},
	{name: "IBM852", enc: charmap.CodePage852, aliases: []string{"cp852", "852"}, code: 852},
	{name: "IBM855", enc: charmap.CodePage855, aliases: []string{"cp855", "855"}, code: 855},
	{name: "IBM858", enc: charmap.CodePage858, aliases: []string{"cp858", "858"}, code: 858},
	{name: "IBM860", enc: charmap.CodePage860, aliases: []string{"cp860", "860"}, code: 860},
	{name: "IBM862", enc: charmap.CodePage862, aliases: []string{"cp862", "862"}, code: 862},
	{name: "IBM863", enc: charmap.CodePage863, aliases: []string{"cp863", "863"}, code: 863},
	{name: "IBM865", enc: charmap.CodePage865, aliases: []string{"cp865", "865"}, code: 865},
	{name: "IBM866", enc: charmap.CodePage866, aliases: []string{"cp866", "866"}, code: 866},

	// ISO-2022-JP is deliberately absent: it is a stateful escape-based
	// encoding, so a run of 7-bit bytes does not mean ASCII, and the
	// ASCII-superset requirement below rejects it. A `.sav` could not
	// carry it anyway — the format's own 0x20 padding would be read as
	// data in whatever mode the last escape sequence left.
	{name: "Shift_JIS", enc: japanese.ShiftJIS, aliases: []string{"sjis", "shift-jis", "shift_jis", "ms_kanji", "cp932", "932", "windows-31j"}, code: 932},
	{name: "EUC-JP", enc: japanese.EUCJP, aliases: []string{"eucjp", "euc_jp", "cp20932", "20932"}, code: 20932},
	{name: "EUC-KR", enc: korean.EUCKR, aliases: []string{"euckr", "euc_kr", "ks_c_5601-1987", "cp949", "949", "windows-949"}, code: 949},
	{name: "GBK", enc: simplifiedchinese.GBK, aliases: []string{"cp936", "936", "windows-936", "gb2312", "euc-cn"}, code: 936},
	{name: "GB18030", enc: simplifiedchinese.GB18030, aliases: []string{"gb-18030", "cp54936", "54936"}, code: 54936},
	{name: "Big5", enc: traditionalchinese.Big5, aliases: []string{"big-5", "big5-hkscs", "cp950", "950", "windows-950"}, code: 950},
}

// charsetIndex maps a normalised spelling to its table entry.
var charsetIndex = buildCharsetIndex()

func buildCharsetIndex() map[string]*charsetEntry {
	m := make(map[string]*charsetEntry, len(charsetTable)*6)
	for i := range charsetTable {
		e := &charsetTable[i]
		m[normaliseCharsetName(e.name)] = e
		for _, a := range e.aliases {
			m[normaliseCharsetName(a)] = e
		}
	}
	return m
}

// normaliseCharsetName folds a charset spelling to its comparison key:
// upper case with every non-alphanumeric byte removed.
//
// This is what makes "windows-1252", "Windows_1252", "WINDOWS 1252" and
// "cp1252" the same lookup, and it is the whole reason the table above can
// stay short. It is deliberately NOT a similarity match: "1252" and "1250"
// remain different keys, so no amount of sloppy spelling can land on the
// wrong codepage.
func normaliseCharsetName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - 32)
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		}
	}
	return b.String()
}

// lookupCharset resolves a charset name to a decoder.
//
// Two stages, in this order:
//
//  1. charsetTable, the explicit list of spellings a `.sav` actually
//     carries. Deterministic and owned by this repository.
//  2. the IANA registry via x/text, which covers registered names the table
//     does not enumerate.
//
// Stage 2 has a trap that is easy to miss and fatal to fidelity:
// ianaindex.Encoding returns (nil, nil) for a name it RECOGNISES but has no
// implementation for. A caller treating that nil as "no transformation
// needed" would silently read the file as UTF-8 — precisely the silent
// fallback this story exists to remove — so a nil encoding from the registry
// is rejected as unsupported.
func lookupCharset(name string) (*charsetDecoder, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, fmt.Errorf("the charset name is empty")
	}
	if e, ok := charsetIndex[normaliseCharsetName(trimmed)]; ok {
		return newCharsetDecoder(e.name, e.enc)
	}

	enc, err := ianaindex.IANA.Encoding(trimmed)
	if err != nil {
		return nil, fmt.Errorf("no charset is registered under that name")
	}
	if enc == nil {
		return nil, fmt.Errorf("the name is IANA-registered but no decoder for it is available")
	}
	canonical, err := ianaindex.IANA.Name(enc)
	if err != nil || canonical == "" {
		canonical = trimmed
	}
	return newCharsetDecoder(canonical, enc)
}

// charsetForCode resolves a record 7/3 character_code to a charset name.
//
// The named codes come from the format description; every other value is a
// Windows code page identifier, which charsetTable already indexes under its
// decimal spelling. Code 0 is not a code at all — it is the field left unset
// — and resolves to nothing so the caller falls through to its default.
func charsetForCode(code int32) (string, bool) {
	switch code {
	case 0:
		return "", false
	case 1:
		return "EBCDIC", true
	case 2, 3:
		return "US-ASCII", true
	case 4:
		return "DEC-Kanji", true
	}
	return strconv.FormatInt(int64(code), 10), true
}

// charsetCodeFor is charsetForCode in reverse: the record 7/3
// character_code that names the charset resolved under the given spelling,
// or 0 when this package has no code for it.
//
// 0 is a legal answer and not a failure. The field is optional — a file
// carrying no 7/3 at all leaves a reader to the 7/20 name, which is the more
// expressive statement anyway — so emitting 0 says "unstated" rather than
// stating something wrong. Guessing a nearby code would be worse than
// silence: a reader that trusts the number over the name would then decode
// with the guess.
func charsetCodeFor(name string) int32 {
	if e, ok := charsetIndex[normaliseCharsetName(name)]; ok {
		return e.code
	}
	return 0
}

// ---------------------------------------------------------------------------
// The decoder
// ---------------------------------------------------------------------------

// charsetDecoder decodes source bytes into UTF-8, strictly.
type charsetDecoder struct {
	// name is the canonical charset name, used in every diagnostic.
	name string

	// enc is the x/text encoding, or nil for the UTF-8 / US-ASCII
	// pass-through path where decoding is validation rather than
	// transformation.
	enc encoding.Encoding

	// ascii restricts the pass-through path to 7-bit input, which is what
	// makes US-ASCII distinct from UTF-8 rather than a synonym for it.
	ascii bool

	// byteText is the per-byte decoding of a single-byte charset: the
	// UTF-8 text each source byte stands for, or "" for a byte the
	// charset leaves undefined.
	//
	// It is precomputed because it turns the whole decode into a table
	// walk with no transformer, no allocation per call beyond the result
	// itself, and — the part that matters — an exact byte offset for the
	// first undecodable byte. A transformer can only report that the
	// input was bad somewhere.
	byteText []string
}

// newCharsetDecoder builds a decoder, rejecting any charset that cannot
// carry the `.sav` format's own ASCII framing.
func newCharsetDecoder(name string, enc encoding.Encoding) (*charsetDecoder, error) {
	c := &charsetDecoder{name: name, enc: enc}
	if enc == nil {
		c.ascii = name == "US-ASCII"
		return c, nil
	}
	if !isASCIISuperset(enc) {
		return nil, fmt.Errorf("it does not encode ASCII as itself, so it cannot carry the space padding and the '=' / tab / newline delimiters the .sav format itself is built from")
	}
	if cm, ok := enc.(*charmap.Charmap); ok {
		c.byteText = buildByteText(cm)
	}
	return c, nil
}

// isASCIISuperset probes the encoding rather than trusting a table: bytes
// 0x00..0x7F must decode to themselves.
//
// x/text keeps the equivalent flag unexported, and a probe is both cheap
// (one 128-byte decode per file) and honest about the property that is
// actually needed.
func isASCIISuperset(enc encoding.Encoding) bool {
	src := make([]byte, 0x80)
	for i := range src {
		src[i] = byte(i)
	}
	out, err := enc.NewDecoder().Bytes(src)
	if err != nil || len(out) != len(src) {
		return false
	}
	for i := range out {
		if out[i] != src[i] {
			return false
		}
	}
	return true
}

// buildByteText precomputes the UTF-8 text of every byte in a single-byte
// charset. An undefined byte — which x/text represents by decoding it to
// U+FFFD — gets the empty string, which is the decode path's error signal.
func buildByteText(cm *charmap.Charmap) []string {
	t := make([]string, 256)
	for i := 0; i < 256; i++ {
		r := cm.DecodeByte(byte(i))
		if r == utf8.RuneError {
			continue
		}
		t[i] = string(r)
	}
	return t
}

// decode renders source bytes as UTF-8.
//
// It returns the decoded text and -1 on success, or "" and the 0-based
// offset of the first undecodable byte on failure. An offset of -1 with an
// empty result cannot happen for a non-empty input, so a caller can treat
// "second return >= 0" as the failure test.
func (c *charsetDecoder) decode(b []byte) (string, int) {
	// Every supported charset encodes ASCII as itself — enforced at
	// construction — so 7-bit input needs no decoding at all. This is the
	// path almost every real cell takes, and skipping the transformer for
	// it is what keeps the decode off the per-case hot path.
	if i := firstHighByte(b); i < 0 {
		return string(b), -1
	} else if c.ascii {
		return "", i
	}

	switch {
	case c.enc == nil:
		// UTF-8: decoding is validation. It is deliberately NOT a
		// transformer pass — unicode.UTF8's decoder substitutes
		// U+FFFD for malformed input without an error, and U+FFFD is
		// a legal character in UTF-8, so no post-hoc scan could tell
		// a substituted one from a real one.
		if i := firstInvalidUTF8(b); i >= 0 {
			return "", i
		}
		return string(b), -1

	case c.byteText != nil:
		var sb strings.Builder
		sb.Grow(len(b) + len(b)/2)
		for i, x := range b {
			t := c.byteText[x]
			if t == "" {
				return "", i
			}
			sb.WriteString(t)
		}
		return sb.String(), -1

	default:
		// A multi-byte charset. Its decoder does not report bad input
		// either: it writes U+FFFD and returns nil. U+FFFD is not
		// representable in any of these charsets, so its presence in
		// the output is an unambiguous failure signal — which is not
		// true of the UTF-8 case above, hence the separate arm.
		out, err := c.enc.NewDecoder().Bytes(b)
		if err != nil {
			return "", 0
		}
		if strings.ContainsRune(string(out), utf8.RuneError) {
			return "", 0
		}
		return string(out), -1
	}
}

// decodeString is decode over a string that is holding raw source bytes.
func (c *charsetDecoder) decodeString(s string) (string, int) {
	return c.decode([]byte(s))
}

// firstHighByte returns the index of the first byte with the high bit set,
// or -1 when the input is entirely 7-bit.
func firstHighByte(b []byte) int {
	for i, x := range b {
		if x >= utf8.RuneSelf {
			return i
		}
	}
	return -1
}

// firstInvalidUTF8 returns the index of the first byte that does not begin a
// valid UTF-8 sequence, or -1 when the input is valid.
func firstInvalidUTF8(b []byte) int {
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return -1
}

// ---------------------------------------------------------------------------
// Resolution and the 7/3 ↔ 7/20 cross-check
// ---------------------------------------------------------------------------

// resolveCharset decides which charset the file's strings are in, records
// the declaration for the write path, and raises the cross-check warning.
//
// Precedence, highest first:
//
//  1. a caller's WithCharset override — the escape hatch for a file that
//     mislabels itself, which is the common reason a real `.sav` fails to
//     decode;
//  2. the record 7/20 NAME;
//  3. the record 7/3 character code;
//  4. defaultCharsetName.
//
// # The cross-check
//
// When 7/20 and 7/3 both resolve and disagree, the NAME wins and the
// disagreement is a warning (PULSE_SPSS_CHARSET_MISMATCH), not an error.
// Three reasons, in order of weight:
//
//   - The name is strictly more informative. Code 3 means "8-bit ASCII",
//     which is ISO-8859-1, windows-1252 and half a dozen national codepages
//     at once; no reading of it can be preferred to a name that says which.
//   - Writers leave the legacy numeric field at its default while filling in
//     7/20 correctly. Treating the disagreement as an error would reject
//     files that decode perfectly, which is a fidelity loss of its own.
//   - The disagreement is still real information about a file that may be
//     mislabelled, so it must not vanish — hence a warning rather than
//     silence.
//
// A 7/3 code of 2 or 3 is ASCII, and every charset this reader supports is
// an ASCII superset, so it is never a disagreement with any name. Without
// that carve-out the warning would fire on most real files and mean nothing.
func resolveCharset(d *dictionary, override string) error {
	info := charsetInfo{
		declaredName: d.charsetName,
		declaredCode: d.machineInteger.characterCode,
	}
	if !d.machineInteger.present {
		info.declaredCode = 0
	}

	switch {
	case override != "":
		info.overridden = true
		dec, err := lookupCharset(override)
		if err != nil {
			return charsetUnsupported(d, override,
				"the caller's charset override %q cannot be used: %v", override, err)
		}
		info.name, info.dec = dec.name, dec

	case info.declaredName != "":
		dec, err := lookupCharset(info.declaredName)
		if err != nil {
			return charsetUnsupported(d, info.declaredName,
				"record 7/20 declares the character encoding %q: %v", info.declaredName, err)
		}
		info.name, info.dec = dec.name, dec
		crossCheckCharset(d, dec, info.declaredCode)

	case info.declaredCode != 0:
		named, _ := charsetForCode(info.declaredCode)
		dec, err := lookupCharset(named)
		if err != nil {
			return charsetUnsupported(d, named,
				"record 7/3 declares character code %d (%s): %v", info.declaredCode, named, err)
		}
		info.name, info.dec = dec.name, dec

	default:
		dec, err := lookupCharset(defaultCharsetName)
		if err != nil { // unreachable: the default is in the table
			return charsetUnsupported(d, defaultCharsetName, "%v", err)
		}
		info.name, info.dec = dec.name, dec
	}

	d.charset = info
	return nil
}

// crossCheckCharset raises PULSE_SPSS_CHARSET_MISMATCH when the record 7/3
// character code names a different charset than the record 7/20 name that
// won. Codes that do not resolve, and the ASCII codes, are not
// disagreements — see resolveCharset.
func crossCheckCharset(d *dictionary, chosen *charsetDecoder, code int32) {
	if code == 0 {
		return
	}
	named, ok := charsetForCode(code)
	if !ok {
		return
	}
	if e, ok := charsetIndex[normaliseCharsetName(named)]; ok {
		if e.name == "US-ASCII" || e.name == chosen.name {
			return
		}
		named = e.name
	} else if _, err := lookupCharset(named); err != nil {
		// A code with no charset behind it says nothing about the
		// name, so it is not evidence of a disagreement.
		return
	}

	ce := errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_MISMATCH,
		"spss: the file states its character encoding twice and the two disagree: record 7/20 names "+
			strconv.Quote(d.charsetName)+" ("+chosen.name+") while record 7/3 carries character code "+
			strconv.FormatInt(int64(code), 10)+" ("+named+"); the 7/20 name is used, because it is the "+
			"more expressive statement and the numeric field is routinely left stale",
		map[string]any{
			errors.DetailSPSSRecord:  recordName(recTypeExtension),
			errors.DetailSPSSCharset: chosen.name,
		})
	d.warnings = append(d.warnings, ce)
}

// charsetUnsupported builds the hard error for a charset that cannot be
// decoded with.
func charsetUnsupported(d *dictionary, name string, format string, args ...any) error {
	msg := "spss: " + fmt.Sprintf(format, args...) +
		"; the file cannot be decoded faithfully, and guessing a codepage would produce text that is plausible and wrong"
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_UNSUPPORTED, msg,
		map[string]any{
			errors.DetailSPSSRecord:  recordName(recTypeExtension),
			errors.DetailSPSSOffset:  d.dataOffset,
			errors.DetailSPSSCharset: name,
		})
}

// charsetInvalid builds the hard error for a byte sequence the declared
// charset cannot decode. what names the thing being decoded; variable names
// the SPSS variable when the fault is inside one, and is "" otherwise.
func charsetInvalid(cs *charsetDecoder, what, variable string, raw []byte, at int) *errors.CodedError {
	var b strings.Builder
	b.WriteString("spss: ")
	b.WriteString(what)
	b.WriteString(": byte 0x")
	if at >= 0 && at < len(raw) {
		const hex = "0123456789abcdef"
		b.WriteByte(hex[raw[at]>>4])
		b.WriteByte(hex[raw[at]&0x0F])
		b.WriteString(" at position ")
		b.WriteString(strconv.Itoa(at))
	} else {
		b.WriteString("??")
	}
	b.WriteString(" of the value ")
	b.WriteString(quoteRawBytes(raw))
	b.WriteString(" is not decodable in the declared character encoding ")
	b.WriteString(cs.name)
	b.WriteString("; it is reported rather than replaced with U+FFFD, because a replacement character is indistinguishable from data")

	details := map[string]any{
		errors.DetailSPSSCharset: cs.name,
	}
	if variable != "" {
		details[errors.DetailSPSSVariable] = variable
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_INVALID, b.String(), details)
}

// quoteRawBytes renders undecodable bytes as a hex escape sequence. The
// value cannot be printed as text — that it is not text is the whole
// complaint — so the message shows the bytes themselves.
func quoteRawBytes(raw []byte) string {
	var b strings.Builder
	b.Grow(len(raw)*4 + 2)
	b.WriteByte('"')
	for _, x := range raw {
		if x >= 0x20 && x < 0x7F && x != '"' && x != '\\' {
			b.WriteByte(x)
			continue
		}
		b.WriteString("\\x")
		const hex = "0123456789abcdef"
		b.WriteByte(hex[x>>4])
		b.WriteByte(hex[x&0x0F])
	}
	b.WriteByte('"')
	return b.String()
}

// ---------------------------------------------------------------------------
// The dictionary transcode pass
// ---------------------------------------------------------------------------

// applyCharset decodes every string the dictionary holds from the file's
// declared charset into UTF-8, in place.
//
// It runs after the whole record walk because record 7/20 appears after
// every record that carries text. Value-label KEYS are decoded here too, and
// the trim-then-decode order matters: the key is cut to the variable's
// DECLARED BYTE WIDTH and stripped of its 0x20 padding first, exactly as
// data.trimStringDatum does to a datum, so a label key still compares equal
// to the datum naming it after both have been decoded.
//
// Data-section cells are NOT decoded here. They are decoded at the point of
// use, where the variable name is in hand for the error message and where
// the per-case fast path can skip 7-bit values without touching a decoder.
func applyCharset(d *dictionary) error {
	cs := d.charset.dec
	if cs == nil {
		return nil
	}

	tr := func(what, variable string, s *string) error {
		if *s == "" {
			return nil
		}
		out, at := cs.decodeString(*s)
		if at >= 0 {
			return charsetInvalid(cs, what, variable, []byte(*s), at)
		}
		*s = out
		return nil
	}

	if err := tr("the file header product name", "", &d.header.productName); err != nil {
		return err
	}
	if err := tr("the file header file label", "", &d.header.fileLabel); err != nil {
		return err
	}

	for i := range d.vars {
		v := &d.vars[i]
		// The short name is decoded before it is used as the variable
		// identity in any later message, so an undecodable one names
		// the record rather than a mangled variable.
		if err := tr("the record type 2 variable name", "", &v.name); err != nil {
			return err
		}
		if err := tr("the record 7/13 long variable name", v.name, &v.longName); err != nil {
			return err
		}
		if err := tr("the variable label", v.fieldName(), &v.label); err != nil {
			return err
		}
		for j := range v.missing.text {
			if err := tr("a missing-value specification", v.fieldName(), &v.missing.text[j]); err != nil {
				return err
			}
		}
	}

	// Value-label LABELS are decoded here; value-label KEYS are not. A
	// key is a datum — the same bytes a case of the same variable would
	// carry — so it is decoded where the data is, at the single point in
	// the schema mapping that reads it, which is also the only place the
	// owning variable is unambiguously in hand for the error message.
	for i := range d.valueLabels {
		set := &d.valueLabels[i]
		owner := d.valueLabelSetOwner(*set)
		for j := range set.labels {
			if err := tr("a value label", owner, &set.labels[j].label); err != nil {
				return err
			}
		}
	}

	for i := range d.documents {
		if err := tr("a record type 6 document line", "", &d.documents[i]); err != nil {
			return err
		}
	}

	for _, set := range d.mrSets {
		switch s := set.(type) {
		case *mrDichotomySet:
			if err := tr("a multiple-response set name", "", &s.name); err != nil {
				return err
			}
			if err := tr("a multiple-response set label", s.name, &s.label); err != nil {
				return err
			}
			if err := tr("a multiple-response set counted value", s.name, &s.countedValue); err != nil {
				return err
			}
			if err := decodeNames(cs, "a multiple-response set member name", s.name, s.vars); err != nil {
				return err
			}
		case *mrCategorySet:
			if err := tr("a multiple-response set name", "", &s.name); err != nil {
				return err
			}
			if err := tr("a multiple-response set label", s.name, &s.label); err != nil {
				return err
			}
			if err := decodeNames(cs, "a multiple-response set member name", s.name, s.vars); err != nil {
				return err
			}
		}
	}

	for i := range d.variableSets {
		vs := &d.variableSets[i]
		if err := tr("a variable set name", "", &vs.name); err != nil {
			return err
		}
		if err := decodeNames(cs, "a variable set member name", vs.name, vs.vars); err != nil {
			return err
		}
	}

	return nil
}

// decodeNames decodes a slice of member names in place.
func decodeNames(cs *charsetDecoder, what, owner string, names []string) error {
	for i := range names {
		if names[i] == "" {
			continue
		}
		out, at := cs.decodeString(names[i])
		if at >= 0 {
			return charsetInvalid(cs, what, owner, []byte(names[i]), at)
		}
		names[i] = out
	}
	return nil
}

// valueLabelSetOwner names a value-label set by its first bound variable,
// for a diagnostic. A set bound to nothing names itself as such rather than
// pretending to a variable.
func (d *dictionary) valueLabelSetOwner(set valueLabelSet) string {
	for _, idx := range set.varIndices {
		if v, first, ok := d.variableByIndex(idx); ok && first {
			return v.fieldName()
		}
	}
	return ""
}
