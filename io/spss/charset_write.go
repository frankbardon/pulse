package spss

// The WRITE side of the character-encoding story: putting the text back on
// the wire in the charset the file declares, and recomputing every byte
// width the transcode moved.
//
// charset.go decodes source bytes into UTF-8 and this file is its exact
// mirror. The structural parallel is deliberate: applyCharset makes ONE pass
// over a finished dictionary once the charset is known, and applyCharsetWrite
// makes one pass over the finished [outFile] before a byte of it is emitted.
// Both hold text as raw bytes inside Go strings between the pass and the
// wire, because a Go string is a byte container and that is what lets the
// emitter's len() calls be BYTE counts in the target charset without knowing
// anything about charsets at all.
//
// # The two hard rules, and why they are not the library's defaults
//
//	Never substitute silently. A character with no representation in the
//	target charset is PULSE_SPSS_CHARSET_UNENCODABLE naming the variable
//	and the value — never '?', never 0x1A, never U+FFFD.
//
//	Widths are byte counts, not rune counts. Transcoding changes byte
//	length, so every declared width is recomputed from the ENCODED bytes,
//	and a width the format fixes is PULSE_SPSS_WIDTH_OVERFLOW rather than
//	a truncation.
//
// E3-S3 established empirically that x/text DECODERS substitute U+FFFD and
// return a nil error, so the read side had to opt out explicitly. Measured
// again here in the encode direction, the answer is the opposite and better:
// the charmap and CJK encoders return "encoding: rune not supported by
// encoding" and write nothing. The substitution is opt-IN — it is what
// encoding.ReplaceUnsupported exists to add, and it is what
// charmap.EncodeRune's `ok` return is there to let a caller skip.
//
// That is a fact about a dependency, not a property of the format, so this
// file does not lean on it. Every encode is verified two ways:
//
//  1. a single-byte charset is encoded through a reverse table INVERTED FROM
//     THE DECODER'S OWN byteText, so the two halves cannot disagree about
//     what a byte means and an unmapped rune is a table miss with an exact
//     offset — which is more than the transformer can report;
//  2. every result is decoded back with charsetDecoder and compared to the
//     input. A rune that encodes but comes back as a different character is
//     refused as if it had not encoded at all. This is not hypothetical:
//     GB18030 maps the whole Private Use Area onto four-byte sequences that
//     decode back to a DIFFERENT code point, and those 2068 runes are the
//     only ones out of 1112064 that behave that way.
//
// TestCharsetEncoder_NoSubstitutionAnywhere sweeps every supported charset
// in the encode direction, matching the bar E3-S3 set on the decode side.
//
// # Ordering: encode, then measure, then segment
//
// Getting this backwards is a corruption bug rather than a fidelity nicety.
// A very long string is sliced across physical variables on a fixed 252-byte
// stride, so a multi-byte character CAN straddle a segment boundary and the
// reader joins the pieces before it decodes them (see dataPlan.stringBytes,
// which says so in as many words). The writer therefore encodes the WHOLE
// logical value first, measures the encoded bytes, sizes the segments from
// that measurement, and only then slices. Segmenting UTF-8 and transcoding
// each piece would encode a partial character and produce a value no reader
// can read back.
//
// # Widening, never narrowing
//
// A declared width recomputed downwards is NOT applied to a width the source
// recorded. The source said A20 and A20 is what the round trip owes back,
// even if every value in it now fits in six bytes; SPSS pads with spaces and
// the read path trims them, so the extra width costs nothing and preserving
// it costs nothing either. A SYNTHESISED width has no source to be faithful
// to and is computed exactly. outVar.widthDerived is which of the two a
// variable is.
//
// # Records 7/10, 7/17 and 7/18 pass through verbatim
//
// The routed question from E3-S3. Those payloads are captured as raw bytes
// and never decoded — the reader does not interpret their grammar, so it has
// nothing to decode them into — which means they are STILL in the source
// charset when this pass runs. Transcoding them would require decoding them
// first, and re-emitting them verbatim into a file declaring the same
// charset is exactly right. Verbatim passthrough is therefore the answer,
// and it is only wrong in one case: a caller who overrode the target charset
// with [WriterOptions.Charset]. Then the bytes and the declaration disagree,
// which is PULSE_SPSS_CHARSET_MISMATCH — a warning, because the bytes are
// the authoritative record of what the source said and dropping them would
// lose more than mislabelling them does. A pure-ASCII payload is not a
// disagreement: every supported charset encodes ASCII as itself.

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frankbardon/pulse/errors"
	"golang.org/x/text/encoding"
)

// ---------------------------------------------------------------------------
// The encoder
// ---------------------------------------------------------------------------

// charsetEncoder encodes UTF-8 text into the emitted file's charset,
// strictly, and carries the declaration the file must make about itself.
type charsetEncoder struct {
	// name is the canonical charset name, used in every diagnostic.
	name string

	// declaredName is what record 7/20 states. It is the SOURCE's own
	// spelling where there is one — a file that said "cp1252" gets
	// "cp1252" back, not this package's "windows-1252" — because the
	// declaration is a quotation of what the file said, not a
	// normalisation of it.
	declaredName string

	// declaredCode is what record 7/3 character_code states, 0 when the
	// field is left unstated.
	declaredCode int32

	// enc is the x/text encoding, or nil for the UTF-8 / US-ASCII
	// pass-through path where encoding is validation rather than
	// transformation.
	enc encoding.Encoding

	// ascii restricts the pass-through path to 7-bit output.
	ascii bool

	// runeByte is the reverse of the decoder's byteText: the single byte
	// each rune is written as in a single-byte charset.
	//
	// It is INVERTED FROM THE DECODER rather than taken from
	// charmap.EncodeRune, for two reasons. Round-tripping is then true by
	// construction: whatever byte this table hands out is a byte the
	// decoder maps back to the rune it came from, even where two bytes of
	// the charset decode to one rune. And a miss is a plain map miss at a
	// known rune, so the diagnostic can name the exact offset —
	// charmap.EncodeRune answers (0x1A, false), and 0x1A is a byte a
	// caller who forgot to check `ok` would cheerfully write.
	runeByte map[rune]byte

	// dec is the matching decoder, used to verify every encode by reading
	// it back. Never nil.
	dec *charsetDecoder
}

// newCharsetEncoder builds an encoder for a charset name.
//
// The name is resolved through the same lookupCharset the read side uses, so
// the set of charsets that can be written is exactly the set that can be
// read — a writer able to emit a file its own reader refuses would be a
// round trip broken at the source.
func newCharsetEncoder(name string) (*charsetEncoder, error) {
	dec, err := lookupCharset(name)
	if err != nil {
		return nil, err
	}
	e := &charsetEncoder{
		name:  dec.name,
		enc:   dec.enc,
		ascii: dec.ascii,
		dec:   dec,
	}
	if dec.byteText != nil {
		e.runeByte = buildRuneByte(dec.byteText)
	}
	if !e.asciiEncodesAsItself() {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_UNSUPPORTED,
			"spss: the charset "+strconv.Quote(dec.name)+" does not encode ASCII as itself, so it cannot carry the space "+
				"padding and the '=' / tab / newline delimiters the .sav format is built from",
			map[string]any{errors.DetailSPSSCharset: dec.name})
	}
	return e, nil
}

// buildRuneByte inverts a decoder's per-byte table.
//
// The LOWEST byte wins when two bytes of the charset decode to the same
// rune. Which one is picked does not matter for correctness — both decode
// back to the rune — but picking deterministically matters a great deal for
// a byte-comparable round trip, and the low byte is the one a codepage's
// primary assignment lives at.
func buildRuneByte(byteText []string) map[rune]byte {
	m := make(map[rune]byte, len(byteText))
	for i := len(byteText) - 1; i >= 0; i-- {
		t := byteText[i]
		if t == "" {
			continue
		}
		r, size := utf8.DecodeRuneInString(t)
		if size != len(t) || r == utf8.RuneError {
			continue
		}
		m[r] = byte(i)
	}
	return m
}

// asciiEncodesAsItself probes the encoder over the whole 7-bit range.
//
// newCharsetDecoder already proves the DECODE direction, and this proves the
// other one. It is what licenses the 7-bit fast path in encode, and it is a
// probe rather than a trusted flag for the same reason isASCIISuperset is:
// the property that is actually needed is cheap to measure and expensive to
// assume.
func (c *charsetEncoder) asciiEncodesAsItself() bool {
	for r := rune(0); r < utf8.RuneSelf; r++ {
		b, at := c.encodeRaw(string(r))
		if at >= 0 || len(b) != 1 || b[0] != byte(r) {
			return false
		}
	}
	return true
}

// encode renders UTF-8 text as the target charset's bytes.
//
// It returns the encoded bytes and -1 on success, or nil and the 0-based
// BYTE offset within s of the first rune that cannot be written, so a caller
// can treat "second return >= 0" as the failure test — the same contract
// charsetDecoder.decode offers in the other direction.
func (c *charsetEncoder) encode(s string) ([]byte, int) {
	out, at := c.encodeRaw(s)
	if at >= 0 {
		return nil, at
	}
	// Verify by reading it back. A rune that encodes to bytes which decode
	// to something else has not been written down; it has been quietly
	// changed, which is the same fault as a substitution wearing a
	// different hat.
	if back, bad := c.dec.decode(out); bad >= 0 || back != s {
		return nil, c.firstUnfaithfulRune(s)
	}
	return out, -1
}

// encodeString is encode with a string result, for the many call sites that
// are moving text from one string slot to another.
func (c *charsetEncoder) encodeString(s string) (string, int) {
	b, at := c.encode(s)
	if at >= 0 {
		return "", at
	}
	return string(b), -1
}

// encodeRaw is the transformation half of encode, without the read-back
// verification. Nothing outside this file calls it.
func (c *charsetEncoder) encodeRaw(s string) ([]byte, int) {
	// Every supported charset encodes ASCII as itself — proved at
	// construction — so 7-bit text needs no encoding at all. This is the
	// path almost every real string takes.
	if i := firstNonASCII(s); i < 0 {
		return []byte(s), -1
	} else if c.ascii {
		return nil, i
	}

	switch {
	case c.enc == nil:
		// UTF-8: encoding is validation. The text came out of this
		// package's own strict decoder so it is valid by construction,
		// but a cohort string can also have been written by any other
		// Pulse path, and invalid UTF-8 must not reach the wire under a
		// declaration that says it is UTF-8.
		if i := firstInvalidUTF8([]byte(s)); i >= 0 {
			return nil, i
		}
		return []byte(s), -1

	case c.runeByte != nil:
		out := make([]byte, 0, len(s))
		for i, r := range s {
			b, ok := c.runeByte[r]
			if !ok {
				return nil, i
			}
			out = append(out, b)
		}
		return out, -1

	default:
		// A multi-byte charset. The transformer reports an unsupported
		// rune as an error but not WHERE it was, so a failure falls back
		// to the per-rune walk that can say.
		out, err := c.enc.NewEncoder().Bytes([]byte(s))
		if err != nil {
			return nil, c.firstUnencodableRune(s)
		}
		return out, -1
	}
}

// firstUnencodableRune returns the byte offset of the first rune the
// encoding refuses on its own.
//
// Per-rune encoding is sound for every charset in the table: the stateful
// escape-based encodings, where a rune's bytes depend on what came before,
// are excluded at construction — ISO-2022-JP is absent from charsetTable and
// could not carry the format's 0x20 padding anyway.
func (c *charsetEncoder) firstUnencodableRune(s string) int {
	enc := c.enc.NewEncoder()
	for i, r := range s {
		if _, err := enc.Bytes([]byte(string(r))); err != nil {
			return i
		}
	}
	// Every rune encodes alone but the whole does not. Nothing in the
	// supported set does this, and reporting offset 0 is the honest answer
	// to "somewhere in here" rather than a wrong precise one.
	return 0
}

// firstUnfaithfulRune returns the byte offset of the first rune that does
// not survive an encode / decode round trip.
func (c *charsetEncoder) firstUnfaithfulRune(s string) int {
	for i, r := range s {
		one := string(r)
		b, at := c.encodeRaw(one)
		if at >= 0 {
			return i
		}
		if back, bad := c.dec.decode(b); bad >= 0 || back != one {
			return i
		}
	}
	return 0
}

// firstNonASCII returns the index of the first byte with the high bit set,
// or -1 when the string is entirely 7-bit. The mirror of firstHighByte.
func firstNonASCII(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Resolution: which charset the emitted file is written in
// ---------------------------------------------------------------------------

// resolveWriteCharset decides the charset the emitted file's strings go out
// in, and the declaration it makes about itself.
//
// Precedence, highest first:
//
//  1. [WriterOptions.Charset] — the caller's explicit choice.
//  2. the sidecar's record 7/20 NAME, in the file's own spelling.
//  3. the sidecar's record 7/3 character code.
//  4. defaultCharsetName.
//
// Rule 2 is the story: emitting UTF-8 into a file whose header declares
// windows-1252 corrupts every non-ASCII label, so the writer re-encodes into
// what the SOURCE declared rather than into what is convenient. It uses the
// file's own spelling because record 7/20 is a quotation, and normalising it
// would make a byte-comparable round trip impossible for no gain.
//
// A caller's WithCharset override on the READ side is deliberately not
// consulted. It changed how the source's bytes were decoded and says nothing
// about what this file should declare; a caller who wants it to is asking
// for [WriterOptions.Charset], which is rule 1 and is stated rather than
// inferred.
//
// The record 7/3 code is the source's own when the source's own declaration
// is what won, and derived from the resolved name otherwise. Re-emitting the
// source's code preserves a file that carried a stale legacy number
// alongside a correct name — including the disagreement, which is real
// information about the source and is a warning on the way back in, not a
// failure.
func resolveWriteCharset(req DictionaryRequest) (*charsetEncoder, error) {
	var declared Charset
	if req.Sidecar != nil && req.Sidecar.Document != nil {
		declared = req.Sidecar.Document.Payload.Charset
	}

	switch {
	case req.Options.Charset != "":
		enc, err := newCharsetEncoder(req.Options.Charset)
		if err != nil {
			return nil, writeCharsetUnsupported(req.Options.Charset,
				"the caller's charset "+strconv.Quote(req.Options.Charset)+" cannot be written in", err)
		}
		enc.declaredName = req.Options.Charset
		enc.declaredCode = charsetCodeFor(enc.name)
		return enc, nil

	case declared.DeclaredName != "":
		enc, err := newCharsetEncoder(declared.DeclaredName)
		if err != nil {
			return nil, writeCharsetUnsupported(declared.DeclaredName,
				"the source file declared the charset "+strconv.Quote(declared.DeclaredName)+
					", which cannot be written in", err)
		}
		enc.declaredName = declared.DeclaredName
		enc.declaredCode = declared.DeclaredCode
		if enc.declaredCode == 0 {
			enc.declaredCode = charsetCodeFor(enc.name)
		}
		return enc, nil

	case declared.DeclaredCode != 0:
		named, _ := charsetForCode(declared.DeclaredCode)
		enc, err := newCharsetEncoder(named)
		if err != nil {
			return nil, writeCharsetUnsupported(named,
				"the source file declared character code "+strconv.FormatInt(int64(declared.DeclaredCode), 10)+
					" ("+named+"), which cannot be written in", err)
		}
		// The NAME is this package's canonical spelling, because the
		// source supplied no name to quote. The CODE is the source's own.
		enc.declaredName = enc.name
		enc.declaredCode = declared.DeclaredCode
		return enc, nil

	default:
		enc, err := newCharsetEncoder(defaultCharsetName)
		if err != nil { // unreachable: the default is in the table
			return nil, writeCharsetUnsupported(defaultCharsetName, "the default charset is unusable", err)
		}
		enc.declaredName = defaultCharsetName
		enc.declaredCode = charsetCodeFor(enc.name)
		return enc, nil
	}
}

// writeCharsetUnsupported builds the hard error for a charset that cannot be
// written in.
func writeCharsetUnsupported(name, what string, cause error) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_UNSUPPORTED,
		"spss: "+what+": "+cause.Error()+
			"; writing the text in some other charset while declaring this one would produce a file that is wrong rather than one that is merely less faithful",
		map[string]any{errors.DetailSPSSCharset: name})
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

// charsetUnencodable builds the hard error for text the target charset
// cannot represent. what names the thing being written; variable names the
// SPSS variable when the fault is inside one, and is "" otherwise.
func charsetUnencodable(cs *charsetEncoder, what, variable, s string, at int) *errors.CodedError {
	var b strings.Builder
	b.WriteString("spss: ")
	b.WriteString(what)
	b.WriteString(": the character ")
	b.WriteString(quoteRuneAt(s, at))
	b.WriteString(" of the value ")
	b.WriteString(strconv.Quote(s))
	b.WriteString(" has no representation in the character encoding ")
	b.WriteString(cs.name)
	b.WriteString(" this file is being written in; it is reported rather than replaced, because a substituted character is indistinguishable from data once it is on the wire")

	details := map[string]any{
		errors.DetailSPSSCharset: cs.name,
		errors.DetailSPSSValue:   s,
	}
	if variable != "" {
		details[errors.DetailSPSSVariable] = variable
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_UNENCODABLE, b.String(), details)
}

// quoteRuneAt renders the rune at a byte offset as a quoted literal plus its
// code point, which is what a caller needs in order to find it.
func quoteRuneAt(s string, at int) string {
	if at < 0 || at >= len(s) {
		return "(unknown)"
	}
	r, _ := utf8.DecodeRuneInString(s[at:])
	if r == utf8.RuneError {
		return "the malformed UTF-8 byte at offset " + strconv.Itoa(at)
	}
	return strconv.QuoteRune(r) + " (U+" + strings.ToUpper(strconv.FormatInt(int64(r), 16)) + ", at byte offset " + strconv.Itoa(at) + ")"
}

// widthOverflow builds the hard error for encoded text that does not fit a
// fixed-width field.
func widthOverflow(cs *charsetEncoder, what, variable, s string, need, have int) *errors.CodedError {
	msg := "spss: " + what + " is " + strconv.Itoa(need) + " byte(s) once encoded in " + cs.name +
		", past the " + strconv.Itoa(have) + " byte(s) the format allows it"
	if s != "" {
		msg += ": " + strconv.Quote(s)
	}
	msg += "; SPSS widths are byte counts and not rune counts, so encoding changes them — truncating the field to fit would cut a value, and a multi-byte character with it"

	details := map[string]any{
		errors.DetailSPSSCharset:       cs.name,
		errors.DetailSPSSWidth:         need,
		errors.DetailSPSSDeclaredWidth: have,
	}
	if variable != "" {
		details[errors.DetailSPSSVariable] = variable
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_WIDTH_OVERFLOW, msg, details)
}

// ---------------------------------------------------------------------------
// The dictionary transcode pass
// ---------------------------------------------------------------------------

// applyCharsetWrite encodes every string the emission model holds into the
// file's charset, in place, and recomputes every byte width the encoding
// moved.
//
// It runs after both front-ends and before emitDictionary, so the emitter
// sees a model whose strings are already wire bytes and whose widths are
// already true. That is what keeps the record emitter free of any charset
// knowledge at all: its len() calls, its counted-string lengths and its
// fixed-width padding are then plain byte arithmetic over the bytes that are
// actually going out.
//
// Order within the pass matters in exactly one place — a string variable's
// values are encoded before its width is recomputed and its segments are
// laid out, because the width is a measurement of the encoded bytes. See the
// file comment.
func applyCharsetWrite(f *outFile) error {
	cs := f.charset
	if cs == nil {
		// Not a defensive nicety: a pass that quietly did nothing would
		// leave every string as UTF-8 under whatever record 7/20 the
		// emitter went on to write, which is the exact corruption this
		// file exists to prevent.
		return errors.NewCodedError(errors.DATA_FILE,
			"spss: the dictionary model carries no charset encoder; the strings cannot be put on the wire without knowing what encoding the file declares")
	}

	tr := func(what, variable string, s *string) error {
		if *s == "" {
			return nil
		}
		out, at := cs.encodeString(*s)
		if at >= 0 {
			return charsetUnencodable(cs, what, variable, *s, at)
		}
		*s = out
		return nil
	}

	// The header's 9-byte creation_date and 8-byte creation_time are NOT
	// transcoded, and that is not an omission: the parse keeps them as
	// fixed-width raw bytes and applyCharset never decodes them, so they
	// are still in the source's charset and go back out unchanged. They
	// are ASCII in every file the format describes anyway.
	if err := tr("the file header file label", "", &f.fileLabel); err != nil {
		return err
	}
	if n := len(f.fileLabel); n > fileLabelLen {
		return widthOverflow(cs, "the file header file label", "", f.fileLabel, n, fileLabelLen)
	}

	for i := range f.documents {
		if err := tr("a record type 6 document line", "", &f.documents[i]); err != nil {
			return err
		}
		if n := len(f.documents[i]); n > documentLineLen {
			return widthOverflow(cs, "a record type 6 document line", "", f.documents[i], n, documentLineLen)
		}
	}

	minter := seedMinter(f)
	for _, v := range f.vars {
		if err := transcodeVariable(cs, minter, v); err != nil {
			return err
		}
	}

	// The weight is named rather than indexed, so it has to travel into
	// the same alphabet the names it is matched against now live in.
	if err := tr("the header weighting variable name", "", &f.weightName); err != nil {
		return err
	}

	for i := range f.mrSets {
		set := &f.mrSets[i]
		if err := tr("a multiple-response set name", "", &set.Name); err != nil {
			return err
		}
		if err := tr("a multiple-response set label", set.Name, &set.Label); err != nil {
			return err
		}
		if set.CountedValue != nil {
			if err := tr("a multiple-response set counted value", set.Name, set.CountedValue); err != nil {
				return err
			}
		}
		if err := encodeNames(cs, "a multiple-response set member name", set.Name, set.Variables); err != nil {
			return err
		}
	}

	for i := range f.varSets {
		vs := &f.varSets[i]
		if err := tr("a variable set name", "", &vs.Name); err != nil {
			return err
		}
		if err := encodeNames(cs, "a variable set member name", vs.Name, vs.Variables); err != nil {
			return err
		}
	}

	checkVerbatimExtensions(f)
	return nil
}

// transcodeVariable encodes one variable's text and recomputes its widths.
func transcodeVariable(cs *charsetEncoder, minter *nameMinter, v *outVar) error {
	// The variable's name is retained as UTF-8 before anything is encoded,
	// and it is what every diagnostic below quotes. Quoting v.name after
	// the pass has run would put codepage bytes into an error message,
	// which is mojibake in a place a human reads.
	v.utf8Name = v.name
	display := v.name

	tr := func(what string, s *string) error {
		if *s == "" {
			return nil
		}
		out, at := cs.encodeString(*s)
		if at >= 0 {
			return charsetUnencodable(cs, what, display, *s, at)
		}
		*s = out
		return nil
	}

	if err := tr("a variable name", &v.name); err != nil {
		return err
	}
	if err := tr("a record type 2 short variable name", &v.shortName); err != nil {
		return err
	}
	if n := len(v.shortName); n > shortNameLen {
		return widthOverflow(cs, "the record type 2 short name of variable "+strconv.Quote(display),
			display, v.shortName, n, shortNameLen)
	}
	if err := tr("a record 7/13 long variable name", &v.longName); err != nil {
		return err
	}
	if err := tr("a variable label", &v.label); err != nil {
		return err
	}

	for i := range v.labels {
		l := &v.labels[i]
		if err := tr("a value label", &l.label); err != nil {
			return err
		}
		// A value label rides a record type 3 whenever its variable's
		// labels fit the eight-byte value slot, and that record counts the
		// label with a SINGLE BYTE. Past 255 there is no field to hold the
		// length: writing it would wrap the count and desynchronise every
		// record after it. A wider string's labels go out on record 7/21,
		// whose length is an int32, so no ceiling applies there.
		if v.width <= maxShortStringWidth && len(l.label) > maxValueLabelLen {
			return widthOverflow(cs, "the value label of variable "+strconv.Quote(display),
				display, "", len(l.label), maxValueLabelLen)
		}
		if err := tr("a value-label value", &l.text); err != nil {
			return err
		}
	}

	for i := range v.categories {
		c := &v.categories[i]
		if c.Text == "" {
			c.Encoded = nil
			continue
		}
		out, at := cs.encode(c.Text)
		if at >= 0 {
			return charsetUnencodable(cs, "a value of variable "+strconv.Quote(display), display, c.Text, at)
		}
		c.Encoded = out
	}

	return recomputeWidth(cs, minter, v, display)
}

// encodeNames encodes a slice of member names in place. The mirror of
// decodeNames.
func encodeNames(cs *charsetEncoder, what, owner string, names []string) error {
	for i := range names {
		if names[i] == "" {
			continue
		}
		out, at := cs.encodeString(names[i])
		if at >= 0 {
			return charsetUnencodable(cs, what, owner, names[i], at)
		}
		names[i] = out
	}
	return nil
}

// ---------------------------------------------------------------------------
// Width recomputation and re-segmentation
// ---------------------------------------------------------------------------

// recomputeWidth sets a string variable's declared byte width from its
// ENCODED values and lays out the physical segments that width needs.
//
// A numeric variable has no width to recompute and its single element is
// already laid out by the front-end, so it returns untouched.
//
// The measurement covers every value the variable can carry: the cohort
// dictionary entries the data encoder writes through, and the value-label
// keys, which are values too and which a record 7/21 pads out to the
// variable's full declared width. Anything narrower than the widest of them
// would be a width the file's own value labels overflow.
func recomputeWidth(cs *charsetEncoder, minter *nameMinter, v *outVar, display string) error {
	if v.width == 0 && !v.widthDerived {
		return nil
	}

	need := 0
	for i := range v.categories {
		if n := len(v.categories[i].Encoded); n > need {
			need = n
		}
	}
	for i := range v.labels {
		if n := len(v.labels[i].text); n > need {
			need = n
		}
	}

	was := v.width
	switch {
	case v.widthDerived:
		// Nothing was recorded, so the width IS the measurement. SPSS has
		// no zero-width string variable, so an all-empty dictionary still
		// declares one byte.
		v.width = need
		if v.width < 1 {
			v.width = 1
		}
	case need > v.width:
		// A recorded width the encoded values overflow is WIDENED, never
		// narrowed and never a truncation. Widening cannot lose anything:
		// SPSS space-pads a value out to the declared width and the read
		// path trims that padding back off, so the extra bytes are
		// invisible on the way home. Narrowing a recorded width, by
		// contrast, would change a declaration the source made.
		v.width = need
	}

	if v.width > maxVeryLongStringWidth {
		return widthOverflow(cs, "the widest value of variable "+strconv.Quote(display), display, "",
			v.width, maxVeryLongStringWidth)
	}

	if v.width != was || len(v.segments) == 0 {
		resegment(minter, v)
		if v.print.Code == fmtA {
			// An A format's width IS the physical variable's declared byte
			// width, so it moves with it. A very long string states the
			// HEAD segment's width here and never its logical total; the
			// trailing segments carry their own, written by
			// writeVariableRecord.
			v.print.Width = stringFormatWidth(v.segments[0].Width)
			v.write.Width = v.print.Width
		}
	}
	return nil
}

// resegment lays out a string variable's physical segments for its current
// logical width.
//
// The arithmetic is longstring.go's, shared rather than restated so the
// writer and the reader cannot disagree about where a value ends. Names are
// REUSED position for position from whatever layout the variable already
// had, so a widened very long string keeps the source's own segment names
// for the segments the source had and mints only the ones it did not.
func resegment(minter *nameMinter, v *outVar) {
	n := vlsSegmentCount(v.width)
	out := make([]SegmentPlan, 0, n)
	for i := 0; i < n; i++ {
		w := vlsSegmentWidth(v.width, i)
		var name string
		switch {
		case i < len(v.segments) && v.segments[i].Name != "":
			name = v.segments[i].Name
		case i == 0:
			name = v.shortName
		default:
			name = minter.mint(vlsSegmentName(v.shortName, i))
		}
		out = append(out, SegmentPlan{
			Name:     name,
			Width:    w,
			Content:  vlsSegmentContent(v.width, i),
			Elements: (w + elementSize - 1) / elementSize,
		})
	}
	v.segments = out
}

// seedMinter builds a name minter that already holds every short name the
// model uses, so a segment name minted during re-segmentation cannot land on
// one of them.
//
// The synthesis front-end has its own minter and this is a second one, which
// is not a duplication: by the time this pass runs, both front-ends have
// finished and the complete set of names is known — including the sidecar
// path's, which never went through a minter at all because it transcribes
// the source's own names.
func seedMinter(f *outFile) *nameMinter {
	m := newNameMinter()
	for _, v := range f.vars {
		m.used[v.shortName] = true
		for _, s := range v.segments {
			m.used[s.Name] = true
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// Verbatim extension payloads
// ---------------------------------------------------------------------------

// checkVerbatimExtensions warns when a record 7/10, 7/17 or 7/18 payload is
// about to be re-emitted verbatim into a file declaring a different charset
// than the one those bytes are in.
//
// See the file comment for why they are emitted verbatim at all. The warning
// fires only when it means something: the payload has to carry a high byte
// (a pure-ASCII one is correct under every supported charset) and the target
// has to differ from what the source declared. Both conditions together are
// reachable in exactly one way, a caller's [WriterOptions.Charset].
func checkVerbatimExtensions(f *outFile) {
	cs := f.charset
	if cs == nil || f.sourceCharset == "" {
		return
	}
	if strings.EqualFold(cs.name, f.sourceCharset) {
		return
	}
	for _, rt := range []*RawText{f.productRaw, f.fileAttrs, f.varAttrs} {
		if rt == nil || firstHighByte(rt.Raw) < 0 {
			continue
		}
		f.warnings = append(f.warnings, errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_CHARSET_MISMATCH,
			"spss: the record 7/"+strconv.Itoa(int(rt.Subtype))+" payload is re-emitted verbatim in the source's "+
				strconv.Quote(f.sourceCharset)+" bytes, but this file is being written in "+cs.name+
				" and declares so; the reader never decodes that record, so its bytes are the authoritative record of what the "+
				"source said and are kept rather than guessed at",
			map[string]any{
				errors.DetailSPSSRecord:  recordName(recTypeExtension),
				errors.DetailSPSSSubtype: rt.Subtype,
				errors.DetailSPSSCharset: cs.name,
			}))
	}
}
