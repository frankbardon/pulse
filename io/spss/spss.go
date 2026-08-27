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
// Deliberately not interpreted: 7/10 extra product info and 7/17 / 7/18
// attributes, which are free-form text with no Pulse home and are captured
// verbatim without warning; 7/14, 7/21 and 7/22, the very-long-string
// records, which belong to a later story and warn as unrecognised until it
// lands. Charset DECODING is not done here either — 7/20's name is recorded,
// and nothing is transcoded with it.
//
// The data section is read in its uncompressed encoding only (see data.go);
// dictionary.dataOffset is the byte offset of its first byte. A file whose
// header declares bytecode or ZSAV compression parses its dictionary
// normally and is then refused at ReadRows with
// PULSE_SPSS_COMPRESSION_UNSUPPORTED, because a compressed data section read
// as though it were uncompressed yields plausible numbers rather than an
// error.
//
// # Errors and warnings
//
// Every parse failure is a *errors.CodedError carrying
// PULSE_SPSS_DICT_INVALID (structurally malformed) or
// PULSE_SPSS_DICT_TRUNCATED (ran out of bytes), with the record and the byte
// offset both in the message and in Details under errors.DetailSPSSRecord and
// errors.DetailSPSSOffset. Malformed input never panics: every read is
// bounds-checked before it happens.
//
// Extension records are the one place the parser tolerates rather than
// rejects. An unrecognised subtype yields one PULSE_SPSS_EXTENSION_UNKNOWN
// warning on dictionary.warnings; a recognised subtype whose payload does not
// match its shape yields PULSE_SPSS_EXTENSION_INVALID. Neither stops a parse,
// because real SPSS versions emit subtypes no published description lists and
// refusing such a file would reject data that is otherwise perfectly
// readable. The line is drawn at record FRAMING: a size or count that would
// desynchronise the walk is still a hard error, since resuming from a
// desynchronised offset would produce a plausible dictionary describing
// nothing in the file.
package spss

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
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

	// opts are the mapping tunables the functional options set.
	opts mappingOptions

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

// NewReader creates a `.sav` reader over a filesystem path.
func NewReader(fs afero.Fs, path string, opts ...Option) *Reader {
	return newReader(&Reader{fs: fs, path: path}, opts)
}

// NewReaderFromBytes creates a `.sav` reader over raw bytes, for callers that
// already hold the file — tests included.
func NewReaderFromBytes(data []byte, opts ...Option) *Reader {
	return newReader(&Reader{data: data}, opts)
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
func (r *Reader) init() error {
	if r.data != nil {
		return nil
	}
	if r.fs == nil {
		return fmt.Errorf("spss.Reader: no data source")
	}
	data, err := afero.ReadFile(r.fs, r.path)
	if err != nil {
		return fmt.Errorf("spss.Reader: reading %s: %w", r.path, err)
	}
	r.data = data
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
	d, err := parseDictionary(r.data)
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
	r.dict = nil
	r.mapped = nil
	r.header = nil
	r.dataWarnings = nil
	return nil
}
