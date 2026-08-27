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
// Record types 6 (documents) and 7 (extensions) are stepped over correctly —
// their length prefixes are read so the walk stays aligned on the terminator
// — but they are not interpreted. A dictionary containing no extension
// records at all is legal and common, so nothing in this package may require
// one; the system-missing sentinel in particular defaults to the spec value
// and treats record 7/4 as an override rather than a precondition.
//
// The data section is not read yet. dictionary.dataOffset is the byte offset
// of its first byte.
//
// # Errors
//
// Every parse failure is a *errors.CodedError carrying
// PULSE_SPSS_DICT_INVALID (structurally malformed) or
// PULSE_SPSS_DICT_TRUNCATED (ran out of bytes), with the record and the byte
// offset both in the message and in Details under errors.DetailSPSSRecord and
// errors.DetailSPSSOffset. Malformed input never panics: every read is
// bounds-checked before it happens.
package spss

import (
	"fmt"

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
}

// NewReader creates a `.sav` reader over a filesystem path.
func NewReader(fs afero.Fs, path string) *Reader {
	return &Reader{fs: fs, path: path}
}

// NewReaderFromBytes creates a `.sav` reader over raw bytes, for callers that
// already hold the file — tests included.
func NewReaderFromBytes(data []byte) *Reader {
	return &Reader{data: data}
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

// Close releases the reader's buffers.
func (r *Reader) Close() error {
	r.data = nil
	r.dict = nil
	return nil
}
