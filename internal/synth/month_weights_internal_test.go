package synth

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// plantedMonths is a 24-month volume series (2024-01 .. 2025-12) with a
// rising trend (the second year draws ~1.5x the first) and a seasonal
// swing (a summer peak, a winter trough) — the shape a flat
// reconstruction loses.
func plantedMonths() []any {
	season := []float64{0.5, 0.6, 0.8, 1.0, 1.3, 1.6, 1.7, 1.5, 1.1, 0.9, 0.7, 0.5}
	out := make([]any, 24)
	for m := range out {
		trend := 1 + float64(m)/24
		out[m] = float64(trend * season[m%12])
	}
	return out
}

// monthVolumeSpec: a datetime and a date, both carrying the planted
// series, the datetime also a business-hours / weekday profile.
func monthVolumeSpec(rows int) *Spec {
	return &Spec{RowCount: rows, Fields: []FieldSpec{
		{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime, Params: map[string]any{
			"start": "2024-01-10", "end": "2025-12-20",
			"hour_weights":    toAny(businessHours()),
			"weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.5, 0.2},
			"month_weights":   plantedMonths()}},
		{Name: "day", Type: "date", Distribution: DistUniformDate, Params: map[string]any{
			"start": "2024-01-10", "end": "2025-12-20", "month_weights": plantedMonths()}},
	}}
}

// monthShares returns field's per-month shares keyed by running month
// index, read on the UTC clock (a date as its day).
func monthShares(t *testing.T, data []byte, field string) map[int64]float64 {
	t.Helper()
	rows, nm := datetimeRows(t, data)
	out := map[int64]float64{}
	n := 0.0
	for i, r := range rows {
		if nm[i][field] {
			continue
		}
		v := r[field].(float64)
		day := int64(v)
		if field == "ts" {
			day = temporal.DateTimeToDay(int64(v))
		}
		out[temporal.MonthIndex(day)]++
		n++
	}
	for k := range out {
		out[k] /= n
	}
	return out
}

func shareTVD(a, b map[int64]float64) float64 {
	keys := map[int64]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	s := 0.0
	for k := range keys {
		s += math.Abs(a[k] - b[k])
	}
	return s / 2
}

// expectedMonthShares is the planted series' share per month: weight ×
// the month's in-range day weights, normalised.
func expectedMonthShares(start, end string, weekday []float64, months []any) map[int64]float64 {
	s, e, _ := parseDayRange(FieldSpec{Params: map[string]any{"start": start, "end": end}}, DistUniformDate)
	w := make([]float64, len(months))
	for i, x := range months {
		w[i] = x.(float64)
	}
	sums := monthDayProducts(s, e, weekday, w, nil, nil)
	total := 0.0
	for _, x := range sums {
		total += x
	}
	out := map[int64]float64{}
	base := temporal.MonthIndex(s)
	for m, x := range sums {
		out[base+int64(m)] = x / total
	}
	return out
}

// TestMonthWeights_SamplerRealisesTheSeries: with month_weights a day's
// weight is weekday × month, so the realised month shares are the
// planted series times each month's in-range days — partial first and
// last months included — for a datetime and a date alike.
func TestMonthWeights_SamplerRealisesTheSeries(t *testing.T) {
	t.Parallel()

	out, _, err := SynthBytes(monthVolumeSpec(30000), Options{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	wantTS := expectedMonthShares("2024-01-10", "2025-12-20", []float64{1, 1, 1, 1, 1, 0.5, 0.2}, plantedMonths())
	wantDay := expectedMonthShares("2024-01-10", "2025-12-20", []float64{1, 1, 1, 1, 1, 1, 1}, plantedMonths())
	if d := shareTVD(monthShares(t, out, "ts"), wantTS); d > 0.02 {
		t.Errorf("ts month TVD vs the planted series = %.4f, want ≤ 0.02", d)
	}
	if d := shareTVD(monthShares(t, out, "day"), wantDay); d > 0.02 {
		t.Errorf("day month TVD vs the planted series = %.4f, want ≤ 0.02", d)
	}
	// The planted shape is not flat: a uniform draw over the same range
	// would sit far from it.
	flat := expectedMonthShares("2024-01-10", "2025-12-20", []float64{1, 1, 1, 1, 1, 1, 1}, flatMonths(24))
	if d := shareTVD(wantDay, flat); d < 0.1 {
		t.Fatalf("planted series is too close to flat (TVD %.4f) to test anything", d)
	}
}

func flatMonths(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = 1.0
	}
	return out
}

// TestMonthWeights_ProfileRegeneratesTheVolume: profile create captures
// the month series, SpecFromProfile rebuilds it as month_weights, and
// the twin's month shares match the source's while its weekday and
// hour marginals still do; the same spec with the month weights
// stripped regenerates flat and misses.
func TestMonthWeights_ProfileRegeneratesTheVolume(t *testing.T) {
	t.Parallel()

	src, _, err := SynthBytes(monthVolumeSpec(30000), Options{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := ProfileBytes(src, ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatal(err)
	}
	var ts, day *FieldProfile
	for i := range prof.Fields {
		switch prof.Fields[i].Name {
		case "ts":
			ts = &prof.Fields[i]
		case "day":
			day = &prof.Fields[i]
		}
	}
	if ts.Datetime.MonthStart != "2024-01" || len(ts.Datetime.Months) != 24 {
		t.Fatalf("ts month series start %q len %d, want 2024-01 / 24", ts.Datetime.MonthStart, len(ts.Datetime.Months))
	}
	if day.Date.MonthStart != "2024-01" || len(day.Date.Months) != 24 {
		t.Fatalf("day month series start %q len %d, want 2024-01 / 24", day.Date.MonthStart, len(day.Date.Months))
	}
	sum := 0
	for _, c := range ts.Datetime.Months {
		sum += c
	}
	if sum != 30000 {
		t.Fatalf("ts month counts sum %d, want every row", sum)
	}

	spec, warns := SpecFromProfile(prof, 30000)
	if len(warns) != 0 {
		t.Fatalf("warnings %v", warns)
	}
	for _, f := range spec.Fields {
		if _, ok := f.Params[monthWeightsParam]; !ok {
			t.Fatalf("%s: no month_weights rebuilt (%v)", f.Name, f.Params)
		}
	}
	twin, _, err := SynthBytes(spec, Options{Seed: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ts", "day"} {
		if d := shareTVD(monthShares(t, src, field), monthShares(t, twin, field)); d > 0.03 {
			t.Errorf("%s month TVD source vs twin = %.4f, want ≤ 0.03", field, d)
		}
	}
	sh, sw, sf, sl, _ := utcShares(t, src)
	gh, gw, gf, gl, _ := utcShares(t, twin)
	if sf != gf || sl != gl {
		t.Errorf("UTC day range %s..%s, want %s..%s", gf, gl, sf, sl)
	}
	for h := range sh {
		if math.Abs(sh[h]-gh[h]) > 0.015 {
			t.Errorf("hour %02d share %.4f, source %.4f", h, gh[h], sh[h])
		}
	}
	for d := range sw {
		if math.Abs(sw[d]-gw[d]) > 0.015 {
			t.Errorf("weekday %d share %.4f, source %.4f", d+1, gw[d], sw[d])
		}
	}

	// Strip the month weights: the twin regenerates flat and misses.
	for i := range spec.Fields {
		delete(spec.Fields[i].Params, monthWeightsParam)
	}
	flatTwin, _, err := SynthBytes(spec, Options{Seed: 8})
	if err != nil {
		t.Fatal(err)
	}
	if d := shareTVD(monthShares(t, src, "ts"), monthShares(t, flatTwin, "ts")); d < 0.1 {
		t.Errorf("flat twin month TVD = %.4f, want ≥ 0.1 (the test cannot tell volume from flat)", d)
	}
}

// TestMonthWeights_RebuildIsAPerDayIntensity: a month the range covers
// only partly gets the weight that reproduces its COUNT over the days it
// has — count ÷ Σ weekday weight over its in-range days.
func TestMonthWeights_RebuildIsAPerDayIntensity(t *testing.T) {
	t.Parallel()

	// 2026-01-29 (Thu) .. 2026-03-03 (Tue): Jan 3 days, Feb 28, Mar 3.
	p, w := dateParamsFromProfile("d", &DateProfile{Start: "2026-01-29", End: "2026-03-03",
		MonthStart: "2026-01", Months: []int{30, 280, 60}})
	if w != "" {
		t.Fatal(w)
	}
	got := p[monthWeightsParam].([]any)
	want := []float64{10, 10, 20}
	for i := range want {
		if got[i].(float64) != want[i] {
			t.Fatalf("month %d weight %v, want %v (count / in-range days)", i, got[i], want[i])
		}
	}
}

// TestMonthWeights_OldAndDegenerateDocumentsRebuildAsBefore: a document
// without a series, or with a one-month series, emits no month_weights
// (the old draw path); a series that does not fit its range emits none
// and says so.
func TestMonthWeights_OldAndDegenerateDocumentsRebuildAsBefore(t *testing.T) {
	t.Parallel()

	base := DatetimeProfile{Start: "2026-01-05", End: "2026-02-14",
		Weekdays: [7]int{1, 1, 1, 1, 1, 1, 1}, Hours: [24]int{9: 5}}
	old, w := datetimeParamsFromProfile("ts", &base)
	if _, ok := old[monthWeightsParam]; ok || w != "" {
		t.Fatalf("pre-series document rebuilt month_weights %v / warning %q", old[monthWeightsParam], w)
	}
	one := base
	one.End, one.MonthStart, one.Months = "2026-01-20", "2026-01", []int{7}
	if p, w := datetimeParamsFromProfile("ts", &one); p[monthWeightsParam] != nil || w != "" {
		t.Fatalf("one-month series rebuilt month_weights %v / warning %q", p[monthWeightsParam], w)
	}
	bad := base
	bad.MonthStart, bad.Months = "2025-12", []int{3, 4}
	p, w := datetimeParamsFromProfile("ts", &bad)
	if p[monthWeightsParam] != nil || !strings.Contains(w, `month series for field "ts"`) {
		t.Fatalf("misfit series rebuilt %v / warning %q, want none + a warning", p[monthWeightsParam], w)
	}
	// The warning reaches SpecFromProfile's channel.
	prof := &Profile{RowCount: 7, Fields: []FieldProfile{{Name: "ts", Type: "datetime", Datetime: &bad}}}
	if _, warns := SpecFromProfile(prof, 10); len(warns) != 1 || warns[0] != w {
		t.Fatalf("SpecFromProfile warnings %v, want the misfit warning", warns)
	}

	// The profile JSON of a series-free column carries no new key.
	raw, err := json.Marshal(&base)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("month")) {
		t.Fatalf("series-free profile JSON %s carries a month key", raw)
	}
}

// TestMonthWeights_Refusals: a series of the wrong length, a negative
// weight, and weights that leave no day drawable are refused.
func TestMonthWeights_Refusals(t *testing.T) {
	t.Parallel()

	cases := map[string]FieldSpec{
		"length": {Name: "d", Type: "date", Distribution: DistUniformDate,
			Params: map[string]any{"start": "2024-01-01", "end": "2024-03-31", "month_weights": []any{1.0, 2.0}}},
		"negative": {Name: "d", Type: "date", Distribution: DistUniformDate,
			Params: map[string]any{"start": "2024-01-01", "end": "2024-02-28", "month_weights": []any{1.0, -1.0}}},
		// January 2024-01-06/07 is a weekend; weekdays-only × January-only
		// leaves nothing.
		"empty": {Name: "ts", Type: "datetime", Distribution: DistUniformDateTime,
			Params: map[string]any{"start": "2024-01-06", "end": "2024-02-02",
				"weekday_weights": []any{0.0, 0.0, 0.0, 0.0, 0.0, 1.0, 1.0}, "month_weights": []any{0.0, 1.0}}},
	}
	for name, f := range cases {
		_, _, err := SynthBytes(&Spec{RowCount: 5, Fields: []FieldSpec{f}}, Options{Seed: 1})
		if codeOf(err) != errors.SERVICE_VALIDATION {
			t.Errorf("%s: err %v, want SERVICE_VALIDATION", name, err)
		}
	}
}

// TestMonthWeights_ZeroMonthIsNeverDrawn: a zero-weight month draws no
// day, and a zero-weight weekday inside a weighted month draws none.
func TestMonthWeights_ZeroMonthIsNeverDrawn(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 3000, Fields: []FieldSpec{{Name: "ts", Type: "datetime", Distribution: DistUniformDateTime,
		Params: map[string]any{"start": "2024-01-01", "end": "2024-03-31",
			"weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 0.0},
			"month_weights":   []any{1.0, 0.0, 1.0}}}}}
	out, _, err := SynthBytes(spec, Options{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := datetimeRows(t, out)
	for _, r := range rows {
		day := temporal.DateTimeToDay(int64(r["ts"].(float64)))
		if temporal.MonthIndex(day) == 2024*12+1 {
			t.Fatalf("drew %s in the zero-weight February", temporal.FormatDay(day))
		}
		if temporal.ISOWeekday(day) == 7 {
			t.Fatalf("drew %s, a zero-weight Sunday", temporal.FormatDay(day))
		}
	}
}

// TestMonthWeights_ThreeDrawsPerRow: a uniform_datetime with
// month_weights draws a day, an hour and a second — three RNG calls —
// so a later field's stream sits where three calls per row put it.
func TestMonthWeights_ThreeDrawsPerRow(t *testing.T) {
	t.Parallel()

	f := FieldSpec{Name: "ts", Type: "datetime", Params: map[string]any{
		"start": "2024-01-01", "end": "2024-03-31", "month_weights": []any{1.0, 2.0, 3.0}}}
	s, err := newUniformDateTimeSampler(f)
	if err != nil {
		t.Fatal(err)
	}
	// Both streams make the same calls: the sampler's three per row, and
	// the reference's Float64, Float64, Int64N(3600) — so their next
	// words agree only if the sampler made exactly those calls.
	a, b := rand.New(rand.NewPCG(11, 12)), rand.New(rand.NewPCG(11, 12))
	for range 50 {
		s.next(a)
		b.Float64()
		b.Float64()
		b.Int64N(temporal.SecondsPerHour)
	}
	if a.Uint64() != b.Uint64() {
		t.Fatal("the month path did not make exactly three draws per row (day, hour, second)")
	}
}
