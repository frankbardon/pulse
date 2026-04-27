package encoding

import (
	"io"

	"github.com/frankbardon/pulse/errors"
)

// MagicBytes identifies a .pulse file. 8 bytes: "PULSE\x00\x00\x00"
var MagicBytes = [8]byte{'P', 'U', 'L', 'S', 'E', 0x00, 0x00, 0x00}

// FormatVersion is the current .pulse format version.
const FormatVersion byte = 0x01

// HeaderSize is the total byte size of the file header (magic + version).
const HeaderSize = 9

// WriteHeader writes the .pulse file header to w.
func WriteHeader(w io.Writer) error {
	var hdr [HeaderSize]byte
	copy(hdr[:8], MagicBytes[:])
	hdr[8] = FormatVersion
	_, err := w.Write(hdr[:])
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing pulse header")
	}
	return nil
}

// ReadHeader reads and validates the .pulse file header from r.
func ReadHeader(r io.Reader) error {
	var hdr [HeaderSize]byte
	n, err := io.ReadFull(r, hdr[:])
	if err != nil || n != HeaderSize {
		return errors.NewCodedError(errors.ENCODING_INVALID, "truncated pulse header")
	}

	for i := 0; i < len(MagicBytes); i++ {
		if hdr[i] != MagicBytes[i] {
			return errors.NewCodedError(errors.ENCODING_INVALID, "invalid pulse magic bytes")
		}
	}

	if hdr[8] != FormatVersion {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"unsupported pulse format version",
			map[string]any{"version": hdr[8]})
	}

	return nil
}
