package encoding

import (
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// MaxFormatVersion is the newest .pulse format version this binary
// reads and writes.
const MaxFormatVersion byte = encoding.FormatVersionV2

// ReadPreamble reads the header and the version-matched schema block,
// leaving r positioned at the first record byte. It is the one-call
// form of [ReadHeader] + [ReadSchema] for callers that do not need to
// wrap the two failures differently.
func ReadPreamble(r io.Reader) (*encoding.Schema, byte, error) {
	v, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, 0, err
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		return nil, 0, err
	}
	return s, v, nil
}

// WritePreamble writes the header and schema block for s at the version
// its content requires ([Schema.RequiredFormatVersion]). A schema that
// uses no 0x02 feature produces bytes identical to [WriteHeader] +
// [WriteSchema].
func WritePreamble(w io.Writer, s *encoding.Schema) error {
	return writePreambleVersion(w, s, s.RequiredFormatVersion())
}

// writePreambleVersion writes header + schema at an explicit version v,
// which must be supported and no older than the schema requires.
// Unexported: the version written is a function of schema content, and
// only tests force a newer-than-required version (to build a 0x02 file
// before any 0x02 feature exists).
func writePreambleVersion(w io.Writer, s *encoding.Schema, v byte) error {
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
