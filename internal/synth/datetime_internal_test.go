package synth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
)

// datetimeRows decodes a generated cohort: per row, every field's value
// (a categorical resolved to its dictionary string) and null flag.
func datetimeRows(t *testing.T, data []byte) ([]map[string]any, []map[string]bool) {
	t.Helper()
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encx.NewRecordReader(r, schema)
	var rows []map[string]any
	var nulls []map[string]bool
	for {
		values := make(map[string]float64)
		nm := make(map[string]bool)
		if err := rr.ReadRecord(values, nm); err != nil {
			break
		}
		row := make(map[string]any, len(values))
		for _, f := range schema.Fields {
			if f.Type.IsCategorical() {
				row[f.Name] = f.Dictionary.Resolve(uint32(values[f.Name]))
				continue
			}
			row[f.Name] = values[f.Name]
		}
		rows = append(rows, row)
		nulls = append(nulls, nm)
	}
	return rows, nulls
}

func mustZone(t *testing.T, name string) *temporal.Zone {
	t.Helper()
	z, err := temporal.LoadZone(name)
	if err != nil {
		t.Fatalf("LoadZone(%q): %v", name, err)
	}
	return z
}

// oneHot is a 24-hour profile drawing only hour h.
func oneHot(h int) []any {
	out := make([]any, 24)
	for i := range out {
		out[i] = 0.0
	}
	out[h] = 1.0
	return out
}

func codeOf(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// dstSpec draws every row inside local hour `hour` of `day` in zone.
func dstSpec(zone, day string, hour int, policy string) *Spec {
	params := map[string]any{
		"start": day, "end": day, "time_zone": zone, "hour_weights": oneHot(hour),
	}
	if policy != "" {
		params["dst_policy"] = policy
	}
	return &Spec{RowCount: 200, Fields: []FieldSpec{{
		Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: params,
	}}}
}

// TestUniformDateTime_DSTGapEveryPolicy: Europe/Berlin skips 02:00-03:00
// local on 2026-03-29 (CET +1 → CEST +2, at 01:00Z). Every row is drawn
// inside the skipped hour, so each dst_policy is exercised on every row:
// `error` (and the default) refuses, `earlier` reads 02:xx at +1 (01:xxZ),
// `later` at +2 (00:xxZ); both resolutions are counted on a warning.
func TestUniformDateTime_DSTGapEveryPolicy(t *testing.T) {
	t.Parallel()

	const day = int64(20541) // 2026-03-29
	midnightZ := temporal.DayToUnix(day)
	for _, policy := range []string{"", "error"} {
		_, _, err := SynthBytes(dstSpec("Europe/Berlin", "2026-03-29", 2, policy), Options{Seed: 3})
		if got := codeOf(err); got != errors.PULSE_SYNTH_DST_NONEXISTENT {
			t.Fatalf("policy %q: code %q (%v), want PULSE_SYNTH_DST_NONEXISTENT", policy, got, err)
		}
		var ce *errors.CodedError
		stderrors.As(err, &ce)
		if ce.Details["field"] != "ts" || ce.Details[errors.DetailTimeZone] != "Europe/Berlin" ||
			!strings.HasPrefix(ce.Details["local_time"].(string), "2026-03-29T02:") {
			t.Errorf("policy %q: details %v do not name the field, zone and drawn local time", policy, ce.Details)
		}
	}
	for _, tc := range []struct {
		policy string
		lo, hi int64 // the UTC window every resolved instant must land in
	}{
		{"earlier", midnightZ + 3600, midnightZ + 7200},
		{"later", midnightZ, midnightZ + 3600},
	} {
		data, res, err := SynthBytes(dstSpec("Europe/Berlin", "2026-03-29", 2, tc.policy), Options{Seed: 3})
		if err != nil {
			t.Fatalf("policy %s: %v", tc.policy, err)
		}
		rows, _ := datetimeRows(t, data)
		for i, r := range rows {
			sec := int64(r["ts"].(float64))
			if sec < tc.lo || sec >= tc.hi {
				t.Fatalf("policy %s row %d: %s outside [%s, %s)", tc.policy, i,
					temporal.FormatUTC(sec), temporal.FormatUTC(tc.lo), temporal.FormatUTC(tc.hi))
			}
		}
		want := `field "ts": 200 generated local times fell in a DST transition and were resolved by dst_policy "` +
			tc.policy + `" (200 skipped by a spring-forward, 0 repeated by a fall-back)`
		if len(res.Warnings) != 1 || res.Warnings[0] != want {
			t.Errorf("policy %s: warnings %q, want [%q]", tc.policy, res.Warnings, want)
		}
	}
}

// TestUniformDateTime_DSTOverlapEveryPolicy: Europe/Berlin repeats
// 02:00-03:00 local on 2026-10-25 (CEST +2 → CET +1, at 01:00Z).
// `earlier` takes the first occurrence (00:xxZ), `later` the second
// (01:xxZ), `error` refuses with the ambiguity code.
func TestUniformDateTime_DSTOverlapEveryPolicy(t *testing.T) {
	t.Parallel()

	midnightZ := temporal.DayToUnix(20751) // 2026-10-25
	_, _, err := SynthBytes(dstSpec("Europe/Berlin", "2026-10-25", 2, "error"), Options{Seed: 5})
	if got := codeOf(err); got != errors.PULSE_SYNTH_DST_AMBIGUOUS {
		t.Fatalf("error policy: code %q (%v), want PULSE_SYNTH_DST_AMBIGUOUS", got, err)
	}
	for _, tc := range []struct {
		policy string
		lo, hi int64
	}{
		{"earlier", midnightZ, midnightZ + 3600},
		{"later", midnightZ + 3600, midnightZ + 7200},
	} {
		data, res, err := SynthBytes(dstSpec("Europe/Berlin", "2026-10-25", 2, tc.policy), Options{Seed: 5})
		if err != nil {
			t.Fatalf("policy %s: %v", tc.policy, err)
		}
		rows, _ := datetimeRows(t, data)
		for i, r := range rows {
			sec := int64(r["ts"].(float64))
			if sec < tc.lo || sec >= tc.hi {
				t.Fatalf("policy %s row %d: %s outside [%s, %s)", tc.policy, i,
					temporal.FormatUTC(sec), temporal.FormatUTC(tc.lo), temporal.FormatUTC(tc.hi))
			}
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "(0 skipped by a spring-forward, 200 repeated by a fall-back)") {
			t.Errorf("policy %s: warnings %q", tc.policy, res.Warnings)
		}
	}
}

// TestUniformDateTime_DSTOutsideTheTransitionIsExact: hours a transition
// day does not touch resolve exactly under the default `error` policy,
// with no warning — the refusal is about the transition hour only.
func TestUniformDateTime_DSTOutsideTheTransitionIsExact(t *testing.T) {
	t.Parallel()

	w := oneHot(9)
	spec := dstSpec("America/New_York", "2026-03-08", 9, "")
	spec.Fields[0].Params["hour_weights"] = w
	data, res, err := SynthBytes(spec, Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %q, want none", res.Warnings)
	}
	ny := mustZone(t, "America/New_York")
	rows, _ := datetimeRows(t, data)
	for i, r := range rows {
		p := temporal.LocalParts(int64(r["ts"].(float64)), ny)
		if p.Year != 2026 || p.Month != 3 || p.Day != 8 || p.Hour != 9 {
			t.Fatalf("row %d: local %v, want 2026-03-08 09:xx in New York (EDT)", i, p)
		}
	}
}

// businessHours is a local opening-hours profile: closed overnight,
// peaking mid-morning.
func businessHours() []float64 {
	w := make([]float64, 24)
	for h := 8; h < 18; h++ {
		w[h] = 1
	}
	w[10], w[11] = 3, 2
	return w
}

func toAny(xs []float64) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// TestUniformDateTime_LocalMarginalsMatchWeights: a per-row zone over
// three sites, a business-hours profile and a closed Sunday. Over whole
// weeks with no DST transition, the realised LOCAL hour-of-day and
// weekday shares — read back on each row's own clock — match the
// declared weights, pooled and within every zone. Reading the hours on
// the UTC clock instead would put Sydney's office hours overnight, which
// is what proves the zone was applied per row.
func TestUniformDateTime_LocalMarginalsMatchWeights(t *testing.T) {
	t.Parallel()

	hours := businessHours()
	weekday := []float64{1, 1, 1, 1, 2, 0.5, 0} // Mon..Sun
	zones := []string{"Australia/Sydney", "America/New_York", "Europe/London"}
	const n = 30000
	spec := &Spec{RowCount: n, Fields: []FieldSpec{
		{Name: "site_tz", Type: "categorical_u8", Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{zones[0], zones[1], zones[2]}}},
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			// 2026-01-05 (Mon) .. 2026-03-01 (Sun): 8 whole weeks, before
			// New York's 2026-03-08 and London's 2026-03-29 transitions and
			// after none of Sydney's (its next is 2026-04-05).
			"start": "2026-01-05", "end": "2026-03-01",
			"time_zone_field": "site_tz",
			"hour_weights":    toAny(hours),
			"weekday_weights": toAny(weekday),
		}},
	}}
	data, res, err := SynthBytes(spec, Options{Seed: 2026})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %q: no transition lies in range", res.Warnings)
	}
	rows, _ := datetimeRows(t, data)
	if len(rows) != n {
		t.Fatalf("rows = %d, want %d", len(rows), n)
	}
	norm := func(w []float64) []float64 {
		var s float64
		for _, x := range w {
			s += x
		}
		out := make([]float64, len(w))
		for i, x := range w {
			out[i] = x / s
		}
		return out
	}
	wantHour, wantDay := norm(hours), norm(weekday)
	type counts struct {
		hour [24]int
		day  [7]int
		utc  [24]int
		n    int
	}
	per := map[string]*counts{}
	all := &counts{}
	loaded := map[string]*temporal.Zone{}
	for _, zn := range zones {
		loaded[zn] = mustZone(t, zn)
	}
	for _, r := range rows {
		zn := r["site_tz"].(string)
		sec := int64(r["ts"].(float64))
		p := temporal.LocalParts(sec, loaded[zn])
		c := per[zn]
		if c == nil {
			c = &counts{}
			per[zn] = c
		}
		for _, cc := range []*counts{c, all} {
			cc.hour[p.Hour]++
			cc.day[temporal.ISOWeekdayOf(p.Weekday)-1]++
			cc.utc[temporal.LocalParts(sec, temporal.UTC).Hour]++
			cc.n++
		}
	}
	check := func(label string, c *counts, tol float64) {
		var worstHour, worstDay float64
		defer func() {
			t.Logf("%s: n=%d, worst |local hour share - weight| %.4f, worst |weekday share - weight| %.4f",
				label, c.n, worstHour, worstDay)
		}()
		for h := range 24 {
			got := float64(c.hour[h]) / float64(c.n)
			worstHour = max(worstHour, got-wantHour[h], wantHour[h]-got)
			if d := got - wantHour[h]; d > tol || d < -tol {
				t.Errorf("%s: local hour %02d share %.4f, want %.4f ± %.3f", label, h, got, wantHour[h], tol)
			}
		}
		for d := range 7 {
			got := float64(c.day[d]) / float64(c.n)
			worstDay = max(worstDay, got-wantDay[d], wantDay[d]-got)
			if x := got - wantDay[d]; x > tol || x < -tol {
				t.Errorf("%s: ISO weekday %d share %.4f, want %.4f ± %.3f", label, d+1, got, wantDay[d], tol)
			}
		}
	}
	check("pooled", all, 0.01)
	for _, zn := range zones {
		c := per[zn]
		if c == nil || c.n < n/4 {
			t.Fatalf("zone %s drew %v rows", zn, c)
		}
		check(zn, c, 0.02)
	}
	// Sydney is UTC+11 in January: its 10:00 peak is 23:00Z the day
	// before, so on the UTC clock the UTC hour 10 bucket is empty.
	if got := per["Australia/Sydney"].utc[10]; got != 0 {
		t.Errorf("Sydney rows at UTC hour 10 = %d; the zone was not applied", got)
	}
}

// TestUniformDateTime_FixedSeedIsByteIdentical: same spec + seed gives
// the same bytes; another seed gives other bytes.
func TestUniformDateTime_FixedSeedIsByteIdentical(t *testing.T) {
	t.Parallel()

	spec := func() *Spec {
		return &Spec{RowCount: 400, Fields: []FieldSpec{
			{Name: "tz", Type: "categorical_u8", Distribution: DistWeightedCategorical,
				Params: map[string]any{"values": []any{"Europe/Berlin", "America/New_York"}}},
			{Name: "ts", Type: "datetime", Nullable: true, NullRate: 0.1, Distribution: DistUniformDateTime,
				Params: map[string]any{"start": "2026-01-01", "end": "2026-12-31",
					"time_zone_field": "tz", "dst_policy": "later"}},
			{Name: "fixed", Type: "datetime", Distribution: DistUniformDateTime,
				Params: map[string]any{"start": "2026-01-01", "end": "2026-12-31",
					"time_zone": "Asia/Kolkata", "weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0}}},
		}}
	}
	a, _, err := SynthBytes(spec(), Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	b, _, err := SynthBytes(spec(), Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same spec and seed produced different bytes")
	}
	c, _, err := SynthBytes(spec(), Options{Seed: 78})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if bytes.Equal(a, c) {
		t.Fatal("a different seed produced identical bytes")
	}
}

// preDatetimeExampleHashes are the SHA-256 of every shipped example spec
// generated at seed 7 BEFORE uniform_datetime existed (commit 3748b7ce),
// plus one uniform_date spec: datetime support must not move a single
// byte of a spec that does not use it.
var preDatetimeExampleHashes = map[string]string{
	"01_minimal.synth.json":              "a80904c65eea1d6455f30ea34df08fce962699abf8da85f0293b308bc8895a47",
	"02_correlated_marginals.synth.json": "734aa529e8497ca6180ee5824985489878d49780b31a61a6e0087db35b4c7df4",
	"03_constrained.synth.json":          "5bd837afbccb15f02cda4231a75929f255a80586dc2a0433c3358a4db9f310f1",
	"04_decimal_currency.synth.json":     "318072cb32c5ae91eee11b84d39e18d09a80930ed6a676c386cee07fa8153402",
	"05_regex_ids.synth.json":            "f3136d1959068beab40edc3d0ff547743dc07b052f4d69a76aa037d1e50c65e2",
	"06_mixture_bimodal.synth.json":      "8d548f8fc3c5469d0ed5693ae3221e68ae717b265648ab02c99aac316133adb9",
	"07_mmm_adstock.synth.json":          "2f52a9167e958671c6b4d6b694ffd0005d1f749be160298d7f4f7aa876d0dd7f",
}

const preDatetimeDateSpecHash = "92c9e5c4d639211b2b352c3c146ced0b12d6bc7dd110a853eb86507401adc6e4"

func TestSynthDatetime_SpecWithoutDatetimeIsByteIdentical(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "examples", "synth")
	for name, want := range preDatetimeExampleHashes {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		spec, err := ParseSpec(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		data, _, err := SynthBytes(spec, Options{Seed: 7})
		if err != nil {
			t.Fatalf("synth %s: %v", name, err)
		}
		if got := datetimeSHA(data); got != want {
			t.Errorf("%s changed bytes\n got %s\nwant %s", name, got, want)
		}
	}
	spec := &Spec{RowCount: 500, Fields: []FieldSpec{
		{Name: "d", Type: "date", Distribution: DistUniformDate, Params: map[string]any{"start": "1969-11-03", "end": "2026-03-29"}},
		{Name: "secs", Type: "u64", Distribution: DistUniform, Params: map[string]any{"min": 1.7e9, "max": 1.8e9}},
		{Name: "c", Type: "categorical_u8", Distribution: DistWeightedCategorical, Params: map[string]any{"values": []any{"a", "b"}}},
	}}
	data, _, err := SynthBytes(spec, Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if got := datetimeSHA(data); got != preDatetimeDateSpecHash {
		t.Errorf("uniform_date spec changed bytes\n got %s\nwant %s", got, preDatetimeDateSpecHash)
	}
}

// TestUniformDateTime_UnzonedStoresTheReadingAsUTC: with no zone (and
// with a UTC-equivalent one) the reading IS the instant and no stage is
// compiled, so the local and UTC clocks agree row by row.
func TestUniformDateTime_UnzonedStoresTheReadingAsUTC(t *testing.T) {
	t.Parallel()

	base := map[string]any{"start": "2026-06-01", "end": "2026-06-07", "hour_weights": oneHot(14)}
	var prev []byte
	for _, zone := range []string{"", "UTC", "Etc/UTC"} {
		params := map[string]any{}
		for k, v := range base {
			params[k] = v
		}
		if zone != "" {
			params["time_zone"] = zone
		}
		spec := &Spec{RowCount: 100, Fields: []FieldSpec{{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: params}}}
		_, wfs, err := buildSchema(spec)
		if err != nil {
			t.Fatalf("zone %q: %v", zone, err)
		}
		if st, err := buildDatetimeStage(spec, wfs); err != nil || st != nil {
			t.Fatalf("zone %q: stage %v, %v; want none", zone, st, err)
		}
		data, _, err := SynthBytes(spec, Options{Seed: 1})
		if err != nil {
			t.Fatalf("zone %q: %v", zone, err)
		}
		rows, _ := datetimeRows(t, data)
		for i, r := range rows {
			if h := temporal.LocalParts(int64(r["ts"].(float64)), temporal.UTC).Hour; h != 14 {
				t.Fatalf("zone %q row %d: UTC hour %d, want 14", zone, i, h)
			}
		}
		if prev != nil && !bytes.Equal(prev, data) {
			t.Errorf("zone %q: bytes differ from the unzoned field", zone)
		}
		prev = data
	}
}

// TestUniformDateTime_NullZoneCellIsNullDatetime: a per-row zone field
// drawn null makes that row's datetime null, never a UTC reading; a
// non-nullable datetime over a nullable zone field is refused up front.
func TestUniformDateTime_NullZoneCellIsNullDatetime(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 500, Fields: []FieldSpec{
		{Name: "tz", Type: "categorical_u8", Nullable: true, NullRate: 0.3, Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"Europe/Paris"}}},
		{Name: "ts", Type: "datetime", Nullable: true, Distribution: DistUniformDateTime,
			Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31", "time_zone_field": "tz"}},
	}}
	data, _, err := SynthBytes(spec, Options{Seed: 4})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	_, nulls := datetimeRows(t, data)
	var nullZones int
	for i, nm := range nulls {
		if nm["tz"] {
			nullZones++
		}
		if nm["tz"] != nm["ts"] {
			t.Fatalf("row %d: zone null %v but datetime null %v", i, nm["tz"], nm["ts"])
		}
	}
	if nullZones == 0 {
		t.Fatal("no null zone cell drawn; the case is untested")
	}

	spec.Fields[1].Nullable = false
	if _, _, err := SynthBytes(spec, Options{Seed: 4}); codeOf(err) != errors.SERVICE_VALIDATION {
		t.Fatalf("non-nullable datetime over a nullable zone field: %v, want SERVICE_VALIDATION", err)
	}
}

// TestUniformDateTime_Refusals pins every configuration refusal.
func TestUniformDateTime_Refusals(t *testing.T) {
	t.Parallel()

	dt := func(params map[string]any) FieldSpec {
		p := map[string]any{"start": "2026-01-01", "end": "2026-01-31"}
		for k, v := range params {
			p[k] = v
		}
		return FieldSpec{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: p}
	}
	cat := func(values ...any) FieldSpec {
		return FieldSpec{Name: "tz", Type: "categorical_u8", Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": values}}
	}
	cases := []struct {
		name   string
		fields []FieldSpec
		code   errors.Code
		msg    string
	}{
		{"uniform_datetime on a date field", []FieldSpec{{Name: "ts", Type: "date", Distribution: DistUniformDateTime,
			Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31"}}}, errors.SERVICE_VALIDATION, "cannot fill a date field"},
		{"uniform_date on a datetime field", []FieldSpec{{Name: "ts", Type: "datetime", Distribution: DistUniformDate,
			Params: map[string]any{"start": "2026-01-01", "end": "2026-01-31"}}}, errors.SERVICE_VALIDATION, "use uniform_datetime"},
		{"unknown zone", []FieldSpec{dt(map[string]any{"time_zone": "EST"})}, errors.PULSE_TIMEZONE_UNKNOWN, "time_zone \"EST\""},
		{"both zone slots", []FieldSpec{cat("UTC"), dt(map[string]any{"time_zone": "UTC", "time_zone_field": "tz"})}, errors.SERVICE_VALIDATION, "exclusive"},
		{"bad dst_policy", []FieldSpec{dt(map[string]any{"dst_policy": "nearest"})}, errors.SERVICE_VALIDATION, "dst_policy \"nearest\""},
		{"short hour profile", []FieldSpec{dt(map[string]any{"hour_weights": []any{1.0, 2.0}})}, errors.SERVICE_VALIDATION, "exactly 24"},
		{"negative weekday weight", []FieldSpec{dt(map[string]any{"weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 1.0, -1.0}})}, errors.SERVICE_VALIDATION, "non-negative"},
		{"all-zero hours", []FieldSpec{dt(map[string]any{"hour_weights": toAny(make([]float64, 24))})}, errors.SERVICE_VALIDATION, "at least one positive"},
		{"range holds only zero-weight weekdays", []FieldSpec{{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime,
			Params: map[string]any{"start": "2026-01-04", "end": "2026-01-04", // a Sunday
				"weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 0.0}}}}, errors.SERVICE_VALIDATION, "no day in [start, end]"},
		{"zone field undeclared", []FieldSpec{dt(map[string]any{"time_zone_field": "nope"})}, errors.SERVICE_VALIDATION, "not a declared field"},
		{"zone field not categorical", []FieldSpec{{Name: "tz", Type: "u8", Distribution: DistUniform},
			dt(map[string]any{"time_zone_field": "tz"})}, errors.SERVICE_VALIDATION, "must be categorical"},
		{"zone field declares a bad name", []FieldSpec{cat("Europe/Berlin", "Mars/Olympus"),
			dt(map[string]any{"time_zone_field": "tz"})}, errors.PULSE_TIMEZONE_UNKNOWN, "Mars/Olympus"},
		{"zone field drawn by regex yields a bad name at run time", []FieldSpec{
			{Name: "tz", Type: "categorical_u8", Distribution: DistRegex, Params: map[string]any{"pattern": "Europe/X[a-z]{2}"}},
			dt(map[string]any{"time_zone_field": "tz"})}, errors.PULSE_TIMEZONE_UNKNOWN, "time_zone_field \"tz\" holds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := SynthBytes(&Spec{RowCount: 10, Fields: tc.fields}, Options{Seed: 1})
			if got := codeOf(err); got != tc.code {
				t.Fatalf("code %q (%v), want %s", got, err, tc.code)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error %q does not mention %q", err, tc.msg)
			}
		})
	}
}

// TestUniformDateTime_RulesReadTheUTCInstant: the conversion runs BEFORE
// the rule pass, so a rule (and a constraint) sees the stored instant.
// The rule copies the datetime into an f64; the copy must equal the
// stored datetime, not the local wall reading 3600 s away from it.
func TestUniformDateTime_RulesReadTheUTCInstant(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 50, Fields: []FieldSpec{
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime,
			Params: map[string]any{"start": "2026-01-10", "end": "2026-01-20", "time_zone": "Europe/Paris"}},
		{Name: "copy", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}},
	}, Rules: []RuleSpec{{SetExpr: map[string]string{"copy": "ts"}}},
		Constraints: []ConstraintSpec{{Expr: "copy == ts"}}}
	data, _, err := SynthBytes(spec, Options{Seed: 8})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	rows, _ := datetimeRows(t, data)
	paris := mustZone(t, "Europe/Paris")
	for i, r := range rows {
		ts, cp := r["ts"].(float64), r["copy"].(float64)
		if ts != cp {
			t.Fatalf("row %d: rule read %v, file stores %v", i, cp, ts)
		}
		local := int64(ts) + int64(paris.Offset(int64(ts)))
		if local-int64(ts) != 3600 {
			t.Fatalf("row %d: Paris offset in January is not +1h", i)
		}
	}
}

// TestUniformDateTime_ResolvedCountsAcceptedRowsOnly: a constraint that
// rejects attempts must not inflate the DST resolution count — the
// warning reports accepted rows exactly.
func TestUniformDateTime_ResolvedCountsAcceptedRowsOnly(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 300, MaxRejectionRate: 0.9, Fields: []FieldSpec{
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2026-03-29", "end": "2026-03-29", "time_zone": "Europe/Berlin",
			"hour_weights": oneHot(2), "dst_policy": "earlier"}},
		{Name: "keep", Type: "f64", Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
	}, Constraints: []ConstraintSpec{{Expr: "keep < 0.5"}}}
	_, res, err := SynthBytes(spec, Options{Seed: 12})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("no attempt rejected; the case is untested")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], ": 300 generated local times") {
		t.Errorf("warnings %q, want a count of exactly the 300 accepted rows", res.Warnings)
	}
}

func datetimeSHA(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
