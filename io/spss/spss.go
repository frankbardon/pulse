// Package spss reads SPSS `.sav` system files for the Pulse I/O pipeline.
//
// The format is a tagged dictionary section followed by a data section. This
// package parses the dictionary spine — the file header, the record type 2
// variable records (with their string continuation records), the record 3/4
// value-label pairs, and the record type 999 terminator — into a faithful
// in-memory transcription of what the file declares.
//
// Nothing is converted here. The dictionary carries SPSS's own types, format
// codes, widths, missing-value specifications and value-label codes verbatim;
// mapping them onto a Pulse schema is a separate concern, and doing it here
// would throw away the very information the mapping needs to be reversible.
//
// # Scope today
//
// Record type 6 documents are captured verbatim, and the record type 7
// extension subtypes that carry the metadata worth having are interpreted:
// 7/3 machine integer info, 7/4 machine float sentinels, 7/5 / 7/7 / 7/19
// multiple-response and variable sets, 7/11 measure level and display
// parameters, 7/13 long variable names, 7/16 the 64-bit case count and 7/20
// the character encoding NAME. Every extension record is ALSO retained
// verbatim, interpreted or not, so the typed slots are a projection of the
// bytes rather than a replacement for them.
//
// A dictionary containing no extension records at all is legal and common,
// so nothing in this package may require one; the system-missing sentinel in
// particular defaults to the spec value and treats record 7/4 as an override
// rather than a precondition, and only adopts a declared sentinel from a
// coherent sysmis < lowest < highest triple.
//
// Every string the dictionary holds is decoded from the file's declared
// character encoding into UTF-8, strictly: an undecodable byte is a coded
// error naming the variable and the offending value, never a U+FFFD
// substitution (see charset.go). The declaration itself — the record 7/20
// name and the record 7/3 character code — is retained verbatim for the
// write path, and a caller can override it with WithCharset.
//
// Deliberately not interpreted: 7/10 extra product info and 7/17 / 7/18
// attributes, which are free-form text with no Pulse home and are captured
// verbatim without warning; 7/14, 7/21 and 7/22, the very-long-string
// records, which belong to a later story and warn as unrecognised until it
// lands.
//
// # Missing values
//
// SPSS has more missing states than a null bitmap bit can hold: present,
// system-missing, and up to three discrete USER-missing codes — or a
// range, or a range plus one code — that separate `refused` from `don't
// know` from `not applicable`. Both obvious mappings lose: keeping the
// codes as data makes AGG_SUM add 99999 for every refusal, and
// collapsing them all to null destroys the item-non-response
// distinction survey analysis is built on.
//
// So a numeric variable declaring user-missing values gets TWO cohort
// columns. The analytic column holds the real values and is null at
// every missing position, so the arithmetic is right; a generated
// `<var>_missing` sibling immediately after it carries the reason as a
// categorical — "sysmis", the value label the file declared for the
// code, or the code itself. Nothing is lost. WithMissingMode(MissingNull)
// suppresses the siblings for callers who want the slimmer schema and
// have accepted losing the reason. A CATEGORICAL column gets no sibling
// either way: its missing code is already a dictionary entry. See
// missing.go.
//
// # The metadata sidecar
//
// An import writes what a `.pulse` file cannot hold — measure levels,
// print formats, arbitrary value codes, missing-value specifications,
// declared byte widths, multiple-response sets, documents, attributes
// and the source charset — to a JSON sidecar beside the cohort, via the
// optional pio.SidecarEmitter contract. Its most important payload is
// the code / label / Pulse dictionary ID triple, because the cohort's
// own dictionary holds CODES and the sidecar is therefore the only
// place the LABELS live. See sidecar.go.
//
// All three data-section encodings the format defines are read:
// uncompressed, SPSS's default bytecode compression (see data.go and
// bytecode.go), and ZSAV zlib block compression (see zsav.go).
// dictionary.dataOffset is the byte offset of the section's first byte, and
// the header's compression flag and bias say how to read what follows.
//
// ZSAV is TWO layers rather than a third encoding: the zlib blocks, indexed
// by the ZHEADER / ZTRAILER pair, inflate to a bytecode command stream, and
// that stream is then decoded exactly as a flag-1 file's would be. Reading
// the inflated bytes as anything else would produce plausible numbers rather
// than an error, which is why the nesting is stated at every level it
// touches. Emission is out of scope in the other direction: there is no
// `.sav` or `.zsav` writer (PULSE_SPSS_EXPORT_UNSUPPORTED).
//
// # Byte order
//
// Both orders are read. The header layout code decides — it always holds 2
// or 3, and neither byte-swaps into anything in range, so reading those four
// bytes both ways identifies the file's order with no residual doubt.
// Record 7/3 states the order a second time and is a corroboration only,
// never a source: reading it already requires knowing the order. A clean
// contradiction between the two is PULSE_SPSS_ENDIANNESS_MISMATCH, a hard
// error, because byte order governs every count, offset and double in the
// file and the wrong choice yields a whole file of plausible wrong numbers
// rather than one bad field. See checkEndianness in dict_parse.go for the
// full reasoning and for what is deliberately NOT a contradiction.
//
// The header magic and the compression flag are cross-checked too, but that
// one WARNS (PULSE_SPSS_MAGIC_FLAG_MISMATCH) and the flag wins: the flag
// describes the bytes, while the magic is a coarse generation label a
// re-saving tool can leave stale.
//
// # Errors and warnings
//
// Every parse failure is a *errors.CodedError. The byte-addressed family
// carries the record and the byte offset both in the message and in Details
// under errors.DetailSPSSRecord and errors.DetailSPSSOffset:
// PULSE_SPSS_FILE_EMPTY (no bytes at all — held apart from truncation
// because a file that was never written and a transfer that was cut short
// have nothing in common but the symptom), PULSE_SPSS_DICT_INVALID
// (structurally malformed) and PULSE_SPSS_DICT_TRUNCATED (ran out of bytes).
// Malformed input never panics: every read is bounds-checked before it
// happens, and that is verified by a corruption sweep over every byte of the
// dictionary in every compression mode plus two fuzz targets, not asserted
// here.
//
// Two places tolerate rather than reject.
//
// Extension records: an unrecognised subtype yields one
// PULSE_SPSS_EXTENSION_UNKNOWN warning on dictionary.warnings; a recognised
// subtype whose payload does not match its shape yields
// PULSE_SPSS_EXTENSION_INVALID. Neither stops a parse, because real SPSS
// versions emit subtypes no published description lists and refusing such a
// file would reject data that is otherwise perfectly readable.
//
// Record 3/4 value-label BINDING: a set naming variables it cannot attach to
// — mixed type or width, a string wider than the 8-byte value slot, an index
// landing on a string continuation — is dropped with
// PULSE_SPSS_VALUE_LABELS_DROPPED naming the variable, and the file imports.
// A value label is display metadata, so refusing the file would cost the
// data to save the labels.
//
// The line under both is record FRAMING: a size or count that would
// desynchronise the walk is still a hard error, since resuming from a
// desynchronised offset would produce a plausible dictionary describing
// nothing in the file. A value-label element index below 1 or past the end
// of the dictionary is on the hard side of that line too — it is damage
// rather than dialect, and it puts the record's own framing in doubt.
package spss

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// Compile-time contract assertions, the house block every Pulse adapter
// carries. The last three are the load-bearing ones for this format:
// SchemaAwareReader is what makes the `.sav` dictionary authoritative
// instead of a hint the inference pass would overrule,
// SourceWarningEmitter is what routes the PULSE_SPSS_* diagnostics out
// of this package and onto the import / convert report, and
// SidecarEmitter is what persists the dictionary metadata `.pulse`
// cannot hold. Losing any of them silently would degrade an import
// rather than break it, which is exactly the failure a compile-time
// assertion is cheap insurance against.
var (
	_ pio.Reader               = (*Reader)(nil)
	_ pio.ResetReader          = (*Reader)(nil)
	_ pio.SchemaAwareReader    = (*Reader)(nil)
	_ pio.SourceWarningEmitter = (*Reader)(nil)
	_ pio.SidecarEmitter       = (*Reader)(nil)
)

// Reader reads an SPSS `.sav` system file.
//
// The whole file is read into memory on first use, matching the other Pulse
// adapters: the dictionary is a linked walk of variable-length records, so
// there is no seek-friendly index to stream against, and the data section is
// addressed by an offset the dictionary walk produces.
type Reader struct {
	fs   afero.Fs
	path string

	data []byte
	dict *dictionary

	// loaded records that the source has been read, which is what lets a
	// ZERO-LENGTH source be distinguished from an unread one. A nil data
	// slice is how NewReaderFromBytes stores an empty buffer and how a
	// fresh path-backed Reader starts out, so the slice alone cannot tell
	// "the caller handed us no bytes" from "we have not opened the file
	// yet" — and those two answer with completely different errors.
	loaded bool

	// opts are the mapping tunables the functional options set.
	opts mappingOptions

	// charsetOverride is the WithCharset name, "" when the file's own
	// declaration decides.
	charsetOverride string

	// mapped is the memoised schema mapping. It is resolved on first use
	// — by PulseSchema or by ReadRows, whichever comes first — and kept
	// across a Reset, because like the dictionary it is a pure function
	// of bytes that have not changed.
	mapped *mapping

	// header is the memoised column-name slice ReadHeader returns. It is
	// derived from dict and is cleared by Reset.
	header []string

	// dataWarnings are the non-fatal diagnostics the most recent ReadRows
	// pass raised. They are held apart from dictionary.warnings because
	// the dictionary is parsed once and memoised while ReadRows can run
	// repeatedly — appending to the dictionary's slice would accumulate a
	// duplicate set on every pass.
	dataWarnings []*errors.CodedError
}

// Option configures a Reader at construction. The set is deliberately
// small: everything about a `.sav` that can be read from the file is read
// from the file, so an option exists only where the reader has to apply a
// judgement the format does not supply.
type Option func(*Reader)

// WithCardinalityWarnFraction sets the share of the case count a
// categorical column's distinct-value count must exceed before the
// mapping raises PULSE_SPSS_CARDINALITY_HIGH — the schema-bloat signal of
// a free-text variable. It defaults to 0.5, and any value above 1
// disables the check.
//
// The warning never blocks an import. Mapping a near-unique string
// variable to categorical_u32 is lossless; what it costs is a large
// inline dictionary block that every read of the cohort pays for, which
// is a performance concern and not a fidelity one.
//
// The check is skipped entirely below a small case-count floor, because
// a distinct-count ratio over a handful of cases says nothing.
func WithCardinalityWarnFraction(fraction float64) Option {
	return func(r *Reader) { r.opts.cardinalityWarnFraction = fraction }
}

// WithMissingMode selects how a numeric variable's USER-missing values
// are represented in the cohort.
//
// The default, [MissingAuto], is the fidelity-preserving split: the
// analytic column is null at every missing position — so AGG_SUM and
// AGG_MEAN never add a refusal code — and a generated `<var>_missing`
// sibling carries WHY each value is missing, as "sysmis", the value
// label the file declared for that code, or the code itself.
//
// [MissingNull] suppresses the siblings. The nulls are identical; what
// is lost is the reason, and with it the refused / don't-know /
// not-applicable distinction survey analysis reports separately and
// weights on. It exists for callers who want the slimmer schema and have
// accepted that; the full missing-value specification still rides the
// metadata sidecar either way.
//
// Neither mode affects a CATEGORICAL column — a string variable or a
// value-labelled numeric — where the missing code is already a
// dictionary entry of its own and a sibling would be pure redundancy.
func WithMissingMode(mode MissingMode) Option {
	return func(r *Reader) { r.opts.missingMode = mode }
}

// WithCharset overrides the character encoding the file declares.
//
// It exists because the commonest reason a real `.sav` fails to decode is
// that the file is wrong about itself: a dictionary transcoded by one tool
// and re-saved by another keeps the old record 7/20 name, and a pre-Unicode
// file often declares nothing at all. Where the bytes and the declaration
// disagree, only the caller can say which is right, so the override is the
// documented answer to both PULSE_SPSS_CHARSET_UNSUPPORTED and
// PULSE_SPSS_CHARSET_INVALID.
//
// The name is resolved by the same lookup the file's own declaration goes
// through, so "windows-1252", "cp1252" and "1252" are the same request; an
// unresolvable one is PULSE_SPSS_CHARSET_UNSUPPORTED naming it. An empty
// string is not an override and leaves the file's declaration in force.
//
// It changes only DECODING. The file's own declaration is still retained
// verbatim, because the write path has to re-encode into the charset the
// source declared and not into the one a reader was told to read with.
func WithCharset(name string) Option {
	return func(r *Reader) { r.charsetOverride = name }
}

// NewReader creates a `.sav` reader over a filesystem path.
func NewReader(fs afero.Fs, path string, opts ...Option) *Reader {
	return newReader(&Reader{fs: fs, path: path}, opts)
}

// NewReaderFromBytes creates a `.sav` reader over raw bytes, for callers that
// already hold the file — tests included.
//
// A nil or empty slice IS a source: it is a source of no bytes, and it
// reports PULSE_SPSS_FILE_EMPTY exactly as a zero-length file on disk does.
// Before E3-S5 it reported "no source configured", which described the
// reader rather than the input and was not a coded error a caller could
// switch on.
func NewReaderFromBytes(data []byte, opts ...Option) *Reader {
	return newReader(&Reader{data: data, loaded: true}, opts)
}

func newReader(r *Reader, opts []Option) *Reader {
	r.opts = defaultMappingOptions()
	for _, o := range opts {
		if o != nil {
			o(r)
		}
	}
	return r
}

// init loads the file bytes, once.
//
// Every failure out of this package is a coded error, this one included: a
// caller that has to string-match "no data source" cannot distinguish a
// misconfigured reader from a file that will not open, and neither is
// something an import report should surface as untyped prose. Filesystem
// faults take DATA_FILE rather than a PULSE_SPSS_* code because nothing
// about them is SPSS-specific — the file was never read, so its contents
// have not been judged.
func (r *Reader) init() error {
	if r.loaded {
		return nil
	}
	if r.fs == nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Reader: no data source; construct one with NewReader(fs, path) or NewReaderFromBytes(data)",
			map[string]any{"path": r.path})
	}
	data, err := afero.ReadFile(r.fs, r.path)
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Reader: reading "+r.path+": "+err.Error(),
			map[string]any{"path": r.path})
	}
	r.data = data
	r.loaded = true
	return nil
}

// loadDictionary parses and memoises the file's dictionary section.
func (r *Reader) loadDictionary() (*dictionary, error) {
	if r.dict != nil {
		return r.dict, nil
	}
	if err := r.init(); err != nil {
		return nil, err
	}
	d, err := parseDictionaryWithCharset(r.data, r.charsetOverride)
	if err != nil {
		return nil, err
	}
	r.dict = d
	return d, nil
}

// loadMapping resolves and memoises the schema mapping: the SPSS
// dictionary and the whole data section reduced to one Pulse column per
// variable.
//
// It is memoised for the same reason the dictionary is — it is a pure
// function of bytes that do not change — and because it walks every case,
// which an infer-then-import sequence would otherwise pay for twice.
func (r *Reader) loadMapping() (*mapping, error) {
	if r.mapped != nil {
		return r.mapped, nil
	}
	d, err := r.loadDictionary()
	if err != nil {
		return nil, err
	}
	m, err := buildMapping(d, r.data, r.opts)
	if err != nil {
		return nil, err
	}
	r.mapped = m
	return m, nil
}

// PulseSchema returns the authoritative .pulse schema for the file,
// satisfying pio.SchemaAwareReader: a `.sav` carries a dictionary that
// DECLARES each column's type, so the shared import path skips inference
// entirely rather than sampling rows and voting on what it sees.
//
// Every obligation the interface names is met here. Types come from the
// SPSS declared type plus its print format code, never from cell text.
// Nullability is a fact, not a sample — the mapping scans every case, so
// a field is nullable exactly when some case carries a value the import
// path reads as null, and no out-of-sample promotion can be needed.
// CsvColumnIdx is the variable's position in the row ReadRows yields, and
// every dictionary-bearing field arrives with its dictionary PRE-SEEDED in
// the file's own order, because entry order is the on-wire encoding and
// handing over the source's ordering is what preserves the source codes.
//
// A fresh schema, with a fresh dictionary per categorical column, is
// returned on every call: encoding.Dictionary is mutable and the import
// path appends to it, so a shared instance would let one import's values
// leak into another's IDs.
//
// It never declines. A `.sav` always has a dictionary, so there is no
// (nil, nil) case; a file whose dictionary or data section cannot be read
// returns the coded error, which fails the import rather than silently
// falling back to inference.
func (r *Reader) PulseSchema() (*encoding.Schema, error) {
	m, err := r.loadMapping()
	if err != nil {
		return nil, err
	}
	return m.schema(), nil
}

// Close releases the reader's buffers.
//
// It is idempotent: every field it clears is already cleared on a second
// call, so calling it twice — which a deferred Close plus an explicit one in
// the happy path routinely does — is a no-op rather than a fault.
//
// A reader built by NewReader can be used again after Close, because init
// re-reads the file. One built by NewReaderFromBytes cannot: its only copy of
// the bytes was the buffer Close just dropped, and a subsequent read reports
// that there is no data source. That matches the csv adapter, whose Close is
// likewise the end of a byte-backed reader's life.
func (r *Reader) Close() error {
	r.data = nil
	r.loaded = false
	r.dict = nil
	r.mapped = nil
	r.header = nil
	r.dataWarnings = nil
	return nil
}
