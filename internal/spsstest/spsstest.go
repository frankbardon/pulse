// Package spsstest builds spec-conformant SPSS `.sav` system files from a
// declarative spec, for use as test fixtures.
//
// # Why this package exists
//
// Pulse has no real-world `.sav` corpus to test against — the obvious
// candidates are GPL-licensed and cannot be vendored. Everything here is
// constructed from the GNU PSPP System File Format specification, which makes
// this package the *ground truth* for the `.sav` reader: if the emitter
// encodes a misreading of the spec, the reader will be built to match that
// misreading and every test will pass against a file no real tool can read.
//
// Two consequences:
//
//  1. The emitter is strict. It refuses to emit anything it cannot justify
//     from the spec rather than guessing — an out-of-range width, a lowercase
//     short name, a value label on a long string. Silent coercion here would
//     become a silent reader bug later.
//  2. One complete generated file is verified byte-by-byte against the spec by
//     hand. See the offset walkthrough on TestReferenceFixture_HandVerified in
//     spsstest_test.go, and [ReferenceSpec], which produces it.
//
// The hand-verification was additionally corroborated against two independent
// implementations whose C readers share no code with anything here — R's
// `foreign::read.spss` and R's `haven` (ReadStat). They recovered the
// reference fixture's variable names, variable label, value labels, string
// widths and system-missing datum exactly as declared, as well as a larger
// fixture exercising a shared value-label set, a string value label, a weight
// variable and a 20-byte string.
//
// [ExtensionReferenceSpec] was cross-checked the same way. Both readers
// recovered its record 7/13 long names (RespondentId, FullName); `haven`
// additionally recovered its record 7/11 display widths (10, 4, 12) in the
// right positions, which is what pins the measure/width/alignment field order
// inside each triple; and `foreign` flagged exactly one record as
// unrecognised — the deliberately-unknown subtype 4242 — which means it
// accepted the framing and subtype tags of every other extension record.
// ReadStat parses subtype 7 strictly and rejected a fixture with a bad type
// code there, so the multiple-response grammar is corroborated too.
//
// The temporal format codes ([FormatDATE] and its siblings) were corroborated
// the same way during E2-S6. Handed a fixture carrying one variable per code,
// `haven` reported DATE / ADATE / EDATE / SDATE / JDATE as R `Date` values at
// day resolution, DATETIME as a `POSIXct` instant with its time of day, and
// TIME / DTIME as `hms`/`difftime` DURATIONS of 3661 and 90061 seconds — which
// is what pins both the code values and the instant-versus-duration split the
// reader's type mapping turns on.
//
// The bytecode-compressed data section was corroborated the same way during
// E3-S1, and it is the construct that most needed it: a compressed fixture is
// where a shared misreading of the spec would be invisible, because the
// reader would be checked only against the encoder that mirrors its own
// mistake. Handed a compressed fixture and its uncompressed twin built from
// one spec, `foreign` and `haven` each recovered IDENTICAL values from both —
// across one-byte integer commands at both ends of the compressible range
// (-99 and 151), verbatim escapes (1e9, 1e300, 0.1 to full precision),
// system-missing, all-spaces string segments, a three-segment 20-byte string,
// and the block padding. Both also honoured a fixture declaring a
// non-conventional compression bias (37 and 50 were tried), recovering the
// same values as the bias-100 twin; `foreign` warns that the bias "is not the
// usual value of 100", which is itself proof it read the header field rather
// than assuming it. Nothing in the bytecode encoding rests on a guess.
//
// ZSAV was cross-checked the same way during E3-S2, and the check was worth
// running: R's `foreign` does NOT implement ZSAV at all and rejects every
// `.zsav` on the "$FL3" magic alone ("not in any supported SPSS format"),
// which is itself confirmation that the magic is the discriminator. `haven`
// (ReadStat) does implement it, and read every ZSAV fixture this package
// emits — the reference fixture, the every-command fixture, a multi-block
// fixture cut at a 16-byte block size, and a fixture declaring a
// non-conventional compression bias — recovering values, value labels and
// variable labels IDENTICAL to each fixture's uncompressed twin (`all.equal`
// on the two data frames). ReadStat also REJECTED both a fixture whose
// trailer block count was rewritten and one with a single flipped byte inside
// a compressed block, which proves the block index and the block payloads are
// genuinely load-bearing rather than being read past.
//
// Three constructs got NO independent corroboration, because neither reader
// interprets them: the subtype 19 'E' extended form, multiple-response
// definitions carried on subtype 5, and the two-int32-per-variable form of
// record 7/11. Those follow the PSPP specification alone.
//
// None of this is automated (it needs R and is not a build dependency); it is
// recorded here so a future change can repeat it:
//
//	write Build(ReferenceSpec()) to reference.sav, then
//	Rscript -e 'print(foreign::read.spss("reference.sav", to.data.frame=FALSE))'
//	Rscript -e 'print(haven::read_sav("reference.sav"))'
//
// For a ZSAV fixture use `haven` only — `foreign` cannot open one — and
// compare against the uncompressed twin rather than reading values by eye:
//
//	Rscript -e 'print(all.equal(haven::read_sav("ref.sav"), haven::read_sav("ref.zsav")))'
//
// For a compressed fixture, build the SAME spec twice — once with
// Compression left at CompressionNone and once with CompressionBytecode or
// CompressionZSAV — and check that both readers report identical values from
// the two files.
//
// # Scope
//
// The file header, record type 2 variable records (numeric and string, with
// string continuation records), record type 3/4 value labels, variable
// labels, record type 6 documents, the record type 7 extension subtypes
// listed on [Spec], the record type 999 dictionary terminator, and a data
// section in either the uncompressed or the bytecode-compressed encoding.
// Little-endian only.
//
// Bytecode compression — SPSS's own save default — is emitted when
// [Spec.Compression] asks for it, under whatever bias [Spec.CompressionBias]
// declares. The encoder here is written from the specification and shares no
// code with the reader under test, which is the whole point: a compressed and
// an uncompressed fixture built from one spec carry the same logical cases
// through two independent encodings, so a reader that agrees with both is
// agreeing with something other than itself.
//
// ZSAV — the zlib-blocked scheme SPSS 21+ writes into a `.zsav` — is emitted
// when [Spec.Compression] asks for it. It is two layers, not a third
// encoding: the bytecode stream above is cut into blocks of
// [Spec.ZSAVBlockSize] bytes, each block deflated into its own zlib stream at
// the pinned [ZSAVCompressionLevel], and a ZHEADER / ZTRAILER index records
// every block's offset and size in both coordinate spaces. A ZSAV file also
// carries the "$FL3" header magic instead of "$FL2".
//
// Deliberately absent, each owned by a later story: non-ASCII codepages, very
// long strings (>255 bytes), big-endian output and missing-value specs. The
// spec types carry the axes for those ([ByteOrder], Var.Width) so they can be
// filled in without reshaping the API.
//
// Every extension record is optional per the format, and a spec that asks for
// none emits none: [ReferenceSpec] still produces a file with no record type
// 7 at all, byte-for-byte as it did before extensions existed here. A reader
// that requires an extension record is reading something the format does not
// promise, and that fixture is what proves it.
//
// # Determinism
//
// [Build] is a pure function of its argument. There is no map iteration in the
// output path, no timestamp, no randomness, and no build-host string: the
// product name and creation date/time are pinned constants. The same spec
// always produces byte-identical output, so fixtures are stable enough to hash.
//
// # Test-only
//
// Nothing outside a _test.go file may import this package. It sits under
// internal/ so it is unreachable from outside the module, and
// TestSPSSTest_NotInLibraryBuild asserts that no non-test file in the module
// imports it, so it never reaches a linked binary.
package spsstest

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Pinned header constants. These exist to keep output byte-deterministic:
// every one of them is a slot where a real writer would put something
// environment-dependent.
const (
	// DefaultProductName goes in the 60-byte prod_name header field. The
	// "@(#) SPSS DATA FILE" prefix is what readers sniff for; the remainder
	// identifies the writer.
	DefaultProductName = "@(#) SPSS DATA FILE pulse spsstest 1.0"

	// DefaultCreationDate is the 9-byte creation_date header field, in the
	// spec's "dd mmm yy" shape.
	DefaultCreationDate = "01 Jan 24"

	// DefaultCreationTime is the 8-byte creation_time header field, "hh:mm:ss".
	DefaultCreationTime = "00:00:00"

	// CompressionBias is the flt64 bias field in the header. The spec calls
	// for 100 and readers assume it; it is written even when the data section
	// is uncompressed, exactly as PSPP does. [Spec.CompressionBias] overrides
	// it, which is how a fixture proves a reader honours the declared bias
	// rather than hardcoding this value.
	CompressionBias = 100.0

	// ZSAVBlockSize is the uncompressed size of one ZSAV zlib block:
	// 0x3ff000, the value every writer of the format uses and the one
	// the ZTRAILER records. [Spec.ZSAVBlockSize] overrides it.
	ZSAVBlockSize = 0x3ff000

	// ZSAVCompressionLevel is the deflate level every ZSAV block is
	// compressed at.
	//
	// It is pinned to a NUMBER rather than left at zlib.DefaultCompression
	// on purpose. DefaultCompression is the sentinel -1, whose meaning is
	// an implementation choice the standard library is free to change; a
	// fixture whose bytes moved with the toolchain would break the
	// byte-determinism this package promises, and it would break it
	// silently, as a golden-hash mismatch nobody could explain from the
	// diff.
	ZSAVCompressionLevel = 6
)

// SysMisDouble is the system-missing sentinel as it appears in an
// uncompressed data section: the IEEE 754 double -DBL_MAX, i.e.
// 0xFFEFFFFFFFFFFFFF.
//
// The authoritative declaration of this value in a real file is the record
// 7/4 "machine float info" extension, which this package does not emit (see
// the package doc). Readers that key off 7/4 and readers that hardcode
// -DBL_MAX agree on this value, which is why it is safe to emit without the
// record.
var SysMisDouble = -math.MaxFloat64

// Header field widths, in bytes, in file order. Named because the
// hand-verification walkthrough refers to them.
const (
	headerRecTypeLen      = 4
	headerProdNameLen     = 60
	headerCreationDateLen = 9
	headerCreationTimeLen = 8
	headerFileLabelLen    = 64
	headerPaddingLen      = 3

	// HeaderSize is the total size of the file header record.
	HeaderSize = headerRecTypeLen + headerProdNameLen + 4 /*layout_code*/ + 4 /*nominal_case_size*/ +
		4 /*compression*/ + 4 /*weight_index*/ + 4 /*ncases*/ + 8 /*bias*/ +
		headerCreationDateLen + headerCreationTimeLen + headerFileLabelLen + headerPaddingLen

	// VariableRecordSize is the size of the fixed part of a record type 2,
	// before any variable label or missing-value payload.
	VariableRecordSize = 4 /*rec_type*/ + 4 /*type*/ + 4 /*has_var_label*/ + 4 /*n_missing_values*/ +
		4 /*print*/ + 4 /*write*/ + shortNameLen

	shortNameLen = 8

	// ElementSize is the width of one data element: 8 bytes, for both a
	// numeric double and one segment of a string.
	ElementSize = 8
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

// Record type 7 extension subtypes this package can emit.
const (
	// SubtypeMachineInteger is 7/3: eight int32s of machine integer info.
	SubtypeMachineInteger int32 = 3
	// SubtypeMachineFloat is 7/4: the sysmis, highest and lowest doubles.
	SubtypeMachineFloat int32 = 4
	// SubtypeVariableSets is 7/5, the older text set-definition record.
	SubtypeVariableSets int32 = 5
	// SubtypeMRSets is 7/7: multiple-response set definitions.
	SubtypeMRSets int32 = 7
	// SubtypeDisplayParams is 7/11: measure, display width and alignment.
	SubtypeDisplayParams int32 = 11
	// SubtypeLongNames is 7/13: the SHORT=Long name mapping.
	SubtypeLongNames int32 = 13
	// SubtypeVeryLongStrings is 7/14: the NAME=WIDTH map of every string
	// wider than 255 bytes.
	SubtypeVeryLongStrings int32 = 14
	// SubtypeNumberOfCases is 7/16: the 64-bit case count.
	SubtypeNumberOfCases int32 = 16
	// SubtypeFileAttributes is 7/17: data-file attribute text.
	SubtypeFileAttributes int32 = 17
	// SubtypeVarAttributes is 7/18: per-variable attribute text.
	SubtypeVarAttributes int32 = 18
	// SubtypeMRSetsExtended is 7/19: the extended multiple-response set
	// record, which adds the E form carrying a counted-value label source.
	SubtypeMRSetsExtended int32 = 19
	// SubtypeCharacterEncoding is 7/20: the IANA-ish charset name.
	SubtypeCharacterEncoding int32 = 20
	// SubtypeLongStringValueLabels is 7/21: value labels for a string too
	// wide to carry them in records 3/4.
	SubtypeLongStringValueLabels int32 = 21
	// SubtypeLongStringMissing is 7/22: missing values for a string too
	// wide to carry them in its record type 2.
	SubtypeLongStringMissing int32 = 22
)

// DocumentLineLen is the fixed width of one record type 6 document line.
// Lines are space-padded out to it, which is why a document line cannot
// carry a meaningful trailing space.
const DocumentLineLen = 80

// typeStringContinuation is the record type 2 `type` field value marking a
// continuation record for a string wider than 8 bytes.
const typeStringContinuation int32 = -1

// Spec limits taken from the PSPP specification. Where the spec documents a
// range because SPSS versions disagree, the conservative end is used: a file
// that stays inside it is readable by every version.
const (
	// MaxShortNameLen is the width of the record type 2 name field.
	MaxShortNameLen = 8

	// MaxStringWidth is the largest string width expressible in the record
	// type 2 `type` field, and hence the width of every very-long-string
	// segment but the last.
	MaxStringWidth = 255

	// VeryLongStringSegmentWidth is how many bytes of a very long string's
	// LOGICAL value each non-final physical segment carries.
	//
	// It is 252 and not the 255 the segment declares: a non-final segment's
	// last three declared bytes are unused. Both PSPP and ReadStat divide
	// by 252, and dividing by 255 shifts every byte after the first segment.
	VeryLongStringSegmentWidth = 252

	// MaxVeryLongStringWidth is the widest string variable SPSS supports.
	// A [Var] wider than [MaxStringWidth] and no wider than this is emitted
	// as a very long string: several physical variables plus a record 7/14
	// stating how to rejoin them.
	MaxVeryLongStringWidth = 32767

	// MaxVarLabelLen is the longest variable label emitted. The spec gives the
	// documented maximum as varying from 120 to 255 across SPSS versions.
	MaxVarLabelLen = 120

	// MaxValueLabelLen is the longest value label emitted. The length field is
	// a single byte so 255 is the hard ceiling; the spec's documented maximum
	// varies from 60 to 120.
	MaxValueLabelLen = 120

	// MaxLongNameLen is the longest name the record 7/13 long variable
	// names extension may declare. SPSS 12 introduced long names with a
	// 64-byte ceiling and it has not moved since.
	MaxLongNameLen = 64

	// MaxShortStringWidth is the widest string variable that can carry value
	// labels through records 3/4, and the widest whose missing values fit a
	// record type 2. Anything wider needs record 7/21 long string value
	// labels and record 7/22 long string missing values — see
	// [Spec.LongStringValueLabels] and [Spec.LongStringMissingValues].
	MaxShortStringWidth = 8

	// MaxLongStringMissingValues is the most missing values record 7/22 can
	// declare for one variable. The count rides a single byte and the format
	// caps it at three, matching record type 2.
	MaxLongStringMissingValues = 3

	// MaxDiscreteMissingValues is the most discrete missing values a
	// record type 2 can carry on its own: the n_missing_values field
	// tops out at 3. A range consumes two of the same slots, so a
	// specification carrying one holds at most ONE discrete value
	// beside it.
	MaxDiscreteMissingValues = 3
)

// ByteOrder selects the file's byte order. Both orders are emitted: every
// multi-byte field in the header, in every record, in every extension
// payload and in the data section — including the ZSAV block index and the
// 8-byte escape payloads of the bytecode stream — is written in the selected
// order, so a big-endian fixture is a whole file and not a big-endian header
// with little-endian innards.
//
// The one thing that is NOT byte-ordered is the record 7/3 endianness field:
// see [MachineIntegerInfo.Endianness], which is emitted verbatim because a
// fixture that states a byte order contradicting its own bytes is exactly
// what a reader's cross-check has to be tested against.
type ByteOrder int

const (
	// LittleEndian is the zero value, so a zero Spec is a valid little-endian spec.
	LittleEndian ByteOrder = iota
	// BigEndian writes every multi-byte field most-significant byte first.
	BigEndian
)

func (b ByteOrder) String() string {
	switch b {
	case LittleEndian:
		return "little-endian"
	case BigEndian:
		return "big-endian"
	default:
		return "ByteOrder(?)"
	}
}

// binary returns the encoding/binary implementation of the order. It is
// unexported: callers select an order with the [ByteOrder] constants and
// never need the binary.ByteOrder itself.
func (b ByteOrder) binary() binary.ByteOrder {
	if b == BigEndian {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// Endianness codes as record 7/3 states them. The field is the second
// statement of something the header layout code already fixed, and the two
// are allowed to disagree only in a fixture built to test that they are
// checked.
const (
	// EndiannessBig is the record 7/3 code for a big-endian file.
	EndiannessBig int32 = 1
	// EndiannessLittle is the record 7/3 code for a little-endian file.
	EndiannessLittle int32 = 2
)

// Compression selects the data-section encoding.
type Compression int

const (
	// CompressionNone is the zero value: an uncompressed data section, 8 bytes
	// per element.
	CompressionNone Compression = iota
	// CompressionBytecode is the SPSS default bytecode scheme: blocks of
	// eight command bytes, each command either standing for an element on
	// its own or naming an eight-byte payload that trails the block.
	CompressionBytecode
	// CompressionZSAV is the zlib-blocked scheme SPSS 21+ writes into a
	// `.zsav`: the bytecode command stream, cut into blocks, each block
	// deflated into its own zlib stream, with a ZHEADER / ZTRAILER index
	// giving every block's offset and size compressed and uncompressed.
	//
	// It is TWO layers. The blocks do not hold case data; they hold the
	// same command stream CompressionBytecode emits in the clear.
	CompressionZSAV
)

func (c Compression) String() string {
	switch c {
	case CompressionNone:
		return "uncompressed"
	case CompressionBytecode:
		return "bytecode"
	case CompressionZSAV:
		return "zsav"
	default:
		return "Compression(?)"
	}
}

// FormatType is an SPSS print/write format type code.
//
// The two general formats plus the temporal family are named, because
// those are the codes a reader's type mapping dispatches on. The values
// are the PSPP output-format table's; the currency and scientific formats
// are omitted only because nothing reads them yet, and any code may still
// be written by spelling the number, since the wire field is a byte.
type FormatType uint8

const (
	// FormatA is the alphanumeric (string) format, code 1.
	FormatA FormatType = 1
	// FormatF is the plain numeric format, code 5.
	FormatF FormatType = 5

	// FormatDATE is dd-mmm-yyyy, code 20. Day resolution.
	FormatDATE FormatType = 20
	// FormatTIME is hh:mm:ss.s, code 21. A DURATION — seconds of day —
	// not an instant.
	FormatTIME FormatType = 21
	// FormatDATETIME is dd-mmm-yyyy hh:mm:ss.s, code 22. An instant at
	// sub-day resolution.
	FormatDATETIME FormatType = 22
	// FormatADATE is mm/dd/yyyy, code 23. Day resolution.
	FormatADATE FormatType = 23
	// FormatJDATE is yyyyddd, code 24. Day resolution.
	FormatJDATE FormatType = 24
	// FormatDTIME is dd hh:mm:ss.s, code 25. A DURATION — a days-plus-time
	// interval — not an instant.
	FormatDTIME FormatType = 25
	// FormatEDATE is dd.mm.yyyy, code 38. Day resolution.
	FormatEDATE FormatType = 38
	// FormatSDATE is yyyy/mm/dd, code 39. Day resolution.
	FormatSDATE FormatType = 39
)

// SPSSEpochOffsetSeconds is the number of seconds between the SPSS epoch
// (1582-10-14 00:00:00 UTC) and the Unix epoch. Every SPSS temporal datum
// is a second count from the former, so a fixture author writes a
// calendar instant as Num(float64(t.Unix() + SPSSEpochOffsetSeconds)).
const SPSSEpochOffsetSeconds int64 = 12219379200

// Format is a print or write format specification. It is packed into an int32
// as 0x00TTWWDD: the most significant byte is unused and zero, then the type
// code, then the field width, then the decimal count.
type Format struct {
	Type     FormatType
	Width    int
	Decimals int
}

// pack renders the format into its int32 wire encoding.
func (f Format) pack() int32 {
	return int32(uint32(f.Type)<<16 | uint32(f.Width&0xFF)<<8 | uint32(f.Decimals&0xFF))
}

// isZero reports whether the format was left unset, in which case Build
// derives a default from the variable's type.
func (f Format) isZero() bool { return f == Format{} }

// Var declares one SPSS variable: one record type 2, plus one continuation
// record for every 8 bytes of string width beyond the first 8.
type Var struct {
	// Name is the 8-byte short name. It must already be a legal SPSS short
	// name — uppercase, and it is written verbatim. The emitter will not
	// upper-case a name for you, because a silent rename is exactly the kind
	// of transform that makes a fixture disagree with the file the author
	// thought they wrote. Long/mixed-case names live in record 7/13, which is
	// out of scope here.
	Name string

	// Width is 0 for a numeric variable, or the byte width for a string
	// variable.
	//
	// A width of 1..[MaxStringWidth] is a plain string: one record type 2
	// plus its 8-byte continuation records. A width above that and no more
	// than [MaxVeryLongStringWidth] is a VERY LONG STRING, which SPSS
	// cannot express in the record type 2 `type` field at all — the file
	// carries several physical variables of 255 bytes each plus a record
	// 7/14 saying how to rejoin them, and this package emits all of that
	// for you. The [Spec] still names ONE variable and each case still
	// carries ONE [Text] value; the split is an emission detail.
	//
	// A very long string derives its own print and write formats per
	// physical segment, so Print and Write must be left zero on one.
	Width int

	// Label is the variable label, or "" for none. A non-empty label sets the
	// record's has_var_label flag and appends the length-prefixed, 4-byte
	// aligned label payload.
	Label string

	// Print and Write are the output formats. A zero Format is replaced by a
	// type-appropriate default: F8.2 for numeric, A<Width> for string. A zero
	// Write copies Print.
	Print Format
	Write Format

	// LongName is the variable's real, case-preserving name, emitted in the
	// record 7/13 long variable names extension as SHORT=LongName. Empty
	// means the variable has no long name and the short Name is its only
	// name.
	//
	// This is a separate slot rather than a loosening of Name on purpose:
	// Name is the 8-byte record type 2 field and stays subject to the short
	// name rules, because those two names really are two different fields
	// with two different rule sets. Emitting a long name does not change
	// what the record type 2 carries.
	LongName string

	// Measure is the variable's measurement level, emitted in the record
	// 7/11 variable display parameters extension. MeasureUnset (the zero
	// value) means the variable contributes no measure of its own; the
	// record is emitted for every variable or for none, so an unset measure
	// still occupies its slot with the value 0.
	Measure Measure

	// DisplayWidth is the column width the record 7/11 extension declares.
	// Zero falls back to the print format width, which is what SPSS does.
	DisplayWidth int

	// Align is the alignment the record 7/11 extension declares. AlignLeft
	// is the zero value and is a real alignment, not "unset" — 7/11 has no
	// unset alignment.
	Align Alignment

	// Missing is the variable's record type 2 missing-value specification,
	// nil when it declares none.
	//
	// This is the record-2 slot, distinct from
	// [Spec.LongStringMissingValues], which is record 7/22. The two are
	// not interchangeable: 7/22 cannot express a range, which only a
	// numeric variable can have, and a record type 2 slot is fixed at
	// eight bytes, which is why a wider string's missing values need
	// 7/22 at all.
	//
	// A record type 2 Missing on a string WIDER than
	// [MaxShortStringWidth] is nonetheless emitted rather than refused,
	// and so is a variable carrying both it and a 7/22 entry. Real files
	// contain both shapes — SPSS compares only a long string's first
	// eight bytes — and a reader has to resolve the conflict, so a
	// fixture has to be able to state it. What is refused is a Missing
	// on a VERY long string (over [MaxStringWidth]), which after
	// segmentation is not one record type 2 at all.
	Missing *MissingValues
}

// MissingValues is one variable's record type 2 missing-value
// specification: up to three discrete values, a lo..hi range, or a range
// plus exactly one discrete value.
//
// The wire form is a single n_missing_values field carrying 0, 1, 2, 3, -2
// or -3, followed by that many eight-byte slots. The sign is what says
// whether the leading two slots are a range, so it is DERIVED here from
// which fields are set rather than declared: a caller cannot state a code
// that disagrees with the slots it supplied.
//
// Negative codes are numeric-only. The format has no range form for
// strings, and a string variable declaring one is rejected rather than
// emitted.
type MissingValues struct {
	// Range is the lo..hi bound, inclusive at both ends, or nil for a
	// purely discrete specification. Numeric variables only.
	//
	// SPSS spells an open-ended range with its LOWEST / HIGHEST
	// sentinels, which are -DBL_MAX and +DBL_MAX. Note that LOWEST is
	// the SAME double as [SysMisDouble]: a reader must test for
	// system-missing before it tests a range, or every sysmis datum
	// falls inside a LOWEST-bounded one.
	Range *MissingRange

	// Discrete are the discrete missing values: up to three on their own,
	// or exactly one alongside a Range.
	//
	// Every entry must match the variable's type — [Num] for a numeric
	// variable, [Text] for a string one. [SysMis] is rejected: the system
	// -missing state is not a user-missing code, and declaring it as one
	// would produce a slot no datum could ever be compared against.
	Discrete []Value
}

// MissingRange is the lo..hi half of a range missing-value specification.
// Both bounds are inclusive.
type MissingRange struct {
	Low  float64
	High float64
}

// code returns the record type 2 n_missing_values field value for the
// specification: 0 for none, 1..3 discrete values, -2 for a bare range and
// -3 for a range plus one discrete value.
func (m *MissingValues) code() int32 {
	if m == nil {
		return 0
	}
	if m.Range != nil {
		return -int32(2 + len(m.Discrete))
	}
	return int32(len(m.Discrete))
}

// slots returns the number of eight-byte slots the specification occupies.
func (m *MissingValues) slots() int {
	c := m.code()
	if c < 0 {
		return int(-c)
	}
	return int(c)
}

// Measure is the record 7/11 measurement level of a variable.
type Measure int32

const (
	// MeasureUnset is the zero value. Files written by older SPSS versions
	// carry it, and it means the writer declared no level.
	MeasureUnset Measure = 0
	// MeasureNominal is an unordered categorical level.
	MeasureNominal Measure = 1
	// MeasureOrdinal is an ordered categorical level.
	MeasureOrdinal Measure = 2
	// MeasureScale is a continuous / interval level.
	MeasureScale Measure = 3
)

func (m Measure) String() string {
	switch m {
	case MeasureUnset:
		return "unset"
	case MeasureNominal:
		return "nominal"
	case MeasureOrdinal:
		return "ordinal"
	case MeasureScale:
		return "scale"
	default:
		return "Measure(?)"
	}
}

// Alignment is the record 7/11 column alignment of a variable.
type Alignment int32

const (
	// AlignLeft is the zero value.
	AlignLeft Alignment = 0
	// AlignRight is right alignment.
	AlignRight Alignment = 1
	// AlignCenter is centre alignment.
	AlignCenter Alignment = 2
)

func (a Alignment) String() string {
	switch a {
	case AlignLeft:
		return "left"
	case AlignRight:
		return "right"
	case AlignCenter:
		return "center"
	default:
		return "Alignment(?)"
	}
}

// MRSetKind discriminates the two multiple-response set flavours. They are
// not two configurations of one thing: a dichotomy is N binary indicators
// with one declared counted value, a category set is N variables sharing one
// value-label set. The zero value is invalid so a bare MRSet{} cannot
// silently mean either.
type MRSetKind int

const (
	// The zero value is unnamed and invalid: a bare MRSet{} must not
	// silently mean either flavour, and Build rejects one that leaves Kind
	// unset.
	_ MRSetKind = iota
	// MRDichotomy is a multiple-dichotomy set: each member variable holding
	// the counted value means that option was selected.
	MRDichotomy
	// MRCategory is a multiple-category set: each member variable holds a
	// code from a shared value-label set.
	MRCategory
)

func (k MRSetKind) String() string {
	switch k {
	case MRDichotomy:
		return "multiple dichotomy"
	case MRCategory:
		return "multiple category"
	default:
		return "MRSetKind(?)"
	}
}

// MRSet is one multiple-response set definition, emitted into the record
// 7/5, 7/7 or 7/19 text payload.
type MRSet struct {
	// Name is the set name. SPSS requires it to begin with '$'.
	Name string

	// Kind selects the dichotomy or category flavour. Required.
	Kind MRSetKind

	// Label is the set's label. It may be empty, which is emitted as a
	// zero-length counted string.
	Label string

	// CountedValue is the value that counts as "selected", for a dichotomy
	// set only. It is written as a counted string exactly as given, because
	// that is what the format stores — the wire form does not say whether it
	// is a number or a string.
	CountedValue string

	// Extended writes the set with the 'E' type code, which carries an
	// explicit label source, instead of the plain 'D'. Dichotomy sets only,
	// and normally paired with Subtype 19.
	Extended bool

	// LabelFromVarLabel selects the '11' label source (the label comes from
	// the first member variable's variable label) rather than '1' (the label
	// is in this record). Only meaningful with Extended.
	LabelFromVarLabel bool

	// Vars names the member variables by short name, in order.
	Vars []string

	// Subtype selects which extension record carries the definition: 5, 7
	// (the default when zero) or 19.
	Subtype int32
}

// VariableSet is one entry of the older record 7/5 variable-sets text
// payload: a display grouping, NOT a multiple-response set. It exists so a
// fixture can prove a reader tells the two apart inside the same subtype.
type VariableSet struct {
	// Name is the set name. A variable set name does not begin with '$'.
	Name string
	// Vars names the member variables by short name.
	Vars []string
}

// MachineIntegerInfo is the record 7/3 payload: eight int32s.
type MachineIntegerInfo struct {
	VersionMajor    int32
	VersionMinor    int32
	VersionRevision int32
	// MachineCode identifies the writing machine; readers ignore it.
	MachineCode int32
	// FloatingPointRep is 1 for IEEE 754, 2 for IBM 370, 3 for DEC VAX E.
	FloatingPointRep int32
	// CompressionCode is 1 in every file the specification describes.
	CompressionCode int32
	// Endianness is [EndiannessBig] (1) or [EndiannessLittle] (2).
	//
	// It is emitted VERBATIM and is never reconciled against
	// [Spec.ByteOrder]. That is deliberate: the field is the file's
	// second statement of a byte order the header layout code already
	// fixed, so the only interesting thing a fixture can do with it is
	// contradict the file it sits in, and a generator that refused to
	// emit the contradiction would make the reader's cross-check
	// untestable. A fixture meaning "an ordinary file of this byte
	// order" should take its 7/3 payload from
	// [DefaultMachineIntegerInfoFor] rather than filling this in by
	// hand.
	Endianness int32
	// CharacterCode is the codepage: 2 or 3 for ASCII, 1252 for
	// windows-1252, 65001 for UTF-8, and so on.
	CharacterCode int32
}

// DefaultMachineIntegerInfo is what a little-endian IEEE-754 ASCII writer
// declares. It is the value emitted when a Spec asks for 7/3 without
// supplying one.
func DefaultMachineIntegerInfo() MachineIntegerInfo {
	return DefaultMachineIntegerInfoFor(LittleEndian)
}

// DefaultMachineIntegerInfoFor is [DefaultMachineIntegerInfo] with the
// record 7/3 endianness field set to agree with bo.
//
// It exists so a big-endian fixture is one field change rather than a
// hand-filled payload: taking DefaultMachineIntegerInfo into a big-endian
// Spec would declare little-endian inside a big-endian file, which is a
// real fixture but not the one an author who wrote "the ordinary defaults"
// meant.
func DefaultMachineIntegerInfoFor(bo ByteOrder) MachineIntegerInfo {
	endian := EndiannessLittle
	if bo == BigEndian {
		endian = EndiannessBig
	}
	return MachineIntegerInfo{
		VersionMajor: 1, VersionMinor: 0, VersionRevision: 0,
		MachineCode:      -1,
		FloatingPointRep: 1,
		CompressionCode:  1,
		Endianness:       endian,
		CharacterCode:    2,
	}
}

// MachineFloatInfo is the record 7/4 payload: three doubles.
type MachineFloatInfo struct {
	// SysMis is the system-missing sentinel. Every real writer puts
	// -DBL_MAX here.
	SysMis float64
	// Highest is the "highest" sentinel, +DBL_MAX.
	Highest float64
	// Lowest is the "lowest" sentinel: the second-lowest double, i.e. the
	// most negative value that is not the system-missing sentinel.
	Lowest float64
}

// DefaultMachineFloatInfo returns the sentinels every conforming writer
// declares.
func DefaultMachineFloatInfo() MachineFloatInfo {
	return MachineFloatInfo{
		SysMis:  -math.MaxFloat64,
		Highest: math.MaxFloat64,
		Lowest:  math.Nextafter(-math.MaxFloat64, 0),
	}
}

// RawExtension is a record type 7 emitted verbatim. It is the escape hatch
// for the subtypes this package does not model — including deliberately
// unrecognised ones, which is how a fixture exercises a reader's
// skip-with-warning path.
type RawExtension struct {
	// Subtype is the extension subtype tag.
	Subtype int32
	// Size is the declared element size. Zero defaults to 1.
	Size int32
	// Payload is the record body. Its length must be a multiple of Size;
	// the count field is derived as len(Payload)/Size, so a payload that
	// does not divide evenly is rejected rather than padded.
	Payload []byte
}

// IsString reports whether the variable is a string variable.
func (v Var) IsString() bool { return v.Width > 0 }

// segments is the number of 8-byte data elements, and hence the number of
// record type 2 entries (one real plus continuations), the variable occupies.
//
// It is meaningful only for a PHYSICAL variable. A very long string is
// expanded into its physical segments before any plan is built, so nothing
// ever calls this on one.
func (v Var) segments() int {
	if !v.IsString() {
		return 1
	}
	return (v.Width + ElementSize - 1) / ElementSize
}

// IsVeryLongString reports whether the variable is wide enough to need the
// record 7/14 segmentation scheme.
func (v Var) IsVeryLongString() bool { return v.Width > MaxStringWidth }

// VeryLongStringSegmentCount returns how many PHYSICAL variables a string of
// the given logical byte width occupies.
func VeryLongStringSegmentCount(width int) int {
	if width <= MaxStringWidth {
		return 1
	}
	return (width + VeryLongStringSegmentWidth - 1) / VeryLongStringSegmentWidth
}

// VeryLongStringSegmentWidthAt returns the DECLARED byte width of segment i
// (0-based) of a string of the given logical width: [MaxStringWidth] for
// every segment but the last, which declares whatever the 252-byte stride
// has left over.
func VeryLongStringSegmentWidthAt(width, i int) int {
	if i < VeryLongStringSegmentCount(width)-1 {
		return MaxStringWidth
	}
	return width - i*VeryLongStringSegmentWidth
}

// VeryLongStringSegmentContentAt returns how many bytes of the LOGICAL value
// segment i carries: [VeryLongStringSegmentWidth] for every segment but the
// last, and the remainder for the last.
//
// It is not the segment's declared width. A non-final segment declares 255
// and carries 252; the difference is why a 256-byte string is stored as two
// segments declaring 255 and 4, whose declared widths sum to 259.
func VeryLongStringSegmentContentAt(width, i int) int {
	if i < VeryLongStringSegmentCount(width)-1 {
		return VeryLongStringSegmentWidth
	}
	return width - i*VeryLongStringSegmentWidth
}

// VeryLongStringSegmentName returns the record type 2 short name this package
// gives segment i of a very long string named base.
//
// Segment 0 keeps the variable's own name — that is the name record 7/14, and
// every other record that cross-references the variable, uses. Later segments
// take a generated name: the base truncated to leave room for the decimal
// index, then that index. SPSS generates names the same way and by the same
// necessity, since eight bytes cannot hold a name plus an unbounded suffix.
func VeryLongStringSegmentName(base string, i int) string {
	if i == 0 {
		return base
	}
	suffix := strconv.Itoa(i - 1)
	keep := MaxShortNameLen - len(suffix)
	if keep < 1 {
		keep = 1
	}
	if len(base) > keep {
		base = base[:keep]
	}
	return base + suffix
}

// ValueLabel is one (value, label) pair inside a record type 3.
type ValueLabel struct {
	// Value is the labelled value. It must match the type of every variable in
	// the enclosing set.
	Value Value
	// Label is the label text, 1..MaxValueLabelLen bytes.
	Label string
}

// ValueLabelSet is one record type 3 (the labels) paired with the record type
// 4 that names the variables they apply to. Modelling it as a set rather than
// as a per-variable field is deliberate: it is what the format actually is,
// and it makes the shared-label-set case — several variables pointing at one
// record 3 — expressible, which a per-variable field could not do.
//
// Both slices are ordered and emitted in order, which is what keeps output
// deterministic.
type ValueLabelSet struct {
	// Vars names the variables the labels apply to, by short name. The spec
	// requires them all to have the same type, and the same width if string.
	Vars []string
	// Labels are the (value, label) pairs, in emission order.
	Labels []ValueLabel
}

// valueKind discriminates a Value. The zero kind is invalid on purpose: a
// bare Value{} in a case row would otherwise silently mean numeric 0.
type valueKind int

const (
	kindInvalid valueKind = iota
	kindNum
	kindText
	kindSysMis
)

// Value is one datum: a numeric double, a string, or the system-missing
// sentinel. Construct one with [Num], [Text] or [SysMis]; the zero Value is
// invalid and Build rejects it.
type Value struct {
	kind valueKind
	num  float64
	str  string
}

// Num returns a numeric value.
func Num(v float64) Value { return Value{kind: kindNum, num: v} }

// Text returns a string value. It is space-padded to the variable's declared
// width on emission, per the format; it is an error for it to exceed that width.
func Text(s string) Value { return Value{kind: kindText, str: s} }

// SysMis returns the system-missing sentinel. It is only valid for numeric
// variables: SPSS has no system-missing state for strings, where an all-spaces
// value is the conventional stand-in.
func SysMis() Value { return Value{kind: kindSysMis} }

// String renders a Value for error messages.
func (v Value) String() string {
	switch v.kind {
	case kindNum:
		return fmt.Sprintf("Num(%g)", v.num)
	case kindText:
		return fmt.Sprintf("Text(%q)", v.str)
	case kindSysMis:
		return "SysMis()"
	default:
		return "Value{} (uninitialised)"
	}
}

// Spec is a complete declarative description of a `.sav` file. [Build] turns
// it into bytes.
type Spec struct {
	// Vars are the variables, in file order. At least one is required.
	Vars []Var

	// Cases is the data section: one row per case, one Value per entry in
	// Vars (not per 8-byte element — string continuations are handled for
	// you). May be empty for a dictionary-only file.
	Cases [][]Value

	// ValueLabels are the record 3/4 pairs, emitted in order after all
	// variable records.
	ValueLabels []ValueLabelSet

	// FileLabel is the 64-byte header file label. Empty means all spaces.
	FileLabel string

	// WeightVar is the short name of the weighting variable, or "" for an
	// unweighted file. It is translated to the 1-based dictionary element
	// index the header actually stores, so callers never have to account for
	// string continuation records themselves.
	WeightVar string

	// UnknownCaseCount writes -1 into the header ncases field instead of the
	// real count. Real files do this, and a reader must cope, so it is
	// reachable from the fixture generator.
	UnknownCaseCount bool

	// ProductName overrides DefaultProductName. Pinned by default to keep
	// output deterministic.
	ProductName string

	// CreationDate overrides DefaultCreationDate ("dd mmm yy", 9 bytes).
	CreationDate string

	// CreationTime overrides DefaultCreationTime ("hh:mm:ss", 8 bytes).
	CreationTime string

	// Compression selects the data-section encoding. All three are
	// implemented: CompressionNone (the zero value), CompressionBytecode
	// and CompressionZSAV.
	//
	// CompressionZSAV also changes the header magic to "$FL3", which is
	// what the format uses to mark a zlib-compressed file.
	Compression Compression

	// ZSAVBlockSize overrides the uncompressed block size a ZSAV data
	// section is cut into, in bytes. Zero means [ZSAVBlockSize], the
	// 0x3ff000 every real writer uses. Ignored unless Compression is
	// CompressionZSAV.
	//
	// It exists so a fixture can carry a MULTI-BLOCK index without being
	// four megabytes: at the conventional size every fixture in this
	// package is one block, and a one-block index exercises none of the
	// cumulative-offset arithmetic a reader has to get right. The
	// block size is carried per file in the ZTRAILER, so a small value is
	// well-formed rather than a trick — but the conventional value is the
	// default precisely because a fixture handed to an outside reader
	// should be as ordinary as possible.
	ZSAVBlockSize int

	// CompressionBias overrides the header's flt64 bias field, which the
	// bytecode encoding subtracts to recover an integer from a command
	// byte. Zero means [CompressionBias], the conventional 100 every real
	// writer emits.
	//
	// It exists so a fixture can be written whose command bytes decode
	// correctly ONLY under the declared bias. A reader that hardcodes 100
	// reads such a file as a plausible set of numbers offset by a
	// constant, which no test against a bias-100 fixture can catch. The
	// field is honoured for an uncompressed file too, because the header
	// carries the bias either way and a reader must not care.
	CompressionBias float64

	// ByteOrder selects the file byte order. LittleEndian is the zero
	// value; BigEndian re-encodes every multi-byte field, so the same
	// spec built both ways yields two files with the same logical
	// content and no bytes in common outside the fixed-width text
	// fields.
	ByteOrder ByteOrder

	// Documents are record type 6 document lines, emitted verbatim and
	// space-padded to DocumentLineLen. A line longer than that is rejected
	// rather than wrapped, because wrapping would invent a line the author
	// did not write. Empty means no record type 6 is emitted at all.
	Documents []string

	// MachineIntegerInfo emits record 7/3. Nil means the record is absent,
	// which is legal; &MachineIntegerInfo{} is NOT a shorthand for the
	// defaults, so use DefaultMachineIntegerInfo when that is what is meant.
	MachineIntegerInfo *MachineIntegerInfo

	// MachineFloatInfo emits record 7/4. Nil means absent — and absent is
	// the common case, which is exactly why a reader must default its
	// system-missing sentinel rather than require this record.
	MachineFloatInfo *MachineFloatInfo

	// CharacterEncoding emits record 7/20 with the given charset name.
	// Empty means the record is absent.
	CharacterEncoding string

	// CaseCount64 emits record 7/16 with the given case count. Nil means
	// absent. The record's leading "1" field is written for you.
	CaseCount64 *int64

	// DisplayParams emits record 7/11 for every variable, from each Var's
	// Measure, DisplayWidth and Align. It is an explicit switch rather than
	// being inferred from the Var fields because AlignLeft and a zero
	// display width are legitimate values, so there is no zero state that
	// could mean "no record".
	DisplayParams bool

	// OmitDisplayWidth writes the two-int32-per-variable form of record
	// 7/11 (measure and alignment only), which older writers emit. Ignored
	// unless DisplayParams is set.
	OmitDisplayWidth bool

	// MultipleResponseSets are emitted into record 7/5, 7/7 and/or 7/19
	// according to each set's Subtype. Sets sharing a subtype share one
	// record, in slice order.
	MultipleResponseSets []MRSet

	// VariableSets are emitted into record 7/5 alongside any 7/5 multiple-
	// response sets. They are display groupings, not response sets; they
	// exist here so a fixture can prove the two are told apart.
	VariableSets []VariableSet

	// FileAttributes is the record 7/17 payload, emitted verbatim. Empty
	// means absent.
	FileAttributes string

	// VarAttributes is the record 7/18 payload, emitted verbatim. Empty
	// means absent.
	VarAttributes string

	// LongStringValueLabels are emitted into record 7/21: value labels for
	// a string variable too wide to carry them through records 3/4.
	//
	// They are a separate slot from ValueLabels rather than a widening of
	// it because the two are different records with different shapes — a
	// record type 3 value is a fixed eight-byte slot shared by several
	// variables, a record 7/21 value is length-prefixed and belongs to
	// exactly one. Collapsing them would hide which record a fixture is
	// actually exercising.
	LongStringValueLabels []LongStringValueLabels

	// LongStringMissingValues are emitted into record 7/22: missing values
	// for a string variable too wide to carry them in its record type 2.
	LongStringMissingValues []LongStringMissingValues

	// RawExtensions are emitted verbatim after every modelled extension
	// record, in slice order.
	RawExtensions []RawExtension
}

// LongStringValueLabels is one record 7/21 entry: the value labels of one
// string variable wider than [MaxShortStringWidth].
type LongStringValueLabels struct {
	// Var names the variable. It is written into the record verbatim, so a
	// fixture can state either the record type 2 short name or the record
	// 7/13 long name.
	//
	// Prefer the variable's FINAL name — its LongName where it has one, its
	// Name where it does not. That is what real writers emit, and ReadStat
	// (R's haven, Python's pyreadstat) refuses to parse a file at all when a
	// record 7/21 or 7/22 entry names a variable by its short name while a
	// long name exists. A fixture using the short name is testing a reader's
	// tolerance, not a file anyone else can read.
	Var string

	// Labels are the (value, label) pairs in emission order, which is the
	// order a reader turns into dictionary IDs.
	Labels []LongStringValueLabel
}

// LongStringValueLabel is one (value, label) pair of a record 7/21 entry.
type LongStringValueLabel struct {
	// Value is the labelled value. It is space-padded out to the
	// variable's declared width on the wire, which is what the format
	// specifies and what a datum of the same variable would look like.
	Value string

	// Label is the label text, written with its own byte length.
	Label string
}

// LongStringMissingValues is one record 7/22 entry: the missing values of one
// string variable wider than [MaxShortStringWidth].
type LongStringMissingValues struct {
	// Var names the variable, verbatim, exactly as
	// [LongStringValueLabels.Var] does — including the reason to prefer the
	// variable's final name over its short one.
	Var string

	// Values are the missing values, 1..[MaxLongStringMissingValues] of
	// them. Each is written as an eight-byte slot: the format fixes the
	// length there because SPSS compares only the first eight bytes of a
	// long string, so a value shorter than eight bytes is space-padded and
	// a longer one is rejected rather than silently cut.
	Values []string
}

// ReferenceSpec returns the fixture that is verified byte-by-byte against the
// specification in the offset walkthrough on TestReferenceFixture_HandVerified.
// It is the smallest spec that still exercises every feature of the v1 subset:
//
//   - a plain numeric variable with no label (ID)
//   - a numeric variable carrying both a variable label and a value label set (SEX)
//   - a string variable wide enough to need a continuation record (NAME, width 10)
//   - a system-missing numeric datum
//   - a string datum shorter than its declared width, so padding is exercised
//
// Downstream reader tests should prefer this fixture over ad-hoc ones: it is
// the only one whose every byte has been checked against the spec by a human.
func ReferenceSpec() Spec {
	return Spec{
		Vars: []Var{
			{Name: "ID", Print: Format{Type: FormatF, Width: 8}},
			{Name: "SEX", Label: "Sex", Print: Format{Type: FormatF, Width: 1}},
			{Name: "NAME", Width: 10},
		},
		ValueLabels: []ValueLabelSet{{
			Vars: []string{"SEX"},
			Labels: []ValueLabel{
				{Value: Num(1), Label: "Male"},
				{Value: Num(2), Label: "Female"},
			},
		}},
		Cases: [][]Value{
			{Num(1), Num(1), Text("ALICE")},
			{Num(2), SysMis(), Text("BOB")},
		},
	}
}

// ExtensionReferenceSpec returns the fixture that exercises every record
// type 7 extension subtype this package emits, plus a record type 6 document
// record and a deliberately unrecognised subtype.
//
// It builds on the same three variables as [ReferenceSpec] so the two can be
// diffed byte-for-byte: everything before the first document record is
// identical.
func ExtensionReferenceSpec() Spec {
	spec := ReferenceSpec()
	spec.Vars[0].LongName = "RespondentId"
	spec.Vars[0].Measure = MeasureScale
	spec.Vars[0].DisplayWidth = 10
	spec.Vars[0].Align = AlignRight
	spec.Vars[1].Measure = MeasureNominal
	spec.Vars[1].DisplayWidth = 4
	spec.Vars[2].LongName = "FullName"
	spec.Vars[2].Measure = MeasureNominal
	spec.Vars[2].DisplayWidth = 12

	mi := DefaultMachineIntegerInfo()
	mf := DefaultMachineFloatInfo()
	n := int64(len(spec.Cases))

	spec.Documents = []string{"Collected 2024-01-01.", "Second line."}
	spec.MachineIntegerInfo = &mi
	spec.MachineFloatInfo = &mf
	spec.CharacterEncoding = "UTF-8"
	spec.CaseCount64 = &n
	spec.DisplayParams = true
	spec.FileAttributes = "$@Role('0'\n)\n"
	spec.VarAttributes = "ID:$@Role('0'\n)\n"
	spec.MultipleResponseSets = []MRSet{
		{
			Name: "$media", Kind: MRDichotomy, Label: "Media used",
			CountedValue: "1", Vars: []string{"ID", "SEX"}, Subtype: SubtypeMRSets,
		},
		{
			Name: "$brands", Kind: MRCategory, Label: "Brands",
			Vars: []string{"ID", "SEX"}, Subtype: SubtypeMRSets,
		},
		{
			Name: "$ext", Kind: MRDichotomy, Label: "Extended", Extended: true,
			LabelFromVarLabel: true, CountedValue: "1",
			Vars: []string{"SEX"}, Subtype: SubtypeMRSetsExtended,
		},
	}
	spec.VariableSets = []VariableSet{{Name: "demographics", Vars: []string{"ID", "SEX"}}}
	spec.RawExtensions = []RawExtension{
		{Subtype: 4242, Size: 1, Payload: []byte("nobody knows what this is")},
	}
	return spec
}

// isASCIIPrintable reports whether s is entirely printable 7-bit ASCII.
//
// It is the rule for a Spec that declares NO character encoding: without a
// record 7/20 a high byte on the wire means nothing in particular, so a
// fixture carrying one would be asserting a decoding the file does not
// justify. A Spec that does declare an encoding is held to the equivalent
// rule in that charset's own alphabet instead — see wireCodec.text.
func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7E {
			return false
		}
	}
	return true
}

// isWirePrintable reports whether s is acceptable as text ON THE WIRE.
//
// It is isASCIIPrintable with the high bytes allowed, and it is what the
// structural checks use once validate has transcoded the spec: a byte at or
// above 0x80 in a wire string is by then the declared charset's encoding of
// a character already proved printable, so re-judging it as ASCII would
// reject every non-ASCII fixture. What it still catches is the thing that
// stays wrong in any charset — a C0 control byte in a fixed-width field or a
// delimiter-separated payload.
func isWirePrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			continue
		}
		if s[i] < 0x20 || s[i] > 0x7E {
			return false
		}
	}
	return true
}

// validShortName reports whether s is a legal SPSS short variable name: 1..8
// bytes, beginning with a capital letter or one of @ # $, and continuing with
// capital letters, digits, or one of . _ @ # $.
func validShortName(s string) bool {
	if s == "" || len(s) > MaxShortNameLen {
		return false
	}
	first := s[0]
	if !(first >= 'A' && first <= 'Z') && !strings.ContainsRune("@#$", rune(first)) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("._@#$", rune(c)):
		default:
			return false
		}
	}
	return true
}
