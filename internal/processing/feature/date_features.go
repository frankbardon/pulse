package feature

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_DATE_FEATURES, newDateFeatures)
}

// dateFeatures expands a date-family field into derived columns: year,
// month, day, day-of-week (0=Sunday..6=Saturday) and quarter (1..4),
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
type dateFeatures struct {
	prefix  string
	parts   []string
	seconds bool
	zone    *temporal.Zone
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
				return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
					fmt.Sprintf("FEAT_DATE_FEATURES: field %q must be of type date or datetime, got %s",
						feat.Field, f.Type))
			}
		}
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
		if !ok {
			for _, p := range c.parts {
				out[c.columnName(p)].Nulls[i] = true
			}
			continue
		}
		if c.seconds {
			y, m, d, dow, q, h := c.decodeInstantParts(int64(v))
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
	if !ok {
		for _, p := range c.parts {
			out[c.columnName(p)] = Output{Values: []float64{0}, Nulls: []bool{true}}
		}
		return out, nil
	}
	if c.seconds {
		y, m, d, dow, q, h := c.decodeInstantParts(int64(v))
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
	return float64(yy), float64(mm), float64(dd), float64(t.Weekday()), float64((int(mm)-1)/3 + 1)
}

// decodeInstantParts is decodeDateParts for a `datetime` instant sec
// (epoch seconds) on the wall clock of the slot's zone (UTC when none
// applies), plus the hour. dow keeps Sunday = 0.
func (c *dateFeatures) decodeInstantParts(sec int64) (year, month, day, dow, quarter, hour float64) {
	z := c.zone
	if z == nil {
		z = temporal.UTC
	}
	p := temporal.LocalParts(sec, z)
	return float64(p.Year), float64(p.Month), float64(p.Day), float64(p.Weekday), float64((int(p.Month)-1)/3 + 1), float64(p.Hour)
}

func (c *dateFeatures) columnName(part string) string {
	return fmt.Sprintf("%s_%s", c.prefix, part)
}
