package synth

import (
	"bytes"
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// This file decides which statistic scores each scalar field type's
// marginal, and carries the two measures the report computes itself
// rather than through a test operator: a datetime's daily and weekly
// cycle, and a set option's effect size.
//
// # The statistic per type, and why
//
//   - u8 … u64, f32, f64, decimal128: TEST_KS + Cohen's d, as before.
//   - u4: TEST_KS + Cohen's d — the same arm as the wider integers. A
//     u4 is an ordinal 0..15 and reconstructs from the same per-level
//     histogram a u8 does (`discrete`), so scoring it differently would
//     make two columns generated the same way read on two scales.
//   - date: TEST_KS + Cohen's d over epoch DAYS.
//   - datetime: TEST_KS + Cohen's d over epoch SECONDS, plus Shape —
//     the hour-of-day and weekday histograms' total variation distance.
//     A planted schedule (Sunday closures, a night shift) lives in the
//     cycle, and both KS and d read the instants as one long line: a
//     twin drawing the right days at the wrong hours passes both.
//   - packed_bool: TEST_CHISQ + Cramér's V (= |phi| on the 2x2 table
//     against `_synthetic`), the categorical arm. A boolean's marginal
//     is one proportion; the χ² test of a 2x2 table is the two-sample
//     proportion test, and V puts it on the same w bands a categorical
//     field reads.
//   - set_*: per option, the selection rates (set_fields) plus V of the
//     option's own 2x2 table, computed directly — a multi-select is
//     neither a scalar KS reads nor one partition χ² reads.
//
// # The clock Shape reads
//
// The clock the datetime was generated on (shapeClockFor): its spec
// field's `time_zone` — one fixed zone for every row — or its
// `time_zone_field`, each row read in its own zone. A profile captured on
// a local clock (ProfileOptions.TimeZone / TimeZoneField, or the cohort's
// default zone field) carries that clock into the rebuilt spec, so the
// report reads the same marginals capture did. A field with neither, a
// UTC-equivalent zone, or no spec reads UTC — the report as it was before
// the local clock existed, byte for byte. A row whose zone cell is null
// (or outside the dictionary) has no local clock: it is left out of every
// bin, never read on UTC, and the report warns with the per-partition
// count. Every zone read is a call into internal/temporal.

// TimeShapeClockUTC is the clock TimeShapeFidelity reads hours and
// weekdays on when the datetime names no zone.
const TimeShapeClockUTC = "utc"

// TimeShapeClockLocal is the clock TimeShapeFidelity reads on when the
// datetime names a zone: TimeShapeFidelity.TimeZone (one fixed zone) or
// TimeShapeFidelity.TimeZoneField (each row's own zone).
const TimeShapeClockLocal = "local"

// TimeShapeFidelity is a datetime or date field's cycle comparison: the
// total variation distance between the source and synthetic partitions'
// hour-of-day (24 bins), ISO weekday (7 bins) and calendar-month (one
// bin per month either partition touches) histograms, each in [0, 1] —
// 0 when the two match, 1 when they share no bin. MonthTVD reads the
// volume series a trend or a seasonal swing lives in (month_weights).
// A date names a day, not an instant: it has no hour (HourTVD nil, the
// key absent) and no zone (Clock is always TimeShapeClockUTC), and its
// weekday and month are read off the epoch day itself.
type TimeShapeFidelity struct {
	// Clock is the clock the bins are read on: TimeShapeClockUTC, or
	// TimeShapeClockLocal with the zone (TimeZone) or the zone field
	// (TimeZoneField) named beside it.
	Clock         string `json:"clock"`
	TimeZone      string `json:"time_zone,omitempty"`
	TimeZoneField string `json:"time_zone_field,omitempty"`
	// HourTVD is set on every datetime shape and nil on a date's.
	HourTVD    *float64 `json:"hour_tvd,omitempty"`
	WeekdayTVD float64  `json:"weekday_tvd"`
	MonthTVD   float64  `json:"month_tvd"`
}

// shapeClock is the clock one datetime's shape is read on: a fixed zone
// (the UTC sentinel when unzoned) or a per-row zone field of the merged
// schema.
type shapeClock struct {
	zone     *temporal.Zone
	zoneName string // TimeShapeFidelity.TimeZone ("" = UTC or per-row)
	field    string // TimeShapeFidelity.TimeZoneField ("" = fixed)
	table    *temporal.ZoneTable
}

// shapeClockFor reads datetime field name's clock from spec: the zone
// its generation drew in (datetimeZoneParams). nil spec, no such field or
// no zone is UTC. A zone field the merged cohort does not carry as a
// categorical (an `"output": false` helper), or whose dictionary holds a
// name the tz database lacks, cannot be read per row: the shape falls
// back to UTC and the returned warning says so.
func shapeClockFor(spec *Spec, mergedSchema *encoding.Schema, name string) (shapeClock, string) {
	c := shapeClock{zone: temporal.UTC}
	if spec == nil {
		return c, ""
	}
	var fs *FieldSpec
	for i := range spec.Fields {
		if spec.Fields[i].Name == name {
			fs = &spec.Fields[i]
			break
		}
	}
	if fs == nil {
		return c, ""
	}
	zone, field, err := datetimeZoneParams(*fs)
	if err != nil {
		return c, ""
	}
	if field == "" {
		if zone != nil {
			c.zone, c.zoneName = zone, zone.Name()
		}
		return c, ""
	}
	f := mergedSchema.Field(field)
	if f == nil || !f.Type.IsCategorical() {
		return c, fmt.Sprintf("datetime field %q: its time_zone_field %q is not a categorical column of the compared cohort, "+
			"so its shape is read on UTC", name, field)
	}
	var names []string
	if f.Dictionary != nil {
		names = f.Dictionary.Values()
	}
	table, err := temporal.LoadZones(names, synthZones.Load)
	if err != nil {
		return c, fmt.Sprintf("datetime field %q: its time_zone_field %q holds a name that is not a known zone (%v), "+
			"so its shape is read on UTC", name, field, err)
	}
	c.field, c.table = field, table
	return c, ""
}

// zoneOf returns the zone this row's reading is binned in; ok is false
// for a per-row clock whose zone cell is null or outside the dictionary.
func (c *shapeClock) zoneOf(values map[string]float64, nulls map[string]bool) (*temporal.Zone, bool) {
	if c.field == "" {
		return c.zone, true
	}
	if nulls[c.field] {
		return nil, false
	}
	z := c.table.At(uint32(values[c.field]))
	return z, z != nil
}

// marginalTest returns the test that scores f's marginal against
// `_synthetic`, or nil for a type the fields section does not score
// (set_* and vectors have their own sections).
func marginalTest(f *encoding.Field) *types.Test {
	switch {
	case ksMarginalType(f.Type):
		return &types.Test{Type: types.TEST_KS, Field: f.Name, SplitBy: SyntheticFieldName}
	case f.Type.IsCategorical() || f.Type == encoding.FieldTypePackedBool:
		return &types.Test{Type: types.TEST_CHISQ, Rows: f.Name, Cols: SyntheticFieldName}
	}
	return nil
}

// ksMarginalType reports whether a type's marginal is scored with
// TEST_KS: the strict numerics plus u4, date and datetime.
func ksMarginalType(ft encoding.FieldType) bool {
	if ft.IsNumeric() {
		return true
	}
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeDate, encoding.FieldTypeDateTime:
		return true
	}
	return false
}

// cohensDType reports whether a KS-scored type also gets Cohen's d:
// every one but decimal128, whose float64 echo is lossy.
func cohensDType(ft encoding.FieldType) bool {
	return ksMarginalType(ft) && !ft.IsDecimal()
}

// attachTimeShapes sets FieldFidelity.Shape on every datetime and date
// entry with a result, decoding records at most once. Each datetime is
// read on its spec clock (shapeClockFor); spec nil reads every field on
// UTC. A date is read off its epoch day (temporal.ISOWeekday /
// MonthIndex — the same running month index a datetime's bins use), with
// no hour bin and no zone.
func attachTimeShapes(report *FidelityReport, mergedSchema *encoding.Schema, records []byte, spec *Spec) {
	type cycle struct {
		date    bool // a date field: epoch days, no hour, no zone
		clock   shapeClock
		hour    [2][24]int
		weekday [2][7]int
		month   map[int64]*[2]int // running month index → per-partition count
		n       [2]int
		unzoned [2]int // non-null readings with no local clock, left out
	}
	var entries []*FieldFidelity
	for _, e := range report.Fields {
		if e.Result == nil {
			continue
		}
		if f := mergedSchema.Field(e.Field); f != nil && (f.Type == encoding.FieldTypeDateTime || f.Type == encoding.FieldTypeDate) {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return
	}
	cycles := make([]cycle, len(entries))
	for i, e := range entries {
		cycles[i].month = map[int64]*[2]int{}
		if mergedSchema.Field(e.Field).Type == encoding.FieldTypeDate {
			cycles[i].date = true
			cycles[i].clock = shapeClock{zone: temporal.UTC}
			continue
		}
		clock, warning := shapeClockFor(spec, mergedSchema, e.Field)
		cycles[i].clock = clock
		if warning != "" {
			report.Warnings = append(report.Warnings, warning)
		}
	}
	rr := encx.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	wide := make(map[string]any, len(mergedSchema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		part := 0
		if values[SyntheticFieldName] != 0 {
			part = 1
		}
		for i, e := range entries {
			if nulls[e.Field] {
				continue
			}
			v := values[e.Field]
			if math.IsNaN(v) {
				continue
			}
			c := &cycles[i]
			var mi int64
			if c.date {
				day := int64(v)
				c.weekday[part][temporal.ISOWeekday(day)-1]++
				mi = temporal.MonthIndex(day)
			} else {
				zone, ok := c.clock.zoneOf(values, nulls)
				if !ok {
					c.unzoned[part]++
					continue
				}
				p := temporal.LocalParts(int64(v), zone)
				c.hour[part][p.Hour]++
				c.weekday[part][temporal.ISOWeekdayOf(p.Weekday)-1]++
				mi = int64(p.Year)*12 + int64(p.Month) - 1
			}
			mc := c.month[mi]
			if mc == nil {
				mc = &[2]int{}
				c.month[mi] = mc
			}
			mc[part]++
			c.n[part]++
		}
	}
	for i, e := range entries {
		c := &cycles[i]
		if c.unzoned[0] > 0 || c.unzoned[1] > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("datetime field %q: %d source and %d synthetic non-null reading(s) "+
				"have a null zone in %q and no local clock; they are left out of its hour, weekday and month shape",
				e.Field, c.unzoned[0], c.unzoned[1], c.clock.field))
		}
		if c.n[0] == 0 || c.n[1] == 0 {
			continue
		}
		e.Shape = &TimeShapeFidelity{
			Clock:      TimeShapeClockUTC,
			WeekdayTVD: histogramTVD(c.weekday[0][:], c.weekday[1][:], c.n[0], c.n[1]),
			MonthTVD:   monthTVD(c.month, c.n[0], c.n[1]),
		}
		if !c.date {
			hour := histogramTVD(c.hour[0][:], c.hour[1][:], c.n[0], c.n[1])
			e.Shape.HourTVD = &hour
		}
		if c.clock.zoneName != "" || c.clock.field != "" {
			e.Shape.Clock = TimeShapeClockLocal
			e.Shape.TimeZone, e.Shape.TimeZoneField = c.clock.zoneName, c.clock.field
		}
	}
}

// monthTVD is histogramTVD over the calendar months either partition
// touches, laid out in month order (never map order), so the sum is
// order-fixed.
func monthTVD(months map[int64]*[2]int, na, nb int) float64 {
	keys := make([]int64, 0, len(months))
	for k := range months {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	a := make([]int, len(keys))
	b := make([]int, len(keys))
	for i, k := range keys {
		a[i], b[i] = months[k][0], months[k][1]
	}
	return histogramTVD(a, b, na, nb)
}

// histogramTVD is half the L1 distance between two count histograms
// over the same bins, each normalised by its own total. Summed in bin
// order with no product, so it is FMA-free and order-fixed.
func histogramTVD(a, b []int, na, nb int) float64 {
	var sum float64
	for i := range a {
		sum += math.Abs(float64(a[i])/float64(na) - float64(b[i])/float64(nb))
	}
	return sum / 2
}

// setOptionEffectSize is Cramér's V of one set option's 2x2 table —
// selected or not, by partition — which for a 2x2 table is |phi|:
// |ad − bc| / sqrt(r1·r2·c1·c2). Read on Cohen's w bands, exactly as a
// categorical field's χ² entry is. nil when a margin is zero (an empty
// partition, or an option selected on every row or on none).
func setOptionEffectSize(srcSel, srcN, synSel, synN int) *FieldEffectSize {
	a, b := float64(srcSel), float64(srcN-srcSel) // source: selected, not
	c, d := float64(synSel), float64(synN-synSel) // synthetic: selected, not
	r1, r2 := a+b, c+d
	c1, c2 := a+c, b+d
	denom := float64(float64(r1*r2) * float64(c1*c2))
	if !(denom > 0) {
		return nil
	}
	v := math.Abs(float64(a*d)-float64(b*c)) / math.Sqrt(denom)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return newFieldEffectSize(EffectMeasureCramersV, v, effectBandsW)
}
