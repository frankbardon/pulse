package encoding

import (
	"io"

	"github.com/frankbardon/pulse/errors"
)

// MagicBytes identifies a .pulse file. 8 bytes: "PULSE\x00\x00\x00"
var MagicBytes = [8]byte{'P', 'U', 'L', 'S', 'E', 0x00, 0x00, 0x00}

// Format versions a .pulse header may declare in byte 8.
//
// The version a writer emits is a FUNCTION OF SCHEMA CONTENT
// ([Schema.RequiredFormatVersion]), never a global flag: a schema that
// uses no 0x02 feature is written as 0x01, byte-identical to every file
// written before 0x02 existed. Old cohorts stay readable forever — the
// reader accepts every version in the supported set and parses the
// schema block by the version it read ([ReadSchema]).
//
// A binary that predates a version refuses it at [ReadHeader] with a
// coded ENCODING_INVALID naming the version, before any schema byte is
// interpreted, so a newer file never misparses silently in an older
// binary.
const (
	// FormatVersionV1 is the original layout: header + field descriptors
	// + inline dictionaries + fixed-stride records, with nothing between
	// the last descriptor and the first record.
	FormatVersionV1 byte = 0x01
	// FormatVersionV2 appends a length-prefixed schema extension block
	// after the field descriptors (see [WriteSchema]). Readers of a v2
	// file always know exactly where the record region starts.
	FormatVersionV2 byte = 0x02
)

// FormatVersion is the BASELINE .pulse format version: the version
// [WriteHeader] emits and the one every schema that uses no 0x02 feature
// is written at. It is not "the newest version this binary understands"
// — that is [FormatVersionV2].
const FormatVersion byte = FormatVersionV1

// ZstdMagic is the zstd frame magic (0xFD2FB528 little-endian, RFC 8878)
// that opens a `.pulse.zst` transfer artifact. It is NOT a cohort layout
// and there is no third magic-byte variant: Pulse never reads a
// compressed cohort. [ReadHeader] recognises these four bytes only so it
// can refuse with PULSE_COHORT_COMPRESSED ("decompress first") instead of
// a generic ENCODING_INVALID.
var ZstdMagic = [4]byte{0x28, 0xB5, 0x2F, 0xFD}

// IsZstdMagic reports whether b begins with [ZstdMagic].
func IsZstdMagic(b []byte) bool {
	return len(b) >= len(ZstdMagic) && [4]byte(b[:4]) == ZstdMagic
}

// CompressedCohortError is the ONE coded refusal for a cohort path that
// holds a zstd transfer artifact rather than a `.pulse` cohort.
func CompressedCohortError() *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_COHORT_COMPRESSED,
		"file is a zstd-compressed transfer artifact, not a cohort: decompress first with `pulse import transfer` (library: Pulse.ImportTransfer) and open the resulting .pulse",
		map[string]any{"codec": "zstd"})
}

// HeaderSize is the total byte size of the file header (magic + version).
const HeaderSize = 9

// IsSupportedFormatVersion reports whether v is a .pulse format version
// this binary can read.
func IsSupportedFormatVersion(v byte) bool {
	return v == FormatVersionV1 || v == FormatVersionV2
}

// SupportedFormatVersions returns the accepted version set, ascending.
func SupportedFormatVersions() []byte {
	return []byte{FormatVersionV1, FormatVersionV2}
}

// unsupportedVersionError is the ONE coded error for a header version
// outside the supported set, shared by [ReadHeader] and the shard-archive
// peek so the details keys never drift between the two.
func unsupportedVersionError(v byte) *errors.CodedError {
	supported := SupportedFormatVersions()
	ints := make([]int, len(supported))
	for i, s := range supported {
		ints[i] = int(s)
	}
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		"unsupported pulse format version: the file declares a version this binary cannot read (a newer Pulse wrote it — upgrade Pulse, or re-import the source with this binary)",
		map[string]any{"version": v, "supported_versions": ints})
}

// WriteHeader writes a .pulse file header declaring the baseline
// [FormatVersion] (0x01). It is the raw-byte primitive for an ungrouped
// cohort; Pulse's own writers derive the version from the schema's
// content (a grouped schema needs 0x02) so the header cannot disagree
// with the schema block that follows.
func WriteHeader(w io.Writer) error {
	return writeHeaderVersion(w, FormatVersion)
}

// writeHeaderVersion writes a header declaring version v. Unexported:
// production writers reach it only through the module-internal preamble
// writer, which picks v from schema content.
func writeHeaderVersion(w io.Writer, v byte) error {
	if !IsSupportedFormatVersion(v) {
		return unsupportedVersionError(v)
	}
	var hdr [HeaderSize]byte
	copy(hdr[:8], MagicBytes[:])
	hdr[8] = v
	_, err := w.Write(hdr[:])
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing pulse header")
	}
	return nil
}

// ReadHeader reads and validates the .pulse file header from r and
// returns the format version it declares. The version must be handed
// to [ReadSchema]: the schema block's layout depends on it, and record
// data begins immediately after the schema block with no terminator, so
// parsing a v2 schema as v1 would misplace every record.
//
// A version outside [SupportedFormatVersions] is ENCODING_INVALID with
// the offending byte under details["version"]. A zstd transfer artifact
// ([ZstdMagic]) is PULSE_COHORT_COMPRESSED ([CompressedCohortError]):
// error classification only — nothing is ever decompressed here.
func ReadHeader(r io.Reader) (byte, error) {
	var hdr [HeaderSize]byte
	n, err := io.ReadFull(r, hdr[:])
	if IsZstdMagic(hdr[:n]) {
		return 0, CompressedCohortError()
	}
	if err != nil || n != HeaderSize {
		return 0, errors.NewCodedError(errors.ENCODING_INVALID, "truncated pulse header")
	}

	for i := 0; i < len(MagicBytes); i++ {
		if hdr[i] != MagicBytes[i] {
			return 0, errors.NewCodedError(errors.ENCODING_INVALID, "invalid pulse magic bytes")
		}
	}

	if !IsSupportedFormatVersion(hdr[8]) {
		return 0, unsupportedVersionError(hdr[8])
	}

	return hdr[8], nil
}
