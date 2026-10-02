package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Date-family field adapter.
//
// Two on-wire field types carry a calendar instant and both are legal
// input to the date-keyed operators (GROUP_DATE, GROUP_DATE_RANGES,
// FILTER_DATE_RANGES):
//
//   - `date`     — whole epoch DAYS   (uint32 on the wire)
//   - `datetime` — whole epoch SECONDS (uint64 on the wire)
//
// Everything downstream of this file — labeled-range matching, calendar
// component bucketing, the ISO-8601 period-boundary formatter — speaks
// epoch DAYS and nothing else. A `datetime` column is therefore
// truncated to the day containing its instant exactly once, here, by
// encoding.DateTimeToDay. Truncation discards the time of day and never
// rounds (23:59:59 stays on its own day), and the timezone policy is
// naive UTC, matching encoding.ParseDateTime.
//
// The two helpers below are the only sanctioned way an operator reads a
// date-family column: resolveDateFieldSeconds decides ONCE at
// construction how a column's decoded values must be read, and
// epochDayFromValue applies that decision per record. No call site
// open-codes a seconds-to-days division.

// resolveDateFieldSeconds classifies fieldName on schema and reports
// whether its decoded record values are epoch SECONDS (a `datetime`
// column, true) or epoch DAYS (a `date` column, false).
//
// strict selects the enforcement posture:
//
//   - strict=true  — a field present on the schema that is neither
//     `date` nor `datetime` is a PROCESSING_CONFIG error naming the
//     operator. This is the posture of the operators that have always
//     policed their input type (GROUP_DATE_RANGES, FILTER_DATE_RANGES).
//   - strict=false — a non-temporal field is accepted and read as
//     epoch days, exactly as it was before `datetime` existed.
//     GROUP_DATE uses this posture: it has never validated its Field
//     against the schema, and tightening that here would reject
//     requests that run today (an integer epoch-day column is a
//     legitimate, if unusual, GROUP_DATE input).
//
// A nil schema or a field absent from the schema resolves to "days" and
// no error under either posture — the probe paths (extension
// validation, a Group with no Field) construct operators without a
// cohort behind them.
func resolveDateFieldSeconds(operator, fieldName string, schema *encoding.Schema, strict bool) (bool, error) {
	if schema == nil {
		return false, nil
	}
	f := schema.Field(fieldName)
	if f == nil {
		return false, nil
	}
	switch f.Type {
	case encoding.FieldTypeDateTime:
		return true, nil
	case encoding.FieldTypeDate:
		return false, nil
	}
	if strict {
		return false, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s requires a date or datetime field, got %q on field %q", operator, f.Type, fieldName))
	}
	return false, nil
}

// epochDayFromValue converts a decoded record value for a date-family
// column into the epoch-day integer every date operator buckets on.
// seconds is the flag resolveDateFieldSeconds returned for that column:
// true means the value is epoch seconds and gets day-truncated, false
// means it already is an epoch-day count and passes through untouched.
func epochDayFromValue(v float64, seconds bool) int64 {
	if seconds {
		return encoding.DateTimeToDay(int64(v))
	}
	return int64(v)
}
