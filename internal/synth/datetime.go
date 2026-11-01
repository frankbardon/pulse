package synth

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// Zone-aware datetime generation (`uniform_datetime`, #362).
//
// A `datetime` column stores UTC epoch SECONDS, but what a spec author
// knows is LOCAL time: a depot opens at 08:00 on its own clock, and a
// Sunday is a Sunday where the depot is. So the distribution draws a
// LOCAL WALL-CLOCK reading — a local calendar day by its weekday weight,
// a local hour by its hour weight, a uniform second inside that hour —
// and the reading is converted to the instant it names in the field's
// zone, fixed (`time_zone`) or per row (`time_zone_field`, a categorical
// field of IANA names). Every piece of zone arithmetic is a call into
// internal/temporal (TestNoZoneMathOutsideTemporal).
//
// # Two representations, one boundary
//
// Until the conversion stage runs, a ZONED datetime field's row value is
// that wall-clock reading as float64 seconds; after it, the UTC instant.
// The stage (datetimeStage.resolve) runs inside drawRow after the model
// stage and BEFORE the rule pass, so:
//
//   - every generation arm that can touch the field (a pair, the copula,
//     a model) writes the local reading, which is then converted — the
//     zone applies whoever drew the value;
//   - rules and constraints read, and write, UTC epoch seconds — the
//     value the file stores. A rule writing a datetime writes an instant;
//     a rule rewriting the zone field after the stage does not move a
//     datetime already resolved against it.
//
// An UNZONED field (no `time_zone`, or a UTC-equivalent one) has no
// stage entry at all: its reading already is the instant, which is what
// keeps such a field's bytes independent of the stage.
//
// # Determinism
//
// The sampler consumes exactly four draws per row whatever the weights —
// a weekday, an occurrence of it, an hour, a second — so the per-row RNG
// sequence is a function of the spec alone. The conversion stage
// consumes NO randomness. A spec without a zoned datetime field compiles
// no stage (nil), so its draw sequence and bytes are unchanged.
//
// # DST
//
// A wall reading a spring-forward skips, or a fall-back repeats, is
// resolved by `dst_policy` with the import's spelling and meaning
// (temporal.ParseAmbiguity): `error` (the default) refuses the run with
// PULSE_SYNTH_DST_NONEXISTENT / PULSE_SYNTH_DST_AMBIGUOUS, `earlier`
// takes the pre-transition offset, `later` the post-transition one. A
// run that resolved any reading reports how many, per field, as a
// terminal warning — the synth counterpart of the import's
// PULSE_IMPORT_DST_RESOLVED count — counted on ACCEPTED rows only.

// DistUniformDateTime is the zone-aware datetime distribution.
const DistUniformDateTime = "uniform_datetime"

// datetimeFieldType is the spec spelling of the one field type
// uniform_datetime writes.
const datetimeFieldType = "datetime"

// synthZones memoises zone loads for every generation in the process.
// Zones are immutable and built from the one embedded tz database, so a
// process-wide cache cannot leak state between specs.
var synthZones temporal.Cache

// uniformDateTimeSampler draws a local wall-clock reading. The weekday
// weight is a per-calendar-day INTENSITY: a weekday's share of the draws
// is its weight times the number of such days in [start, end], so over
// whole weeks the realised weekday marginal is the normalised weights.
type uniformDateTimeSampler struct {
	// firstDay[i] / count[i]: the first epoch day in range whose ISO
	// weekday is i+1 (Monday = 1), and how many such days the range has.
	firstDay [7]int64
	count    [7]int64
	dayCum   [7]float64
	dayTotal float64
	hourCum  [24]float64
	hourSum  float64

	// The zone the reading is read in. zone is nil for a per-row zone
	// (zoneField set) and for an unzoned / UTC-equivalent field.
	zone       *temporal.Zone
	zoneField  string
	policy     temporal.Ambiguity
	policyName string
}

func newUniformDateTimeSampler(f FieldSpec) (sampler, error) {
	if f.Type != datetimeFieldType {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: uniform_datetime draws a datetime and cannot fill a %s field; use uniform_date for a date", f.Name, f.Type),
			map[string]any{"field": f.Name, "type": f.Type})
	}
	startDay, endDay, err := parseDayRange(f, DistUniformDateTime)
	if err != nil {
		return nil, err
	}
	s := &uniformDateTimeSampler{}

	s.zone, s.zoneField, err = datetimeZoneParams(f)
	if err != nil {
		return nil, err
	}

	policyName, _, err := paramString(f.Name, f.Params, "dst_policy", "")
	if err != nil {
		return nil, err
	}
	policy, ok := temporal.ParseAmbiguity(policyName)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: dst_policy %q is not \"error\", \"earlier\" or \"later\"", f.Name, policyName),
			map[string]any{"field": f.Name, "option": "dst_policy", "value": policyName})
	}
	s.policy, s.policyName = policy, policy.String()

	weekday, err := profileWeights(f, "weekday_weights", 7)
	if err != nil {
		return nil, err
	}
	hours, err := profileWeights(f, "hour_weights", 24)
	if err != nil {
		return nil, err
	}

	startWeekday := temporal.ISOWeekday(startDay) // 1..7
	for i := range 7 {
		off := (int64(i+1) - startWeekday + 7) % 7
		first := startDay + off
		s.firstDay[i] = first
		if first <= endDay {
			s.count[i] = (endDay-first)/7 + 1
		}
		s.dayTotal += float64(weekday[i] * float64(s.count[i]))
		s.dayCum[i] = s.dayTotal
	}
	if !(s.dayTotal > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: no day in [start, end] has a positive weekday weight", f.Name),
			map[string]any{"field": f.Name})
	}
	for h := range 24 {
		s.hourSum += hours[h]
		s.hourCum[h] = s.hourSum
	}
	return s, nil
}

// datetimeZoneParams reads a uniform_datetime field's zone: a fixed
// non-UTC zone, a per-row zone field, or neither (nil, "") for an
// unzoned or UTC-equivalent field. The sampler and the rule functions
// (row_time.go) both read the zone through it, so the zone a rule reads
// a reading in is the one the reading was drawn in.
func datetimeZoneParams(f FieldSpec) (zone *temporal.Zone, field string, err error) {
	zoneName, hasZone, err := paramString(f.Name, f.Params, "time_zone", "")
	if err != nil {
		return nil, "", err
	}
	zoneField, hasZoneField, err := paramString(f.Name, f.Params, "time_zone_field", "")
	if err != nil {
		return nil, "", err
	}
	if hasZone && hasZoneField {
		return nil, "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: time_zone and time_zone_field are exclusive; name one zone or one zone field", f.Name),
			map[string]any{"field": f.Name, "time_zone": zoneName, "time_zone_field": zoneField})
	}
	if hasZone {
		z, zerr := synthZones.Load(zoneName)
		if zerr != nil {
			return nil, "", errors.NewCodedErrorWithDetails(errors.PULSE_TIMEZONE_UNKNOWN,
				fmt.Sprintf("field %q: time_zone %q is not \"UTC\" or an IANA Area/Location name in the tz database", f.Name, zoneName),
				map[string]any{"field": f.Name, errors.DetailTimeZone: zoneName})
		}
		if !z.IsUTC() {
			zone = z
		}
	}
	if hasZoneField {
		if zoneField == "" || zoneField == f.Name {
			return nil, "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: time_zone_field must name another, categorical field of zone names", f.Name),
				map[string]any{"field": f.Name, "time_zone_field": zoneField})
		}
		field = zoneField
	}

	return zone, field, nil
}

// parseDayRange reads the inclusive [start, end] local calendar-day
// range (YYYY-MM-DD) uniform_date and uniform_datetime share.
func parseDayRange(f FieldSpec, dist string) (startDay, endDay int64, err error) {
	startStr, ok, err := paramString(f.Name, f.Params, "start", dateEpoch)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: %s requires start", f.Name, dist), nil)
	}
	endStr, ok, err := paramString(f.Name, f.Params, "end", "")
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: %s requires end", f.Name, dist), nil)
	}
	startT, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		return 0, 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: invalid start date %q", f.Name, startStr), nil)
	}
	endT, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		return 0, 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: invalid end date %q", f.Name, endStr), nil)
	}
	if endT.Before(startT) {
		return 0, 0, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: %s end must not be before start", f.Name, dist), nil)
	}
	// Both instants are UTC midnight (time.Parse of a bare date), so the
	// toward-past truncation equals the old toward-zero division exactly.
	return temporal.TimeToDay(startT), temporal.TimeToDay(endT), nil
}

// profileWeights reads an optional weight profile of exactly n finite,
// non-negative entries with a positive sum. Absent means uniform.
func profileWeights(f FieldSpec, key string, n int) ([]float64, error) {
	w, ok, err := paramFloatSlice(f.Name, f.Params, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		out := make([]float64, n)
		for i := range out {
			out[i] = 1
		}
		return out, nil
	}
	if len(w) != n {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: %s must list exactly %d weights, got %d", f.Name, key, n, len(w)),
			map[string]any{"field": f.Name, "param": key, "want": n, "got": len(w)})
	}
	var sum float64
	for i, x := range w {
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: %s[%d] must be a finite, non-negative number", f.Name, key, i),
				map[string]any{"field": f.Name, "param": key, "index": i})
		}
		sum += x
	}
	if !(sum > 0) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: %s must carry at least one positive weight", f.Name, key),
			map[string]any{"field": f.Name, "param": key})
	}
	return w, nil
}

// pickCumulative returns the first index whose cumulative weight
// exceeds u, skipping zero-width slots; a u at (or, by rounding, past)
// the total lands on the last positive-width slot.
func pickCumulative(cum []float64, u float64) int {
	last := -1
	prev := 0.0
	for i, c := range cum {
		if c > prev {
			if u < c {
				return i
			}
			last = i
		}
		prev = c
	}
	return last
}

// next draws one local wall-clock reading: four draws, always.
func (s *uniformDateTimeSampler) next(rng *rand.Rand) (any, bool) {
	wd := pickCumulative(s.dayCum[:], float64(rng.Float64()*s.dayTotal))
	k := rng.Int64N(s.count[wd])
	day := s.firstDay[wd] + 7*k
	hour := pickCumulative(s.hourCum[:], float64(rng.Float64()*s.hourSum))
	sec := rng.Int64N(temporal.SecondsPerHour)
	return float64(temporal.WallSeconds(day, int64(hour), sec)), false
}

// zoned reports whether the field's reading needs converting at all.
func (s *uniformDateTimeSampler) zoned() bool { return s.zone != nil || s.zoneField != "" }

// unwrapSampler strips the null wrappers to reach a field's own sampler.
func unwrapSampler(s sampler) sampler {
	for {
		switch w := s.(type) {
		case ruleOwnedNullSampler:
			s = w.inner
			continue
		case *nullableSampler:
			s = w.inner
			continue
		}
		return s
	}
}

// datetimeField is one zoned datetime field's conversion state.
type datetimeField struct {
	name      string
	zone      *temporal.Zone // fixed zone; nil when zoneField is set
	zoneField string
	policy    temporal.Ambiguity
	policyStr string

	// pending counts the current attempt's resolved readings; committed
	// only when the row is accepted, so a rejected attempt leaves no
	// trace in the reported figure.
	pendingGap, pendingOverlap int
	gap, overlap               int

	// rowZone is the zone the CURRENT row's reading was resolved in (a
	// per-row zone field's value at the stage), read by the rule
	// functions (row_time.go) so a rule reads the instant on the clock
	// it was drawn on even when an earlier rule rewrote the zone field.
	rowZone *temporal.Zone
}

// datetimeStage converts every zoned datetime field's wall reading to
// its UTC instant. nil for a spec with no zoned datetime field.
type datetimeStage struct {
	fields []*datetimeField
}

// buildDatetimeStage compiles the conversion stage and checks each
// per-row zone field: it must be declared, categorical, never null
// where the datetime cannot be, and every zone name its distribution
// declares must load.
func buildDatetimeStage(s *Spec, wfs []*writerField) (*datetimeStage, error) {
	var st *datetimeStage
	byName := make(map[string]*writerField, len(wfs))
	for _, wf := range wfs {
		byName[wf.spec.Name] = wf
	}
	owned := ruleOwnedNullFields(s.Rules)
	for _, wf := range wfs {
		dts, ok := unwrapSampler(wf.sampler).(*uniformDateTimeSampler)
		if !ok || !dts.zoned() {
			continue
		}
		df := &datetimeField{
			name: wf.spec.Name, zone: dts.zone, zoneField: dts.zoneField,
			policy: dts.policy, policyStr: dts.policyName,
		}
		if df.zoneField != "" {
			zf, ok := byName[df.zoneField]
			if !ok {
				return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: time_zone_field %q is not a declared field", df.name, df.zoneField),
					map[string]any{"field": df.name, "time_zone_field": df.zoneField})
			}
			if !zf.field.Type.IsCategorical() {
				return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: time_zone_field %q is a %s field; it must be categorical_* holding IANA zone names", df.name, df.zoneField, zf.spec.Type),
					map[string]any{"field": df.name, "time_zone_field": df.zoneField, "field_type": zf.spec.Type})
			}
			if zf.spec.NullRate > 0 && !owned[zf.spec.Name] && !wf.spec.Nullable {
				return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: time_zone_field %q can be null and a null zone makes the datetime null; declare %q nullable or give the zone field no null_rate", df.name, df.zoneField, df.name),
					map[string]any{"field": df.name, "time_zone_field": df.zoneField})
			}
			if names, known := declaredCategoricalDomain(zf.spec); known {
				for _, n := range names {
					if _, err := synthZones.Load(n); err != nil {
						return nil, unknownRowZone(df.name, df.zoneField, n)
					}
				}
			}
		}
		if st == nil {
			st = &datetimeStage{}
		}
		st.fields = append(st.fields, df)
	}
	return st, nil
}

func unknownRowZone(field, zoneField, name string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TIMEZONE_UNKNOWN,
		fmt.Sprintf("field %q: time_zone_field %q holds %q, which is not \"UTC\" or an IANA Area/Location name in the tz database", field, zoneField, name),
		map[string]any{"field": field, "time_zone_field": zoneField, errors.DetailTimeZone: name})
}

// resolve converts each zoned field's wall reading in place. It consumes
// no RNG. A null zone cell makes the datetime null, never UTC.
func (st *datetimeStage) resolve(row map[string]any, nullMask map[string]bool) error {
	if st == nil {
		return nil
	}
	for _, df := range st.fields {
		df.pendingGap, df.pendingOverlap = 0, 0
		df.rowZone = nil
		if nullMask[df.name] {
			continue
		}
		zone := df.zone
		if df.zoneField != "" {
			if nullMask[df.zoneField] {
				nullMask[df.name] = true
				continue
			}
			name, _ := row[df.zoneField].(string)
			z, err := synthZones.Load(name)
			if err != nil {
				return unknownRowZone(df.name, df.zoneField, name)
			}
			zone = z
		}
		df.rowZone = zone
		f := toFloat64(row[df.name])
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > maxDatetimeSeconds {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				fmt.Sprintf("field %q: datetime reading %v is not a representable epoch-second value", df.name, f),
				map[string]any{"field": df.name})
		}
		wall := int64(math.Floor(f + 0.5))
		sec, kind, err := temporal.ResolveLocal(wall, zone, df.policy)
		if err != nil {
			code := errors.PULSE_SYNTH_DST_NONEXISTENT
			if kind == temporal.LocalAmbiguous {
				code = errors.PULSE_SYNTH_DST_AMBIGUOUS
			}
			return errors.NewCodedErrorWithDetails(code,
				fmt.Sprintf("field %q: drew local time %s, which %s in %s; set dst_policy to \"earlier\" or \"later\"",
					df.name, temporal.FormatWall(wall), dstVerb(kind), zone.Name()),
				map[string]any{
					"field":               df.name,
					"local_time":          temporal.FormatWall(wall),
					errors.DetailTimeZone: zone.Name(),
					"dst_policy":          df.policyStr,
				})
		}
		switch kind {
		case temporal.LocalNonexistent:
			df.pendingGap++
		case temporal.LocalAmbiguous:
			df.pendingOverlap++
		}
		row[df.name] = float64(sec)
	}
	return nil
}

// maxDatetimeSeconds bounds a datetime reading well inside int64 (and
// inside every zone offset's reach), so the round and the conversion
// cannot overflow.
const maxDatetimeSeconds = 1 << 61

func dstVerb(k temporal.LocalKind) string {
	if k == temporal.LocalAmbiguous {
		return "a DST fall-back repeats"
	}
	return "a DST spring-forward skips"
}

// rowZoneOf returns the zone field name's current row was resolved in;
// ok is false when the stage holds no such field or the row's zone cell
// was null (the datetime is then null too).
func (st *datetimeStage) rowZoneOf(name string) (*temporal.Zone, bool) {
	if st == nil {
		return nil, false
	}
	for _, df := range st.fields {
		if df.name == name {
			return df.rowZone, df.rowZone != nil
		}
	}
	return nil, false
}

// commitAccepted folds the accepted attempt's resolved readings into
// the run's counts.
func (st *datetimeStage) commitAccepted() {
	if st == nil {
		return
	}
	for _, df := range st.fields {
		df.gap += df.pendingGap
		df.overlap += df.pendingOverlap
	}
}

// warnings reports, per field in schema order, how many accepted rows'
// readings a DST transition skipped or repeated and dst_policy resolved.
func (st *datetimeStage) warnings() []string {
	if st == nil {
		return nil
	}
	var out []string
	for _, df := range st.fields {
		if df.gap+df.overlap == 0 {
			continue
		}
		out = append(out, fmt.Sprintf(
			"field %q: %d generated local times fell in a DST transition and were resolved by dst_policy %q (%d skipped by a spring-forward, %d repeated by a fall-back)",
			df.name, df.gap+df.overlap, df.policyStr, df.gap, df.overlap))
	}
	return out
}
