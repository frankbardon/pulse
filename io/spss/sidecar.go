package spss

// The metadata sidecar: everything an SPSS dictionary declares that a
// `.pulse` file has nowhere to put.
//
// # Why a sidecar at all
//
// `.pulse` is a 9-byte header, a schema block, inline dictionaries and
// fixed-width records. It has no slot for a measure level, a print
// format, an SPSS value code, a missing-value specification, a declared
// string width, a multiple-response set, a document record or a source
// charset. Every one of those is load-bearing for a round trip, and
// discarding them would be exactly the quiet degradation this reader
// exists to prevent.
//
// A schema metadata block INSIDE `.pulse` was considered and deferred,
// not rejected: it would delete this file and the "sidecar walked away
// from its cohort" risk with it, but it is a genuine FormatVersion
// 0x01 -> 0x02 bump whose blast radius is every Pulse user rather than
// only SPSS ones. The door is deliberately held open, and that is what
// shapes the document below.
//
// # The flat / liftable constraint
//
// [Document] is split in two, and the split IS the contract:
//
//   - [Fingerprint] is the only file-bound part. It describes the
//     relationship between this file and a cohort that lives somewhere
//     else, so it is meaningless in-band — a block inside the cohort
//     cannot usefully hash the cohort containing it.
//   - [Payload] is self-contained: no file paths, no byte offsets into
//     the `.sav`, no references to anything outside itself. Lifting it
//     into a `.pulse` schema metadata block later is "serialise Payload
//     instead of Document" and nothing more. Nothing in Payload may
//     acquire a dependence on being a separate file.
//
// "Flat" here means self-contained and reference-free, not depth-one:
// a dictionary of this shape cannot be expressed without nesting, and
// pretending otherwise would produce a key-mangled document that is
// harder to lift, not easier.
//
// # The single most important payload
//
// The code <-> label <-> Pulse dictionary ID triple, one [Category] per
// dictionary entry per categorical column. A Pulse dictionary ID is
// POSITIONAL (0, 1, 2, ...) while an SPSS code is ARBITRARY (1, 2, 5, 9
// or -1, 0, 1). The cohort's own dictionary holds the CODES — E2-S6's
// decision, because two SPSS codes may share one label and a
// label-keyed dictionary would collapse them — so this document is the
// only place the LABELS live, and the only place the pairing between
// the two is written down. Lose it and an export has to invent codes,
// at which point downstream SPSS syntax reading `IF q1 EQ 5` silently
// addresses a different category.
//
// Both degenerate halves of the triple are representable rather than
// flattened away: `labelled` is false for a value the data carried that
// no record type 3 declared (appended in first-seen order), `observed`
// is false for a declared label no case ever used, and two entries may
// share an `id` when two distinct source values collapsed to one
// dictionary text.
//
// # Charset
//
// [Charset.DeclaredName] is the file's OWN spelling — a file that said
// `cp1252` says `cp1252` here, not `windows-1252`. The write path
// re-encodes against the declaration, not against the normalised name a
// reader resolved it to, and not against a caller's WithCharset
// override, which changes only decoding.
//
// # Free-form extension text
//
// Records 7/10 (product info), 7/17 (data-file attributes) and 7/18
// (per-variable attributes) are captured verbatim and never
// interpreted. 7/17 and 7/18 are held in SEPARATE slots because they
// are separate records saying different things — one describes the
// file, the other describes variables — and merging them would lose
// which was which.
//
// Their payloads are also the only strings in this document that have
// NOT been through the dictionary-wide transcode, because the extension
// walk retains extension bytes verbatim. Handing raw source-charset
// bytes to encoding/json would let it substitute U+FFFD for every
// undecodable byte, silently. So [RawText] carries the bytes as the
// authoritative record and offers decoded text only when the decode
// succeeded cleanly.

import (
	"encoding/hex"
	stdjson "encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// SidecarSuffix is appended to a cohort's path to form its SPSS
// metadata sidecar filename, following the imports.Sidecar convention:
// "data.pulse" + SidecarSuffix == "data.pulse.spss.json".
//
// It is deliberately NOT imports.SidecarSuffix (".meta.json"). A
// managed import writes that file for the same cohort, so sharing the
// suffix would have one artefact overwrite the other; the two carry
// unrelated documents and both must survive.
const SidecarSuffix = ".spss.json"

// SidecarFormatVersion is the version of the [Document] shape. It is
// the document's own version and has nothing to do with
// encoding.FormatVersion (the `.pulse` byte format) or the `--json`
// envelope's format_version.
//
// Additive slots do not bump it; a rename or a removal does.
const SidecarFormatVersion = 1

// SidecarKind identifies the producing adapter, so a reader can reject
// a document written by something else before trusting its shape.
const SidecarKind = "spss"

// SidecarPath derives the deterministic sidecar path for a cohort.
// Pure, no filesystem access — the mirror of encoding.SidecarIndexPath.
func SidecarPath(cohortPath string) string { return cohortPath + SidecarSuffix }

// ---------------------------------------------------------------------------
// Document
// ---------------------------------------------------------------------------

// Document is the whole sidecar file.
//
// Only Payload is liftable into a future `.pulse` schema metadata
// block; FormatVersion, Kind and Fingerprint are the envelope that
// makes it a standalone file. See the package-level commentary above.
type Document struct {
	// FormatVersion is SidecarFormatVersion at write time.
	FormatVersion int `json:"format_version"`

	// Kind is SidecarKind.
	Kind string `json:"kind"`

	// Fingerprint binds the document to the cohort it describes.
	Fingerprint Fingerprint `json:"fingerprint"`

	// Payload is the self-contained metadata.
	Payload Payload `json:"payload"`
}

// Fingerprint is the staleness block, modelled directly on
// encoding.Index: a 32-byte SHA-256 plus a size and a modification
// time cheap enough to check on every read.
//
// It fingerprints the `.pulse` COHORT, not the source `.sav`. The
// cohort is what the document describes and what a consumer holds; the
// `.sav` may be long gone, and re-importing it would produce a new
// cohort with its own fingerprint anyway.
//
// The read path (E5-S1) compares SourceSize and SourceModTime, an O(1)
// stat; SHA256 is the authoritative recompute for a verify pass. A
// stale sidecar is an ERROR, not a warning — applying a stale
// dictionary to changed data yields a file that looks authoritative and
// is wrong. An ABSENT sidecar is only a warning: a cohort that was
// never SPSS-derived correctly has none.
type Fingerprint struct {
	// SHA256 is the 32-byte SHA-256 digest of the cohort's full bytes,
	// hex-encoded to 64 characters. Decode with [Fingerprint.Digest].
	SHA256 string `json:"sha256"`

	// SourceSize is the cohort's byte length at write time.
	SourceSize uint64 `json:"source_size"`

	// SourceModTime is the cohort's modification time at write time, as
	// Unix nanoseconds.
	SourceModTime int64 `json:"source_mod_time"`
}

// Digest decodes SHA256 back into the 32-byte digest, reporting
// whether the field held a well-formed one.
func (f Fingerprint) Digest() (encoding.Fingerprint, bool) {
	var out encoding.Fingerprint
	raw, err := hex.DecodeString(f.SHA256)
	if err != nil || len(raw) != encoding.FingerprintSize {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

// Payload is the liftable half: every SPSS dictionary element with no
// `.pulse` home, and nothing that depends on being a separate file.
type Payload struct {
	// Source is the file-header and geometry metadata.
	Source Source `json:"source"`

	// Charset is the file's own encoding declaration.
	Charset Charset `json:"charset"`

	// Weight is the weighting variable, nil for an unweighted file.
	Weight *Weight `json:"weight,omitempty"`

	// Documents are the record type 6 lines in file order, each still
	// the full fixed-width 80-byte field. Untrimmed on purpose: which
	// trailing spaces are padding and which are the author's is not
	// knowable, and guessing would not round-trip.
	Documents []string `json:"documents,omitempty"`

	// ProductInfo is record 7/10, extra product identification.
	ProductInfo *RawText `json:"product_info,omitempty"`

	// FileAttributes is record 7/17: attributes of the FILE.
	// Deliberately distinct from VariableAttributes.
	FileAttributes *RawText `json:"file_attributes,omitempty"`

	// VariableAttributes is record 7/18: attributes of VARIABLES.
	// Deliberately distinct from FileAttributes.
	VariableAttributes *RawText `json:"variable_attributes,omitempty"`

	// MultipleResponseSets are the records 7/5, 7/7 and 7/19 set
	// definitions. E4-S5 owns what is DONE with them; they are recorded
	// here regardless, because an export has to write them back.
	MultipleResponseSets []MRSet `json:"multiple_response_sets,omitempty"`

	// VariableSets are the record 7/5 display groupings. They ride the
	// same record as multiple-response sets and are NOT response sets;
	// they have no Pulse home at all and are sidecar-only.
	VariableSets []VarSet `json:"variable_sets,omitempty"`

	// VeryLongStrings are the record 7/14 declarations: one
	// (head short name, logical byte width) pair per very long string.
	// The per-variable physical segmentation is on Variable.VeryLongString.
	VeryLongStrings []VLSDeclaration `json:"very_long_strings,omitempty"`

	// Variables are the columns in cohort order, one per Pulse field.
	Variables []Variable `json:"variables"`

	// Derived is the reserved registry slot for columns this import
	// SYNTHESISED rather than read: the `<var>_missing` siblings
	// (E4-S2 / E4-S3) and the multiple-dichotomy `set_*` convenience
	// columns (E4-S4). It is empty here because E4-S1 derives nothing;
	// the slot exists so those stories add entries rather than
	// restructure the document, and so an export can already tell
	// "column absent from Variables" from "column absent from the
	// source". Additive: entries may gain fields without a version bump.
	Derived []Derived `json:"derived,omitempty"`
}

// Source is the file-header metadata plus the case geometry.
type Source struct {
	// Magic is "$FL2" (a plain system file) or "$FL3" (ZSAV).
	Magic string `json:"magic"`

	// ProductName is the 60-byte header prod_name field.
	ProductName string `json:"product_name"`

	// FileLabel is the 64-byte header file label.
	FileLabel string `json:"file_label,omitempty"`

	// CreationDate is the header "dd mmm yy" field.
	CreationDate string `json:"creation_date,omitempty"`

	// CreationTime is the header "hh:mm:ss" field.
	CreationTime string `json:"creation_time,omitempty"`

	// ByteOrder is "little" or "big".
	ByteOrder string `json:"byte_order"`

	// LayoutCode is the header layout probe (2 or 3) the byte order was
	// derived from.
	LayoutCode int32 `json:"layout_code"`

	// Compression is "none", "bytecode" or "zsav".
	Compression string `json:"compression"`

	// CompressionBias is the header bias field, normally 100. It is
	// written even for an uncompressed file, so it is recorded
	// unconditionally.
	CompressionBias Float `json:"compression_bias"`

	// NominalCaseSize is the header's CLAIM about 8-byte elements per
	// case. It is a claim, not a fact.
	NominalCaseSize int32 `json:"nominal_case_size"`

	// ElementCount is the AUTHORITATIVE elements-per-case, counted from
	// the record type 2 stream. Both are kept so a consumer can see a
	// disagreement rather than inherit a resolution.
	ElementCount int32 `json:"element_count"`

	// CaseCount is the number of cases the reader actually walked.
	CaseCount int64 `json:"case_count"`

	// DeclaredCaseCount is what the file claimed: the record 7/16
	// 64-bit count where present, else the header's int32, which is -1
	// when the writer did not know.
	DeclaredCaseCount int64 `json:"declared_case_count"`

	// Sysmis is the system-missing sentinel in force, which is the spec
	// default (-DBL_MAX) unless record 7/4 declared a coherent one.
	Sysmis Float `json:"sysmis"`

	// MachineFloat is the record 7/4 sentinel triple, nil when the file
	// carries no 7/4 — the common case.
	MachineFloat *MachineFloat `json:"machine_float,omitempty"`
}

// MachineFloat is the record 7/4 payload.
type MachineFloat struct {
	Sysmis  Float `json:"sysmis"`
	Highest Float `json:"highest"`
	Lowest  Float `json:"lowest"`
}

// Charset is the file's character-encoding declaration.
//
// DeclaredName is the file's OWN spelling and is what a write path
// re-encodes against. ResolvedName is what this reader decoded with,
// recorded so a mismatch is visible, never so it can be substituted.
type Charset struct {
	// DeclaredName is the record 7/20 name exactly as spelled, "" when
	// the file carries no 7/20.
	DeclaredName string `json:"declared_name,omitempty"`

	// DeclaredCode is the record 7/3 character_code, 0 when absent.
	DeclaredCode int32 `json:"declared_code,omitempty"`

	// Declared reports whether the file said anything at all. A file
	// that did not is being read by assumption, and the write path must
	// know the difference — which "" alone cannot tell it, since a
	// declaration could in principle be blank.
	Declared bool `json:"declared"`

	// Overridden reports that a caller's WithCharset replaced the
	// declaration for DECODING. The declaration above is untouched.
	Overridden bool `json:"overridden"`

	// ResolvedName is the canonical name actually decoded with.
	ResolvedName string `json:"resolved_name"`
}

// Weight is the header weighting variable.
type Weight struct {
	// Index is the 1-based dictionary ELEMENT index the header stores.
	Index int32 `json:"index"`

	// Variable is the Pulse field name it resolves to, "" when the
	// index names no variable's first element.
	Variable string `json:"variable,omitempty"`
}

// RawText is a free-form extension payload captured verbatim.
//
// Raw is authoritative and always present; Text is a convenience that
// is omitted whenever the bytes did not decode cleanly under the file's
// charset. A consumer re-emitting the record MUST use Raw.
type RawText struct {
	// Subtype is the record type 7 subtype: 10, 17 or 18.
	Subtype int32 `json:"subtype"`

	// Raw is the payload bytes, base64-encoded by encoding/json.
	Raw []byte `json:"raw"`

	// Text is the decoded payload, omitted when undecodable.
	Text string `json:"text,omitempty"`
}

// MRSet is one multiple-response set definition.
//
// Kind is the JSON discriminant standing in for the two Go types the
// parser keeps apart, and CountedValue is a POINTER so a consumer that
// reads it without checking Kind gets nil rather than a meaningless
// empty string: a multiple-category set has no counted value at all.
type MRSet struct {
	// Name is the set name, including its leading '$'.
	Name string `json:"name"`

	// Kind is "dichotomy" or "category".
	Kind string `json:"kind"`

	// Label is the set label, possibly empty.
	Label string `json:"label,omitempty"`

	// Subtype is the extension subtype the definition came from: 5, 7
	// or 19.
	Subtype int32 `json:"subtype"`

	// Variables names the member variables by SHORT name, in file
	// order — the name the record itself carries.
	Variables []string `json:"variables"`

	// CountedValue is the value meaning "selected", held verbatim as
	// the text the record carried; the wire form does not say whether
	// it is a number or a string. Non-nil only for a dichotomy set.
	CountedValue *string `json:"counted_value,omitempty"`

	// LabelFromVariableLabel is the subtype 19 'E' form's "11" label
	// source: the label comes from the first member's variable label.
	LabelFromVariableLabel bool `json:"label_from_variable_label,omitempty"`

	// Extended records the 'E' type code, which only subtype 19 writes.
	Extended bool `json:"extended,omitempty"`
}

// VarSet is a record 7/5 display grouping. Not a response set.
type VarSet struct {
	Name      string   `json:"name"`
	Variables []string `json:"variables"`
}

// VLSDeclaration is one record 7/14 entry.
type VLSDeclaration struct {
	// Name is the HEAD physical variable's short name; 7/14 keys by
	// short name.
	Name string `json:"name"`

	// Width is the LOGICAL byte width, 256..32767.
	Width int `json:"width"`
}

// VLSLayout is one very long string's physical segmentation, retained
// so a write path can reproduce the source's own layout rather than
// re-deriving one.
type VLSLayout struct {
	// Width is the LOGICAL byte width and the sum of every segment's
	// Content.
	Width int `json:"width"`

	// Segments are the physical variables in file order, head first.
	// Always at least two.
	Segments []VLSSegment `json:"segments"`
}

// VLSSegment is one PHYSICAL variable of a very long string.
type VLSSegment struct {
	// Name is the physical variable's record type 2 short name.
	Name string `json:"name"`

	// Width is the physical variable's DECLARED byte width.
	Width int `json:"width"`

	// Content is how many bytes of the LOGICAL value this segment
	// carries. NOT Width: a non-final segment's last three declared
	// bytes are unused padding.
	Content int `json:"content"`

	// Elements is how many 8-byte data elements the physical variable
	// occupies.
	Elements int `json:"elements"`
}

// Format is a print or write format specification, carried verbatim.
// It is what lets an export reconstruct the display format, and for a
// TIME / DTIME / precision-downcast column it is the only record of
// what the raw seconds mean.
type Format struct {
	// Code is the SPSS format type code: 1 = A, 5 = F, 20 = DATE, ...
	Code uint8 `json:"code"`

	// Width is the field width in characters.
	Width int `json:"width"`

	// Decimals is the number of decimal places.
	Decimals int `json:"decimals"`
}

// Variable is one source variable's full record.
type Variable struct {
	// Name is the Pulse field name: the record 7/13 long name where the
	// file declares one, else the short name.
	Name string `json:"name"`

	// ShortName is the ORIGINAL 8-byte record type 2 name. Retained
	// because the record 7/13 mapping is short -> long and an export
	// has to write the short name back; and because records 7/5, 7/7,
	// 7/14 and 7/19 all key by it.
	ShortName string `json:"short_name"`

	// LongName is the record 7/13 declaration, "" when the file
	// declares none. Held apart from Name so "the file declared a long
	// name equal to the short one" stays distinguishable from "the file
	// declared none".
	LongName string `json:"long_name,omitempty"`

	// Index is the 1-based dictionary ELEMENT index of the variable's
	// first element — what every index-bearing field in the file counts.
	Index int32 `json:"index"`

	// Position is the 0-based column position in the cohort.
	Position int `json:"position"`

	// Label is the SPSS variable label, which also became the Pulse
	// field description.
	Label string `json:"label,omitempty"`

	// HasLabel distinguishes "carried an empty label" from "carried
	// none", which the record type 2 flag really does distinguish.
	HasLabel bool `json:"has_label"`

	// TypeCode is the raw record type 2 `type` field: 0 for numeric,
	// else the declared byte width.
	TypeCode int32 `json:"type_code"`

	// DeclaredWidth is the declared width in BYTES — never runes — 0
	// for a numeric. SPSS space-pads to it and this reader trims, so
	// this is what an export re-pads to. For a very long string it is
	// the LOGICAL total, not the 255 a single segment declares.
	DeclaredWidth int `json:"declared_width"`

	// Segments is how many 8-byte elements the variable occupies.
	Segments int `json:"segments"`

	// PrintFormat and WriteFormat are the SPSS output formats.
	PrintFormat Format `json:"print_format"`
	WriteFormat Format `json:"write_format"`

	// Measure is the record 7/11 level: "unset", "nominal", "ordinal"
	// or "scale". It informs Pulse smart defaults on import but is not
	// derivable back from a Pulse schema, so it is stored regardless.
	Measure string `json:"measure"`

	// Alignment is the record 7/11 alignment: "left", "right" or
	// "center". 7/11 has no unset alignment.
	Alignment string `json:"alignment"`

	// DisplayWidth is the record 7/11 display column width. Nil for the
	// older two-int32-per-variable form of the record, which omits it.
	DisplayWidth *int32 `json:"display_width,omitempty"`

	// HasDisplayParams reports whether a record 7/11 was applied at
	// all, so an absent record stays distinguishable from one declaring
	// every field unset.
	HasDisplayParams bool `json:"has_display_params"`

	// PulseType is the resolved .pulse field type, as
	// encoding.FieldType.String().
	PulseType string `json:"pulse_type"`

	// Kind is the mapping family: "numeric", "duration", "date",
	// "datetime" or "categorical". A duration is f64 exactly as a
	// numeric is; the distinction is recorded so a TIME / DTIME column
	// is never mistaken for an instant.
	Kind string `json:"kind"`

	// Nullable is a fact, not a sample: the whole data section was
	// scanned.
	Nullable bool `json:"nullable"`

	// DefaultAggregation and DefaultGrouper are the smart-default hints
	// the measure level implied. Empty means no default applies.
	DefaultAggregation string `json:"default_aggregation,omitempty"`
	DefaultGrouper     string `json:"default_grouper,omitempty"`

	// Missing is the missing-value specification, nil when the variable
	// declares none.
	Missing *Missing `json:"missing,omitempty"`

	// Categories is the code <-> label <-> ID triple, one per
	// dictionary entry, in ID order. Empty for a non-categorical column.
	Categories []Category `json:"categories,omitempty"`

	// VeryLongString is the record 7/14 physical segmentation, non-nil
	// only for a logical string wider than 255 bytes.
	VeryLongString *VLSLayout `json:"very_long_string,omitempty"`
}

// Missing is a variable's missing-value specification, in whichever of
// the three shapes the file used.
//
// All three are representable and the raw slots are kept besides, so a
// shape this reader interprets narrowly has still lost nothing:
//
//	Kind                  Code   Contents
//	--------------------  -----  ---------------------------------------
//	"discrete"            1..3   Discrete (numeric) or DiscreteText
//	"range"               -2     Range
//	"range_plus_discrete" -3     Range plus exactly one discrete value
//
// Negative codes are numeric-only; the format has no range form for
// strings. A record 7/22 long-string specification lands here too, as
// a discrete DiscreteText list — it means the same thing, and exists
// only because a record type 2 cannot carry a missing value for a
// string wider than eight bytes.
type Missing struct {
	// Code is the raw record type 2 n_missing_values field.
	Code int32 `json:"code"`

	// Kind is the shape discriminant, per the table above.
	Kind string `json:"kind"`

	// Range is the lo..hi bound, non-nil for "range" and
	// "range_plus_discrete".
	Range *MissingRange `json:"range,omitempty"`

	// Discrete are the discrete numeric missing codes, in file order.
	// For "range_plus_discrete" there is exactly one.
	Discrete []Float `json:"discrete,omitempty"`

	// DiscreteText are the discrete STRING missing values, trimmed to
	// the declared width exactly as a datum of the same variable is.
	DiscreteText []string `json:"discrete_text,omitempty"`

	// Raw are the abs(Code) eight-byte slots verbatim, base64-encoded
	// by encoding/json. Authoritative: the decoded fields above are a
	// projection of these bytes, never a replacement.
	Raw [][]byte `json:"raw"`
}

// MissingRange is the lo..hi half of a range specification.
type MissingRange struct {
	Low  Float `json:"low"`
	High Float `json:"high"`
}

// Category is one dictionary entry's provenance: the code <-> label <->
// Pulse dictionary ID triple.
//
// Code and Text are pointers because exactly one of them is meaningful
// and which one depends on Numeric. A consumer reading the wrong one
// gets nil rather than a plausible zero.
type Category struct {
	// ID is the Pulse dictionary ID: the entry's position, and the
	// value stored on the wire. Two entries MAY share an ID, when two
	// distinct source values collapsed to one dictionary text; that is
	// represented rather than flattened so the ambiguity stays visible.
	ID uint32 `json:"id"`

	// Value is the dictionary entry text at position ID — the SPSS
	// value's canonical rendering, never the label.
	Value string `json:"value"`

	// Numeric reports whether the source variable is numeric, and hence
	// which of Code and Text carries the SPSS value.
	Numeric bool `json:"numeric"`

	// Code is the SPSS numeric value. Non-nil only when Numeric.
	Code *Float `json:"code,omitempty"`

	// Text is the SPSS string value with its declared-width padding
	// removed. Non-nil only when not Numeric. It can differ from Value,
	// which is additionally trimmed of leading space.
	Text *string `json:"text,omitempty"`

	// Label is the record type 3 (or 7/21) value label.
	Label string `json:"label,omitempty"`

	// Labelled reports whether the file DECLARED a label for this
	// value. False for an entry appended from the data section — an
	// unlabelled code is perfectly legal SPSS.
	Labelled bool `json:"labelled"`

	// Observed reports whether at least one case carried this value.
	// False for a declared label nothing used, which still occupies its
	// ID so the file's own code ordering survives.
	Observed bool `json:"observed"`
}

// Derived is one column this import synthesised rather than read.
//
// Reserved by E4-S1 and populated by E4-S2 / E4-S3 (the `<var>_missing`
// siblings) and E4-S4 (the multiple-dichotomy `set_*` convenience
// columns). An export drops derived columns and reconstructs the source
// from the real ones, so it needs to know which is which without
// pattern-matching on names.
type Derived struct {
	// Name is the derived Pulse field name.
	Name string `json:"name"`

	// Kind says what derived it. E4-S2/S3/S4 own the vocabulary.
	Kind string `json:"kind"`

	// Sources names the source variables it was derived FROM, by Pulse
	// field name.
	Sources []string `json:"sources,omitempty"`
}

// ---------------------------------------------------------------------------
// JSON-safe float
// ---------------------------------------------------------------------------

// Float is a float64 that survives JSON.
//
// encoding/json REFUSES to marshal NaN and +/-Inf, so a single
// pathological double anywhere in a dictionary would fail the whole
// sidecar write and, with it, an import that is otherwise perfectly
// good. Emitting them as the three JSON.parse-compatible-ish string
// tokens keeps the document writable and, crucially, keeps the failure
// out of the fidelity path: the authoritative record for a missing
// value is its raw eight-byte slot, and for a category it is the
// canonical Value text.
//
// A non-finite value loses its NaN payload bits in this form. That is
// accepted: the raw slots carry the bits where the bits matter.
type Float float64

// Non-finite tokens. Spelled as JSON strings because JSON numbers
// cannot express them.
const (
	floatNaN    = "NaN"
	floatPosInf = "Infinity"
	floatNegInf = "-Infinity"
)

// MarshalJSON emits a JSON number for a finite value and one of the
// three tokens otherwise.
func (f Float) MarshalJSON() ([]byte, error) {
	v := float64(f)
	switch {
	case math.IsNaN(v):
		return stdjson.Marshal(floatNaN)
	case math.IsInf(v, 1):
		return stdjson.Marshal(floatPosInf)
	case math.IsInf(v, -1):
		return stdjson.Marshal(floatNegInf)
	}
	return stdjson.Marshal(v)
}

// UnmarshalJSON accepts either form.
func (f *Float) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := stdjson.Unmarshal(b, &s); err != nil {
			return err
		}
		switch s {
		case floatNaN:
			*f = Float(math.NaN())
		case floatPosInf:
			*f = Float(math.Inf(1))
		case floatNegInf:
			*f = Float(math.Inf(-1))
		default:
			return errors.NewCodedError(errors.ENCODING_INVALID,
				"spss sidecar: "+strconv.Quote(s)+" is not a recognised non-finite float token")
		}
		return nil
	}
	var v float64
	if err := stdjson.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = Float(v)
	return nil
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// WriteSidecar builds the metadata sidecar for cohortPath and writes it
// to SidecarPath(cohortPath), satisfying pio.SidecarEmitter.
//
// It is called by ImportJob.Run AFTER the cohort has been written,
// which is not incidental: the fingerprint describes the cohort's
// bytes, so those bytes have to exist first.
//
// A failure here fails the import. The cohort write on the same
// filesystem has just succeeded, so a sidecar write that then fails is
// a genuine fault rather than an expected condition — and a cohort
// silently missing the only record of its value codes is precisely the
// quiet fidelity loss this document exists to prevent. "Absent sidecar
// is only a warning" is a READ-path rule about cohorts that never had
// one; it is not a licence to fail to write one.
func (r *Reader) WriteSidecar(fs afero.Fs, cohortPath string) error {
	if fs == nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Reader.WriteSidecar: no filesystem",
			map[string]any{"path": cohortPath})
	}
	d, err := r.loadDictionary()
	if err != nil {
		return err
	}
	m, err := r.loadMapping()
	if err != nil {
		return err
	}
	fp, err := fingerprintCohort(fs, cohortPath)
	if err != nil {
		return err
	}

	doc := buildDocument(d, m, fp)
	// encoding/json, never fmt.Sprintf. Indented because a sidecar is
	// read by people as often as by programs, and a diffable one makes
	// a fidelity regression visible in review.
	raw, err := stdjson.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Reader.WriteSidecar: encoding the metadata sidecar for "+cohortPath+": "+err.Error(),
			map[string]any{"path": SidecarPath(cohortPath)})
	}
	raw = append(raw, '\n')
	if err := afero.WriteFile(fs, SidecarPath(cohortPath), raw, 0644); err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Reader.WriteSidecar: writing "+SidecarPath(cohortPath)+": "+err.Error(),
			map[string]any{"path": SidecarPath(cohortPath)})
	}
	return nil
}

// fingerprintCohort hashes and stats the cohort the sidecar describes.
//
// Both halves come from the same file: the SHA-256 is the authoritative
// digest a verify pass recomputes, and the size + mtime are the O(1)
// pair a read-path staleness check compares. Modelled on
// encoding.Index's build-time snapshot.
func fingerprintCohort(fs afero.Fs, cohortPath string) (Fingerprint, error) {
	fail := func(err error) (Fingerprint, error) {
		return Fingerprint{}, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Reader.WriteSidecar: fingerprinting "+cohortPath+": "+err.Error(),
			map[string]any{"path": cohortPath})
	}
	f, err := fs.Open(cohortPath)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = f.Close() }()

	digest, ferr := encoding.ComputeFingerprint(f)
	if ferr != nil {
		return Fingerprint{}, ferr
	}
	info, err := fs.Stat(cohortPath)
	if err != nil {
		return fail(err)
	}
	size := info.Size()
	if size < 0 {
		size = 0
	}
	return Fingerprint{
		SHA256:        hex.EncodeToString(digest[:]),
		SourceSize:    uint64(size),
		SourceModTime: info.ModTime().UnixNano(),
	}, nil
}
