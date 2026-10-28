package encoding

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// DescriptionLengthRefusal refuses the first field of fields whose
// description is longer than encoding.MaxDescriptionBytes with
// PULSE_IMPORT_DESCRIPTION_TOO_LONG, details field + length + max — the
// one rule the cohort builder's and an explicit import schema's
// pre-flight (internal/io) and a materialize predict / pre-flight
// (internal/descriptor) share, so they cannot disagree on the field or
// the details. Nil when every description fits.
func DescriptionLengthRefusal(fields []encoding.Field) *errors.CodedError {
	for i := range fields {
		f := &fields[i]
		if n := len(f.Description); n > encoding.MaxDescriptionBytes {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG,
				"field "+strconv.Quote(f.Name)+" description is "+strconv.Itoa(n)+" bytes, max "+strconv.Itoa(encoding.MaxDescriptionBytes),
				map[string]any{"field": f.Name, "length": n, "max": encoding.MaxDescriptionBytes},
			)
		}
	}
	return nil
}
