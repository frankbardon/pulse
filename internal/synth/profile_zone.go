package synth

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// Local-clock datetime capture (#594, U76 E3-S2).
//
// A `datetime` column stores instants, so a capture on the UTC clock
// reproduces the UTC weekday / hour / month marginals — and quietly
// shifts every local one by the zone offset: a depot open 08:00-18:00 in
// New York shows up as 13:00-23:00, and Sunday evening as Monday. The
// capture clock picks the zone each instant is read in:
//
//   - ProfileOptions.TimeZone: one fixed IANA zone for every row;
//   - ProfileOptions.TimeZoneField: a categorical field of IANA names,
//     each row read in its own zone;
//   - neither, and the cohort declares a default zone field
//     (encoding.Schema.ZoneField): that field, per row — the explicit
//     options win;
//   - none of these, or a UTC-equivalent TimeZone: UTC, byte-identical to the capture before the
//     options existed (temporal.LocalParts / LocalDay under the UTC
//     sentinel are exactly the UTC reads).
//
// Every zone read is a call into internal/temporal
// (TestNoZoneMathOutsideTemporal).

// profileDSTPolicy is the dst_policy SpecFromProfile emits beside a
// captured zone. A captured local reading always exists, but a
// REFILLED one can land in a spring-forward gap (an hour weight drawn on
// a transition day), and the distribution's default `error` would abort
// a from-profile run on it. `earlier` reads a gap reading at the
// pre-transition offset — 02:30 on a skipped hour becomes 03:30, the
// java.time / RFC 5545 convention — and takes an overlap's first
// occurrence; the run reports the resolved counts as its usual terminal
// warning.
const profileDSTPolicy = "earlier"

// captureClock is the clock a datetime column is captured on: a fixed
// zone (UTC when nothing names one) or a per-row zone field.
type captureClock struct {
	zone     *temporal.Zone // fixed zone; the UTC sentinel when unzoned
	zoneName string         // DatetimeProfile.TimeZone ("" = UTC)
	field    string         // DatetimeProfile.TimeZoneField ("" = fixed)
	table    *temporal.ZoneTable
}

// resolveCaptureClock reads the capture clock from opts and the
// cohort's declared default zone field. An unknown zone name is
// PULSE_TIMEZONE_UNKNOWN; naming both options, or a zone field that is
// not a categorical field of the cohort, is SERVICE_VALIDATION.
func resolveCaptureClock(schema *encoding.Schema, opts ProfileOptions) (captureClock, error) {
	c := captureClock{zone: temporal.UTC}
	switch {
	case opts.TimeZone != "" && opts.TimeZoneField != "":
		return c, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"profile time_zone and time_zone_field are exclusive; name one zone or one zone field",
			map[string]any{"time_zone": opts.TimeZone, "time_zone_field": opts.TimeZoneField})
	case opts.TimeZone != "":
		z, err := synthZones.Load(opts.TimeZone)
		if err != nil {
			return c, errors.NewCodedErrorWithDetails(errors.PULSE_TIMEZONE_UNKNOWN,
				fmt.Sprintf("profile time_zone %q is not \"UTC\" or an IANA Area/Location name in the tz database", opts.TimeZone),
				map[string]any{"option": "time_zone", errors.DetailTimeZone: opts.TimeZone})
		}
		// A UTC-equivalent zone (UTC, Etc/UTC) IS the UTC capture: it
		// records no zone, so `--time-zone UTC` reproduces the pre-option
		// document byte for byte even over a declared-zone cohort.
		if !z.IsUTC() {
			c.zone, c.zoneName = z, opts.TimeZone
		}
		return c, nil
	case opts.TimeZoneField != "":
		c.field = opts.TimeZoneField
	case schema.HasZoneField():
		c.field = schema.ZoneField
	default:
		return c, nil
	}
	f := schema.Field(c.field)
	if f == nil || !f.Type.IsCategorical() {
		return c, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("profile time_zone_field %q must name a categorical field of the cohort holding IANA zone names", c.field),
			map[string]any{"time_zone_field": c.field})
	}
	var names []string
	if f.Dictionary != nil {
		names = f.Dictionary.Values()
	}
	table, err := temporal.LoadZones(names, synthZones.Load)
	if err != nil {
		if ce, ok := err.(*errors.CodedError); ok {
			details := make(map[string]any, len(ce.Details)+1)
			for k, v := range ce.Details {
				details[k] = v
			}
			details["time_zone_field"] = c.field
			return c, errors.NewCodedErrorWithDetails(ce.Code,
				fmt.Sprintf("profile time_zone_field %q: %s", c.field, ce.Message), details)
		}
		return c, err
	}
	c.table = table
	return c, nil
}

// zoneOf returns the zone this row's instants are read in. ok is false
// for a per-row clock whose zone cell is null (or outside the
// dictionary): such a reading has no local clock.
func (c *captureClock) zoneOf(values map[string]float64, nulls map[string]bool) (*temporal.Zone, bool) {
	if c.field == "" {
		return c.zone, true
	}
	if nulls[c.field] {
		return nil, false
	}
	z := c.table.At(uint32(values[c.field]))
	return z, z != nil
}

// unzonedReadingsWarning is the capture's line for a datetime whose
// zone cell was null on some non-null readings: they have no local
// clock, so they are left out of the range and every count rather than
// read on UTC (which would mix two clocks in one histogram).
func unzonedReadingsWarning(field, zoneField string, n int) string {
	return fmt.Sprintf("datetime field %q: %d non-null reading(s) have a null zone in %q and no local clock; "+
		"they are left out of its day range and its weekday, hour and month counts", field, n, zoneField)
}

// zoneParamsFromProfile adds the captured clock to rebuilt
// uniform_datetime params: the zone, or the zone field, plus
// profileDSTPolicy. A UTC-clock document adds nothing; a hand-edited
// one naming both keeps both, so the spec's own exclusivity refusal
// reports it rather than one being dropped silently.
func zoneParamsFromProfile(params map[string]any, dp *DatetimeProfile) {
	if dp.TimeZone == "" && dp.TimeZoneField == "" {
		return
	}
	if dp.TimeZone != "" {
		params["time_zone"] = dp.TimeZone
	}
	if dp.TimeZoneField != "" {
		params["time_zone_field"] = dp.TimeZoneField
	}
	params["dst_policy"] = profileDSTPolicy
}

// profileNullRate is the captured null rate of field name (0 when the
// document does not carry it).
func profileNullRate(p *Profile, name string) float64 {
	for i := range p.Fields {
		if p.Fields[i].Name == name {
			return p.Fields[i].NullRate
		}
	}
	return 0
}
