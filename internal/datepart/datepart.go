// Package datepart is the one reading of ATTR_DATE_PART's params and
// field, shared by the operator factory (internal/processing) and
// predict (internal/descriptor) so both refuse the same request with
// the same code and message. It decides what a request MEANS and never
// extracts a value: the calendar arithmetic lives in internal/temporal.
//
// Leaf: stdlib + the public errors / encoding packages.
package datepart

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// PartHour is the sub-day part: the local wall-clock hour (0..23).
const PartHour = "hour"

// partList is the part set in the order refusals name it.
const partList = "year, month, day, year_month, year_month_day, month_day, hour"

var validParts = map[string]bool{
	"year":           true,
	"month":          true,
	"day":            true,
	"year_month":     true,
	"year_month_day": true,
	"month_day":      true,
	PartHour:         true,
}

type params struct {
	Part string `json:"part"`
}

// Parse validates raw ATTR_DATE_PART params and returns the part.
// Refusals, in order, are all PROCESSING_CONFIG: absent params,
// malformed JSON, a missing part and an unknown part.
func Parse(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG, "date_part attribute requires params with a \"part\" field")
	}
	var p params
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", errors.WrapCodedError(err, errors.PROCESSING_CONFIG, "parsing date_part params")
	}
	if p.Part == "" {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG, "date_part attribute requires a \"part\" field in params")
	}
	if !validParts[p.Part] {
		return "", errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid date part %q: must be one of %s", p.Part, partList))
	}
	return p.Part, nil
}

// CheckField judges the schema field f (nil: absent from the schema)
// that part reads from field. A `date` or `datetime` is accepted, except
// that `hour` needs a `datetime` (a calendar date carries no time of
// day, so every row would read hour 0). Anything else — including an
// absent field — is PROCESSING_CONFIG.
func CheckField(part, field string, f *encoding.Field) error {
	if f == nil || (f.Type != encoding.FieldTypeDate && f.Type != encoding.FieldTypeDateTime) {
		return errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("date_part attribute requires a date or datetime field, got %q", field))
	}
	if part == PartHour && f.Type != encoding.FieldTypeDateTime {
		return errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("ATTR_DATE_PART part=hour requires a datetime field; field %q is of type %s", field, f.Type))
	}
	return nil
}
