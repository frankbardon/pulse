package spss

// The `.sav` DICTIONARY WRITER: the bytes from the file header through the
// record type 999 terminator.
//
// It has ONE emitter and TWO front-ends. The emitter walks an intermediate
// [outFile] — a list of physical variables plus the file-level facts — and
// knows nothing about where that came from; the front-ends build it either
// from a validated metadata sidecar (dict_source.go) or from the `.pulse`
// schema alone (dict_synth.go). That split is the point: the synthesised
// path is not a reduced writer with its own byte-level shortcuts, it is the
// same record emitter fed a different plan, so "no sidecar" cannot quietly
// produce a differently-shaped file.
//
// # Why the original SPSS codes, and nothing else, may be emitted
//
// A Pulse categorical dictionary ID is POSITIONAL — 0, 1, 2 — and an SPSS
// value code is arbitrary — 1, 2, 5, 9. E2-S6 stores the CODES as the
// cohort's dictionary entries and E4-S1's sidecar carries the
// code / label / ID triple per category. This writer emits the code from
// that triple and never the ID. Inventing a code from a position is not a
// display-level infidelity: every piece of downstream SPSS syntax that says
// `IF q1 EQ 5` addresses a value, and renumbering the values silently
// re-points every one of them at different rows.
//
// When there is no sidecar there is no triple, and the answer is NOT to
// invent one. A categorical with no recorded codes is emitted as a STRING
// variable holding the dictionary text, which is what the cohort actually
// knows. See dict_synth.go.
//
// # Scope, and the seams the rest of E5 hangs on
//
// This file emits the dictionary section only. The data section is E5-S3's,
// which is why [DictionaryPlan] carries the decode-side state that section
// needs — the per-variable [ColumnPlan] binding a cohort field to a run of
// 8-byte elements, the byte order, the sysmis sentinel, and the byte offsets
// of the two header fields a streaming encoder may have to patch once the
// case count is known.
//
// # Where the text is turned into bytes
//
// Every string this file emits has ALREADY been encoded into the file's own
// charset by applyCharsetWrite (charset_write.go), which runs between the
// front-end and emitDictionary. That is what lets the record emitter below
// treat a Go string as a plain byte container: its len() calls, its
// counted-string lengths and its fixed-width padding are byte arithmetic
// over the bytes that are actually going out, with no charset knowledge
// anywhere in it. The same pass recomputes every declared byte width the
// encoding moved and lays out the physical segments that width needs, so the
// [SegmentPlan] list this file walks is already true of the encoded value.
//
// Deliberately NOT done here, each owned by a later story:
//
//   - Name validation and derived-column fold-back (E5-S5). The sidecar path
//     already emits exactly the source's variables, because the document's
//     Variables list holds only those — the derived columns live in its
//     separate Derived registry — but nothing here validates a name or folds
//     a `<var>_missing` sibling back into a missing-value code.
//   - ZSAV emission, which is out of scope for the whole effort.

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// ---------------------------------------------------------------------------
// Emitted-format constants
// ---------------------------------------------------------------------------

const (
	// writerProductName is the 60-byte prod_name header field this writer
	// emits. The "@(#) SPSS DATA FILE" prefix is what readers sniff for.
	//
	// It is emitted even when a sidecar records the SOURCE file's product
	// name, and that is deliberate: prod_name identifies the program that
	// WROTE THESE BYTES. Re-emitting the original would be a false claim
	// about provenance, and the original is not lost — it stays in the
	// sidecar under source.product_name.
	writerProductName = "@(#) SPSS DATA FILE pulse"

	// writerCreationDate and writerCreationTime are the header stamps used
	// when no sidecar supplies the source's own.
	//
	// They are CONSTANTS, not a clock read. Emission has to be
	// byte-deterministic to be testable at all — the hand-verified
	// walkthrough in dict_write_test.go pins absolute offsets — and there is
	// no clock knob on WriterOptions to inject one through yet. E5-S6 may
	// add one; until then a fixed stamp is the honest answer, because a
	// wrong-but-plausible timestamp is worth nothing to a reader.
	writerCreationDate = "01 Jan 24"
	writerCreationTime = "00:00:00"

	// writerCompressionBias is the header bias field. The spec calls for
	// 100 and readers assume it; it is written even for an uncompressed
	// file, exactly as PSPP does.
	writerCompressionBias = 100.0

	// The record 7/20 charset declaration and the matching record 7/3
	// character_code are NOT constants here: they are resolved per file by
	// resolveWriteCharset, from the source's own declaration where there is
	// one and from defaultCharsetName where there is not. See
	// charset_write.go — the declaration follows the bytes, and since E5-S4
	// the bytes follow the source.

	// writerLayoutCode is the header endianness probe, written in the
	// file's own byte order.
	writerLayoutCode = 2

	// writerEndiannessCode is the record 7/3 endianness field: 1 for
	// big-endian, 2 for little-endian. This writer always emits
	// little-endian (see [DictionaryPlan.ByteOrder]), so it is always 2 —
	// never the source's recorded order, which would be a second statement
	// contradicting the layout code and is a hard
	// PULSE_SPSS_ENDIANNESS_MISMATCH on the way back in.
	writerEndiannessCode = 2

	// writerFloatingPointRep is the record 7/3 floating-point
	// representation: 1 for IEEE 754.
	writerFloatingPointRep = 1

	// writerCompressionCode is the record 7/3 compression code, which is 1
	// in every file the format describes.
	writerCompressionCode = 1
)

// writerHighest and writerLowest are the record 7/4 companions of
// [defaultSysmis]: +DBL_MAX, and the double one ULP above -DBL_MAX.
//
// The triple must satisfy sysmis < lowest < highest for the reader to adopt
// it (see applyMachineFloat), which is exactly why lowest is not -DBL_MAX
// itself.
var (
	writerHighest = math.MaxFloat64
	writerLowest  = math.Nextafter(-math.MaxFloat64, 0)
)

// ---------------------------------------------------------------------------
// Request
// ---------------------------------------------------------------------------

// DictionaryRequest is everything [BuildDictionary] needs.
//
// It is NOT a second knob bag beside [WriterOptions] and must not grow into
// one. WriterOptions holds what a CALLER decides — E5-S6's CLI flags map onto
// it one for one. This struct holds what the WRITER already knows by the time
// it emits a dictionary: the cohort's schema, the sidecar resolution E5-S1
// produced, and the two facts about the data section that only the data
// encoder can supply. Caller knobs ride Options.
type DictionaryRequest struct {
	// Schema is the `.pulse` cohort schema, as delivered through
	// pio.SchemaAwareWriter.SetPulseSchema. Required.
	Schema *encoding.Schema

	// Sidecar is [LoadSidecar]'s resolution. A nil resolution, or one whose
	// [SidecarResolution.Synthesise] reports true, selects the synthesised
	// default dictionary.
	//
	// Staleness is NOT re-checked here. LoadSidecar returns a resolution
	// only when the export may proceed, so a resolution in hand is already
	// the permission to use it.
	Sidecar *SidecarResolution

	// Cases is the number of cases the data section will carry, or -1 when
	// the writer does not yet know.
	//
	// -1 is a legal header value meaning "unknown" and every reader handles
	// it, so a streaming encoder may emit the dictionary first and either
	// leave it at -1 or patch [DictionaryPlan.CaseCountOffset] (and
	// [DictionaryPlan.CaseCount64Offset]) once the last case is written.
	Cases int64

	// Compression is the header compression flag the data section will be
	// written under: 0 for uncompressed, 1 for SPSS bytecode. ZSAV (2) is
	// rejected — emission of it is out of scope for this effort.
	Compression int32

	// Options are the caller's knobs.
	Options WriterOptions
}

// ---------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------

// ValueEncoding says how a cohort field's value becomes the 8-byte SPSS
// elements of one variable. It is the data encoder's dispatch, resolved once
// here so E5-S3 never has to re-derive it from a field type plus a format
// code plus a sidecar kind.
type ValueEncoding int

const (
	// EncodeUnbound is a variable no cohort field backs. It is the zero
	// value so an unfilled plan cannot be mistaken for a numeric one, and
	// no plan this package returns carries it.
	EncodeUnbound ValueEncoding = iota

	// EncodeNumeric writes the cohort's f64 straight through.
	//
	// It covers plain measurements AND the temporal formats that were never
	// converted on the way in: TIME and DTIME durations, and the MOYR (28) /
	// QYR (29) / WKYR (30) formats E2-S6 mapped to raw SPSS seconds. See
	// [ColumnPlan.PrintFormat] for why that is the right answer for them.
	EncodeNumeric

	// EncodeDateDays writes a `date` column: epoch DAYS, converted to
	// seconds since the SPSS epoch.
	EncodeDateDays

	// EncodeDateTimeSeconds writes a `datetime` column: epoch SECONDS,
	// rebased onto the SPSS epoch.
	EncodeDateTimeSeconds

	// EncodeCategoricalCode writes a NUMERIC variable from a categorical
	// column: the dictionary ID indexes [ColumnPlan.Categories] and the
	// entry's Code is what goes on the wire. Never the ID.
	EncodeCategoricalCode

	// EncodeText writes a STRING variable: the dictionary ID indexes
	// [ColumnPlan.Categories] and the entry's Text is space-padded out to
	// the declared width.
	EncodeText

	// EncodeSetMember writes one indicator variable of a multiple-dichotomy
	// set: [ColumnPlan.SetBit] of the source `set_*` mask decides between
	// the counted value and 0.
	EncodeSetMember
)

// String renders the encoding for diagnostics.
func (e ValueEncoding) String() string {
	switch e {
	case EncodeUnbound:
		return "unbound"
	case EncodeNumeric:
		return "numeric"
	case EncodeDateDays:
		return "date_days"
	case EncodeDateTimeSeconds:
		return "datetime_seconds"
	case EncodeCategoricalCode:
		return "categorical_code"
	case EncodeText:
		return "text"
	case EncodeSetMember:
		return "set_member"
	default:
		return "ValueEncoding(" + strconv.Itoa(int(e)) + ")"
	}
}

// CategoryCode is one cohort dictionary ID's SPSS value: the other half of
// the code / label / ID triple, projected into the form a data encoder
// indexes by ID.
type CategoryCode struct {
	// Code is the SPSS numeric value, meaningful when the variable is
	// numeric.
	Code float64

	// Text is the SPSS string value, meaningful when the variable is a
	// string. It is UTF-8, as every string in a Pulse cohort is.
	Text string

	// Encoded is Text in the EMITTED FILE's charset — the bytes the data
	// encoder lays into a case, and the bytes the variable's declared width
	// is measured in.
	//
	// It is precomputed rather than encoded per case for two reasons, and
	// only one of them is speed. A dictionary holds every value the column
	// can carry, so encoding it once at plan time means an unencodable
	// character stops the export before a single byte of the file is
	// written, instead of part way through the data section. Nil for an
	// empty value and for a numeric variable.
	Encoded []byte

	// Known reports that the value came from a sidecar's recorded triple
	// rather than from the cohort dictionary text. A synthesised plan sets
	// it false for every entry, which is the writer saying in the plan
	// itself that it did not invent codes.
	Known bool

	// Ambiguous marks an ID two distinct SOURCE values collapsed onto — the
	// PULSE_SPSS_VALUE_COLLISION case. The first is what this entry
	// carries; the collision is flagged rather than hidden, because
	// re-emitting either one is a guess about which row meant which.
	Ambiguous bool
}

// ColumnPlan is one emitted SPSS variable, bound to the cohort field it is
// written from.
//
// One cohort field can produce SEVERAL variables — a `set_*` column becomes
// one indicator variable per member — and a variable can be backed by no
// field at all in principle, so this is not a per-field list.
type ColumnPlan struct {
	// Name is the variable's FINAL name: the record 7/13 long name where
	// one exists, else the record type 2 short name.
	//
	// It is UTF-8, unlike the wire form of the same name that goes into
	// the file. Every other string on this struct describes bytes; this
	// one is what a diagnostic quotes and what a later pass matches a
	// cohort column against, and neither of those wants codepage bytes.
	//
	// This is the name records 7/21 and 7/22 carry. ReadStat — the C reader
	// behind haven, pyreadstat and most of the ecosystem — REFUSES a file
	// whose 7/21 or 7/22 entry names a variable by its short name when a
	// long name exists, so emitting anything else here produces files most
	// tools reject.
	Name string

	// ShortName is the 8-byte record type 2 name, as it was WRITTEN —
	// encoded in the emitted file's charset. Records 7/5, 7/7, 7/14 and
	// 7/19 key by it, and they key by the bytes.
	//
	// In practice it is 7-bit either way: a synthesised short name is
	// folded to ASCII by the name minter and a source's own is ASCII by
	// the format's rules, so the encode is an identity. It is documented
	// as bytes because that is what it is, not because it usually differs.
	ShortName string

	// Index is the 1-based dictionary ELEMENT index of the variable's first
	// element — what the header weight_index, the record type 4 bindings
	// and every other index-bearing field count.
	Index int32

	// Elements is how many 8-byte elements the variable occupies across all
	// of its physical segments.
	Elements int

	// Width is 0 for a numeric variable, else the LOGICAL declared byte
	// width — the total across the physical segments of a very long string,
	// not the 255 a single segment declares.
	Width int

	// Segments are the physical record type 2 variables, head first. Always
	// at least one; more than one only for a string wider than 255 bytes.
	Segments []SegmentPlan

	// PrintFormat and WriteFormat are the emitted output formats.
	//
	// For a MOYR (28) / QYR (29) / WKYR (30) column these are the SOURCE's
	// codes, re-emitted unchanged over an unchanged raw-seconds value. That
	// is the routed export-side answer to E2-S6's deferral: those formats
	// are date-valued in SPSS but not day-resolution, so the import kept
	// them as f64 raw seconds rather than truncating to a `date`. Nothing
	// was converted on the way in, so nothing needs converting on the way
	// out — and the format code is the ONLY thing that makes those seconds
	// render as "JAN 2024" instead of 1.4e10. Downgrading them to F would
	// lose the meaning of a column whose values are otherwise perfect.
	PrintFormat Format
	WriteFormat Format

	// Encoding is how a cohort value becomes this variable's elements.
	Encoding ValueEncoding

	// Field is the index into DictionaryRequest.Schema.Fields of the cohort
	// column this variable is written from, or -1 when nothing backs it.
	Field int

	// FieldName is the cohort field's name, kept beside Field so a
	// diagnostic does not have to reach back into the schema.
	FieldName string

	// FieldType is the cohort field's `.pulse` type.
	FieldType encoding.FieldType

	// Categories maps cohort dictionary ID to SPSS value, for
	// EncodeCategoricalCode and EncodeText. Indexed by ID; an ID past the
	// end has no recorded value.
	Categories []CategoryCode

	// SetBit is the source `set_*` mask bit this indicator variable stands
	// for, or -1 when the variable is not a set member.
	SetBit int

	// CountedValue is the numeric value meaning "selected", for
	// EncodeSetMember.
	CountedValue float64
}

// SegmentPlan is one PHYSICAL record type 2 variable.
type SegmentPlan struct {
	// Name is the segment's own 8-byte short name.
	Name string

	// Width is the segment's DECLARED byte width.
	Width int

	// Content is how many bytes of the LOGICAL value the segment carries.
	// It is not Width: a non-final very-long-string segment declares 255
	// and carries 252.
	Content int

	// Elements is how many 8-byte elements the segment occupies.
	Elements int

	// Index is the 1-based dictionary element index of the segment's first
	// element.
	Index int32
}

// DictionaryPlan is the emitted dictionary section plus the state the data
// section encoder needs. It is the E5-S2 / E5-S3 seam.
type DictionaryPlan struct {
	// Bytes are the dictionary section: the file header through the record
	// type 999 terminator, inclusive. The data section starts at
	// len(Bytes).
	Bytes []byte

	// ByteOrder is the order every multi-byte field in Bytes was written
	// in, and the order the data section must be written in.
	//
	// Always little-endian. Byte order carries no information about the
	// DATA — the values are identical either way — so re-emitting a
	// big-endian source's order would buy nothing and cost compatibility
	// with the many tools that only ever meet little-endian files.
	ByteOrder binary.ByteOrder

	// Compression is the header compression flag that was written.
	Compression int32

	// Bias is the header compression bias that was written.
	Bias float64

	// Sysmis is the system-missing sentinel the data section must use.
	Sysmis float64

	// ElementCount is the 8-byte elements per case: the header's
	// nominal_case_size, and the stride the data section must produce.
	ElementCount int32

	// CaseCount is the case count that was written, -1 for unknown.
	CaseCount int64

	// CaseCountOffset is the byte offset within Bytes of the header's
	// int32 ncases field, so a streaming encoder can patch it.
	CaseCountOffset int

	// CaseCount64Offset is the byte offset of the record 7/16 int64 case
	// count, or -1 when no 7/16 was emitted (which is the case exactly when
	// CaseCount is -1). Patching one and not the other leaves a file whose
	// two case counts disagree.
	CaseCount64Offset int

	// Columns are the emitted variables in element order.
	Columns []ColumnPlan

	// UnboundFields lists the cohort schema field indices no emitted
	// variable is written from, in schema order.
	//
	// For a sidecar-driven plan these are the DERIVED columns — the
	// `<var>_missing` reason siblings and the multiple-dichotomy `set_*`
	// convenience columns — which the document keeps in its own Derived
	// registry rather than in Variables. E5-S5 cross-checks this list
	// against that registry: a field that is unbound and NOT derived is a
	// column about to be silently dropped, which is the one outcome the
	// fold must refuse.
	UnboundFields []int

	// Status is the sidecar resolution the plan was built under.
	Status SidecarStatus

	// Synthesised reports that the dictionary was built from the `.pulse`
	// schema alone.
	Synthesised bool

	// Warnings are the diagnostics to surface, the sidecar resolution's own
	// warning first when it raised one.
	Warnings []*errors.CodedError
}

// ---------------------------------------------------------------------------
// Intermediate model
// ---------------------------------------------------------------------------

// outVar is one LOGICAL variable as the front-ends describe it and the
// emitter writes it.
type outVar struct {
	name      string // final name; wire bytes after applyCharsetWrite
	utf8Name  string // final name as UTF-8, retained for diagnostics
	shortName string
	longName  string // emitted in record 7/13 when non-empty
	label     string
	hasLabel  bool

	// width is 0 for numeric, else the LOGICAL byte width.
	//
	// It is the width BEFORE applyCharsetWrite, which recomputes it from
	// the encoded values. See widthDerived.
	width int

	// widthDerived says the width above was derived from the cohort rather
	// than recorded by a source file, which decides what the transcode pass
	// may do with it: a derived width is recomputed exactly from the
	// encoded values, a recorded one is only ever WIDENED to fit them.
	// Narrowing a recorded width would change a declaration the source
	// made; widening one cannot lose anything, because SPSS pads a value
	// out to the declared width and the read path trims that padding off.
	widthDerived bool

	print, write Format

	measure      int32
	align        int32
	displayWidth int32

	// missingCode is the record type 2 n_missing_values field and
	// missingSlots the raw 8-byte slots it counts. Held verbatim from the
	// source where there is one: re-deriving them from decoded floats would
	// put a round trip through the one place the format is exact.
	missingCode  int32
	missingSlots [][elementSize]byte

	// longMissing are record 7/22 slots, used instead of the record type 2
	// spec when the variable is a string wider than eight bytes.
	longMissing [][elementSize]byte

	// labels are the value labels, emitted as records 3/4 for a numeric or
	// a string of eight bytes or fewer and as record 7/21 otherwise.
	labels []outLabel

	segments []SegmentPlan

	// The data-plan half.
	enc          ValueEncoding
	field        int
	fieldName    string
	fieldType    encoding.FieldType
	categories   []CategoryCode
	setBit       int
	countedValue float64
}

// outLabel is one value label awaiting emission.
type outLabel struct {
	// numeric is the value slot of a numeric variable's label.
	numeric float64

	// text is the value of a string variable's label, unpadded.
	text string

	label string
}

// outFile is the whole dictionary as the front-ends describe it.
type outFile struct {
	fileLabel    string
	creationDate string
	creationTime string

	compression int32
	bias        float64
	caseCount   int64

	// weightName is the FINAL name of the weighting variable, "" when the
	// file is unweighted.
	weightName string

	sysmis       float64
	machineFloat *MachineFloat

	documents  []string
	productRaw *RawText
	fileAttrs  *RawText
	varAttrs   *RawText

	mrSets  []MRSet
	varSets []VarSet

	// charset is the encoder every string in this model has been put
	// through, and the declaration records 7/20 and 7/3 make. Never nil by
	// the time emitDictionary runs.
	charset *charsetEncoder

	// sourceCharset is the canonical name the SOURCE file's strings were
	// in, "" when there was no source. It is held beside charset only so
	// the verbatim-passthrough check can tell whether the retained record
	// 7/10, 7/17 and 7/18 bytes still match what this file declares.
	sourceCharset string

	// displayParams reports whether a record 7/11 is emitted at all. It is
	// false only when a sidecar records that the source carried none.
	displayParams bool

	vars []*outVar

	warnings []*errors.CodedError
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// BuildDictionary emits the `.sav` dictionary section for a cohort.
//
// It branches once, on [SidecarResolution.Synthesise]: recorded source
// metadata, or a default synthesised from the `.pulse` schema. Both branches
// converge on the same record emitter.
func BuildDictionary(req DictionaryRequest) (*DictionaryPlan, error) {
	if req.Schema == nil {
		return nil, errors.NewCodedError(errors.DATA_FILE,
			"spss.BuildDictionary: no .pulse schema; the writer cannot emit a dictionary without the cohort's own schema")
	}
	switch req.Compression {
	case compressionNone, compressionBytecode:
	case compressionZSAV:
		return nil, errors.NewCodedError(errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED,
			"spss.BuildDictionary: ZSAV emission is out of scope; write an uncompressed or bytecode-compressed file instead")
	default:
		return nil, errors.NewCodedError(errors.PULSE_SPSS_COMPRESSION_INVALID,
			"spss.BuildDictionary: compression flag "+strconv.Itoa(int(req.Compression))+
				" is not one the format defines; use 0 (uncompressed) or 1 (bytecode)")
	}

	var (
		f   *outFile
		err error
	)
	synth := req.Sidecar.Synthesise()
	if synth {
		f, err = synthesiseDictionary(req)
	} else {
		f, err = dictionaryFromSidecar(req)
	}
	if err != nil {
		return nil, err
	}

	// The transcode pass sits between the front-ends and the emitter, and
	// it is not optional: until it has run, every string in the model is
	// UTF-8 and every declared width is a count of UTF-8 bytes, which is
	// the file's charset only by coincidence. See charset_write.go.
	cs, err := resolveWriteCharset(req)
	if err != nil {
		return nil, err
	}
	f.charset = cs
	if err := applyCharsetWrite(f); err != nil {
		return nil, err
	}

	plan, err := emitDictionary(f)
	if err != nil {
		return nil, err
	}
	plan.Synthesised = synth
	plan.Status = SidecarStatusUnknown
	if req.Sidecar != nil {
		plan.Status = req.Sidecar.Status
		if req.Sidecar.Warning != nil {
			plan.Warnings = append([]*errors.CodedError{req.Sidecar.Warning}, plan.Warnings...)
		}
	}
	plan.UnboundFields = unboundFields(req.Schema, plan.Columns)
	return plan, nil
}

// unboundFields lists the schema fields no emitted variable is written from.
func unboundFields(s *encoding.Schema, cols []ColumnPlan) []int {
	bound := make([]bool, len(s.Fields))
	for _, c := range cols {
		if c.Field >= 0 && c.Field < len(bound) {
			bound[c.Field] = true
		}
	}
	out := []int{}
	for i, b := range bound {
		if !b {
			out = append(out, i)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Emission
// ---------------------------------------------------------------------------

// emitDictionary writes the dictionary section from the intermediate model.
//
// Record order is the format's own: the header, every record type 2, the
// record 3/4 value-label pairs, the record type 6 documents, the record type
// 7 extensions in ASCENDING SUBTYPE order, then the record 999 terminator.
//
// Ascending subtype order is not required by the format — a reader that
// depended on it would be broken, and ours does not — but it is what SPSS,
// PSPP and the internal/spsstest fixture generator all emit, which makes an
// emitted file diffable against a generated one byte for byte. That
// comparison is worth more than the freedom to reorder.
func emitDictionary(f *outFile) (*DictionaryPlan, error) {
	if err := assignIndices(f); err != nil {
		return nil, err
	}
	e := &dictEncoder{bo: binary.LittleEndian}

	plan := &DictionaryPlan{
		ByteOrder:         e.bo,
		Compression:       f.compression,
		Bias:              f.bias,
		Sysmis:            f.sysmis,
		CaseCount:         f.caseCount,
		CaseCount64Offset: -1,
		Warnings:          f.warnings,
	}

	elements := int32(0)
	for _, v := range f.vars {
		for _, s := range v.segments {
			elements += int32(s.Elements)
		}
	}
	plan.ElementCount = elements

	plan.CaseCountOffset = writeFileHeader(e, f, elements)
	for _, v := range f.vars {
		writeVariableRecord(e, v)
	}
	for _, v := range f.vars {
		writeValueLabelRecord(e, v)
	}
	writeDocumentRecord(e, f)
	caseCount64At := writeExtensionRecords(e, f)
	plan.CaseCount64Offset = caseCount64At

	// Record 999 closes the dictionary. The second int32 is a filler the
	// spec defines as zero.
	e.i32(recTypeTerminator)
	e.i32(0)

	plan.Bytes = e.buf
	plan.Columns = make([]ColumnPlan, 0, len(f.vars))
	for _, v := range f.vars {
		plan.Columns = append(plan.Columns, v.columnPlan())
	}
	return plan, nil
}

// columnPlan projects an outVar onto the public plan entry.
func (v *outVar) columnPlan() ColumnPlan {
	els := 0
	for _, s := range v.segments {
		els += s.Elements
	}
	idx := int32(0)
	if len(v.segments) > 0 {
		idx = v.segments[0].Index
	}
	return ColumnPlan{
		// The UTF-8 name, not the wire one: see ColumnPlan.Name.
		Name:         orDefault(v.utf8Name, v.name),
		ShortName:    v.shortName,
		Index:        idx,
		Elements:     els,
		Width:        v.width,
		Segments:     append([]SegmentPlan(nil), v.segments...),
		PrintFormat:  v.print,
		WriteFormat:  v.write,
		Encoding:     v.enc,
		Field:        v.field,
		FieldName:    v.fieldName,
		FieldType:    v.fieldType,
		Categories:   append([]CategoryCode(nil), v.categories...),
		SetBit:       v.setBit,
		CountedValue: v.countedValue,
	}
}

// assignIndices numbers every physical segment by dictionary ELEMENT
// position, 1-based, which is what the format counts.
func assignIndices(f *outFile) error {
	if len(f.vars) == 0 {
		return errors.NewCodedError(errors.PULSE_SPSS_DICT_INVALID,
			"spss: the dictionary would declare no variables; a system file must carry at least one")
	}
	next := int32(1)
	for _, v := range f.vars {
		if len(v.segments) == 0 {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_DICT_INVALID,
				"spss: variable "+strconv.Quote(v.name)+" plans no physical segments",
				map[string]any{errors.DetailSPSSVariable: v.name})
		}
		for i := range v.segments {
			v.segments[i].Index = next
			next += int32(v.segments[i].Elements)
		}
	}
	return nil
}

// writeFileHeader emits the 176-byte header record and returns the byte
// offset of its ncases field.
func writeFileHeader(e *dictEncoder, f *outFile, elements int32) int {
	// "$FL2" covers both encodings this writer emits; "$FL3" marks ZSAV,
	// which it does not. The magic is derived from the compression choice
	// rather than declared, so the two can never disagree.
	e.ascii(magicSAV, 4)
	e.ascii(writerProductName, 60)
	e.i32(writerLayoutCode)
	e.i32(elements)
	e.i32(f.compression)
	e.i32(weightIndexOf(f))
	ncasesAt := e.at()
	e.i32(headerCaseCount(f.caseCount))
	e.f64(f.bias)
	e.ascii(orDefault(f.creationDate, writerCreationDate), 9)
	e.ascii(orDefault(f.creationTime, writerCreationTime), 8)
	e.ascii(f.fileLabel, 64)
	e.zeros(3)
	return ncasesAt
}

// headerCaseCount narrows a case count onto the header's int32 field.
//
// A count past the int32 ceiling is written as -1 ("unknown") rather than
// truncated, because the record 7/16 64-bit count carries the real number
// and a wrapped int32 would be a plausible wrong answer.
func headerCaseCount(n int64) int32 {
	if n < 0 || n > math.MaxInt32 {
		return -1
	}
	return int32(n)
}

// weightIndexOf resolves the header weight_index: the 1-based ELEMENT index
// of the weighting variable's first element, 0 when unweighted.
//
// It resolves by NAME rather than re-emitting the source's recorded index,
// because the emitted layout is not required to reproduce the source's
// element numbering and an index that once pointed at the weight would then
// point at whatever now sits there.
//
// A name that matches nothing yields 0, an UNWEIGHTED file. That case only
// arises when the source's own weight_index named no variable's first
// element — the import records "" for the name precisely then — so there is
// no weight to lose; re-emitting the raw index instead would point the new
// file's weight at an unrelated column.
func weightIndexOf(f *outFile) int32 {
	if f.weightName == "" {
		return 0
	}
	for _, v := range f.vars {
		if strings.EqualFold(v.name, f.weightName) || strings.EqualFold(v.shortName, f.weightName) {
			return v.segments[0].Index
		}
	}
	return 0
}

// writeVariableRecord emits a variable's record type 2 for every physical
// segment, each followed by the continuation records its width needs.
//
// The variable label and the missing-value specification ride the FIRST
// segment only. A later segment carries neither: the spec says a
// continuation's remaining fields are to be ignored, and a segment head that
// repeated the label would have a second reader apply it twice.
func writeVariableRecord(e *dictEncoder, v *outVar) {
	for i, seg := range v.segments {
		typeCode := int32(seg.Width)
		hasLabel := int32(0)
		if i == 0 && v.hasLabel {
			hasLabel = 1
		}
		missing := int32(0)
		if i == 0 {
			missing = v.missingCode
		}

		// A trailing very-long-string segment carries its OWN A format,
		// derived from its declared width, which is what SPSS writes:
		// A255 for each 255-byte segment and A<remainder> for the last.
		// The head keeps the format the source recorded, so nothing
		// re-derives a format that was actually stated.
		print, write := v.print, v.write
		if i > 0 {
			print = Format{Code: fmtA, Width: stringFormatWidth(seg.Width)}
			write = print
		}

		e.i32(recTypeVariable)
		e.i32(typeCode)
		e.i32(hasLabel)
		e.i32(missing)
		e.i32(print.pack())
		e.i32(write.pack())
		e.ascii(seg.Name, shortNameLen)

		if hasLabel == 1 {
			// label_len carries the TRUE byte length; the text is padded
			// out to a multiple of 4 with zeros. A reader must slice by
			// label_len, never by the padded size.
			e.i32(int32(len(v.label)))
			e.raw([]byte(v.label))
			e.zeros(roundUp(len(v.label), 4) - len(v.label))
		}
		if missing != 0 {
			// The slots come AFTER the label, not before: the label is
			// length-prefixed and 4-byte aligned, so swapping the two
			// desynchronises every following record on the first variable
			// that carries both.
			for _, slot := range v.missingSlots {
				e.raw(slot[:])
			}
		}

		for c := 1; c < seg.Elements; c++ {
			e.i32(recTypeVariable)
			e.i32(typeStringContinuation)
			e.i32(0) // has_var_label
			e.i32(0) // n_missing_values
			e.i32(0) // print
			e.i32(0) // write
			e.ascii("", shortNameLen)
		}
	}
}

// writeValueLabelRecord emits one record type 3 and the record type 4 that
// must follow it immediately, for a variable whose labels fit the 8-byte
// value slot.
//
// One pair per variable, never a shared set. The format lets several
// variables share a record type 3 and SPSS exploits that, but sharing is a
// SIZE optimisation and nothing else: a reader binds by the record type 4's
// index list either way, and one-pair-per-variable removes a class of bug
// where two variables that merely happen to agree today get welded together.
//
// A wider string's labels cannot ride an 8-byte slot at all and go out as
// record 7/21 instead — see writeLongStringValueLabels.
func writeValueLabelRecord(e *dictEncoder, v *outVar) {
	if len(v.labels) == 0 || v.width > maxShortStringWidth {
		return
	}
	e.i32(recTypeValueLabel)
	e.i32(int32(len(v.labels)))
	for _, l := range v.labels {
		if v.width == 0 {
			e.f64(l.numeric)
		} else {
			// A short-string value occupies the full eight bytes,
			// space-padded, regardless of the variable's declared width.
			e.ascii(l.text, elementSize)
		}
		// A one-byte length, the text, then zero padding so the two
		// together fill a multiple of eight.
		e.raw([]byte{byte(len(l.label))})
		e.raw([]byte(l.label))
		e.zeros(roundUp(len(l.label)+1, elementSize) - (len(l.label) + 1))
	}

	e.i32(recTypeLabelVars)
	e.i32(1)
	e.i32(v.segments[0].Index)
}

// writeDocumentRecord emits record type 6: a line count, then that many
// fixed-width 80-byte lines.
func writeDocumentRecord(e *dictEncoder, f *outFile) {
	if len(f.documents) == 0 {
		return
	}
	e.i32(recTypeDocument)
	e.i32(int32(len(f.documents)))
	for _, line := range f.documents {
		e.ascii(line, documentLineLen)
	}
}

// writeExtensionRecords emits every record type 7, ascending by subtype, and
// returns the byte offset of the record 7/16 case count (or -1).
func writeExtensionRecords(e *dictEncoder, f *outFile) int {
	// 7/3 machine integer info. Always emitted: it is where the character
	// code lives, and the endianness field must state THIS file's order
	// rather than the source's.
	mi := &dictEncoder{bo: e.bo}
	for _, v := range []int32{0, 0, 0, 0, writerFloatingPointRep, writerCompressionCode,
		writerEndiannessCode, f.charset.declaredCode} {
		mi.i32(v)
	}
	writeExtension(e, extMachineInteger, 4, mi.buf)

	// 7/4 machine float info, only when there is something to say. A
	// synthesised dictionary states its own sentinels; a sidecar-driven one
	// re-emits the source's 7/4 if it had one and stays silent if it did
	// not, because the reader treats an absent 7/4 as "the spec default"
	// and that is exactly what the source meant.
	if f.machineFloat != nil {
		mf := &dictEncoder{bo: e.bo}
		mf.f64(float64(f.machineFloat.Sysmis))
		mf.f64(float64(f.machineFloat.Highest))
		mf.f64(float64(f.machineFloat.Lowest))
		writeExtension(e, extMachineFloat, 8, mf.buf)
	}

	// 7/5, 7/7 and 7/19 carry set definitions. Each set goes back out on
	// the subtype it came in on; the variable sets ride 7/5 with them.
	if text := renderSetRecord(f, extVariableSets); text != "" {
		writeExtension(e, extVariableSets, 1, []byte(text))
	}
	if text := renderSetRecord(f, extMRSets); text != "" {
		writeExtension(e, extMRSets, 1, []byte(text))
	}

	if f.productRaw != nil {
		writeExtension(e, extProductInfo, 1, f.productRaw.Raw)
	}

	// 7/11 is POSITIONAL over the physical record type 2 variables, not
	// over the logical ones: a reader applies it before the very-long-string
	// fold, so a three-segment string needs three entries.
	if f.displayParams {
		dp := &dictEncoder{bo: e.bo}
		for _, v := range f.vars {
			for range v.segments {
				dp.i32(v.measure)
				dp.i32(v.displayWidth)
				dp.i32(v.align)
			}
		}
		writeExtension(e, extDisplayParams, 4, dp.buf)
	}

	// 7/13 is the tab-separated SHORT=Long map, keyed by the HEAD segment's
	// short name.
	if text := renderLongNames(f); text != "" {
		writeExtension(e, extLongNames, 1, []byte(text))
	}

	// 7/14 declares the logical width of every string wider than 255 bytes,
	// keyed by its head segment's short name.
	if text := renderVeryLongStrings(f); text != "" {
		writeExtension(e, extVeryLongStrings, 1, []byte(text))
	}

	caseCount64At := -1
	if f.caseCount >= 0 {
		nc := &dictEncoder{bo: e.bo}
		nc.i64(1) // the spec's constant leading field, and an endianness probe
		caseCount64At = e.at() + 16 + 8
		nc.i64(f.caseCount)
		writeExtension(e, extNumberOfCases, 8, nc.buf)
	}

	if f.fileAttrs != nil {
		writeExtension(e, extFileAttributes, 1, f.fileAttrs.Raw)
	}
	if f.varAttrs != nil {
		writeExtension(e, extVarAttributes, 1, f.varAttrs.Raw)
	}
	if text := renderSetRecord(f, extMRSetsExtended); text != "" {
		writeExtension(e, extMRSetsExtended, 1, []byte(text))
	}

	// 7/20 names the charset THESE bytes are in, which since E5-S4 is the
	// charset the source declared — in the source's own spelling, because
	// the record is a quotation and normalising it would make a
	// byte-comparable round trip impossible for no gain.
	writeExtension(e, extCharacterEncoding, 1, []byte(f.charset.declaredName))

	if payload := renderLongStringValueLabels(e.bo, f); len(payload) > 0 {
		writeExtension(e, extLongStringValueLabels, 1, payload)
	}
	if payload := renderLongStringMissing(e.bo, f); len(payload) > 0 {
		writeExtension(e, extLongStringMissing, 1, payload)
	}
	return caseCount64At
}

// writeExtension emits one record type 7. The count is DERIVED from the
// payload length rather than declared, so the two can never disagree.
func writeExtension(e *dictEncoder, subtype, size int32, payload []byte) {
	e.i32(recTypeExtension)
	e.i32(subtype)
	e.i32(size)
	e.i32(int32(len(payload)) / size)
	e.raw(payload)
}

// renderLongNames builds the record 7/13 payload: TAB-separated SHORT=Long
// pairs, no trailing tab.
func renderLongNames(f *outFile) string {
	var parts []string
	for _, v := range f.vars {
		if v.longName == "" {
			continue
		}
		parts = append(parts, v.shortName+"="+v.longName)
	}
	return strings.Join(parts, "\t")
}

// renderVeryLongStrings builds the record 7/14 payload: NAME=WIDTH entries
// separated by a NUL and a tab, with a trailing separator.
func renderVeryLongStrings(f *outFile) string {
	var b strings.Builder
	for _, v := range f.vars {
		if len(v.segments) < 2 {
			continue
		}
		b.WriteString(v.shortName)
		b.WriteString("=")
		b.WriteString(strconv.Itoa(v.width))
		b.WriteString(vlsEntrySeparator)
	}
	return b.String()
}

// renderSetRecord builds the text payload of one of the three set-carrying
// subtypes. Subtype 5 additionally carries the variable sets.
//
// The grammar, from the specification:
//
//	name '=' ( 'C' ' ' | 'D' counted | 'E' ' ' ('1'|'11') ' ' counted )
//	         ' ' counted-label ( ' ' varname )* '\n'
//
// where a counted string is a decimal byte length, a space, and that many
// bytes. Member names are SHORT names: 7/5, 7/7 and 7/19 all key by them.
func renderSetRecord(f *outFile, subtype int32) string {
	var b strings.Builder
	for _, set := range f.mrSets {
		st := set.Subtype
		if st == 0 {
			st = extMRSets
		}
		if st != subtype {
			continue
		}
		if len(set.Variables) == 0 || !strings.HasPrefix(set.Name, "$") {
			// A set with no members, or without the '$' that is the only
			// thing distinguishing a response set from a variable set
			// inside record 7/5, is not expressible. It was recorded
			// verbatim by the import and is dropped rather than emitted as
			// something a reader would misclassify.
			continue
		}
		b.WriteString(set.Name)
		b.WriteString("=")
		switch set.Kind {
		case MRSetKindCategory:
			b.WriteString("C ")
		case MRSetKindDichotomy:
			counted := ""
			if set.CountedValue != nil {
				counted = *set.CountedValue
			}
			if set.Extended {
				b.WriteString("E ")
				if set.LabelFromVariableLabel {
					b.WriteString("11 ")
				} else {
					b.WriteString("1 ")
				}
			} else {
				b.WriteString("D")
			}
			b.WriteString(countedString(counted))
			b.WriteString(" ")
		default:
			continue
		}
		b.WriteString(countedString(set.Label))
		for _, name := range set.Variables {
			b.WriteString(" " + name)
		}
		b.WriteString("\n")
	}

	if subtype == extVariableSets {
		for _, vs := range f.varSets {
			if vs.Name == "" || strings.HasPrefix(vs.Name, "$") || len(vs.Variables) == 0 {
				continue
			}
			b.WriteString(vs.Name + "=")
			for _, name := range vs.Variables {
				b.WriteString(" " + name)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// countedString renders the format's counted-string form: a decimal byte
// length, a space, then the bytes.
func countedString(s string) string { return strconv.Itoa(len(s)) + " " + s }

// renderLongStringValueLabels builds the record 7/21 payload for every
// string variable too wide to carry its labels in records 3/4.
//
// Layout, repeated:
//
//	int32 name_len; byte name[name_len]
//	int32 var_width
//	int32 n_labels
//	  int32 value_len; byte value[value_len]
//	  int32 label_len; byte label[label_len]
//
// The name is the variable's FINAL name. ReadStat refuses a file whose 7/21
// entry names a variable by its short name when a long name exists, so
// anything else here produces files haven, pyreadstat and the rest of the
// ReadStat-backed world reject. Pulse's own reader tolerates both, long
// first — that tolerance is a reader's licence, not a writer's.
func renderLongStringValueLabels(bo binary.ByteOrder, f *outFile) []byte {
	e := &dictEncoder{bo: bo}
	for _, v := range f.vars {
		if v.width <= maxShortStringWidth || len(v.labels) == 0 {
			continue
		}
		e.counted(v.name)
		e.i32(int32(v.width))
		e.i32(int32(len(v.labels)))
		for _, l := range v.labels {
			// The value is padded to the variable's full declared width,
			// which is what SPSS writes and what the reader trims back.
			e.counted(padRight(l.text, v.width))
			e.counted(l.label)
		}
	}
	return e.buf
}

// renderLongStringMissing builds the record 7/22 payload.
//
// Layout, repeated:
//
//	int32 name_len; byte name[name_len]
//	byte  n_missing_values          (1..3)
//	  int32 value_len; byte value[value_len]    (value_len is always 8)
//
// The eight-byte slot is the format's, not a truncation invented here: SPSS
// compares only the first eight bytes of a long string against a missing
// value, which is why a record type 2 cannot carry one for a wider string at
// all. The name is the FINAL name, for the ReadStat reason above.
func renderLongStringMissing(bo binary.ByteOrder, f *outFile) []byte {
	e := &dictEncoder{bo: bo}
	for _, v := range f.vars {
		if len(v.longMissing) == 0 {
			continue
		}
		n := len(v.longMissing)
		if n > 3 {
			n = 3
		}
		e.counted(v.name)
		e.raw([]byte{byte(n)})
		for _, slot := range v.longMissing[:n] {
			e.i32(elementSize)
			e.raw(slot[:])
		}
	}
	return e.buf
}

// ---------------------------------------------------------------------------
// Encoder
// ---------------------------------------------------------------------------

// dictEncoder appends fixed-width fields in the file's byte order.
type dictEncoder struct {
	buf []byte
	bo  binary.ByteOrder
}

// at returns the offset the next field will be written at.
func (e *dictEncoder) at() int { return len(e.buf) }

func (e *dictEncoder) i32(v int32) {
	var b [4]byte
	e.bo.PutUint32(b[:], uint32(v))
	e.buf = append(e.buf, b[:]...)
}

func (e *dictEncoder) i64(v int64) {
	var b [8]byte
	e.bo.PutUint64(b[:], uint64(v))
	e.buf = append(e.buf, b[:]...)
}

func (e *dictEncoder) f64(v float64) {
	var b [8]byte
	e.bo.PutUint64(b[:], math.Float64bits(v))
	e.buf = append(e.buf, b[:]...)
}

func (e *dictEncoder) raw(b []byte) { e.buf = append(e.buf, b...) }

func (e *dictEncoder) zeros(n int) {
	for i := 0; i < n; i++ {
		e.buf = append(e.buf, 0)
	}
}

// ascii writes s space-padded (or truncated) to exactly n bytes, which is how
// every fixed-width text field in the format is stored.
func (e *dictEncoder) ascii(s string, n int) {
	if len(s) > n {
		s = s[:n]
	}
	e.buf = append(e.buf, s...)
	for i := len(s); i < n; i++ {
		e.buf = append(e.buf, ' ')
	}
}

// counted writes the format's counted-string form used inside the record
// type 7 payloads: an int32 byte length followed by the bytes.
func (e *dictEncoder) counted(s string) {
	e.i32(int32(len(s)))
	e.raw([]byte(s))
}

// pack encodes a format as the wire's 0x00TTWWDD int32.
func (f Format) pack() int32 {
	return int32(uint32(f.Code)<<16 | uint32(uint8(f.Width))<<8 | uint32(uint8(f.Decimals)))
}

// padRight space-pads s out to n bytes, leaving it alone when it is already
// at least that wide. Spaces, not NULs: 0x20 is what SPSS pads a string
// datum with.
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// orDefault returns s, or fallback when s is empty.
func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
