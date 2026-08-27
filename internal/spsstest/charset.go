package spsstest

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
)

// Emitting a fixture in a non-UTF-8 charset.
//
// A Spec is authored in Go source, so every string in it is UTF-8. A `.sav`
// file, on the other hand, holds text in whatever charset its record 7/20
// declares — that is the whole point of the record. So when a Spec sets
// CharacterEncoding, every piece of text it carries is ENCODED into that
// charset before a single byte is written, and the resulting fixture holds
// codepage bytes exactly as a pre-Unicode SPSS release would have written
// them.
//
// # Why this relaxes an earlier rule rather than adding to it
//
// Until this file existed, every string in a Spec had to be printable 7-bit
// ASCII, and the reason was stated at each check site: without a declared
// encoding, a high byte on the wire is ambiguous — nothing in the file says
// what it means, so a fixture carrying one would be asserting a decoding the
// file does not justify. That reasoning holds only for a file with no
// record 7/20. Once the Spec declares one, the bytes are no longer ambiguous
// and the restriction is exactly backwards: a fixture that cannot carry
// non-ASCII text cannot test charset decoding at all.
//
// So the ASCII gate moved rather than disappearing. It now lives in
// transcodeSpec, which runs before any other validation, and it applies only
// when the Spec declares nothing. With a declaration in force the rule
// becomes "every rune must be printable AND representable in the declared
// charset", which is the same guarantee expressed in the right alphabet.
//
// # Independent of the reader on purpose
//
// This resolves charset names with its own small table rather than calling
// into io/spss. A fixture generator that shares its charset lookup with the
// reader under test cannot catch a bug in that lookup: both sides would
// agree on the wrong codepage and the round trip would pass. The cost is
// that the two tables can drift, and that is the intended trade — drift
// shows up as a fixture the reader rejects, which is a visible failure.

// wireCodec encodes fixture text into a file's declared charset.
type wireCodec struct {
	// name is the charset name as the Spec spelled it, for diagnostics.
	name string

	// enc is the x/text encoding, or nil when the wire form is the UTF-8
	// the Spec already holds.
	enc encoding.Encoding

	// ascii restricts text to printable 7-bit ASCII. Set for an
	// undeclared charset (where a high byte would be ambiguous) and for
	// an explicit US-ASCII declaration.
	ascii bool
}

// specWireCodec resolves a Spec's CharacterEncoding to a codec.
//
// The table is deliberately small — the charsets a fixture has any reason to
// be written in — and an unrecognised name is an error naming it, because a
// silently ignored CharacterEncoding would emit a UTF-8 fixture under a
// windows-1252 declaration and quietly test nothing.
func specWireCodec(name string) (wireCodec, error) {
	if name == "" {
		return wireCodec{name: "(undeclared)", ascii: true}, nil
	}
	// The charset name is the one string in the Spec that is never
	// transcoded: a charset name that needs a charset to read is a
	// contradiction. It is checked here rather than at emission because
	// nothing downstream can proceed without a usable name.
	if !isASCIIPrintable(name) {
		return wireCodec{}, fmt.Errorf("spsstest: CharacterEncoding %q is not printable 7-bit ASCII; a charset name that needs a charset to read is a contradiction", name)
	}
	switch normaliseWireCharset(name) {
	case "UTF8", "UTF8MB4", "65001":
		return wireCodec{name: name}, nil
	case "USASCII", "ASCII", "20127":
		return wireCodec{name: name, ascii: true}, nil
	}
	if enc, ok := wireCharsets[normaliseWireCharset(name)]; ok {
		return wireCodec{name: name, enc: enc}, nil
	}
	enc, err := ianaindex.IANA.Encoding(name)
	if err != nil || enc == nil {
		return wireCodec{}, fmt.Errorf("spsstest: CharacterEncoding %q is not a charset this fixture generator can encode into; add it to wireCharsets if a fixture needs it", name)
	}
	return wireCodec{name: name, enc: enc}, nil
}

// wireCharsets is the set of codepages a fixture may be emitted in, keyed by
// normalised name.
var wireCharsets = map[string]encoding.Encoding{
	"WINDOWS1250": charmap.Windows1250,
	"CP1250":      charmap.Windows1250,
	"WINDOWS1251": charmap.Windows1251,
	"CP1251":      charmap.Windows1251,
	"WINDOWS1252": charmap.Windows1252,
	"CP1252":      charmap.Windows1252,
	"1252":        charmap.Windows1252,
	"WINDOWS1253": charmap.Windows1253,
	"CP1253":      charmap.Windows1253,
	"ISO88591":    charmap.ISO8859_1,
	"LATIN1":      charmap.ISO8859_1,
	"ISO88592":    charmap.ISO8859_2,
	"ISO88595":    charmap.ISO8859_5,
	"ISO88597":    charmap.ISO8859_7,
	"ISO885915":   charmap.ISO8859_15,
	"KOI8R":       charmap.KOI8R,
	"MACINTOSH":   charmap.Macintosh,
	"IBM850":      charmap.CodePage850,
	"CP850":       charmap.CodePage850,
}

func normaliseWireCharset(s string) string {
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

// text encodes one piece of fixture text into the wire charset.
//
// what names the Spec field, so a rejection points at the thing the author
// wrote rather than at a string with no provenance.
func (c wireCodec) text(what, s string) (string, error) {
	if s == "" {
		return s, nil
	}
	if c.ascii {
		if !isASCIIPrintable(s) {
			return "", fmt.Errorf("spsstest: %s is not printable 7-bit ASCII; a non-ASCII value needs a declared encoding (record 7/20) — set Spec.CharacterEncoding", what)
		}
		return s, nil
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("spsstest: %s is not valid UTF-8; a Spec is authored in Go source and is expected to hold text, not raw codepage bytes — set Spec.CharacterEncoding and write the text as UTF-8", what)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", fmt.Errorf("spsstest: %s contains the non-printable character %q; the .sav fixed-width fields and delimiter-separated payloads cannot carry one unambiguously", what, r)
		}
	}
	if c.enc == nil {
		return s, nil
	}
	out, err := c.enc.NewEncoder().String(s)
	if err != nil {
		return "", fmt.Errorf("spsstest: %s (%q) is not representable in the declared encoding %s; a fixture cannot declare a charset and then carry text the charset has no bytes for", what, s, c.name)
	}
	return out, nil
}

// transcodeSpec validates every string a Spec carries and returns the Spec
// with each one replaced by its WIRE form.
//
// Everything downstream of this — the width checks, the delimiter checks,
// the fixed-field checks — then operates on wire bytes, which is what makes
// them correct: an SPSS string width is a BYTE count, so "café" in a
// width-4 windows-1252 variable fits and the same text in UTF-8 does not.
// Checking the width before the encode would get that backwards.
//
// Variable SHORT names are excluded. They have their own grammar
// (validShortName: A-Z, digits and ._@#$), which is ASCII by construction,
// and SPSS keeps them ASCII regardless of the file's charset — the
// case-preserving, possibly non-ASCII name lives in LongName, which IS
// transcoded. The charset name itself is excluded for the obvious reason:
// a charset name that needs a charset to read is a contradiction.
func transcodeSpec(spec Spec, c wireCodec) (Spec, error) {
	var err error
	set := func(what string, dst *string) bool {
		var out string
		out, err = c.text(what, *dst)
		if err != nil {
			return false
		}
		*dst = out
		return true
	}

	if !set("the file label", &spec.FileLabel) ||
		!set("the product name", &spec.ProductName) ||
		!set("the creation date", &spec.CreationDate) ||
		!set("the creation time", &spec.CreationTime) {
		return spec, err
	}

	spec.Vars = append([]Var(nil), spec.Vars...)
	for i := range spec.Vars {
		v := &spec.Vars[i]
		if !set(fmt.Sprintf("Vars[%d] (%s) label", i, v.Name), &v.Label) ||
			!set(fmt.Sprintf("Vars[%d] (%s) LongName", i, v.Name), &v.LongName) {
			return spec, err
		}
	}

	spec.Documents = append([]string(nil), spec.Documents...)
	for i := range spec.Documents {
		if !set(fmt.Sprintf("Documents[%d]", i), &spec.Documents[i]) {
			return spec, err
		}
	}

	spec.ValueLabels = append([]ValueLabelSet(nil), spec.ValueLabels...)
	for si := range spec.ValueLabels {
		set := &spec.ValueLabels[si]
		set.Labels = append([]ValueLabel(nil), set.Labels...)
		for li := range set.Labels {
			l := &set.Labels[li]
			out, terr := c.text(fmt.Sprintf("ValueLabels[%d].Labels[%d]", si, li), l.Label)
			if terr != nil {
				return spec, terr
			}
			l.Label = out
			if l.Value.kind == kindText {
				out, terr = c.text(fmt.Sprintf("ValueLabels[%d].Labels[%d] value %s", si, li, l.Value), l.Value.str)
				if terr != nil {
					return spec, terr
				}
				l.Value.str = out
			}
		}
	}

	spec.Cases = append([][]Value(nil), spec.Cases...)
	for ci := range spec.Cases {
		spec.Cases[ci] = append([]Value(nil), spec.Cases[ci]...)
		for vi := range spec.Cases[ci] {
			val := &spec.Cases[ci][vi]
			if val.kind != kindText {
				continue
			}
			out, terr := c.text(fmt.Sprintf("Cases[%d][%d] %s", ci, vi, *val), val.str)
			if terr != nil {
				return spec, terr
			}
			val.str = out
		}
	}

	spec.LongStringValueLabels = append([]LongStringValueLabels(nil), spec.LongStringValueLabels...)
	for si := range spec.LongStringValueLabels {
		lsvl := &spec.LongStringValueLabels[si]
		lsvl.Labels = append([]LongStringValueLabel(nil), lsvl.Labels...)
		for li := range lsvl.Labels {
			l := &lsvl.Labels[li]
			if !set(fmt.Sprintf("LongStringValueLabels[%d].Labels[%d] value", si, li), &l.Value) ||
				!set(fmt.Sprintf("LongStringValueLabels[%d].Labels[%d] label", si, li), &l.Label) {
				return spec, err
			}
		}
	}

	spec.LongStringMissingValues = append([]LongStringMissingValues(nil), spec.LongStringMissingValues...)
	for mi := range spec.LongStringMissingValues {
		entry := &spec.LongStringMissingValues[mi]
		entry.Values = append([]string(nil), entry.Values...)
		for vi := range entry.Values {
			if !set(fmt.Sprintf("LongStringMissingValues[%d].Values[%d]", mi, vi), &entry.Values[vi]) {
				return spec, err
			}
		}
	}

	spec.MultipleResponseSets = append([]MRSet(nil), spec.MultipleResponseSets...)
	for i := range spec.MultipleResponseSets {
		s := &spec.MultipleResponseSets[i]
		if !set(fmt.Sprintf("MultipleResponseSets[%d] (%s) label", i, s.Name), &s.Label) ||
			!set(fmt.Sprintf("MultipleResponseSets[%d] (%s) CountedValue", i, s.Name), &s.CountedValue) {
			return spec, err
		}
	}

	return spec, nil
}
