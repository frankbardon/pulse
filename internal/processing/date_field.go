package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
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
// truncated to the day containing its instant exactly once, here.
// Truncation discards the time of day and never rounds (23:59:59 stays
// on its own day). The day is the UTC day (encoding.DateTimeToDay)
// unless the slot carries a non-UTC zone in its `tz` — written there by
// the request's zone resolution (slot `tz` → request `time_zone` →
// Options.DefaultTimeZone) — in which case it is the LOCAL day a wall
// clock in that zone shows (temporal.LocalDay).
//
// The helpers below are the only sanctioned way an operator reads a
// date-family column: resolveDateField decides ONCE at construction how
// a column's decoded values must be read (seconds or days, and in which
// zone), and dateField.epochDay applies that decision per record. No
// call site open-codes a seconds-to-days division or a zone offset.

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

// slotZones memoises the zones the date-family operators load from a
// slot `tz`. The service resolves every zone through the instance cache
// before execution and the operator factories (which see only the slot
// and the schema) re-load the same name here; a Zone is immutable and
// built from Pulse's one embedded tz database, so sharing it across
// instances changes nothing but the table-build cost.
var slotZones temporal.Cache

// dateField is how one operator reads its date-family column: seconds
// is resolveDateFieldSeconds' answer and zone the slot's resolved zone,
// nil for UTC, a UTC-equivalent zone or a `date` column — the nil case
// is exactly the pre-zone code path.
type dateField struct {
	seconds bool
	zone    *temporal.Zone
}

// resolveDateField is resolveDateFieldSeconds plus the slot zone tz
// ("" = none). A zone applies only to a `datetime` column (seconds);
// the zone resolver never lets a non-UTC zone reach any other field
// (an explicit `tz` on a schema field that is not `datetime` is refused,
// an inherited one is not written, a derived field is refused). An
// unknown tz is the PULSE_TIMEZONE_UNKNOWN coded error.
func resolveDateField(operator, fieldName, tz string, schema *encoding.Schema, strict bool) (dateField, error) {
	seconds, err := resolveDateFieldSeconds(operator, fieldName, schema, strict)
	if err != nil {
		return dateField{}, err
	}
	df := dateField{seconds: seconds}
	if tz == "" {
		return df, nil
	}
	z, err := slotZones.Load(tz)
	if err != nil {
		return dateField{}, err
	}
	if seconds && !z.IsUTC() {
		df.zone = z
	}
	return df, nil
}

// epochDay is epochDayFromValue in the column's zone: the local epoch
// day of an instant when a non-UTC zone applies, the UTC path otherwise.
// The zoned arm is a separate call so this stays inlinable and the
// zone-free path costs one nil check over epochDayFromValue.
func (d dateField) epochDay(v float64) int64 {
	if d.zone == nil {
		return epochDayFromValue(v, d.seconds)
	}
	return d.localDay(v)
}

// localDay is the zoned arm of epochDay (zone non-nil, a `datetime`).
// Kept out of line so epochDay itself inlines.
//
//go:noinline
func (d dateField) localDay(v float64) int64 {
	return temporal.LocalDay(int64(v), d.zone)
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
