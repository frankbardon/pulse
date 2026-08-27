package spss

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// Record names used in diagnostics and in the CodedError details under
// errors.DetailSPSSRecord.
const (
	recordHeader  = "header"
	recordUnknown = "unknown"
)

// recordName renders a record type tag for diagnostics.
func recordName(rt int32) string {
	switch rt {
	case recTypeVariable, recTypeValueLabel, recTypeLabelVars,
		recTypeDocument, recTypeExtension, recTypeTerminator:
		return fmt.Sprintf("%d", rt)
	default:
		return recordUnknown
	}
}

// Byte offsets of the file header fields, used when an error has to point at
// a field the parser has already stepped past.
const (
	offLayoutCode  = 64
	offCompression = 72
	offWeightIndex = 76
	offCaseCount   = 80
	offBias        = 84
)

// parser walks the dictionary section of a `.sav` file.
//
// Every read goes through a bounds-checked accessor, so a truncated or
// hostile file yields a coded error rather than a panic. Nothing here indexes
// p.b directly without first proving the bytes are there.
type parser struct {
	b   []byte
	off int
	bo  binary.ByteOrder

	// record names the record currently being read, for error reporting.
	record string
	// recordOff is the byte offset that record started at.
	recordOff int
}

// parseDictionary decodes the dictionary section of a `.sav` file: the header
// record, the record type 2 variable records with their string continuations,
// the record 3/4 value-label pairs, the record type 6 documents, the record
// type 7 extension subtypes, and the record type 999 terminator.
//
// Documents and extension payloads are retained verbatim whether or not they
// are interpreted, and an extension subtype this reader does not know is a
// warning on dictionary.warnings, never a parse failure.
//
// On return, dictionary.dataOffset is the offset of the first byte after the
// terminator. The data section is untouched.
func parseDictionary(b []byte) (*dictionary, error) {
	return parseDictionaryWithCharset(b, "")
}

// parseDictionaryWithCharset is parseDictionary with a caller-supplied
// charset that overrides whatever the file declares. An empty override means
// the file's own declaration decides — see resolveCharset.
func parseDictionaryWithCharset(b []byte, override string) (*dictionary, error) {
	p := &parser{b: b, record: recordHeader}

	d := &dictionary{sysmis: defaultSysmis}
	if err := p.parseHeader(d); err != nil {
		return nil, err
	}
	if err := p.parseRecords(d); err != nil {
		return nil, err
	}
	if err := p.resolveValueLabels(d); err != nil {
		return nil, err
	}
	// Extensions are interpreted only after the whole record walk, so the
	// subtypes keyed to the variable list (7/11, 7/13) do not depend on
	// record order. Nothing here can fail: every extension fault is a
	// warning, because the record framing was already validated during the
	// walk and a bad payload therefore cannot desynchronise anything.
	p.applyExtensions(d)

	// The charset is resolved last for the same reason: records 7/3 and
	// 7/20 declare it, and both come after every record that carries text.
	// Until this point every string in the dictionary is the raw bytes the
	// file held; applyCharset is what turns them into UTF-8.
	if err := resolveCharset(d, override); err != nil {
		return nil, err
	}
	if err := applyCharset(d); err != nil {
		return nil, err
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// invalid reports a structurally malformed dictionary at the parser's current
// record and the given offset.
func (p *parser) invalid(off int, format string, args ...any) error {
	return p.coded(errors.PULSE_SPSS_DICT_INVALID, off, format, args...)
}

// truncated reports a dictionary that ran out of bytes. what names the field
// or payload the read was reaching for.
func (p *parser) truncated(off int, what string) error {
	return p.coded(errors.PULSE_SPSS_DICT_TRUNCATED, off,
		"the file ends before %s; %d byte(s) remain", what, len(p.b)-off)
}

func (p *parser) coded(code errors.Code, off int, format string, args ...any) error {
	where := "file header"
	if p.record != recordHeader {
		where = "record type " + p.record
	}
	msg := "spss: " + where +
		fmt.Sprintf(" starting at byte offset %d (0x%X): ", p.recordOff, p.recordOff) +
		fmt.Sprintf(format, args...) +
		fmt.Sprintf(" [at byte offset %d (0x%X)]", off, off)
	return errors.NewCodedErrorWithDetails(code, msg, map[string]any{
		errors.DetailSPSSRecord: p.record,
		errors.DetailSPSSOffset: off,
	})
}

// ---------------------------------------------------------------------------
// Bounds-checked primitives
// ---------------------------------------------------------------------------

func (p *parser) remaining() int { return len(p.b) - p.off }

// i32 reads one 32-bit signed integer in the file's byte order.
func (p *parser) i32(what string) (int32, error) {
	if p.remaining() < 4 {
		return 0, p.truncated(p.off, what)
	}
	v := int32(p.bo.Uint32(p.b[p.off : p.off+4]))
	p.off += 4
	return v, nil
}

// f64 reads one IEEE 754 double in the file's byte order.
func (p *parser) f64(what string) (float64, error) {
	if p.remaining() < 8 {
		return 0, p.truncated(p.off, what)
	}
	v := math.Float64frombits(p.bo.Uint64(p.b[p.off : p.off+8]))
	p.off += 8
	return v, nil
}

// take returns the next n bytes. The returned slice aliases the input, so
// callers that retain it must copy.
func (p *parser) take(n int, what string) ([]byte, error) {
	if n < 0 {
		return nil, p.invalid(p.off, "%s has a negative length %d", what, n)
	}
	if p.remaining() < n {
		return nil, p.truncated(p.off, what)
	}
	out := p.b[p.off : p.off+n]
	p.off += n
	return out, nil
}

// skip advances over n bytes without reading them.
func (p *parser) skip(n int64, what string) error {
	if n < 0 {
		return p.invalid(p.off, "%s has a negative length %d", what, n)
	}
	if int64(p.remaining()) < n {
		return p.truncated(p.off, what)
	}
	p.off += int(n)
	return nil
}

// fixedString reads an n-byte space-padded ASCII field and strips the
// padding. Trailing spaces in these fields are padding by definition; there is
// no way for a writer to express a trailing space as content.
func (p *parser) fixedString(n int, what string) (string, error) {
	raw, err := p.take(n, what)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), " \x00"), nil
}

// ---------------------------------------------------------------------------
// File header
// ---------------------------------------------------------------------------

// parseHeader decodes the 176-byte file header record and, as a side effect,
// fixes the file's byte order from the layout code.
func (p *parser) parseHeader(d *dictionary) error {
	p.record = recordHeader
	p.recordOff = 0

	if len(p.b) < headerSize {
		// bo is not established yet, but no multi-byte field is read on
		// this path.
		p.bo = binary.LittleEndian
		return p.truncated(len(p.b),
			fmt.Sprintf("the %d-byte file header record is complete", headerSize))
	}

	magic := string(p.b[0:4])
	if magic != magicSAV && magic != magicZSAV {
		p.bo = binary.LittleEndian
		return p.invalid(0,
			"the file opens with %q, not the SPSS system-file magic %q or %q; this is not a .sav system file",
			magic, magicSAV, magicZSAV)
	}

	// The layout code is written in the file's own byte order and is always
	// 2 or 3, which makes it the endianness probe. Reading it both ways is
	// the only way to know which order the rest of the file is in.
	raw := p.b[offLayoutCode : offLayoutCode+4]
	switch {
	case isLayoutCode(binary.LittleEndian.Uint32(raw)):
		p.bo = binary.LittleEndian
	case isLayoutCode(binary.BigEndian.Uint32(raw)):
		p.bo = binary.BigEndian
	default:
		p.bo = binary.LittleEndian
		return p.invalid(offLayoutCode,
			"layout_code reads %d little-endian and %d big-endian; neither is the expected 2 or 3, so the file's byte order cannot be determined",
			int32(binary.LittleEndian.Uint32(raw)), int32(binary.BigEndian.Uint32(raw)))
	}
	d.byteOrder = p.bo

	h := fileHeader{magic: magic}
	p.off = 4

	var err error
	if h.productName, err = p.fixedString(60, "the 60-byte prod_name field"); err != nil {
		return err
	}
	if h.layoutCode, err = p.i32("the layout_code field"); err != nil {
		return err
	}
	if h.nominalCaseSize, err = p.i32("the nominal_case_size field"); err != nil {
		return err
	}
	if h.compression, err = p.i32("the compression field"); err != nil {
		return err
	}
	if h.weightIndex, err = p.i32("the weight_index field"); err != nil {
		return err
	}
	if h.caseCount, err = p.i32("the ncases field"); err != nil {
		return err
	}
	if h.bias, err = p.f64("the compression bias field"); err != nil {
		return err
	}
	if h.creationDate, err = p.fixedString(9, "the 9-byte creation_date field"); err != nil {
		return err
	}
	if h.creationTime, err = p.fixedString(8, "the 8-byte creation_time field"); err != nil {
		return err
	}
	if h.fileLabel, err = p.fixedString(64, "the 64-byte file_label field"); err != nil {
		return err
	}
	if err := p.skip(3, "the 3 bytes of header padding"); err != nil {
		return err
	}

	if h.compression < compressionNone || h.compression > compressionZSAV {
		return p.invalid(offCompression, "compression is %d; the format defines only 0 (uncompressed), 1 (bytecode) and 2 (ZSAV)", h.compression)
	}
	if h.weightIndex < 0 {
		return p.invalid(offWeightIndex, "weight_index is %d; it must be 0 (unweighted) or a 1-based dictionary element index", h.weightIndex)
	}
	if h.caseCount < -1 {
		return p.invalid(offCaseCount, "ncases is %d; it must be a non-negative count or -1 for unknown", h.caseCount)
	}

	d.header = h
	return nil
}

// isLayoutCode reports whether v is one of the two values the header layout
// code is allowed to hold.
func isLayoutCode(v uint32) bool { return v == 2 || v == 3 }

// ---------------------------------------------------------------------------
// Record walk
// ---------------------------------------------------------------------------

// parseRecords walks the tagged records between the header and the record
// type 999 terminator.
func (p *parser) parseRecords(d *dictionary) error {
	// pendingSegments counts continuation records still owed by the last
	// string variable. It is what lets a stray or missing continuation be
	// caught where it happens rather than as a mystery offset later.
	pendingSegments := 0
	// sawTerminator guards the loop; the walk ends only at record 999.
	for {
		recordOff := p.off
		p.record = recordUnknown
		p.recordOff = recordOff

		if p.remaining() == 0 {
			return p.truncated(recordOff,
				"the record type 999 dictionary terminator; the dictionary has no terminator")
		}
		rt, err := p.i32("a 4-byte record type tag")
		if err != nil {
			return err
		}
		p.record = recordName(rt)

		switch rt {
		case recTypeVariable:
			if err := p.parseVariableRecord(d, &pendingSegments); err != nil {
				return err
			}
		case recTypeValueLabel:
			if pendingSegments != 0 {
				return p.invalid(recordOff,
					"a value-label record interrupts a long string variable that still owes %d continuation record(s)", pendingSegments)
			}
			if err := p.parseValueLabelRecord(d); err != nil {
				return err
			}
		case recTypeLabelVars:
			return p.invalid(recordOff,
				"a record type 4 appeared without an immediately preceding record type 3; the format binds them as a pair")
		case recTypeDocument:
			if err := p.parseDocumentRecord(d); err != nil {
				return err
			}
		case recTypeExtension:
			if err := p.parseExtensionRecord(d); err != nil {
				return err
			}
		case recTypeTerminator:
			if pendingSegments != 0 {
				return p.invalid(recordOff,
					"the dictionary terminates while a long string variable still owes %d continuation record(s)", pendingSegments)
			}
			// The terminator is rec_type plus a 4-byte filler whose value
			// the spec says to ignore. A file that stops between the two
			// is truncated, not terminated.
			if _, err := p.i32("the record type 999 filler field"); err != nil {
				return err
			}
			d.dataOffset = p.off
			return nil
		default:
			return p.invalid(recordOff,
				"unknown record type %d; the format defines 2, 3, 4, 6, 7 and 999", rt)
		}
	}
}

// parseVariableRecord decodes one record type 2 — either a real variable or a
// continuation of the string variable before it.
func (p *parser) parseVariableRecord(d *dictionary, pendingSegments *int) error {
	recordOff := p.recordOff

	typeCode, err := p.i32("the variable record type field")
	if err != nil {
		return err
	}
	hasLabelFlag, err := p.i32("the has_var_label field")
	if err != nil {
		return err
	}
	nMissing, err := p.i32("the n_missing_values field")
	if err != nil {
		return err
	}
	printRaw, err := p.i32("the print format field")
	if err != nil {
		return err
	}
	writeRaw, err := p.i32("the write format field")
	if err != nil {
		return err
	}
	name, err := p.fixedString(shortNameLen, "the 8-byte variable name field")
	if err != nil {
		return err
	}

	if hasLabelFlag != 0 && hasLabelFlag != 1 {
		return p.invalid(recordOff+8, "has_var_label is %d; it must be 0 or 1", hasLabelFlag)
	}
	switch nMissing {
	case -3, -2, 0, 1, 2, 3:
	default:
		return p.invalid(recordOff+12,
			"n_missing_values is %d; the format allows only -3, -2, 0, 1, 2 or 3", nMissing)
	}

	// The label and missing-value payloads are read whenever the flags say
	// they are present, continuation record or not. The spec says a
	// continuation's remaining fields are to be ignored, but "ignored"
	// cannot mean "not present": if a writer set the flag it also wrote the
	// payload, and skipping it would desynchronise every following record.
	var label string
	if hasLabelFlag == 1 {
		label, err = p.parseVariableLabel()
		if err != nil {
			return err
		}
	}

	width := 0
	isContinuation := typeCode == typeStringContinuation
	switch {
	case isContinuation:
	case typeCode == 0:
	case typeCode >= 1 && typeCode <= 255:
		width = int(typeCode)
	default:
		return p.invalid(recordOff+4,
			"the variable type field is %d; it must be 0 for numeric, 1..255 for a string byte width, or -1 for a string continuation record", typeCode)
	}

	missing, err := p.parseMissingValues(nMissing, width)
	if err != nil {
		return err
	}

	if isContinuation {
		if *pendingSegments == 0 {
			return p.invalid(recordOff,
				"a string continuation record (type -1) appeared where no long string variable is expecting one")
		}
		*pendingSegments--
		d.elementCount++
		return nil
	}

	if *pendingSegments != 0 {
		return p.invalid(recordOff,
			"variable %q starts while the previous long string variable still owes %d continuation record(s)", name, *pendingSegments)
	}

	segments := 1
	if width > 0 {
		segments = (width + elementSize - 1) / elementSize
	}
	v := variable{
		name:     name,
		index:    d.elementCount + 1,
		typeCode: typeCode,
		width:    width,
		segments: segments,
		print:    unpackFormat(printRaw),
		write:    unpackFormat(writeRaw),
		hasLabel: hasLabelFlag == 1,
		label:    label,
		missing:  missing,
		offset:   recordOff,
	}
	d.vars = append(d.vars, v)
	d.elementCount++
	*pendingSegments = segments - 1
	return nil
}

// parseVariableLabel reads the length-prefixed variable label and steps over
// the padding that rounds the text out to a multiple of 4 bytes.
func (p *parser) parseVariableLabel() (string, error) {
	lenOff := p.off
	n, err := p.i32("the variable label length field")
	if err != nil {
		return "", err
	}
	if n < 0 {
		return "", p.invalid(lenOff, "the variable label length is %d; it cannot be negative", n)
	}
	if int64(n) > int64(p.remaining()) {
		return "", p.truncated(p.off, fmt.Sprintf("the %d-byte variable label it declares", n))
	}
	raw, err := p.take(int(n), "the variable label text")
	if err != nil {
		return "", err
	}
	label := string(raw)
	padding := int64(roundUp(int(n), 4) - int(n))
	if err := p.skip(padding, "the variable label's 32-bit alignment padding"); err != nil {
		return "", err
	}
	return label, nil
}

// parseMissingValues reads the abs(nMissing) eight-byte missing-value slots.
func (p *parser) parseMissingValues(nMissing int32, width int) (missingSpec, error) {
	spec := missingSpec{code: nMissing}
	n := int(nMissing)
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return spec, nil
	}
	if width > 0 && nMissing < 0 {
		return spec, p.invalid(p.off,
			"n_missing_values is %d, a lo..hi range, on a string variable; the format has no range form for strings", nMissing)
	}
	for i := 0; i < n; i++ {
		raw, err := p.take(elementSize, fmt.Sprintf("missing value slot %d of %d", i+1, n))
		if err != nil {
			return spec, err
		}
		var slot [elementSize]byte
		copy(slot[:], raw)
		spec.raw = append(spec.raw, slot)
		if width > 0 {
			trim := width
			if trim > elementSize {
				trim = elementSize
			}
			spec.text = append(spec.text, strings.TrimRight(string(slot[:trim]), " "))
		} else {
			spec.numeric = append(spec.numeric, math.Float64frombits(p.bo.Uint64(slot[:])))
		}
	}
	return spec, nil
}

// parseValueLabelRecord decodes a record type 3 and the record type 4 the
// format requires to follow it immediately.
func (p *parser) parseValueLabelRecord(d *dictionary) error {
	recordOff := p.recordOff

	countOff := p.off
	count, err := p.i32("the value-label count field")
	if err != nil {
		return err
	}
	if count < 0 {
		return p.invalid(countOff, "the value-label count is %d; it cannot be negative", count)
	}
	// Each pair is at least 8 bytes of value plus 8 bytes of
	// length-and-text, so a count claiming more pairs than could possibly
	// fit is a corrupt length rather than a huge allocation.
	if int64(count)*int64(2*elementSize) > int64(p.remaining()) {
		return p.truncated(p.off, fmt.Sprintf("the %d value-label pair(s) it declares", count))
	}

	set := valueLabelSet{offset: recordOff, labels: make([]valueLabel, 0, count)}
	for i := int32(0); i < count; i++ {
		raw, err := p.take(elementSize, fmt.Sprintf("the value slot of value-label pair %d of %d", i+1, count))
		if err != nil {
			return err
		}
		var l valueLabel
		copy(l.raw[:], raw)

		lenByte, err := p.take(1, fmt.Sprintf("the label length byte of value-label pair %d of %d", i+1, count))
		if err != nil {
			return err
		}
		// The length is a single unsigned byte, so 255 is the hard ceiling
		// and no value of it is out of range.
		n := int(lenByte[0])
		text, err := p.take(n, fmt.Sprintf("the %d-byte label of value-label pair %d of %d", n, i+1, count))
		if err != nil {
			return err
		}
		// The length byte is exact, so the text is taken verbatim: a label
		// is user content and trimming it would silently rewrite data. The
		// padding that follows is separate and is stepped over, not read.
		l.label = string(text)
		// The length byte and the text together pad out to a multiple of 8.
		padding := int64(roundUp(n+1, elementSize) - (n + 1))
		if err := p.skip(padding, "the value label's 64-bit alignment padding"); err != nil {
			return err
		}
		set.labels = append(set.labels, l)
	}

	// The record type 4 binding must come next. Reading it here rather than
	// from the main loop is what enforces the adjacency the format requires.
	nextOff := p.off
	rt, err := p.i32("the record type 4 that must follow a record type 3")
	if err != nil {
		return err
	}
	if rt != recTypeLabelVars {
		return p.invalid(nextOff,
			"a record type 3 is followed by record type %d; the format requires the record type 4 naming its variables to follow immediately", rt)
	}
	p.record = recordName(recTypeLabelVars)
	p.recordOff = nextOff
	set.varsOffset = nextOff

	varCountOff := p.off
	varCount, err := p.i32("the record type 4 variable count field")
	if err != nil {
		return err
	}
	if varCount < 1 {
		return p.invalid(varCountOff,
			"the record type 4 names %d variables; it must name at least one", varCount)
	}
	if int64(varCount)*4 > int64(p.remaining()) {
		return p.truncated(p.off, fmt.Sprintf("the %d variable index/indices it declares", varCount))
	}
	set.varIndices = make([]int32, 0, varCount)
	for i := int32(0); i < varCount; i++ {
		idx, err := p.i32(fmt.Sprintf("variable index %d of %d", i+1, varCount))
		if err != nil {
			return err
		}
		set.varIndices = append(set.varIndices, idx)
	}

	d.valueLabels = append(d.valueLabels, set)
	return nil
}

// parseDocumentRecord reads a record type 6 and keeps its lines verbatim.
//
// Nothing is trimmed or re-wrapped. A document line is a fixed-width 80-byte
// field, so its trailing spaces are indistinguishable from padding; deciding
// which they are is a guess, and the round trip that has to write these lines
// back needs the bytes that were actually there. Presentation is the
// caller's problem.
func (p *parser) parseDocumentRecord(d *dictionary) error {
	nOff := p.off
	n, err := p.i32("the document record line count")
	if err != nil {
		return err
	}
	if n < 0 {
		return p.invalid(nOff, "the document record declares %d lines; it cannot be negative", n)
	}
	// A line count claiming more bytes than remain is a corrupt length, not
	// an allocation request.
	if int64(n)*documentLineLen > int64(p.remaining()) {
		return p.truncated(p.off,
			fmt.Sprintf("the %d document line(s) of %d bytes it declares", n, documentLineLen))
	}
	for i := int32(0); i < n; i++ {
		raw, err := p.take(documentLineLen,
			fmt.Sprintf("document line %d of %d", i+1, n))
		if err != nil {
			return err
		}
		d.documents = append(d.documents, string(raw))
	}
	return nil
}

// parseExtensionRecord reads a record type 7 of any subtype and keeps its
// payload verbatim. Interpretation happens after the walk, in
// applyExtensions.
//
// The record's FRAMING is validated strictly here — a negative or
// overreaching size/count would desynchronise every following record, so it
// is a hard error. The payload's CONTENT is never validated here, because a
// dictionary is allowed to contain no extension records at all and any
// number of subtypes this reader has never seen.
func (p *parser) parseExtensionRecord(d *dictionary) error {
	recordOff := p.recordOff
	subtype, err := p.i32("the extension record subtype")
	if err != nil {
		return err
	}
	sizeOff := p.off
	size, err := p.i32("the extension record element size")
	if err != nil {
		return err
	}
	count, err := p.i32("the extension record element count")
	if err != nil {
		return err
	}
	if size < 0 || count < 0 {
		return p.invalid(sizeOff,
			"extension subtype %d declares size %d and count %d; neither can be negative", subtype, size, count)
	}
	n := int64(size) * int64(count)
	if n > int64(p.remaining()) {
		return p.truncated(p.off,
			fmt.Sprintf("the %d x %d payload bytes of extension subtype %d", count, size, subtype))
	}
	payloadOff := p.off
	raw, err := p.take(int(n),
		fmt.Sprintf("the %d x %d payload bytes of extension subtype %d", count, size, subtype))
	if err != nil {
		return err
	}
	d.extensions = append(d.extensions, extensionRecord{
		subtype:       subtype,
		size:          size,
		count:         count,
		offset:        recordOff,
		payloadOffset: payloadOff,
		// The payload is copied rather than aliased: it outlives the parse
		// and a caller holding the source buffer must be free to reuse it.
		payload: append([]byte(nil), raw...),
	})
	return nil
}

// ---------------------------------------------------------------------------
// Post-walk resolution
// ---------------------------------------------------------------------------

// resolveValueLabels binds every record 3/4 pair to its variables once the
// whole record type 2 stream is known, and fixes the common width that
// decides how each 8-byte value slot is read.
//
// It runs after the walk rather than inline because the format does not
// promise that every variable record precedes every value-label record.
func (p *parser) resolveValueLabels(d *dictionary) error {
	for i := range d.valueLabels {
		set := &d.valueLabels[i]
		p.record = recordName(recTypeLabelVars)
		p.recordOff = set.varsOffset

		width := -1
		for _, idx := range set.varIndices {
			if idx < 1 {
				return p.invalid(set.varsOffset,
					"a record type 4 names dictionary element index %d; indices are 1-based", idx)
			}
			v, isFirst, ok := d.variableByIndex(idx)
			if !ok {
				return p.invalid(set.varsOffset,
					"a record type 4 names dictionary element index %d, but the dictionary has only %d element(s)", idx, d.elementCount)
			}
			if !isFirst {
				return p.invalid(set.varsOffset,
					"a record type 4 names dictionary element index %d, which is a continuation element of the long string variable %q, not a variable", idx, v.name)
			}
			if v.isString() && v.width > maxShortStringWidth {
				return p.invalid(set.varsOffset,
					"a record type 4 attaches value labels to %q, a %d-byte string; strings wider than %d carry their value labels in the record 7/21 extension instead", v.name, v.width, maxShortStringWidth)
			}
			if width == -1 {
				width = v.width
				continue
			}
			if v.width != width {
				return p.invalid(set.varsOffset,
					"a record type 4 mixes variables of width %d and %d (%q); every variable sharing one value-label set must have the same type and width", width, v.width, v.name)
			}
		}
		set.width = width
	}
	return nil
}

func roundUp(n, mult int) int { return (n + mult - 1) / mult * mult }
