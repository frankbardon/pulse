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
// — that is [MaxFormatVersion].
const FormatVersion byte = FormatVersionV1

// MaxFormatVersion is the newest .pulse format version this binary
// reads and writes.
const MaxFormatVersion byte = FormatVersionV2

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
// [FormatVersion] (0x01). A writer that already holds the schema should
// prefer [WritePreamble], which derives the version from the schema's
// content and cannot disagree with the schema block that follows.
func WriteHeader(w io.Writer) error {
	return writeHeaderVersion(w, FormatVersion)
}

// writeHeaderVersion writes a header declaring version v. Unexported:
// production writers reach it only through [WritePreamble], which picks
// v from schema content.
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
// the offending byte under details["version"].
func ReadHeader(r io.Reader) (byte, error) {
	var hdr [HeaderSize]byte
	n, err := io.ReadFull(r, hdr[:])
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

// ReadPreamble reads the header and the version-matched schema block,
// leaving r positioned at the first record byte. It is the one-call
// form of [ReadHeader] + [ReadSchema] for callers that do not need to
// wrap the two failures differently.
func ReadPreamble(r io.Reader) (*Schema, byte, error) {
	v, err := ReadHeader(r)
	if err != nil {
		return nil, 0, err
	}
	s, err := ReadSchema(r, v)
	if err != nil {
		return nil, 0, err
	}
	return s, v, nil
}

// WritePreamble writes the header and schema block for s at the version
// its content requires ([Schema.RequiredFormatVersion]). A schema that
// uses no 0x02 feature produces bytes identical to [WriteHeader] +
// [WriteSchema].
func WritePreamble(w io.Writer, s *Schema) error {
	return writePreambleVersion(w, s, s.RequiredFormatVersion())
}

// writePreambleVersion writes header + schema at an explicit version v,
// which must be supported and no older than the schema requires.
// Unexported: the version written is a function of schema content, and
// only tests force a newer-than-required version (to build a 0x02 file
// before any 0x02 feature exists).
func writePreambleVersion(w io.Writer, s *Schema, v byte) error {
	if req := s.RequiredFormatVersion(); v < req {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"schema requires a newer pulse format version than requested",
			map[string]any{"version": v, "required_version": req})
	}
	if err := writeHeaderVersion(w, v); err != nil {
		return err
	}
	return writeSchemaVersion(w, s, v)
}
