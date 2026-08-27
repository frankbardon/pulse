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
// The hand-verification was additionally corroborated against an independent
// implementation — R's `foreign::read.spss`, whose C reader shares no code with
// anything here — which recovered the reference fixture's variable names,
// variable label, value labels, string widths and system-missing datum exactly
// as declared, as well as a larger fixture exercising a shared value-label set,
// a string value label, a weight variable and a 20-byte string. That check is
// not automated (it needs R and is not a build dependency); it is recorded here
// so a future change can repeat it:
//
//	write Build(ReferenceSpec()) to reference.sav, then
//	Rscript -e 'print(foreign::read.spss("reference.sav", to.data.frame=FALSE))'
//
// # Scope
//
// This is the v1 subset: the file header, record type 2 variable records
// (numeric and string, with string continuation records), record type 3/4
// value labels, variable labels, the record type 999 dictionary terminator,
// and an uncompressed data section. Little-endian only.
//
// Deliberately absent, each owned by a later story: extension records (type
// 7 subtypes), bytecode compression, ZSAV, non-ASCII codepages, very long
// strings (>255 bytes), big-endian output, missing-value specs and
// multiple-response sets. The spec types carry the axes for those
// ([Compression], [ByteOrder], Var.Width) so they can be filled in without
// reshaping the API.
//
// No extension (type 7) records are emitted at all. They are all optional per
// the spec; a reader that requires one is reading something the format does
// not promise.
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
	"fmt"
	"math"
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
	// is uncompressed, exactly as PSPP does.
	CompressionBias = 100.0
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
	recTypeTerminator int32 = 999
)

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
	// type 2 `type` field. Wider strings need the record 7/14 very-long-string
	// segmentation scheme, which is out of scope here.
	MaxStringWidth = 255

	// MaxVarLabelLen is the longest variable label emitted. The spec gives the
	// documented maximum as varying from 120 to 255 across SPSS versions.
	MaxVarLabelLen = 120

	// MaxValueLabelLen is the longest value label emitted. The length field is
	// a single byte so 255 is the hard ceiling; the spec's documented maximum
	// varies from 60 to 120.
	MaxValueLabelLen = 120

	// MaxShortStringWidth is the widest string variable that can carry value
	// labels through records 3/4. Anything wider needs record 7/21 long string
	// value labels, which is out of scope here.
	MaxShortStringWidth = 8
)

// ByteOrder selects the file's byte order. v1 emits little-endian only;
// BigEndian is declared so the axis exists and is rejected explicitly rather
// than silently ignored.
type ByteOrder int

const (
	// LittleEndian is the zero value, so a zero Spec is a valid little-endian spec.
	LittleEndian ByteOrder = iota
	// BigEndian is not implemented yet.
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

// Compression selects the data-section encoding. v1 emits uncompressed only.
type Compression int

const (
	// CompressionNone is the zero value: an uncompressed data section, 8 bytes
	// per element.
	CompressionNone Compression = iota
	// CompressionBytecode is the SPSS default bytecode scheme. Not implemented yet.
	CompressionBytecode
	// CompressionZSAV is the zlib-blocked scheme used by SPSS 21+. Not implemented yet.
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

// FormatType is an SPSS print/write format type code. Only the two the v1
// subset needs are named; the full table (date and currency formats in
// particular) arrives with the format-decoding story.
type FormatType uint8

const (
	// FormatA is the alphanumeric (string) format, code 1.
	FormatA FormatType = 1
	// FormatF is the plain numeric format, code 5.
	FormatF FormatType = 5
)

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

	// Width is 0 for a numeric variable, or the byte width (1..255) for a
	// string variable.
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
}

// IsString reports whether the variable is a string variable.
func (v Var) IsString() bool { return v.Width > 0 }

// segments is the number of 8-byte data elements, and hence the number of
// record type 2 entries (one real plus continuations), the variable occupies.
func (v Var) segments() int {
	if !v.IsString() {
		return 1
	}
	return (v.Width + ElementSize - 1) / ElementSize
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

	// Compression selects the data-section encoding. Only CompressionNone (the
	// zero value) is implemented.
	Compression Compression

	// ByteOrder selects the file byte order. Only LittleEndian (the zero
	// value) is implemented.
	ByteOrder ByteOrder
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

// isASCIIPrintable reports whether s is entirely printable 7-bit ASCII.
// Anything else needs a declared character encoding (record 7/20), which this
// package does not emit, so it would be ambiguous on the wire.
func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
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
