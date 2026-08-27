package spss

import (
	"encoding/binary"
	"math"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// Fixed sizes taken from the GNU PSPP System File Format specification.
const (
	// headerSize is the length of the file header record: 4 + 60 + 4 + 4 +
	// 4 + 4 + 4 + 8 + 9 + 8 + 64 + 3.
	headerSize = 176

	// elementSize is the width of one data element — 8 bytes, for both a
	// numeric double and one segment of a string.
	elementSize = 8

	// shortNameLen is the width of the record type 2 name field.
	shortNameLen = 8

	// maxShortStringWidth is the widest string variable whose value labels
	// can ride records 3/4. Anything wider needs record 7/21 long string
	// value labels.
	maxShortStringWidth = 8

	// documentLineLen is the fixed width of one record type 6 line.
	documentLineLen = 80
)

// Record type tags.
const (
	recTypeVariable   int32 = 2
	recTypeValueLabel int32 = 3
	recTypeLabelVars  int32 = 4
	recTypeDocument   int32 = 6
	recTypeExtension  int32 = 7
	recTypeTerminator int32 = 999
)

// typeStringContinuation is the record type 2 `type` field value marking a
// continuation record for a string wider than 8 bytes.
const typeStringContinuation int32 = -1

// Magic strings. `$FL3` marks a ZSAV (zlib-compressed) file; the dictionary
// itself is laid out identically, so both parse here.
const (
	magicSAV  = "$FL2"
	magicZSAV = "$FL3"
)

// Header compression field values.
const (
	compressionNone     int32 = 0
	compressionBytecode int32 = 1
	compressionZSAV     int32 = 2
)

// defaultSysmis is the system-missing sentinel every SPSS writer uses in an
// uncompressed data section: the IEEE 754 double -DBL_MAX
// (0xFFEFFFFFFFFFFFFF).
//
// A file MAY declare its own sentinel in the record 7/4 machine float info
// extension, but that record is optional and plenty of real dictionaries —
// including every fixture internal/spsstest emits — omit it entirely while
// still writing -DBL_MAX. So the default is the spec value and 7/4 is an
// override, never a precondition: a reader that requires 7/4 to recognise
// system-missing silently reads -DBL_MAX as a finite datum of about -1.8e308.
var defaultSysmis = -math.MaxFloat64

// fileHeader is the decoded 176-byte file header record.
//
// The fixed-width ASCII fields are space-padded on the wire; the padding is
// stripped on the way in because it is padding, not content.
type fileHeader struct {
	// magic is "$FL2" (a plain system file) or "$FL3" (ZSAV).
	magic string

	// productName is the 60-byte prod_name field. Writers put a
	// "@(#) SPSS DATA FILE" prefix in front of their own identification.
	productName string

	// layoutCode is the 2-or-3 probe written in the file's own byte order.
	// It is what byteOrder was derived from; it is kept so E3-S5 can
	// cross-check it against the record 7/3 machine integer info.
	layoutCode int32

	// nominalCaseSize is the header's claim about how many 8-byte elements
	// each case occupies. It is a claim, not a fact — see
	// dictionary.elementCount.
	nominalCaseSize int32

	// compression is 0 (uncompressed), 1 (bytecode) or 2 (ZSAV).
	compression int32

	// weightIndex is the 1-based dictionary element index of the weighting
	// variable, or 0 when the file is unweighted.
	weightIndex int32

	// caseCount is the number of cases, or -1 when the writer did not know.
	// A file with more than 2^31-1 cases carries the real count in the
	// record 7/16 extension instead.
	caseCount int32

	// bias is the compression bias, normally 100. It is written even for an
	// uncompressed file.
	bias float64

	// creationDate is the 9-byte "dd mmm yy" field.
	creationDate string

	// creationTime is the 8-byte "hh:mm:ss" field.
	creationTime string

	// fileLabel is the 64-byte free-text file label, empty when unset.
	fileLabel string
}

// format is a decoded print or write format specification. On the wire it is
// packed into an int32 as 0x00TTWWDD: an unused zero byte, the type code, the
// field width, then the decimal count.
type format struct {
	// code is the SPSS format type code: 1 = A (string), 5 = F (plain
	// numeric), 20 = DATE, 23 = ADATE, 38 = EDATE, 39 = SDATE, and so on.
	// It is carried verbatim; interpreting it is E2-S6's job.
	code uint8
	// width is the field width in characters.
	width int
	// decimals is the number of decimal places.
	decimals int
}

// unpackFormat decodes the 0x00TTWWDD wire encoding.
func unpackFormat(v int32) format {
	u := uint32(v)
	return format{
		code:     uint8(u >> 16),
		width:    int(u>>8) & 0xFF,
		decimals: int(u) & 0xFF,
	}
}

// missingSpec is a variable's record type 2 missing-value specification.
//
// The `code` field is the raw n_missing_values value and is the only thing
// that says which shape the slots are in:
//
//	 0  no missing values
//	 1  one discrete missing value
//	 2  two discrete missing values
//	 3  three discrete missing values
//	-2  a lo..hi range
//	-3  a lo..hi range plus one discrete value
//
// Negative codes are numeric-only; the format has no range form for strings.
type missingSpec struct {
	code int32

	// raw holds the abs(code) eight-byte slots exactly as they appeared,
	// so nothing is lost to a premature interpretation.
	raw [][elementSize]byte

	// numeric holds the slots decoded as doubles. Populated for numeric
	// variables only.
	numeric []float64

	// text holds the slots decoded as strings, trimmed to the variable's
	// declared width. Populated for string variables only.
	text []string
}

// count returns the number of eight-byte slots the spec occupies.
func (m missingSpec) count() int { return len(m.raw) }

// isRange reports whether the spec opens with a lo..hi range, which is the
// case for codes -2 and -3.
func (m missingSpec) isRange() bool { return m.code < 0 }

// discreteCount returns the number of discrete missing values, which is the
// slot count less the two slots a range consumes.
func (m missingSpec) discreteCount() int {
	if m.isRange() {
		return len(m.raw) - 2
	}
	return len(m.raw)
}

// variable is one SPSS variable: its record type 2 plus the continuation
// records that carry the rest of a string wider than 8 bytes.
type variable struct {
	// name is the 8-byte short name with its space padding stripped. It is
	// the record type 2 field and nothing else; the real, case-preserving
	// name lives in longName. Use fieldName, not this, when a name is
	// wanted for display or for a Pulse field.
	name string

	// longName is the variable's real name as declared by the record 7/13
	// long variable names extension, or "" when the file declares none.
	// Where it is present it supersedes name — the short name is a
	// truncated, upper-cased derivation SPSS keeps only for backward
	// compatibility.
	longName string

	// display is the record 7/11 variable display parameters entry:
	// measurement level, display width and alignment. Absent when the file
	// carries no 7/11 record.
	display displayParams

	// index is the 1-based dictionary element index of the variable's first
	// element. Records 4, the header weight_index and every other
	// index-bearing field count elements, so a variable following a wide
	// string does not have its ordinal position as its index.
	index int32

	// typeCode is the raw record type 2 `type` field: 0 for numeric, or the
	// declared byte width 1..255 for a string.
	typeCode int32

	// width is 0 for a numeric variable, else the declared byte width.
	width int

	// segments is the number of 8-byte elements the variable occupies:
	// 1 for numeric, ceil(width/8) for a string.
	segments int

	// print and write are the output formats.
	print format
	write format

	// hasLabel records whether the record carried a variable label at all,
	// which is distinct from carrying an empty one.
	hasLabel bool

	// label is the variable label, empty when hasLabel is false.
	label string

	// missing is the missing-value specification.
	missing missingSpec

	// offset is the byte offset of the variable's record type 2, kept for
	// diagnostics.
	offset int
}

// isString reports whether the variable is a string variable.
func (v variable) isString() bool { return v.width > 0 }

// fieldName returns the name to use for the variable: the record 7/13 long
// name when the file declares one, and the 8-byte short name otherwise.
//
// This is the only name-selection rule in the package. Reading .name
// directly anywhere a name reaches a caller would silently prefer "QN1A" to
// "SatisfactionWithService", which is the whole reason record 7/13 exists.
func (v variable) fieldName() string {
	if v.longName != "" {
		return v.longName
	}
	return v.name
}

// valueLabel is one (value, label) pair inside a record type 3.
type valueLabel struct {
	// raw is the 8-byte value slot verbatim. For a numeric variable it is a
	// double; for a short string it is the text, space-padded to the full
	// eight bytes.
	raw [elementSize]byte

	// label is the label text with its padding removed.
	label string
}

// numeric decodes the value slot as a double, for a set bound to numeric
// variables.
func (l valueLabel) numeric(bo binary.ByteOrder) float64 {
	return math.Float64frombits(bo.Uint64(l.raw[:]))
}

// text decodes the value slot as the key of a string variable of the given
// declared width.
//
// The eight-byte slot is padded to its FULL width regardless of what the
// variable declares: a width-4 variable labelling "AB" stores
// "AB      " — two characters and six spaces — not "AB  ". Trimming to the
// declared width first and only then stripping trailing spaces is what makes
// a label key compare equal to the same value read out of the data section,
// which is trimmed to the declared width by construction. Skip the width trim
// and every short-string label lookup misses.
func (l valueLabel) text(width int) string {
	n := width
	if n <= 0 || n > elementSize {
		n = elementSize
	}
	return strings.TrimRight(string(l.raw[:n]), " ")
}

// valueLabelSet is one record type 3 (the labels) together with the record
// type 4 that binds it to variables. It is modelled as a set rather than as a
// per-variable field because that is what the format is: several variables
// can share one record type 3.
type valueLabelSet struct {
	// labels are the (value, label) pairs in file order.
	labels []valueLabel

	// varIndices are the 1-based dictionary element indices named by the
	// record type 4.
	varIndices []int32

	// width is the common declared width of every variable in the set: 0
	// for numeric, else the string byte width. It selects which of
	// valueLabel.numeric and valueLabel.text decodes the value slot.
	width int

	// offset is the byte offset of the record type 3, kept for diagnostics.
	offset int

	// varsOffset is the byte offset of the record type 4 that binds the set
	// to its variables, kept for diagnostics.
	varsOffset int
}

// dictionary is the parsed dictionary section of a `.sav` file.
//
// It is deliberately a faithful transcription of what the file says, not a
// Pulse schema: nothing here has been converted, widened or renamed. E2-S3
// attaches the record 7/* extension data to it, E2-S4 reads the data section
// with it, and E2-S6 maps it onto an encoding.Schema.
type dictionary struct {
	// byteOrder is the file's byte order, derived from the header layout
	// code. Every multi-byte field in the dictionary AND in the data
	// section is in this order.
	byteOrder binary.ByteOrder

	// header is the decoded file header record.
	header fileHeader

	// vars are the real variables in file order — continuation records are
	// folded into the variable they continue and never appear here.
	vars []variable

	// elementCount is the number of 8-byte elements per case counted from
	// the record type 2 stream, continuation records included.
	//
	// This, not header.nominalCaseSize, is the authoritative case stride.
	// The header field is a writer's claim; PSPP itself treats a
	// disagreement as a warning and trusts the records. Both are kept so a
	// caller can see the disagreement.
	elementCount int32

	// valueLabels are the record 3/4 pairs in file order.
	valueLabels []valueLabelSet

	// sysmis is the system-missing sentinel. It is seeded with the spec
	// default (-DBL_MAX) by the dictionary parse, because record 7/4 is
	// optional; E2-S3 overwrites it when the file does declare one.
	sysmis float64

	// dataOffset is the byte offset of the first byte after the record type
	// 999 terminator — that is, the first byte of the data section. It is
	// the handoff point for E2-S4.
	dataOffset int

	// extensions are every record type 7 in the file, in file order, with
	// their payloads copied verbatim. Interpreted subtypes ALSO appear
	// here: the typed slots below are a projection of these bytes, never a
	// replacement for them, so a subtype this reader interprets partially
	// (or wrongly) has not lost anything.
	extensions []extensionRecord

	// documents are the record type 6 lines in file order, each still the
	// full fixed-width 80-byte field. They are held verbatim and
	// uninterpreted: document text is free-form user prose with no Pulse
	// home, and trimming it here would be a guess about which trailing
	// spaces were padding.
	documents []string

	// machineInteger is the record 7/3 payload. Its present field is false
	// when the file carries no 7/3.
	machineInteger machineIntegerInfo

	// machineFloat is the record 7/4 payload, which declares the file's own
	// sysmis/highest/lowest sentinels. Its present field is false when the
	// file carries no 7/4 — the common case, which is why sysmis above is
	// seeded from the spec default rather than from this.
	machineFloat machineFloatInfo

	// caseCount64 is the record 7/16 64-bit case count, valid only when
	// hasCaseCount64 is set. It supersedes header.caseCount, which is an
	// int32 and therefore cannot express a file of more than 2^31-1 cases.
	caseCount64    int64
	hasCaseCount64 bool

	// charsetName is the record 7/20 character encoding name, or "" when
	// the file declares none. It is the NAME only; decoding with it is
	// E3-S3's job.
	charsetName string

	// mrSets are the multiple-response set definitions from records 7/5,
	// 7/7 and 7/19, merged by name with the later subtype winning. Each
	// element is either an *mrDichotomySet or an *mrCategorySet — a type
	// switch, not a flag test, is what tells them apart.
	mrSets []multipleResponseSet

	// variableSets are the display groupings that also ride record 7/5.
	// They are NOT response sets and must never be treated as one.
	variableSets []variableSet

	// hasDisplayParams records whether a record 7/11 was applied, so an
	// absent record is distinguishable from one declaring every variable
	// unset.
	hasDisplayParams bool

	// warnings are the non-fatal diagnostics raised while parsing: an
	// extension subtype this reader does not interpret, or one whose
	// payload did not match its declared shape. A warning never stops a
	// parse; the bytes behind every one of them are still in extensions.
	warnings []*errors.CodedError
}

// rawExtension returns the first record type 7 with the given subtype, and
// whether the file carried one.
func (d *dictionary) rawExtension(subtype int32) (extensionRecord, bool) {
	for _, x := range d.extensions {
		if x.subtype == subtype {
			return x, true
		}
	}
	return extensionRecord{}, false
}

// variableByIndex returns the variable owning the given 1-based dictionary
// element index, and whether that index is the variable's FIRST element.
// An index landing on a continuation element resolves to the owning variable
// with ok false.
func (d *dictionary) variableByIndex(idx int32) (variable, bool, bool) {
	for _, v := range d.vars {
		if idx >= v.index && idx < v.index+int32(v.segments) {
			return v, idx == v.index, true
		}
	}
	return variable{}, false, false
}
