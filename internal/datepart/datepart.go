// Package datepart is the one reading of ATTR_DATE_PART's params and
// field, shared by the operator factory (internal/processing) and
// predict (internal/descriptor) so both refuse the same request with
// the same code and message. It decides what a request MEANS and never
// extracts a value: the calendar arithmetic lives in internal/temporal.
//
// Leaf: stdlib, the public errors / encoding / types packages and the
// accepted-types table (internal/fieldaccept, itself a leaf).
package datepart

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// PartHour is the sub-day part: the local wall-clock hour (0..23).
const PartHour = "hour"

// PartISOWeekday is the ISO 8601 weekday of the (local) calendar day:
// 1 = Monday through 7 = Sunday.
const PartISOWeekday = "iso_weekday"

// partList is the part set in the order refusals name it.
const partList = "year, month, day, year_month, year_month_day, month_day, hour, iso_weekday"

var validParts = map[string]bool{
	"year":           true,
	"month":          true,
	"day":            true,
	"year_month":     true,
	"year_month_day": true,
	"month_day":      true,
	PartHour:         true,
	PartISOWeekday:   true,
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
	if err := paramread.Decode(paramread.Attribute, string(types.ATTR_DATE_PART), raw, &p); err != nil {
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
// day, so every row would read hour 0). Any other type is the
// accepted-types table's PULSE_FIELD_TYPE_UNSUPPORTED (as is a date
// under `hour`); an absent field is PROCESSING_CONFIG.
func CheckField(part, field string, f *encoding.Field) error {
	if f == nil {
		return errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("date_part attribute requires a date or datetime field, got %q", field))
	}
	if cerr := fieldaccept.Refusal(string(types.ATTR_DATE_PART), fieldaccept.SlotField, field, f.Type); cerr != nil {
		return cerr
	}
	if part == PartHour && f.Type != encoding.FieldTypeDateTime {
		return errors.NewCodedErrorWithDetails(errors.PULSE_FIELD_TYPE_UNSUPPORTED,
			fmt.Sprintf("ATTR_DATE_PART part=hour reads a time of day; %s is of type %s", field, f.Type),
			map[string]any{
				"type":          string(types.ATTR_DATE_PART),
				"slot":          string(fieldaccept.SlotField),
				"field":         field,
				"field_type":    f.Type.String(),
				"accepts_types": []string{encoding.FieldTypeDateTime.String()},
				"part":          part,
			})
	}
	return nil
}
