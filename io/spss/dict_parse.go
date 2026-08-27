package spss

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
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

	// The record 7/3 endianness field is the file's SECOND statement of a
	// byte order the header layout code already fixed. Checking it here —
	// after the extension walk, before anything derived from the
	// dictionary is built — is the last point at which a contradiction can
	// still be reported instead of silently governing every number read
	// from the file.
	if err := p.checkEndianness(d); err != nil {
		return nil, err
	}

	// The record 7/14 very-long-string fold is a SECOND pass over the
	// interpreted extensions, not part of the first. Subtypes 7/11 and
	// 7/13 address the variable list positionally and by name, and the
	// format does not promise 7/14 comes after them; folding mid-walk
	// would move the ground under whichever of the three came later.
	// Records 7/21 and 7/22 bind after the fold for the same reason from
	// the other side — an entry naming a very long string has to find the
	// LOGICAL variable, not its head segment. See longstring.go.
	p.foldVeryLongStrings(d)
	p.bindLongStringValueLabels(d)
	p.bindLongStringMissingValues(d)

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

	// bo is not established yet on any of the three failure paths below,
	// but none of them reads a multi-byte field.
	if len(p.b) == 0 {
		// A zero-length source is reported apart from a truncated one on
		// purpose. "Truncated" says a real file stops part way through a
		// record, and points a caller at a transfer that was cut short; a
		// file with no bytes has no first record to be part way through,
		// and its cause is a target that was created and never written.
		// Folding the two would answer the wrong question for whichever
		// case actually happened.
		p.bo = binary.LittleEndian
		return p.coded(errors.PULSE_SPSS_FILE_EMPTY, 0,
			"the source is empty; a system file is at least the %d-byte file header", headerSize)
	}
	if len(p.b) < headerSize {
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
	p.checkMagicAgainstCompression(d, h)

	d.header = h
	return nil
}

// checkMagicAgainstCompression raises PULSE_SPSS_MAGIC_FLAG_MISMATCH when the
// 4-byte magic and the compression flag disagree about whether the file is a
// ZSAV.
//
// # Why this is a warning and not an error
//
// The two fields are not equally informative, which is the same shape as the
// record 7/20 versus 7/3 charset disagreement and gets the same answer. The
// compression flag says how the data section is ENCODED, and it is what the
// reader dispatches on; the magic is a coarse generation label that says
// which family of writer produced the file. A tool that inflates a `.zsav`
// and rewrites the data section uncompressed has every reason to leave `$FL3`
// in place, and such a file reads perfectly — rejecting it would lose the
// data to enforce a label.
//
// This is the OPPOSITE call from the endianness cross-check three fields
// away, and deliberately so. Byte order governs how every multi-byte field in
// the file is READ, so getting it wrong yields a whole file of plausible
// wrong numbers with nothing to notice; the compression flag governs only
// which decoder runs, and the wrong decoder fails loudly on the first case
// rather than quietly succeeding.
//
// The guess we are recording: no real writer is known to emit a mismatched
// pair, so this warning fires on nothing we have ever seen. It exists because
// the flag is the single dispatch point by design, and a file that carries
// the ZSAV magic without the ZSAV flag should say so rather than pass
// silently.
func (p *parser) checkMagicAgainstCompression(d *dictionary, h fileHeader) {
	zsavMagic := h.magic == magicZSAV
	zsavFlag := h.compression == compressionZSAV
	if zsavMagic == zsavFlag {
		return
	}
	msg := "spss: the file opens with the magic " + strconv.Quote(h.magic) +
		" but declares compression " + strconv.FormatInt(int64(h.compression), 10) +
		"; " + strconv.Quote(magicZSAV) + " marks a ZSAV and pairs with compression 2, while " +
		strconv.Quote(magicSAV) + " pairs with 0 (uncompressed) or 1 (bytecode). " +
		"The compression flag decides how the data section is read, because it is the field " +
		"that describes the bytes; the magic is a generation label a re-saving tool can leave stale"
	d.warnings = append(d.warnings, errors.NewCodedErrorWithDetails(
		errors.PULSE_SPSS_MAGIC_FLAG_MISMATCH, msg, map[string]any{
			errors.DetailSPSSRecord: recordHeader,
			errors.DetailSPSSOffset: offCompression,
		}))
}

// isLayoutCode reports whether v is one of the two values the header layout
// code is allowed to hold.
func isLayoutCode(v uint32) bool { return v == 2 || v == 3 }

// Record 7/3 endianness codes.
const (
	endiannessBig    int32 = 1
	endiannessLittle int32 = 2
)

// checkEndianness reconciles the byte order the header layout code fixed with
// the one record 7/3 declares.
//
// # Why the layout code drives and 7/3 only checks
//
// The layout code is unambiguous by construction: it holds 2 or 3, and
// neither 2 nor 3 byte-swaps into the other or into anything in range
// (2 swaps to 0x02000000). Reading it both ways therefore identifies the
// file's order with no residual doubt, which is precisely why the format put
// it there. Record 7/3 cannot do the same job, because reading ITS endianness
// field already requires knowing the byte order — so it is a corroboration,
// never a source.
//
// # Why a disagreement is an error
//
// The sibling charset cross-check (record 7/20 versus 7/3) is a warning: the
// name wins, the number is routinely stale, and the cost of choosing wrongly
// is mis-decoded text in some strings. Byte order is not like that. It
// governs every count, every offset, every length prefix and every double in
// the file, so there is no partial damage: one of the two readings produces a
// coherent file and the other produces garbage that may still parse. When the
// file's own two statements disagree, we cannot tell which reading the writer
// meant, and "the layout code, obviously" is a guess that would silently
// pick a whole file's worth of numbers. Fail loudly instead.
//
// # What is NOT a disagreement
//
// A missing record 7/3, an endianness field of 0, and any value outside
// {1, 2} are all left alone. Only a clean statement of the OTHER order is a
// contradiction. An out-of-range value is warned about under
// PULSE_SPSS_EXTENSION_INVALID and otherwise ignored — it is a field a writer
// failed to fill, not a claim about byte order. Note that a byte-swapped 1 or
// 2 lands in exactly that bucket (they read as 16777216 and 33554432), so the
// case where a reader has the order wrong reports as an unfillable field
// rather than as a mismatch; the layout code already made that case
// unreachable.
func (p *parser) checkEndianness(d *dictionary) error {
	mi := d.machineInteger
	if !mi.present {
		return nil
	}
	x, ok := d.rawExtension(extMachineInteger)
	if !ok {
		// Unreachable: machineInteger.present is set only from a record.
		return nil
	}

	var declared binary.ByteOrder
	switch mi.endianness {
	case endiannessBig:
		declared = binary.BigEndian
	case endiannessLittle:
		declared = binary.LittleEndian
	default:
		if mi.endianness != 0 {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+24,
				"the endianness field is %d; the format defines only %d (big-endian) and %d (little-endian), so it says nothing about the file's byte order and the header layout code stands alone",
				mi.endianness, endiannessBig, endiannessLittle)
		}
		return nil
	}
	if declared == p.bo {
		return nil
	}

	msg := "spss: the file states its byte order twice and the two disagree: the header layout code reads as " +
		strconv.FormatInt(int64(d.header.layoutCode), 10) + " " + byteOrderName(p.bo) +
		" while record 7/3 declares endianness " + strconv.FormatInt(int64(mi.endianness), 10) +
		" (" + byteOrderName(declared) + "); byte order governs every multi-byte field in the file, " +
		"so reading it the wrong way would produce a complete set of plausible and incorrect numbers " +
		"rather than one bad field [at byte offset " + strconv.Itoa(x.offset) + "]"
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_ENDIANNESS_MISMATCH, msg, map[string]any{
		errors.DetailSPSSRecord:  recordName(recTypeExtension),
		errors.DetailSPSSSubtype: x.subtype,
		errors.DetailSPSSOffset:  x.offset,
	})
}

// byteOrderName renders a byte order for a diagnostic.
func byteOrderName(bo binary.ByteOrder) string {
	if bo == binary.ByteOrder(binary.BigEndian) {
		return "big-endian"
	}
	return "little-endian"
}

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
			// Kept strict, reviewed in E3-S5. A record type 4 reaching the
			// main loop means its record type 3 is not where the format
			// puts it, and the record 3 is where the LABELS are — so the
			// tolerant reading would import a binding with nothing to
			// bind. The pairing is also what lets the record 4 be read
			// inline (see parseValueLabelRecord): admitting a free-standing
			// one would mean guessing, from the record type tag alone,
			// whether the bytes ahead are a count or the next record.
			//
			// What this rejects that might be real: a writer batching
			// several record 3s before their record 4s. No such writer is
			// known, and PSPP's own reader requires the adjacency too.
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
	//
	// Reviewed in E3-S5 and kept, with the guess recorded rather than
	// resolved. No defensive fallback is possible here: the two readings
	// diverge by a whole payload, so a reader that guessed wrong would not
	// notice until some later record failed to frame, by which point it
	// cannot tell whether the fault was here or there — and "retry the
	// whole walk the other way" would silently accept whichever reading
	// happened to parse, which is exactly the plausible-and-wrong outcome
	// this package refuses everywhere else. Every fixture writes zeros in
	// these fields on a continuation, so both readings agree on everything
	// we can generate; a real writer that sets the flag and omits the
	// payload would be caught as a framing error further down the walk,
	// which is a loud failure rather than a wrong dictionary.
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
		// The other half of the adjacency rule, and kept strict for the
		// same reason: without the record 4 the labels just read name no
		// variables, and the parser has no second place to look for them.
		// This is a FRAMING rule, which is the line E3-S5 drew — what the
		// record 4 turns out to MEAN is tolerated (see resolveValueLabels),
		// where it sits is not.
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
//
// # Where the strict line sits, and why it moved
//
// E2-S2 rejected the whole FILE for any binding fault here. E3-S5 splits
// them, on the principle that a fault which desynchronises the walk is not
// the same kind of thing as a fault in what a record MEANS:
//
//   - An element index below 1, or past the end of the dictionary, is
//     CORRUPTION. It cannot be produced by a writer with a different reading
//     of the format, only by damaged bytes, and damaged bytes here mean the
//     record 4 payload is not what it claims — which puts everything around
//     it in doubt. Still a hard PULSE_SPSS_DICT_INVALID.
//
//   - A set that names a real variable it cannot be BOUND to — a mixed-width
//     set, a set on a string wider than the 8-byte value slot, an index
//     landing on a string continuation — is a dialect problem, and it is one
//     PSPP's own reader tolerates by skipping the record. The set is dropped
//     with a PULSE_SPSS_VALUE_LABELS_DROPPED warning naming the variable, and
//     the file imports.
//
// The trade being made, stated plainly: a value label is display metadata,
// so dropping one costs presentation while rejecting the file costs the data.
// The alternative — binding the set anyway — is the one option ruled out
// entirely, because an 8-byte value slot matched against a 20-byte string
// would label every value sharing a prefix, and a silently wrong label is
// worse than an absent one.
//
// What a strict reading would still have rejected, now that it does not: a
// pre-SPSS-13 file whose long-string value labels predate the record 7/21
// extension, and a file whose VALUE LABELS command was applied across
// variables of different declared widths. Neither is confirmed to exist in
// the wild — we have no real-world corpus — so both are recorded here as
// what the tolerance is FOR rather than as observed shapes.
//
// A dropped set is removed from d.valueLabels outright rather than flagged,
// so no later pass — charset decoding, the mapping's projection onto
// variables, the record 7/21 fold — has to know about the concept.
func (p *parser) resolveValueLabels(d *dictionary) error {
	kept := d.valueLabels[:0]
	for i := range d.valueLabels {
		set := &d.valueLabels[i]
		p.record = recordName(recTypeLabelVars)
		p.recordOff = set.varsOffset

		width := -1
		drop := false
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
				p.dropValueLabels(d, set, v.name,
					"it names dictionary element index %d, which is a continuation element of the long string variable %q rather than a variable of its own",
					idx, v.name)
				drop = true
				break
			}
			if v.isString() && v.width > maxShortStringWidth {
				p.dropValueLabels(d, set, v.name,
					"it attaches value labels to %q, a %d-byte string, and a record type 3 value slot holds only %d bytes; strings wider than that carry their value labels in the record 7/21 extension, which this reader does read",
					v.name, v.width, maxShortStringWidth)
				drop = true
				break
			}
			if width == -1 {
				width = v.width
				continue
			}
			if v.width != width {
				p.dropValueLabels(d, set, v.name,
					"it mixes variables of width %d and %d (%q), and the width is what decides whether each 8-byte value slot is read as a double or as text; one set cannot be read both ways",
					width, v.width, v.name)
				drop = true
				break
			}
		}
		if drop {
			continue
		}
		set.width = width
		kept = append(kept, *set)
	}
	d.valueLabels = kept
	return nil
}

// dropValueLabels records one PULSE_SPSS_VALUE_LABELS_DROPPED warning. The
// caller drops the set; this only says so.
func (p *parser) dropValueLabels(d *dictionary, set *valueLabelSet, variable string, format string, args ...any) {
	msg := "spss: the record 3/4 value-label set starting at byte offset " +
		strconv.Itoa(set.offset) + " was dropped: " + fmt.Sprintf(format, args...) +
		"; the variable's DATA is unaffected, only its labels [at byte offset " +
		strconv.Itoa(set.varsOffset) + "]"
	details := map[string]any{
		errors.DetailSPSSRecord: recordName(recTypeLabelVars),
		errors.DetailSPSSOffset: set.varsOffset,
	}
	if variable != "" {
		details[errors.DetailSPSSVariable] = variable
	}
	d.warnings = append(d.warnings, errors.NewCodedErrorWithDetails(
		errors.PULSE_SPSS_VALUE_LABELS_DROPPED, msg, details))
}

func roundUp(n, mult int) int { return (n + mult - 1) / mult * mult }
