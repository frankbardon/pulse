package encoding

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// MaxDescriptionBytes is the maximum allowed byte length for a field description.
const MaxDescriptionBytes = 1000

// WriteDescription writes a field description to w.
// Format: u16 length + utf8 bytes. Returns PULSE_IMPORT_DESCRIPTION_TOO_LONG
// if the UTF-8 byte length exceeds MaxDescriptionBytes.
func WriteDescription(w io.Writer, desc string) error {
	b := []byte(desc)
	if len(b) > MaxDescriptionBytes {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG,
			fmt.Sprintf("field description is %d bytes, max %d", len(b), MaxDescriptionBytes),
			map[string]any{"length": len(b), "max": MaxDescriptionBytes},
		)
	}
	slen := uint16(len(b))
	if err := binary.Write(w, binary.LittleEndian, slen); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing description length")
	}
	if len(b) > 0 {
		if _, err := w.Write(b); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "writing description bytes")
		}
	}
	return nil
}

// ReadDescription reads a field description from r.
// Format: u16 length + utf8 bytes.
func ReadDescription(r io.Reader) (string, error) {
	var slen uint16
	if err := binary.Read(r, binary.LittleEndian, &slen); err != nil {
		return "", errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading description length")
	}
	if slen == 0 {
		return "", nil
	}
	buf := make([]byte, slen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading description bytes")
	}
	return string(buf), nil
}
