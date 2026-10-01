package spss

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// Record type 7 extension subtypes.
//
// The set this reader interprets is deliberately open at the bottom: an
// unrecognised subtype is a warning and a skip, never a parse failure. Real
// SPSS versions emit subtypes no published description lists, and a reader
// that rejects a file over one is rejecting data it could have read.
const (
	extMachineInteger    int32 = 3
	extMachineFloat      int32 = 4
	extVariableSets      int32 = 5
	extMRSets            int32 = 7
	extProductInfo       int32 = 10
	extDisplayParams     int32 = 11
	extLongNames         int32 = 13
	extNumberOfCases     int32 = 16
	extFileAttributes    int32 = 17
	extVarAttributes     int32 = 18
	extMRSetsExtended    int32 = 19
	extCharacterEncoding int32 = 20

	// extVeryLongStrings is 7/14: the NAME=WIDTH map of every string wider
	// than 255 bytes, which SPSS stores as several physical variables.
	extVeryLongStrings int32 = 14
	// extLongStringValueLabels is 7/21: value labels for a string variable
	// too wide to carry them in records 3/4.
	extLongStringValueLabels int32 = 21
	// extLongStringMissing is 7/22: missing values for a string variable
	// too wide to carry them in its record type 2.
	extLongStringMissing int32 = 22
)

// extensionRecord is one record type 7 exactly as it appeared on the wire.
//
// Every extension record is kept, interpreted or not. The typed slots on
// dictionary are a projection of these bytes: a subtype this reader gets
// wrong, or has not learned, has still not lost anything, which is what makes
// the skip-with-warning policy safe rather than lossy.
type extensionRecord struct {
	// subtype is the extension subtype tag.
	subtype int32
	// size is the declared element size in bytes.
	size int32
	// count is the declared element count. The payload is size*count bytes.
	count int32
	// offset is the byte offset of the record's type tag.
	offset int
	// payloadOffset is the byte offset of the first payload byte.
	payloadOffset int
	// payload is a copy of the record body, so it outlives any reslicing of
	// the source buffer.
	payload []byte
}

// text renders the payload as a string, for the subtypes whose payload is
// text.
func (x extensionRecord) text() string { return string(x.payload) }

// machineIntegerInfo is the record 7/3 payload: eight int32s describing the
// machine that wrote the file.
type machineIntegerInfo struct {
	// present is false when the file carries no record 7/3.
	present bool

	versionMajor    int32
	versionMinor    int32
	versionRevision int32

	// machineCode identifies the writing machine. No reader uses it.
	machineCode int32

	// floatingPointRep is 1 for IEEE 754, 2 for IBM 370, 3 for DEC VAX E.
	floatingPointRep int32

	// compressionCode is 1 in every file the format describes.
	compressionCode int32

	// endianness is 1 for big-endian, 2 for little-endian. It is a SECOND
	// statement of something the header layout code already established;
	// reconciling the two is E3-S5's job, so nothing here acts on it.
	endianness int32

	// characterCode names the codepage as a number: 2 or 3 for ASCII, 1252
	// for windows-1252, 65001 for UTF-8. Record 7/20 states the same thing
	// as a name and is preferred where both are present.
	characterCode int32
}

// machineFloatInfo is the record 7/4 payload: the three sentinel doubles the
// file declares.
type machineFloatInfo struct {
	// present is false when the file carries no record 7/4, which is the
	// common case.
	present bool

	// sysmis is the system-missing sentinel.
	sysmis float64
	// highest is the "highest" sentinel, normally +DBL_MAX.
	highest float64
	// lowest is the "lowest" sentinel, normally the double just above
	// -DBL_MAX.
	lowest float64
}

// measureLevel is the record 7/11 measurement level of a variable. E2-S6 maps
// it onto Pulse's smart-default table: nominal picks the categorical
// operators, scale the numeric ones.
type measureLevel int32

const (
	// measureUnset is what a writer that declared no level leaves behind.
	measureUnset measureLevel = 0
	// measureNominal is an unordered categorical level.
	measureNominal measureLevel = 1
	// measureOrdinal is an ordered categorical level.
	measureOrdinal measureLevel = 2
	// measureScale is a continuous / interval level.
	measureScale measureLevel = 3
)

func (m measureLevel) String() string {
	switch m {
	case measureUnset:
		return "unset"
	case measureNominal:
		return "nominal"
	case measureOrdinal:
		return "ordinal"
	case measureScale:
		return "scale"
	default:
		return "measureLevel(" + strconv.Itoa(int(m)) + ")"
	}
}

// alignment is the record 7/11 column alignment of a variable.
type alignment int32

const (
	alignLeft   alignment = 0
	alignRight  alignment = 1
	alignCenter alignment = 2
)

func (a alignment) String() string {
	switch a {
	case alignLeft:
		return "left"
	case alignRight:
		return "right"
	case alignCenter:
		return "center"
	default:
		return "alignment(" + strconv.Itoa(int(a)) + ")"
	}
}

// displayParams is one variable's record 7/11 entry.
//
// Only measure is consumed downstream today. width and align are carried
// anyway because they are part of what the file states, and the sidecar that
// makes a round trip possible has to be able to state it back.
type displayParams struct {
	// present is false when the file carries no record 7/11.
	present bool

	measure measureLevel

	// width is the declared display column width. hasWidth is false for the
	// older two-fields-per-variable form of the record, which omits it.
	width    int32
	hasWidth bool

	align alignment
}

// multipleResponseSet is one multiple-response set definition.
//
// The two flavours are two TYPES, not two configurations of one type, and
// that is deliberate. A multiple dichotomy is N indicator variables plus one
// counted value; a multiple category is N variables sharing a value-label
// set and has no counted value at all. Modelling them as one struct with a
// kind flag would put a countedValue field on the category case, where it is
// meaningless, and the first consumer to read it without checking the flag
// would silently mistake a category set for a dichotomy. A type switch
// cannot be forgotten the way a flag test can.
type multipleResponseSet interface {
	// setName is the set name, including its leading '$'.
	setName() string
	// setLabel is the set label, possibly empty.
	setLabel() string
	// setVars names the member variables, by SHORT name, in file order.
	setVars() []string
	// setSubtype is the extension subtype the definition was read from: 5,
	// 7 or 19.
	setSubtype() int32
	// isMultipleResponseSet keeps the interface closed to this package.
	isMultipleResponseSet()
}

// mrDichotomySet is a multiple-dichotomy set: each member variable holding
// countedValue means that option was selected.
//
// This is the flavour that maps onto a Pulse set_* bitmask. It is also the
// lossy one: a member variable can itself be system-missing, and a bitmask
// cannot tell "not selected" from "not asked".
type mrDichotomySet struct {
	name  string
	label string
	vars  []string

	// countedValue is the value that counts as selected, held verbatim as
	// the text the record carried. The wire form does not say whether it is
	// a number or a string — that follows from the member variables' type —
	// so interpreting it here would be a guess.
	countedValue string

	// labelFromVarLabel is set by the subtype 19 'E' form when the label
	// source is the first member variable's variable label ("11") rather
	// than this record ("1").
	labelFromVarLabel bool

	// extended records that the definition used the 'E' type code, which
	// only subtype 19 writes.
	extended bool

	subtype int32
}

func (s *mrDichotomySet) setName() string        { return s.name }
func (s *mrDichotomySet) setLabel() string       { return s.label }
func (s *mrDichotomySet) setVars() []string      { return s.vars }
func (s *mrDichotomySet) setSubtype() int32      { return s.subtype }
func (s *mrDichotomySet) isMultipleResponseSet() {}

// mrCategorySet is a multiple-category set: each member variable holds a code
// from a shared value-label set.
//
// It is positional and permits duplicates, so it is genuinely N categorical
// columns and NOT a set. There is no counted value, which is why this type
// does not have one.
type mrCategorySet struct {
	name  string
	label string
	vars  []string

	subtype int32
}

func (s *mrCategorySet) setName() string        { return s.name }
func (s *mrCategorySet) setLabel() string       { return s.label }
func (s *mrCategorySet) setVars() []string      { return s.vars }
func (s *mrCategorySet) setSubtype() int32      { return s.subtype }
func (s *mrCategorySet) isMultipleResponseSet() {}

// variableSet is a display grouping from record 7/5. It shares a record with
// multiple-response sets and is not one: it has no type code, no counted
// value, and its name does not begin with '$'.
type variableSet struct {
	name string
	vars []string
}

// ---------------------------------------------------------------------------
// Payload cursor
// ---------------------------------------------------------------------------

// extCursor is a bounds-checked cursor over one extension payload. Every read
// reports whether it succeeded; nothing indexes the payload directly.
type extCursor struct {
	b   []byte
	off int
	bo  binary.ByteOrder
}

func (c *extCursor) i32() (int32, bool) {
	if len(c.b)-c.off < 4 {
		return 0, false
	}
	v := int32(c.bo.Uint32(c.b[c.off : c.off+4]))
	c.off += 4
	return v, true
}

func (c *extCursor) i64() (int64, bool) {
	if len(c.b)-c.off < 8 {
		return 0, false
	}
	v := int64(c.bo.Uint64(c.b[c.off : c.off+8]))
	c.off += 8
	return v, true
}

func (c *extCursor) f64() (float64, bool) {
	if len(c.b)-c.off < 8 {
		return 0, false
	}
	v := math.Float64frombits(c.bo.Uint64(c.b[c.off : c.off+8]))
	c.off += 8
	return v, true
}

// done reports whether the cursor has consumed the whole payload.
func (c *extCursor) done() bool { return c.off >= len(c.b) }

// byteAt reads one byte, which records 7/22 uses for its missing-value count.
func (c *extCursor) byteAt() (byte, bool) {
	if len(c.b)-c.off < 1 {
		return 0, false
	}
	v := c.b[c.off]
	c.off++
	return v, true
}

// counted reads an int32 byte length followed by that many bytes, the
// length-prefixed string form records 7/21 and 7/22 are built out of.
//
// A negative length, or one past the end of the payload, fails rather than
// being clamped: a clamp would let a corrupt length silently swallow the
// rest of the record and report a plausible-looking value.
func (c *extCursor) counted() (string, bool) {
	n, ok := c.i32()
	if !ok {
		return "", false
	}
	if n < 0 || int64(n) > int64(len(c.b)-c.off) {
		// Rewind so the caller's diagnostic points at the length field
		// rather than past it.
		c.off -= 4
		return "", false
	}
	s := string(c.b[c.off : c.off+int(n)])
	c.off += int(n)
	return s, true
}

// ---------------------------------------------------------------------------
// Interpretation
// ---------------------------------------------------------------------------

// applyExtensions interprets the record type 7 payloads collected by the
// dictionary walk.
//
// It runs after the walk, not inline, for the same reason resolveValueLabels
// does: records 7/11 and 7/13 are keyed to the variable list, and the format
// does not promise every record type 2 precedes every record type 7. Running
// after the walk makes the interpretation independent of record order.
//
// It returns no error. Every fault an extension payload can carry is a
// warning here — the record's FRAMING was already validated during the walk,
// so a bad payload cannot desynchronise anything, and a dictionary whose
// spine parsed is worth returning even if one decoration did not.
func (p *parser) applyExtensions(d *dictionary) {
	for _, x := range d.extensions {
		switch x.subtype {
		case extMachineInteger:
			p.applyMachineInteger(d, x)
		case extMachineFloat:
			p.applyMachineFloat(d, x)
		case extVariableSets, extMRSets, extMRSetsExtended:
			p.applySets(d, x)
		case extDisplayParams:
			p.applyDisplayParams(d, x)
		case extLongNames:
			p.applyLongNames(d, x)
		case extNumberOfCases:
			p.applyNumberOfCases(d, x)
		case extCharacterEncoding:
			p.applyCharacterEncoding(d, x)
		case extVeryLongStrings:
			p.applyVeryLongStrings(d, x)
		case extLongStringValueLabels:
			p.applyLongStringValueLabels(d, x)
		case extLongStringMissing:
			p.applyLongStringMissingValues(d, x)
		case extProductInfo, extFileAttributes, extVarAttributes:
			// Captured verbatim in d.extensions and deliberately not
			// interpreted: attribute and product-info text is free-form
			// key/value prose with no Pulse home. It is not "unknown", so
			// it does not warn — warning on a record every real SPSS file
			// carries would train a caller to ignore the warning channel.
		default:
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_UNKNOWN, x, x.offset,
				"this reader does not interpret extension subtype %d; its %d byte(s) were skipped and retained verbatim, and the rest of the dictionary parsed normally",
				x.subtype, len(x.payload))
		}
	}
}

// warnExtension records one non-fatal extension diagnostic.
func (p *parser) warnExtension(d *dictionary, code errors.Code, x extensionRecord, off int, format string, args ...any) {
	msg := "spss: record type 7 extension subtype " + strconv.Itoa(int(x.subtype)) +
		" starting at byte offset " + strconv.Itoa(x.offset) + ": " +
		fmt.Sprintf(format, args...) +
		" [at byte offset " + strconv.Itoa(off) + "]"
	d.warnings = append(d.warnings, errors.NewCodedErrorWithDetails(code, msg, map[string]any{
		errors.DetailSPSSRecord:  recordName(recTypeExtension),
		errors.DetailSPSSSubtype: x.subtype,
		errors.DetailSPSSOffset:  off,
	}))
}

// checkShape validates an extension record's declared element size and count
// against what its subtype defines, warning and reporting false on a
// mismatch. A wantCount of -1 accepts any count, which is the right rule for
// the variable-length text payloads.
func (p *parser) checkShape(d *dictionary, x extensionRecord, wantSize int32, wantCount int32) bool {
	if x.size != wantSize {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.offset,
			"the record declares an element size of %d, but subtype %d is defined with %d-byte elements; the payload was left uninterpreted",
			x.size, x.subtype, wantSize)
		return false
	}
	if wantCount >= 0 && x.count != wantCount {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.offset,
			"the record declares %d element(s), but subtype %d is defined with exactly %d; the payload was left uninterpreted",
			x.count, x.subtype, wantCount)
		return false
	}
	return true
}

// applyMachineInteger decodes record 7/3: eight int32s.
func (p *parser) applyMachineInteger(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 4, 8) {
		return
	}
	c := &extCursor{b: x.payload, bo: p.bo}
	fields := []*int32{}
	mi := machineIntegerInfo{present: true}
	fields = append(fields, &mi.versionMajor, &mi.versionMinor, &mi.versionRevision,
		&mi.machineCode, &mi.floatingPointRep, &mi.compressionCode, &mi.endianness, &mi.characterCode)
	for _, f := range fields {
		v, ok := c.i32()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"the payload ran out after %d of its 8 int32 field(s)", c.off/4)
			return
		}
		*f = v
	}
	d.machineInteger = mi
}

// applyMachineFloat decodes record 7/4: the sysmis, highest and lowest
// sentinels the file declares.
//
// Record 7/4 is an OVERRIDE of the spec default, never a precondition for
// having one. A dictionary with no 7/4 keeps -DBL_MAX, because that is what
// every writer emits whether or not it says so, and a reader that required
// the record would read -DBL_MAX as a finite datum of about -1.8e308.
//
// The override is taken only from a COHERENT triple. The record describes
// three ordered sentinels — the system-missing value, then the lowest and
// highest values a real datum may take — so a conforming payload satisfies
// sysmis < lowest < highest. That is a check on the record's own internal
// consistency, not a hardcoded expectation of -DBL_MAX, and it is what keeps
// an all-zero or garbage payload from declaring 0 to be the missing value
// and turning every zero in the file into a null. An incoherent triple is
// warned about, retained in full on machineFloat, and not applied.
func (p *parser) applyMachineFloat(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 8, 3) {
		return
	}
	c := &extCursor{b: x.payload, bo: p.bo}
	mf := machineFloatInfo{present: true}
	for i, f := range []*float64{&mf.sysmis, &mf.highest, &mf.lowest} {
		v, ok := c.f64()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"the payload ran out after %d of its 3 double(s)", i)
			return
		}
		*f = v
	}
	d.machineFloat = mf

	if !(mf.sysmis < mf.lowest && mf.lowest < mf.highest) {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
			"the record declares sysmis=%v, lowest=%v, highest=%v, which is not the ordered sysmis < lowest < highest triple the format defines; the system-missing sentinel was left at the spec default %v rather than adopting a value that could turn ordinary data into nulls",
			mf.sysmis, mf.lowest, mf.highest, defaultSysmis)
		return
	}
	if mf.sysmis != defaultSysmis {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
			"the file declares %v as its system-missing sentinel instead of the conventional -DBL_MAX (%v); the declared value is being used, so a datum equal to it reads as missing",
			mf.sysmis, defaultSysmis)
	}
	d.sysmis = mf.sysmis
}

// applyNumberOfCases decodes record 7/16: a constant 1 followed by the 64-bit
// case count.
func (p *parser) applyNumberOfCases(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 8, 2) {
		return
	}
	c := &extCursor{b: x.payload, bo: p.bo}
	one, ok := c.i64()
	if !ok {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
			"the payload is too short to hold its two int64 fields")
		return
	}
	n, ok := c.i64()
	if !ok {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+8,
			"the payload holds the leading constant but not the case count")
		return
	}
	if one != 1 {
		// The leading field is a fixed 1 whose only purpose is to catch a
		// byte order the header got wrong. A value other than 1 says this
		// payload is not what it claims, so the count beside it is not
		// trustworthy either.
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
			"the record's leading field is %d, not the constant 1 the format defines; the 64-bit case count beside it was not applied", one)
		return
	}
	if n < 0 {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+8,
			"the record declares %d cases; a case count cannot be negative", n)
		return
	}
	d.caseCount64 = n
	d.hasCaseCount64 = true
}

// applyCharacterEncoding decodes record 7/20: the charset NAME.
//
// The name is recorded and nothing is decoded with it. Transcoding is E3-S3's
// job, and doing any of it here would apply a codepage to bytes the rest of
// this package has already handed out untouched.
func (p *parser) applyCharacterEncoding(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 1, -1) {
		return
	}
	name := strings.TrimRight(x.text(), " \x00")
	if name == "" {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
			"the record declares an empty character encoding name")
		return
	}
	d.charsetName = name
}

// applyDisplayParams decodes record 7/11: measure, display width and
// alignment for every variable, in variable order.
//
// The record comes in two shapes — three int32s per variable, or two when the
// writer omits the display width. Which one is in play is decided by the
// element count against the variable count; there is no flag.
func (p *parser) applyDisplayParams(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 4, -1) {
		return
	}
	n := len(d.vars)
	if n == 0 {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.offset,
			"the dictionary declares no variables for the record's %d element(s) to describe", x.count)
		return
	}
	var perVar int
	switch {
	case int(x.count) == 3*n:
		perVar = 3
	case int(x.count) == 2*n:
		perVar = 2
	default:
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.offset,
			"the record declares %d element(s), which is neither 2 nor 3 per each of the dictionary's %d variable(s); the payload was left uninterpreted",
			x.count, n)
		return
	}

	c := &extCursor{b: x.payload, bo: p.bo}
	parsed := make([]displayParams, n)
	for i := 0; i < n; i++ {
		dp := displayParams{present: true}
		measure, ok := c.i32()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"the payload ran out at variable %d of %d", i+1, n)
			return
		}
		if perVar == 3 {
			w, ok := c.i32()
			if !ok {
				p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
					"the payload ran out at the display width of variable %d of %d", i+1, n)
				return
			}
			dp.width, dp.hasWidth = w, true
		}
		align, ok := c.i32()
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"the payload ran out at the alignment of variable %d of %d", i+1, n)
			return
		}
		// An out-of-range enum is warned about and dropped to its unset
		// value rather than carried: a downstream smart-default rule
		// switching on measureLevel(9) would fall through to whatever its
		// default arm is, which is exactly the silent degradation the
		// warning exists to prevent.
		if measure < int32(measureUnset) || measure > int32(measureScale) {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"variable %q declares measurement level %d, outside the 0..3 the format defines; it was recorded as unset",
				d.vars[i].fieldName(), measure)
			measure = int32(measureUnset)
		}
		if align < int32(alignLeft) || align > int32(alignCenter) {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+c.off,
				"variable %q declares alignment %d, outside the 0..2 the format defines; it was recorded as left",
				d.vars[i].fieldName(), align)
			align = int32(alignLeft)
		}
		dp.measure = measureLevel(measure)
		dp.align = alignment(align)
		parsed[i] = dp
	}

	for i := range d.vars {
		d.vars[i].display = parsed[i]
	}
	d.hasDisplayParams = true
}

// applyLongNames decodes record 7/13: tab-separated SHORT=Long pairs.
//
// A recovered long name SUPERSEDES the 8-byte short name everywhere a name
// reaches a caller, which is what variable.fieldName implements. The short
// name is kept beside it because records 7/5, 7/7, 7/19 and 7/14 all
// cross-reference variables by short name.
func (p *parser) applyLongNames(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 1, -1) {
		return
	}
	byShort := make(map[string]int, len(d.vars))
	for i, v := range d.vars {
		byShort[strings.ToUpper(v.name)] = i
	}
	assigned := make(map[string]string, len(d.vars))

	for _, pair := range strings.Split(x.text(), "\t") {
		pair = strings.Trim(pair, "\x00")
		if strings.TrimSpace(pair) == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
				"the entry %q carries no '='; the record is a tab-separated list of SHORT=Long pairs", pair)
			continue
		}
		short := strings.TrimSpace(pair[:eq])
		long := pair[eq+1:]
		idx, ok := byShort[strings.ToUpper(short)]
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
				"the entry %q names the short variable name %q, which no record type 2 in this dictionary declares; the long name was not applied",
				pair, short)
			continue
		}
		if long == "" {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
				"the entry for short name %q declares an empty long name; the short name was kept", short)
			continue
		}
		if prev, dup := assigned[strings.ToUpper(long)]; dup {
			// Two variables answering to one name is not survivable
			// downstream — a Pulse schema cannot hold two fields with the
			// same name — so the collision is surfaced here rather than as
			// a confusing schema error later.
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
				"the long name %q is claimed by both short name %q and short name %q; the second was not applied",
				long, prev, short)
			continue
		}
		assigned[strings.ToUpper(long)] = short
		d.vars[idx].longName = long
	}
}

// applySets decodes the three text records that carry set definitions: 7/5,
// 7/7 and 7/19.
//
// Subtype 5 is the odd one. It is the OLDER record and it carries plain
// variable sets — display groupings of the form "name= var1 var2" — as well
// as, in some writers, multiple-response definitions. The two are told apart
// by the only thing that distinguishes them on the wire: a response set's
// name begins with '$' and is followed by a 'C', 'D' or 'E' type code. A
// definition that does not match that shape is recorded as a variable set,
// not warned about, and above all not mistaken for a response set.
//
// Definitions merge by name across the three subtypes with the LATER subtype
// winning, because 19 restates the sets of 7 with the extra label-source
// field rather than adding new ones. Since d.extensions is in file order and
// SPSS writes 5 before 7 before 19, plain sequential replacement gives that.
func (p *parser) applySets(d *dictionary, x extensionRecord) {
	if !p.checkShape(d, x, 1, -1) {
		return
	}
	t := &setText{b: x.payload}
	for {
		t.skipNewlines()
		if t.eof() {
			return
		}
		start := t.off
		name, ok := t.token('=')
		if !ok {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
				"a set definition has no '=' before the end of the payload; the remainder was skipped")
			return
		}
		isResponseSet := strings.HasPrefix(name, "$") && t.peekIsAny("CDE")
		if !isResponseSet {
			vars := t.restOfLine()
			if x.subtype != extVariableSets {
				p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
					"the definition %q is not a multiple-response set (a response set's name begins with '$' and is followed by a C, D or E type code), and subtype %d carries only response sets; it was skipped",
					name, x.subtype)
				continue
			}
			d.variableSets = append(d.variableSets, variableSet{name: name, vars: vars})
			continue
		}

		set, err := p.parseMRSet(t, name, x.subtype)
		if err != "" {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset+start,
				"the multiple-response set %q could not be read: %s; it was skipped and the remaining definitions were still read", name, err)
			t.restOfLine()
			continue
		}
		p.checkMRSetVars(d, x, set)
		d.putMRSet(set)
	}
}

// parseMRSet reads one multiple-response set definition, positioned just
// after its '='. It returns a non-empty string describing the fault instead
// of an error value, because every fault here is a warning by construction.
//
// The grammar:
//
//	name '=' ( 'C' ' ' | 'D' counted | 'E' ' ' ('1'|'11') ' ' counted )
//	         counted-label ( ' ' varname )* '\n'
//
// where a counted string is a decimal byte length, a space, then that many
// bytes.
func (p *parser) parseMRSet(t *setText, name string, subtype int32) (multipleResponseSet, string) {
	kind, ok := t.byteAt()
	if !ok {
		return nil, "the payload ends immediately after the '='"
	}
	t.off++

	switch kind {
	case 'C':
		if !t.match(' ') {
			return nil, "a 'C' multiple-category type code is not followed by a space"
		}
		label, ok := t.counted()
		if !ok {
			return nil, "the set label is not a well-formed counted string"
		}
		return &mrCategorySet{
			name: name, label: label, vars: t.restOfLine(), subtype: subtype,
		}, ""

	case 'D', 'E':
		set := &mrDichotomySet{name: name, subtype: subtype, extended: kind == 'E'}
		if kind == 'E' {
			if !t.match(' ') {
				return nil, "an 'E' extended type code is not followed by a space"
			}
			src, ok := t.token(' ')
			if !ok {
				return nil, "the 'E' label-source field is not terminated by a space"
			}
			switch src {
			case "11":
				set.labelFromVarLabel = true
			case "1":
			default:
				return nil, "the 'E' label-source field is " + strconv.Quote(src) + ", not the 1 or 11 the format defines"
			}
		}
		counted, ok := t.counted()
		if !ok {
			return nil, "the counted value is not a well-formed counted string"
		}
		set.countedValue = counted
		label, ok := t.counted()
		if !ok {
			return nil, "the set label is not a well-formed counted string"
		}
		set.label = label
		set.vars = t.restOfLine()
		return set, ""

	default:
		return nil, "the type code is " + strconv.Quote(string(kind)) + ", not the C, D or E the format defines"
	}
}

// checkMRSetVars warns about a set naming a variable the dictionary does not
// have. Member names are SHORT names, matched case-insensitively because
// short names are stored upper-cased.
func (p *parser) checkMRSetVars(d *dictionary, x extensionRecord, set multipleResponseSet) {
	if len(set.setVars()) == 0 {
		p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
			"the multiple-response set %q names no member variables", set.setName())
		return
	}
	for _, name := range set.setVars() {
		found := false
		for _, v := range d.vars {
			if strings.EqualFold(v.name, name) {
				found = true
				break
			}
		}
		if !found {
			p.warnExtension(d, errors.PULSE_SPSS_EXTENSION_INVALID, x, x.payloadOffset,
				"the multiple-response set %q names the member variable %q, which no record type 2 in this dictionary declares",
				set.setName(), name)
		}
	}
}

// putMRSet installs a set, replacing any earlier definition of the same name.
// Later definitions win because subtype 19 restates subtype 7's sets with an
// extra field rather than declaring different ones.
func (d *dictionary) putMRSet(set multipleResponseSet) {
	for i, existing := range d.mrSets {
		if existing.setName() == set.setName() {
			d.mrSets[i] = set
			return
		}
	}
	d.mrSets = append(d.mrSets, set)
}

// ---------------------------------------------------------------------------
// Set-definition text cursor
// ---------------------------------------------------------------------------

// setText is a cursor over the text payload of record 7/5, 7/7 or 7/19.
type setText struct {
	b   []byte
	off int
}

func (t *setText) eof() bool { return t.off >= len(t.b) }

func (t *setText) byteAt() (byte, bool) {
	if t.eof() {
		return 0, false
	}
	return t.b[t.off], true
}

// peekIsAny reports whether the next byte is one of the given set.
func (t *setText) peekIsAny(set string) bool {
	c, ok := t.byteAt()
	return ok && strings.IndexByte(set, c) >= 0
}

// match consumes the next byte if it equals c.
func (t *setText) match(c byte) bool {
	if got, ok := t.byteAt(); ok && got == c {
		t.off++
		return true
	}
	return false
}

// skipNewlines steps over the line separators between definitions. Both \n
// and \r are treated as separators so a file that has been through a
// text-mode transfer still reads.
func (t *setText) skipNewlines() {
	for !t.eof() && (t.b[t.off] == '\n' || t.b[t.off] == '\r') {
		t.off++
	}
}

// token reads up to the next occurrence of sep, consuming the separator. It
// fails at end of payload or at a newline, either of which means the
// definition was cut short.
func (t *setText) token(sep byte) (string, bool) {
	start := t.off
	for !t.eof() {
		c := t.b[t.off]
		if c == sep {
			s := string(t.b[start:t.off])
			t.off++
			return s, true
		}
		if c == '\n' {
			return "", false
		}
		t.off++
	}
	return "", false
}

// counted reads a counted string: decimal digits, a space, then that many
// bytes. Leading spaces are tolerated because the writer puts one between a
// dichotomy's counted value and the label that follows it.
func (t *setText) counted() (string, bool) {
	for t.match(' ') {
	}
	start := t.off
	for !t.eof() && t.b[t.off] >= '0' && t.b[t.off] <= '9' {
		t.off++
	}
	if t.off == start {
		return "", false
	}
	n, err := strconv.Atoi(string(t.b[start:t.off]))
	if err != nil || n < 0 {
		t.off = start
		return "", false
	}
	if !t.match(' ') {
		t.off = start
		return "", false
	}
	if len(t.b)-t.off < n {
		t.off = start
		return "", false
	}
	s := string(t.b[t.off : t.off+n])
	t.off += n
	return s, true
}

// restOfLine reads the space-separated tokens up to the next newline,
// consuming the newline. Empty tokens are dropped, so a run of spaces or a
// trailing space does not produce a phantom variable name.
func (t *setText) restOfLine() []string {
	start := t.off
	for !t.eof() && t.b[t.off] != '\n' {
		t.off++
	}
	out := strings.Fields(string(t.b[start:t.off]))
	if !t.eof() {
		t.off++ // the newline
	}
	return out
}
