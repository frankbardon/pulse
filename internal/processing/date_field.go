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
// clock in that zone shows (temporal.LocalDay). A slot carrying a
// per-row zone field in its `tz_field` reads each instant on the local
// day of the zone its own row names (dateField.epochDayAt); a row whose
// zone cell is null has a null local day.
//
// The helpers below are the only sanctioned way an operator reads a
// date-family column: resolveDateField decides ONCE at construction how
// a column's decoded values must be read (seconds or days, and in which
// zone), and dateField.epochDay (epochDayAt) applies that decision per record. No
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
		return false, errors.NewCodedError(errors.PULSE_FIELD_TYPE_UNSUPPORTED,
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
//
// zones is the per-row arm (slot `tz_field`, or an inherited request
// `time_zone_field` that descx.ZonedRequest wrote there): the zone of
// every entry of the categorical zoneField's dictionary, indexed by
// dictionary code, built once per constructed operator and forked so
// each worker's lookup caches are its own. Non-nil only over a
// `datetime` column; a record's instant is then read in the zone its
// own zoneField cell names, and a NULL cell (or a code outside the
// dictionary) makes the row's local value null — never a silent UTC
// day (rowZone).
type dateField struct {
	seconds   bool
	zone      *temporal.Zone
	zoneField string
	zones     *temporal.ZoneTable
}

// resolveDateField is resolveDateFieldSeconds plus the slot zone tz
// ("" = none) or per-row zone field tzField ("" = none; the zone
// resolver never lets both reach one slot). A zone applies only to a
// `datetime` column (seconds); the zone resolver never lets a non-UTC
// zone reach any other field (an explicit `tz` / `tz_field` on a schema
// field that is not `datetime` is refused, an inherited one is not
// written, a derived field is refused). An unknown tz is the
// PULSE_TIMEZONE_UNKNOWN coded error; so is a zone-field dictionary
// entry that names no zone (temporal.LoadZones, which ResolveZones
// refuses first).
func resolveDateField(operator, fieldName, tz, tzField string, schema *encoding.Schema, strict bool) (dateField, error) {
	seconds, err := resolveDateFieldSeconds(operator, fieldName, schema, strict)
	if err != nil {
		return dateField{}, err
	}
	df := dateField{seconds: seconds}
	if tzField != "" && seconds {
		zones, err := loadZoneTable(operator, tzField, schema)
		if err != nil {
			return dateField{}, err
		}
		df.zoneField, df.zones = tzField, zones
		return df, nil
	}
	if tz == "" {
		return df, nil
	}
	z, err := slotZones.Load(tz)
	if err != nil {
		return dateField{}, err
	}
	if seconds && !z.IsUTC() {
		// A fork per constructed operator: the parallel arms build one
		// operator set per worker, so each worker's lookup cache is its
		// own instead of every worker contending on the shared Zone's.
		df.zone = z.Fork()
	}
	return df, nil
}

// loadZoneTable builds the per-row zone table of zone field name: its
// dictionary's entries loaded through the process-wide slotZones cache
// (a repeated name shares one Zone), then forked for this operator. The
// field-reference pass and ResolveZones refuse an absent or
// non-categorical zone field before any operator is built; reaching
// one here is a wiring fault, PROCESSING_CONFIG naming the operator.
func loadZoneTable(operator, name string, schema *encoding.Schema) (*temporal.ZoneTable, error) {
	var f *encoding.Field
	if schema != nil {
		f = schema.Field(name)
	}
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s tz_field %q must name a categorical schema field of IANA zone names", operator, name),
			map[string]any{"operator": operator, "tz_field": name})
	}
	t, err := temporal.LoadZones(f.Dictionary.Values(), slotZones.Load)
	if err != nil {
		return nil, err
	}
	return t.Fork(), nil
}

// perRow reports whether a per-row zone field applies.
func (d dateField) perRow() bool { return d.zones != nil }

// rowZone is the zone record r's instant is read in under a per-row
// zone field: the zone its zoneField cell names, nil when that zone is
// UTC-equivalent (the zone-free arithmetic applies). ok=false when the
// cell is null or names no dictionary entry: the row's local value is
// null. Reads one decoded value and indexes a slice — no allocation.
func (d dateField) rowZone(r *Record) (z *temporal.Zone, ok bool) {
	code, ok := r.NumericValue(d.zoneField)
	if !ok {
		return nil, false
	}
	z = d.zones.At(uint32(code))
	if z == nil {
		return nil, false
	}
	if z.IsUTC() {
		return nil, true
	}
	return z, true
}

// epochDay is epochDayFromValue in the column's zone: the local epoch
// day of an instant when a non-UTC zone applies, the UTC path otherwise.
// The zoned arm is a separate call so this stays inlinable and the
// zone-free path costs one nil check over epochDayFromValue. It ignores
// a per-row zone field; the record-reading operators call epochDayAt.
func (d dateField) epochDay(v float64) int64 {
	if d.zone == nil {
		return epochDayFromValue(v, d.seconds)
	}
	return d.localDay(v)
}

// epochDayAt is epochDay for value v of record r: under a per-row zone
// field the local day in r's own zone, ok=false when r's zone cell is
// null (rowZone). Without one it is epochDay(v), always ok.
func (d dateField) epochDayAt(r *Record, v float64) (int64, bool) {
	if d.zones == nil {
		return d.epochDay(v), true
	}
	return d.rowDay(r, v)
}

// rowDay is the per-row arm of epochDayAt, kept out of line so
// epochDayAt inlines.
//
//go:noinline
func (d dateField) rowDay(r *Record, v float64) (int64, bool) {
	z, ok := d.rowZone(r)
	if !ok {
		return 0, false
	}
	if z == nil {
		return epochDayFromValue(v, d.seconds), true
	}
	return temporal.LocalDay(int64(v), z), true
}

// localDay is the zoned arm of epochDay (zone non-nil, a `datetime`).
// Kept out of line so epochDay itself inlines.
//
//go:noinline
func (d dateField) localDay(v float64) int64 {
	return temporal.LocalDay(int64(v), d.zone)
}

// epochHour is the bucket unit of GROUP_DATE's `hour` component: the
// local epoch hour of an instant in the column's zone (the UTC hour when
// no zone applies). A day count — only reachable on the schema-less
// probe paths, since `hour` refuses a non-datetime field against a
// schema — reads as hour 00 of that day.
func (d dateField) epochHour(v float64) int64 {
	if !d.seconds {
		return temporal.LocalHour(temporal.DayToUnix(int64(v)), temporal.UTC)
	}
	z := d.zone
	if z == nil {
		z = temporal.UTC
	}
	return temporal.LocalHour(int64(v), z)
}

// epochHourAt is epochHour for value v of record r: under a per-row
// zone field the local hour in r's own zone, ok=false when r's zone
// cell is null (rowZone).
func (d dateField) epochHourAt(r *Record, v float64) (int64, bool) {
	if d.zones == nil {
		return d.epochHour(v), true
	}
	z, ok := d.rowZone(r)
	if !ok {
		return 0, false
	}
	if z == nil {
		z = temporal.UTC
	}
	return temporal.LocalHour(int64(v), z), true
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
