package feature

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_DATE_FEATURES, newDateFeatures)
}

// dateFeatures expands a date-family field into derived columns: year,
// month, day, day-of-week (ISO 8601: 1=Monday..7=Sunday) and quarter (1..4),
// plus hour (0..23) over a `datetime`. Default column names are
// "<field>_year", "<field>_month", etc. A Label override substitutes
// the prefix.
//
// A `date` is days-since-Unix-epoch (the encoding.FieldTypeDate
// convention) read on the calendar, mirroring ATTR_DATE_PART; no zone
// ever applies to it. A `datetime` is an instant whose features are the
// wall clock in the slot's zone (`tz`, written there by the request's
// zone resolution) via temporal.LocalParts — UTC when none applies. A
// field absent from the schema (derived) is read as epoch days; the
// zone resolver refuses a non-UTC zone onto one.
//
// Under a per-row zone field (`tz_field`, or an inherited request
// `time_zone_field`) a `datetime` is read on the wall clock of the zone
// its own row's zone cell names (zones, indexed by dictionary code);
// a null zone cell makes every column of that row null — the instant
// is known, its local calendar is not — never the UTC parts.
type dateFeatures struct {
	prefix    string
	parts     []string
	seconds   bool
	zone      *temporal.Zone
	zoneField string
	zones     *temporal.ZoneTable
}

// featureZones memoises zones loaded from a slot `tz` (a Zone is
// immutable; each operator forks its own lookup cache).
var featureZones temporal.Cache

func newDateFeatures(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_DATE_FEATURES requires a field")
	}
	c := &dateFeatures{prefix: feat.Label, parts: dateFeatureParts}
	if c.prefix == "" {
		c.prefix = feat.Field
	}
	if schema != nil {
		if f := schema.Field(feat.Field); f != nil {
			switch f.Type {
			case encoding.FieldTypeDate:
			case encoding.FieldTypeDateTime:
				c.seconds, c.parts = true, dateTimeFeatureParts
			default:
				// The accepted-types table's refusal; predict raises
				// the same one.
				return nil, fieldaccept.Refusal(string(types.FEAT_DATE_FEATURES), fieldaccept.SlotField, feat.Field, f.Type)
			}
		}
	}
	if feat.TimeZoneField != "" && c.seconds {
		zones, err := featureZoneTable(feat.TimeZoneField, schema)
		if err != nil {
			return nil, err
		}
		c.zoneField, c.zones = feat.TimeZoneField, zones
		return c, nil
	}
	if feat.TimeZone != "" {
		z, err := featureZones.Load(feat.TimeZone)
		if err != nil {
			return nil, err
		}
		if c.seconds && !z.IsUTC() {
			c.zone = z.Fork()
		}
	}
	return c, nil
}

// featureZoneTable builds the per-row zone table of zone field name
// (its dictionary's entries through featureZones), forked for this
// operator. ResolveZones and the field-reference pass refuse an absent
// or non-categorical zone field first; reaching one here is a wiring
// fault, PROCESSING_CONFIG.
func featureZoneTable(name string, schema *encoding.Schema) (*temporal.ZoneTable, error) {
	var f *encoding.Field
	if schema != nil {
		f = schema.Field(name)
	}
	if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("FEAT_DATE_FEATURES tz_field %q must name a categorical schema field of IANA zone names", name),
			map[string]any{"operator": string(types.FEAT_DATE_FEATURES), "tz_field": name})
	}
	t, err := temporal.LoadZones(f.Dictionary.Values(), featureZones.Load)
	if err != nil {
		return nil, err
	}
	return t.Fork(), nil
}

// rowZone is the zone record r's instant is read in: the slot zone
// (nil: UTC) without a per-row zone field, else the zone r's zone cell
// names; ok=false when that cell is null or names no dictionary entry.
func (c *dateFeatures) rowZone(r Record) (*temporal.Zone, bool) {
	if c.zones == nil {
		return c.zone, true
	}
	code, ok := r.NumericValue(c.zoneField)
	if !ok {
		return nil, false
	}
	z := c.zones.At(uint32(code))
	return z, z != nil
}

var dateFeatureParts = []string{"year", "month", "day", "dow", "quarter"}

// dateTimeFeatureParts is dateFeatureParts plus the wall-clock hour,
// emitted only over a `datetime`.
var dateTimeFeatureParts = []string{"year", "month", "day", "dow", "quarter", "hour"}

func (c *dateFeatures) Compute(records []Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, len(c.parts))
	for _, p := range c.parts {
		out[c.columnName(p)] = Output{
			Values: make([]float64, len(records)),
			Nulls:  make([]bool, len(records)),
		}
	}

	for i, r := range records {
		v, ok := r.NumericValue(field)
		var z *temporal.Zone
		if ok && c.seconds {
			z, ok = c.rowZone(r)
		}
		if !ok {
			for _, p := range c.parts {
				out[c.columnName(p)].Nulls[i] = true
			}
			continue
		}
		if c.seconds {
			y, m, d, dow, q, h := decodeInstantParts(int64(v), z)
			out[c.columnName("year")].Values[i] = y
			out[c.columnName("month")].Values[i] = m
			out[c.columnName("day")].Values[i] = d
			out[c.columnName("dow")].Values[i] = dow
			out[c.columnName("quarter")].Values[i] = q
			out[c.columnName("hour")].Values[i] = h
			continue
		}
		y, m, d, dow, q := decodeDateParts(v)
		out[c.columnName("year")].Values[i] = y
		out[c.columnName("month")].Values[i] = m
		out[c.columnName("day")].Values[i] = d
		out[c.columnName("dow")].Values[i] = dow
		out[c.columnName("quarter")].Values[i] = q
	}
	return out, nil
}

func (c *dateFeatures) PrePass(_ Record, _ string) error { return nil }

func (c *dateFeatures) Finalize() error { return nil }

func (c *dateFeatures) EmitRow(r Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, len(c.parts))
	v, ok := r.NumericValue(field)
	var z *temporal.Zone
	if ok && c.seconds {
		z, ok = c.rowZone(r)
	}
	if !ok {
		for _, p := range c.parts {
			out[c.columnName(p)] = Output{Values: []float64{0}, Nulls: []bool{true}}
		}
		return out, nil
	}
	if c.seconds {
		y, m, d, dow, q, h := decodeInstantParts(int64(v), z)
		out[c.columnName("year")] = Output{Values: []float64{y}}
		out[c.columnName("month")] = Output{Values: []float64{m}}
		out[c.columnName("day")] = Output{Values: []float64{d}}
		out[c.columnName("dow")] = Output{Values: []float64{dow}}
		out[c.columnName("quarter")] = Output{Values: []float64{q}}
		out[c.columnName("hour")] = Output{Values: []float64{h}}
		return out, nil
	}
	y, m, d, dow, q := decodeDateParts(v)
	out[c.columnName("year")] = Output{Values: []float64{y}}
	out[c.columnName("month")] = Output{Values: []float64{m}}
	out[c.columnName("day")] = Output{Values: []float64{d}}
	out[c.columnName("dow")] = Output{Values: []float64{dow}}
	out[c.columnName("quarter")] = Output{Values: []float64{q}}
	return out, nil
}

// decodeDateParts converts a days-since-Unix-epoch value into the five
// derived parts (year, month, day, day-of-week, quarter) that
// FEAT_DATE_FEATURES emits. Shared between Compute and EmitRow.
func decodeDateParts(v float64) (year, month, day, dow, quarter float64) {
	t := temporal.DayToTime(int64(v))
	yy, mm, dd := t.Date()
	return float64(yy), float64(mm), float64(dd), float64(temporal.ISOWeekday(int64(v))), float64((int(mm)-1)/3 + 1)
}

// decodeInstantParts is decodeDateParts for a `datetime` instant sec
// (epoch seconds) on the wall clock of zone z (the slot's or the row's;
// nil: UTC), plus the hour. dow is the LOCAL day's ISO weekday.
func decodeInstantParts(sec int64, z *temporal.Zone) (year, month, day, dow, quarter, hour float64) {
	if z == nil {
		z = temporal.UTC
	}
	p := temporal.LocalParts(sec, z)
	return float64(p.Year), float64(p.Month), float64(p.Day), float64(temporal.ISOWeekdayOf(p.Weekday)), float64((int(p.Month)-1)/3 + 1), float64(p.Hour)
}

func (c *dateFeatures) columnName(part string) string {
	return fmt.Sprintf("%s_%s", c.prefix, part)
}
